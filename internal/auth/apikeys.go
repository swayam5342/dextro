package auth

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/swayam5342/dextro/internal/audit"
	"github.com/swayam5342/dextro/internal/db/dbgen"
	"github.com/swayam5342/dextro/internal/httpx"
)

const (
	apiKeyTokenPrefix = "dxt_"
	// apiKeyVisibleChars is how much of a key its owner sees again later: enough
	// to tell keys apart, and useless for guessing the 256 random bits behind it.
	apiKeyVisibleChars = 12
	maxAPIKeyNameRunes = 100
)

// Errors the handlers turn into stable API error codes.
var (
	ErrInvalidAPIKey  = errors.New("invalid api key details")
	ErrAPIKeyNotFound = errors.New("api key not found")
)

// APIKey is a key as its owner sees it, without the secret.
type APIKey struct {
	ID         uuid.UUID
	Name       string
	Prefix     string
	Scopes     []string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// CreateAPIKey makes a key and returns its secret, which exists only in this
// return value: the database keeps just its hash.
func (s *Service) CreateAPIKey(ctx context.Context, userID uuid.UUID, rawName string, scopes []string, c Client) (APIKey, string, error) {
	name, err := validateAPIKey(rawName, scopes)
	if err != nil {
		return APIKey{}, "", err
	}
	token := apiKeyTokenPrefix + newSecret()
	unique := slices.Clone(scopes)
	slices.Sort(unique)
	unique = slices.Compact(unique)

	var created APIKey
	err = s.store.InTx(ctx, func(tx *Store) error {
		var err error
		created, err = tx.CreateAPIKey(ctx, userID, name, hashToken(token), token[:apiKeyVisibleChars], unique)
		if err != nil {
			return err
		}
		return tx.Audit().Record(ctx, audit.Event{
			ActorID:  userID,
			Action:   audit.ActionAPIKeyCreated,
			IP:       c.IP,
			Metadata: map[string]any{"key_id": created.ID, "scopes": unique},
		})
	})
	if err != nil {
		return APIKey{}, "", err
	}
	return created, token, nil
}

func validateAPIKey(rawName string, scopes []string) (string, error) {
	name := strings.TrimSpace(rawName)
	if name == "" || utf8.RuneCountInString(name) > maxAPIKeyNameRunes || strings.ContainsFunc(name, unicode.IsControl) {
		return "", ErrInvalidAPIKey
	}
	if len(scopes) == 0 {
		return "", ErrInvalidAPIKey
	}
	for _, scope := range scopes {
		if scope != httpx.ScopeFilesRead && scope != httpx.ScopeFilesWrite {
			return "", ErrInvalidAPIKey
		}
	}
	return name, nil
}

// ListAPIKeys returns a user's keys.
func (s *Service) ListAPIKeys(ctx context.Context, userID uuid.UUID) ([]APIKey, error) {
	return s.store.ListAPIKeysForUser(ctx, userID)
}

// RevokeAPIKey ends one of a user's own keys.
func (s *Service) RevokeAPIKey(ctx context.Context, userID, keyID uuid.UUID, c Client) error {
	return s.store.InTx(ctx, func(tx *Store) error {
		if err := tx.DeleteAPIKeyForUser(ctx, userID, keyID); err != nil {
			return err
		}
		return tx.Audit().Record(ctx, audit.Event{
			ActorID:  userID,
			Action:   audit.ActionAPIKeyRevoked,
			IP:       c.IP,
			Metadata: map[string]any{"key_id": keyID},
		})
	})
}

// ResolveAPIKey implements httpx.APIKeyResolver. The principal never carries the
// admin role, even for an admin's key: keys are for files, not for running the
// server.
func (s *Service) ResolveAPIKey(ctx context.Context, token string) (httpx.Principal, error) {
	key, err := s.store.APIKeyByTokenHash(ctx, hashToken(token))
	if errors.Is(err, ErrAPIKeyNotFound) {
		return httpx.Principal{}, httpx.ErrNoSession
	}
	if err != nil {
		return httpx.Principal{}, err
	}
	if key.LastUsedAt == nil || time.Since(*key.LastUsedAt) > sessionTouchInterval {
		if err := s.store.TouchAPIKey(ctx, key.ID); err != nil {
			return httpx.Principal{}, err
		}
	}
	return httpx.Principal{
		UserID: key.UserID,
		Key:    &httpx.KeyAccess{ID: key.ID, Scopes: key.Scopes},
	}, nil
}

// CreateAPIKey saves a key.
func (s *Store) CreateAPIKey(ctx context.Context, userID uuid.UUID, name string, tokenHash []byte, prefix string, scopes []string) (APIKey, error) {
	row, err := s.queries.CreateAPIKey(ctx, dbgen.CreateAPIKeyParams{
		UserID:    userID,
		Name:      name,
		TokenHash: tokenHash,
		Prefix:    prefix,
		Scopes:    scopes,
	})
	if err != nil {
		return APIKey{}, fmt.Errorf("create api key: %w", err)
	}
	return toAPIKey(row), nil
}

// ListAPIKeysForUser returns a user's keys, newest first.
func (s *Store) ListAPIKeysForUser(ctx context.Context, userID uuid.UUID) ([]APIKey, error) {
	rows, err := s.queries.ListAPIKeysForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	keys := make([]APIKey, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, toAPIKey(row))
	}
	return keys, nil
}

// DeleteAPIKeyForUser removes one of a user's keys.
func (s *Store) DeleteAPIKeyForUser(ctx context.Context, userID, keyID uuid.UUID) error {
	rows, err := s.queries.DeleteAPIKeyForUser(ctx, dbgen.DeleteAPIKeyForUserParams{UserID: userID, ID: keyID})
	if err != nil {
		return fmt.Errorf("delete api key: %w", err)
	}
	if rows == 0 {
		return ErrAPIKeyNotFound
	}
	return nil
}

// liveKey is what an API key resolves to.
type liveKey struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Scopes     []string
	LastUsedAt *time.Time
}

// APIKeyByTokenHash finds a key from its hashed secret.
func (s *Store) APIKeyByTokenHash(ctx context.Context, tokenHash []byte) (liveKey, error) {
	row, err := s.queries.GetAPIKeyByTokenHash(ctx, tokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return liveKey{}, ErrAPIKeyNotFound
	}
	if err != nil {
		return liveKey{}, fmt.Errorf("find api key: %w", err)
	}
	return liveKey{ID: row.ID, UserID: row.UserID, Scopes: row.Scopes, LastUsedAt: row.LastUsedAt}, nil
}

// TouchAPIKey records that a key was just used.
func (s *Store) TouchAPIKey(ctx context.Context, keyID uuid.UUID) error {
	if err := s.queries.TouchAPIKey(ctx, keyID); err != nil {
		return fmt.Errorf("touch api key: %w", err)
	}
	return nil
}

func toAPIKey(row dbgen.ApiKey) APIKey {
	return APIKey{
		ID:         row.ID,
		Name:       row.Name,
		Prefix:     row.Prefix,
		Scopes:     row.Scopes,
		CreatedAt:  row.CreatedAt,
		LastUsedAt: row.LastUsedAt,
	}
}
