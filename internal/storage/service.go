// Package storage manages the Google Drive accounts users link to dextro.
package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/audit"
	"github.com/swayam5342/dextro/internal/provider"
	"github.com/swayam5342/dextro/internal/provider/gdrive"
	"github.com/swayam5342/dextro/internal/secrets"
)

// ProviderKind says which service a connection uses.
type ProviderKind string

// ProviderGoogleDrive is the only service. The value is stored, so don't rename it.
const ProviderGoogleDrive ProviderKind = "google_drive"

const (
	checkTimeout    = 20 * time.Second
	secretContextID = "storage_connection:"
	uploadContextID = "upload_session:"
)

// Errors the handlers turn into stable API error codes.
var (
	ErrConnectionFailed   = errors.New("connection failed")
	ErrConnectionNotFound = errors.New("connection not found")
)

// Connection is a linked account as its owner sees it. It holds no credentials,
// so it is always safe to send to the browser.
type Connection struct {
	ID        uuid.UUID
	Name      string
	Provider  ProviderKind
	Summary   string
	CreatedAt time.Time
}

// DriveAuth is Google's side of connecting a Drive account.
type DriveAuth interface {
	AuthCodeURL(state, verifier string) string
	Connect(ctx context.Context, code, verifier string) (gdrive.Settings, gdrive.Credentials, error)
}

// OpenDrive builds a provider from a connection's details.
type OpenDrive func(context.Context, gdrive.Settings, gdrive.Credentials) (provider.Provider, error)

// NewOpenDrive returns the real adapter. Drive may be nil if Google isn't
// configured.
func NewOpenDrive(drive *gdrive.Client) OpenDrive {
	return func(ctx context.Context, s gdrive.Settings, c gdrive.Credentials) (provider.Provider, error) {
		if drive == nil {
			return nil, errors.New("google isn't configured on this server")
		}
		return drive.Open(ctx, s, c)
	}
}

// Deps is everything a Service needs.
type Deps struct {
	Store     *Store
	Keyring   *secrets.Keyring
	OpenDrive OpenDrive
	// DriveAuth is nil when Google isn't configured.
	DriveAuth DriveAuth
}

// Service holds the rules for linked accounts.
type Service struct {
	store     *Store
	keyring   *secrets.Keyring
	openDrive OpenDrive
	driveAuth DriveAuth
}

// NewService returns a Service.
func NewService(d Deps) *Service {
	return &Service{store: d.Store, keyring: d.Keyring, openDrive: d.OpenDrive, driveAuth: d.DriveAuth}
}

// DriveEnabled reports whether Google Drive can be connected.
func (s *Service) DriveEnabled() bool {
	return s.driveAuth != nil
}

// DriveAuthURL returns where to send the browser to connect a Drive account.
func (s *Service) DriveAuthURL(state, verifier string) string {
	return s.driveAuth.AuthCodeURL(state, verifier)
}

// List returns a user's connections.
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]Connection, error) {
	records, err := s.store.ListConnectionsForOwner(ctx, userID)
	if err != nil {
		return nil, err
	}
	connections := make([]Connection, 0, len(records))
	for _, r := range records {
		connection, err := withSummary(r)
		if err != nil {
			return nil, err
		}
		connections = append(connections, connection)
	}
	return connections, nil
}

func check(ctx context.Context, opened provider.Provider) error {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	if err := opened.Check(ctx); err != nil {
		return fmt.Errorf("%w: %w", ErrConnectionFailed, err)
	}
	return nil
}

// seal encrypts credentials, bound to this connection's ID so that the value
// can't be moved onto another row.
func (s *Service) seal(id, userID uuid.UUID, name string, settings gdrive.Settings, credentials gdrive.Credentials) (record, error) {
	encodedSettings, err := json.Marshal(settings)
	if err != nil {
		return record{}, fmt.Errorf("encode settings: %w", err)
	}
	encodedCredentials, err := json.Marshal(credentials) //nolint:gosec // Goes straight into Encrypt below.
	if err != nil {
		return record{}, fmt.Errorf("encode credentials: %w", err)
	}
	return record{
		Connection: Connection{ID: id, Name: name, Provider: ProviderGoogleDrive},
		userID:     userID,
		settings:   encodedSettings,
		sealed:     s.keyring.Encrypt(encodedCredentials, secretContextID+id.String()),
	}, nil
}

