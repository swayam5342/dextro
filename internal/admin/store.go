// Package admin holds instance-wide settings and maintenance.
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/swayam5342/dextro/internal/audit"
	"github.com/swayam5342/dextro/internal/db/dbgen"
)

// ErrSettingNotFound means no value has been stored for the key.
var ErrSettingNotFound = errors.New("setting not found")

const settingAllowRegistration = "allow_registration"

// database is a pool or a transaction.
type database interface {
	dbgen.DBTX
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Store reads and writes instance-wide settings. These belong to the instance
// rather than to a user, so its functions take no user ID; access control is
// the job of the admin route group.
type Store struct {
	db      database
	queries *dbgen.Queries
}

// NewStore returns a Store that runs its queries on db.
func NewStore(db database) *Store {
	return &Store{db: db, queries: dbgen.New(db)}
}

// InTx runs fn with a Store bound to one transaction.
func (s *Store) InTx(ctx context.Context, fn func(tx *Store) error) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		return fn(NewStore(tx))
	})
}

// Audit returns the audit log on the same connection as this store.
func (s *Store) Audit() *audit.Store {
	return audit.NewStore(s.db)
}

// GetSetting returns the JSON value stored for key.
func (s *Store) GetSetting(ctx context.Context, key string) (json.RawMessage, error) {
	row, err := s.queries.GetSetting(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSettingNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get setting %q: %w", key, err)
	}
	return row.Value, nil
}

// SetSetting creates or replaces the JSON value stored for key.
func (s *Store) SetSetting(ctx context.Context, key string, value json.RawMessage) error {
	err := s.queries.UpsertSetting(ctx, dbgen.UpsertSettingParams{Key: key, Value: value})
	if err != nil {
		return fmt.Errorf("set setting %q: %w", key, err)
	}
	return nil
}

// AllowRegistration reports whether new accounts may be created. Registration
// stays open until an admin closes it.
func (s *Store) AllowRegistration(ctx context.Context) (bool, error) {
	raw, err := s.GetSetting(ctx, settingAllowRegistration)
	if errors.Is(err, ErrSettingNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	var allowed bool
	if err := json.Unmarshal(raw, &allowed); err != nil {
		return false, fmt.Errorf("decode setting %q: %w", settingAllowRegistration, err)
	}
	return allowed, nil
}
