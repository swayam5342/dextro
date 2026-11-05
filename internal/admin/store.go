// Package admin holds instance-wide settings and maintenance.
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/swayam5342/dextro/internal/db/dbgen"
)

// ErrSettingNotFound means no value has been stored for the key.
var ErrSettingNotFound = errors.New("setting not found")

// Store reads and writes instance-wide settings. These belong to the instance
// rather than to a user, so its functions take no user ID; access control is
// the job of the admin route group.
type Store struct {
	queries *dbgen.Queries
}

// NewStore returns a Store that runs its queries on db.
func NewStore(db dbgen.DBTX) *Store {
	return &Store{queries: dbgen.New(db)}
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