func recordConnected(ctx context.Context, tx *Store, r record, ip string) error {
	return tx.Audit().Record(ctx, audit.Event{
		ActorID:  r.userID,
		Action:   audit.ActionStorageConnected,
		IP:       ip,
		Metadata: map[string]any{"connection_id": r.ID, "provider": r.Provider},
	})
}

// ConnectDrive finishes connecting a Drive account. Connecting an account that's
// already connected replaces its token, which is how a revoked or expired
// connection is repaired.
func (s *Service) ConnectDrive(ctx context.Context, userID uuid.UUID, code, verifier, ip string) (Connection, error) {
	settings, credentials, err := s.driveAuth.Connect(ctx, code, verifier)
	if err != nil {
		return Connection{}, fmt.Errorf("%w: %w", ErrConnectionFailed, err)
	}
	name := "Google Drive (" + settings.Email + ")"

	var saved record
	err = s.store.InTx(ctx, func(tx *Store) error {
		existing, err := tx.GetConnectionByNameForOwner(ctx, userID, name)
		if errors.Is(err, ErrConnectionNotFound) {
			r, err := s.seal(uuid.New(), userID, name, settings, credentials)
			if err != nil {
				return err
			}
			if saved, err = tx.CreateConnection(ctx, r); err != nil {
				return err
			}
			return recordConnected(ctx, tx, saved, ip)
		}
		if err != nil {
			return err
		}
		r, err := s.seal(existing.ID, userID, name, settings, credentials)
		if err != nil {
			return err
		}
		if saved, err = tx.UpdateConnectionForOwner(ctx, r); err != nil {
			return err
		}
		return recordConnected(ctx, tx, saved, ip)
	})
	if err != nil {
		return Connection{}, err
	}
	return withSummary(saved)
}

// Provider opens one of a user's connections.
func (s *Service) Provider(ctx context.Context, userID, id uuid.UUID) (provider.Provider, error) {
	r, err := s.store.GetConnectionForOwner(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	return s.open(ctx, r)
}

func (s *Service) open(ctx context.Context, r record) (provider.Provider, error) {
	secret, err := s.keyring.Decrypt(r.sealed, secretContextID+r.ID.String())
	if err != nil {
		return nil, fmt.Errorf("decrypt connection %s: %w", r.ID, err)
	}
	var settings gdrive.Settings
	if err := json.Unmarshal(r.settings, &settings); err != nil {
		return nil, fmt.Errorf("decode settings: %w", err)
	}
	var credentials gdrive.Credentials
	// The decode error is dropped: JSON errors can quote what they failed on.
	if json.Unmarshal(secret, &credentials) != nil {
		return nil, errors.New("decode credentials: malformed value")
	}
	return s.openDrive(ctx, settings, credentials)
}

// SealUploadSession encrypts a session's address, bound to that upload so the value
// can't be moved onto another row.
func (s *Service) SealUploadSession(id uuid.UUID, address string) secrets.Sealed {
	return s.keyring.Encrypt([]byte(address), uploadContextID+id.String())
}

// OpenUploadSession decrypts a session's address.
func (s *Service) OpenUploadSession(id uuid.UUID, sealed secrets.Sealed) (string, error) {
	address, err := s.keyring.Decrypt(sealed, uploadContextID+id.String())
	if err != nil {
		return "", fmt.Errorf("decrypt upload %s: %w", id, err)
	}
	return string(address), nil
}

// Test checks that a connection still works.
func (s *Service) Test(ctx context.Context, userID, id uuid.UUID) error {
	opened, err := s.Provider(ctx, userID, id)
	if err != nil {
		return err
	}
	return check(ctx, opened)
}

// Delete removes a connection and forgets its credentials. It doesn't touch the
// account's files, which belong to the user wherever they are.
func (s *Service) Delete(ctx context.Context, userID, id uuid.UUID, ip string) error {
	return s.store.InTx(ctx, func(tx *Store) error {
		if err := tx.DeleteConnectionForOwner(ctx, userID, id); err != nil {
			return err
		}
		return tx.Audit().Record(ctx, audit.Event{
			ActorID:  userID,
			Action:   audit.ActionStorageRemoved,
			IP:       ip,
			Metadata: map[string]any{"connection_id": id},
		})
	})
}

func withSummary(r record) (Connection, error) {
	var settings gdrive.Settings
	if err := json.Unmarshal(r.settings, &settings); err != nil {
		return Connection{}, fmt.Errorf("decode settings: %w", err)
	}
	connection := r.Connection
	connection.Summary = settings.Email
	return connection, nil
}
