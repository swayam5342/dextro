package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/swayam5342/dextro/internal/audit"
	"github.com/swayam5342/dextro/internal/db/dbgen"
	"github.com/swayam5342/dextro/internal/secrets"
)

const pgUniqueViolation = "23505"

// database is a pool or a transaction.
type database interface {
	dbgen.DBTX
	Begin(ctx context.Context) (pgx.Tx, error)
}

// record is a stored connection with its settings and sealed credentials.
type record struct {
	Connection
	userID   uuid.UUID
	settings []byte
	sealed   secrets.Sealed
}

// Store reads and writes storage connections. Every function that loads one
// takes the owner's ID.
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

// CreateConnection saves a new connection. It returns ErrNameTaken if the owner
// already has one with this name.
func (s *Store) CreateConnection(ctx context.Context, r record) (record, error) {
	row, err := s.queries.CreateConnection(ctx, dbgen.CreateConnectionParams{
		ID:               r.ID,
		UserID:           r.userID,
		Provider:         string(r.Provider),
		Name:             r.Name,
		Settings:         r.settings,
		SecretKeyID:      r.sealed.KeyID,
		SecretCiphertext: r.sealed.Ciphertext,
	})
	if isUniqueViolation(err) {
		return record{}, ErrNameTaken
	}
	if err != nil {
		return record{}, fmt.Errorf("create connection: %w", err)
	}
	return toRecord(row), nil
}

// ListConnectionsForOwner returns a user's connections, oldest first.
func (s *Store) ListConnectionsForOwner(ctx context.Context, userID uuid.UUID) ([]record, error) {
	rows, err := s.queries.ListConnectionsForOwner(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list connections: %w", err)
	}
	records := make([]record, 0, len(rows))
	for _, row := range rows {
		records = append(records, toRecord(row))
	}
	return records, nil
}

// GetConnectionForOwner returns one of a user's connections.
func (s *Store) GetConnectionForOwner(ctx context.Context, userID, id uuid.UUID) (record, error) {
	row, err := s.queries.GetConnectionForOwner(ctx, dbgen.GetConnectionForOwnerParams{UserID: userID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return record{}, ErrConnectionNotFound
	}
	if err != nil {
		return record{}, fmt.Errorf("get connection: %w", err)
	}
	return toRecord(row), nil
}

// GetConnectionByNameForOwner finds one of a user's connections by name.
func (s *Store) GetConnectionByNameForOwner(ctx context.Context, userID uuid.UUID, name string) (record, error) {
	row, err := s.queries.GetConnectionByNameForOwner(ctx, dbgen.GetConnectionByNameForOwnerParams{UserID: userID, Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return record{}, ErrConnectionNotFound
	}
	if err != nil {
		return record{}, fmt.Errorf("find connection: %w", err)
	}
	return toRecord(row), nil
}

// UpdateConnectionForOwner replaces a connection's settings and credentials.
func (s *Store) UpdateConnectionForOwner(ctx context.Context, r record) (record, error) {
	row, err := s.queries.UpdateConnectionForOwner(ctx, dbgen.UpdateConnectionForOwnerParams{
		UserID:           r.userID,
		ID:               r.ID,
		Settings:         r.settings,
		SecretKeyID:      r.sealed.KeyID,
		SecretCiphertext: r.sealed.Ciphertext,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return record{}, ErrConnectionNotFound
	}
	if err != nil {
		return record{}, fmt.Errorf("update connection: %w", err)
	}
	return toRecord(row), nil
}

// DeleteConnectionForOwner removes one of a user's connections.
func (s *Store) DeleteConnectionForOwner(ctx context.Context, userID, id uuid.UUID) error {
	rows, err := s.queries.DeleteConnectionForOwner(ctx, dbgen.DeleteConnectionForOwnerParams{UserID: userID, ID: id})
	if err != nil {
		return fmt.Errorf("delete connection: %w", err)
	}
	if rows == 0 {
		return ErrConnectionNotFound
	}
	return nil
}

func toRecord(row dbgen.StorageConnection) record {
	return record{
		Connection: Connection{
			ID:        row.ID,
			Name:      row.Name,
			Provider:  ProviderKind(row.Provider),
			CreatedAt: row.CreatedAt,
		},
		userID:   row.UserID,
		settings: row.Settings,
		sealed:   secrets.Sealed{KeyID: row.SecretKeyID, Ciphertext: row.SecretCiphertext},
	}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}
