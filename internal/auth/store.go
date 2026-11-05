package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/swayam5342/dextro/internal/audit"
	"github.com/swayam5342/dextro/internal/db/dbgen"
)

const (
	providerGoogle      = "google"
	pgUniqueViolationID = "23505"
)

// errIdentityNotFound means nobody has signed in with this outside identity.
var errIdentityNotFound = errors.New("identity not found")

// database is a pool or a transaction.
type database interface {
	dbgen.DBTX
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Store reads and writes accounts and sessions. Functions that load a user's
// data take that user's ID; the exceptions are named for what they go by, since
// sign-in starts from an email, an outside identity or a session token.
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

// LockUserCreation holds until the current transaction ends, so that two
// sign-ups can't both see an empty instance.
func (s *Store) LockUserCreation(ctx context.Context) error {
	if err := s.queries.LockUserCreation(ctx); err != nil {
		return fmt.Errorf("lock user creation: %w", err)
	}
	return nil
}

// AnyUsers reports whether at least one account exists.
func (s *Store) AnyUsers(ctx context.Context) (bool, error) {
	exists, err := s.queries.AnyUsersExist(ctx)
	if err != nil {
		return false, fmt.Errorf("check for users: %w", err)
	}
	return exists, nil
}

// CreateUser adds an account. passwordHash is empty for accounts with no
// password. It returns ErrEmailTaken if the address is in use.
func (s *Store) CreateUser(ctx context.Context, email, passwordHash, role string, mustChangePassword bool) (uuid.UUID, error) {
	row, err := s.queries.CreateUser(ctx, dbgen.CreateUserParams{
		Email:              email,
		PasswordHash:       pgtype.Text{String: passwordHash, Valid: passwordHash != ""},
		Role:               role,
		MustChangePassword: mustChangePassword,
	})
	if isUniqueViolation(err) {
		return uuid.Nil, ErrEmailTaken
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("create user: %w", err)
	}
	return row.ID, nil
}

// account is a user plus their password hash, which must not leave this package.
type account struct {
	User
	passwordHash string
}

// Account returns the user with the given ID.
func (s *Store) Account(ctx context.Context, userID uuid.UUID) (account, error) {
	row, err := s.queries.GetUser(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return account{}, ErrUserNotFound
	}
	if err != nil {
		return account{}, fmt.Errorf("get user: %w", err)
	}
	return account{
		User: User{
			ID:                 row.ID,
			Email:              row.Email,
			Role:               row.Role,
			HasPassword:        row.PasswordHash.Valid,
			GoogleLinked:       row.GoogleLinked,
			MustChangePassword: row.MustChangePassword,
			CreatedAt:          row.CreatedAt,
		},
		passwordHash: row.PasswordHash.String,
	}, nil
}

// AccountForLogin finds the account an email belongs to.
func (s *Store) AccountForLogin(ctx context.Context, email string) (account, error) {
	row, err := s.queries.GetUserForLogin(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return account{}, ErrUserNotFound
	}
	if err != nil {
		return account{}, fmt.Errorf("find user for login: %w", err)
	}
	return s.Account(ctx, row.ID)
}

// UpdatePassword replaces a user's password hash.
func (s *Store) UpdatePassword(ctx context.Context, userID uuid.UUID, passwordHash string) error {
	rows, err := s.queries.UpdateUserPassword(ctx, dbgen.UpdateUserPasswordParams{
		ID:           userID,
		PasswordHash: pgtype.Text{String: passwordHash, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	if rows == 0 {
		return ErrUserNotFound
	}
	return nil
}

// SetTemporaryPassword replaces a user's password with one they must change.
func (s *Store) SetTemporaryPassword(ctx context.Context, userID uuid.UUID, passwordHash string) error {
	rows, err := s.queries.SetTemporaryPassword(ctx, dbgen.SetTemporaryPasswordParams{
		ID:           userID,
		PasswordHash: pgtype.Text{String: passwordHash, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("set temporary password: %w", err)
	}
	if rows == 0 {
		return ErrUserNotFound
	}
	return nil
}

// MakeAdmin moves the admin role to the account with this email. The database
// allows one admin, so it must run in a transaction: the old one steps down first.
func (s *Store) MakeAdmin(ctx context.Context, email string) error {
	if err := s.queries.DemoteAdmin(ctx); err != nil {
		return fmt.Errorf("demote admin: %w", err)
	}
	rows, err := s.queries.PromoteUserByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("promote user: %w", err)
	}
	if rows == 0 {
		return ErrUserNotFound
	}
	return nil
}

// ListUsersForAdmin returns every account. It is unscoped on purpose and is for
// the admin's user list only.
func (s *Store) ListUsersForAdmin(ctx context.Context) ([]ManagedUser, error) {
	rows, err := s.queries.ListUsersForAdmin(ctx)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	users := make([]ManagedUser, 0, len(rows))
	for _, row := range rows {
		users = append(users, ManagedUser{
			User: User{
				ID:                 row.ID,
				Email:              row.Email,
				Role:               row.Role,
				HasPassword:        row.PasswordHash.Valid,
				GoogleLinked:       row.GoogleLinked,
				MustChangePassword: row.MustChangePassword,
				CreatedAt:          row.CreatedAt,
			},
			LinkedAccounts: row.LinkedAccounts,
		})
	}
	return users, nil
}

// DeleteUser removes an account and, through foreign keys, everything it owns
// here. Only the admin's user list calls this.
func (s *Store) DeleteUser(ctx context.Context, userID uuid.UUID) error {
	rows, err := s.queries.DeleteUserForAdmin(ctx, userID)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if rows == 0 {
		return ErrUserNotFound
	}
	return nil
}

// GoogleIdentityOwner returns the user a Google subject belongs to.
func (s *Store) GoogleIdentityOwner(ctx context.Context, subject string) (uuid.UUID, error) {
	userID, err := s.queries.GetIdentityUserID(ctx, dbgen.GetIdentityUserIDParams{
		Provider: providerGoogle,
		Subject:  subject,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, errIdentityNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("find google identity: %w", err)
	}
	return userID, nil
}

// CreateGoogleIdentity ties a Google subject to a user. It returns
// ErrGoogleAlreadyLinked if either side is already tied to something else.
func (s *Store) CreateGoogleIdentity(ctx context.Context, userID uuid.UUID, subject, email string) error {
	err := s.queries.CreateIdentity(ctx, dbgen.CreateIdentityParams{
		Provider: providerGoogle,
		Subject:  subject,
		UserID:   userID,
		Email:    email,
	})
	if isUniqueViolation(err) {
		return ErrGoogleAlreadyLinked
	}
	if err != nil {
		return fmt.Errorf("create google identity: %w", err)
	}
	return nil
}

// CreateSession saves a new session and clears out the user's expired ones,
// which keeps the table from growing without a background job.
func (s *Store) CreateSession(ctx context.Context, userID uuid.UUID, tokenHash []byte, c Client, expiresAt time.Time) error {
	if err := s.queries.DeleteExpiredSessionsForUser(ctx, userID); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	_, err := s.queries.CreateSession(ctx, dbgen.CreateSessionParams{
		UserID:    userID,
		TokenHash: tokenHash,
		UserAgent: c.UserAgent,
		Ip:        c.IP,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// liveSession is what a session token resolves to.
type liveSession struct {
	id                 uuid.UUID
	userID             uuid.UUID
	role               string
	lastSeenAt         time.Time
	mustChangePassword bool
}

// SessionByTokenHash finds an unexpired session from its hashed token.
func (s *Store) SessionByTokenHash(ctx context.Context, tokenHash []byte) (liveSession, error) {
	row, err := s.queries.GetSessionByTokenHash(ctx, tokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return liveSession{}, ErrSessionNotFound
	}
	if err != nil {
		return liveSession{}, fmt.Errorf("find session: %w", err)
	}
	return liveSession{
		id:                 row.ID,
		userID:             row.UserID,
		role:               row.Role,
		lastSeenAt:         row.LastSeenAt,
		mustChangePassword: row.MustChangePassword,
	}, nil
}

// TouchSession records that a session was just used.
func (s *Store) TouchSession(ctx context.Context, sessionID uuid.UUID) error {
	if err := s.queries.TouchSession(ctx, sessionID); err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

// ListSessionsForUser returns a user's live sessions, newest first.
func (s *Store) ListSessionsForUser(ctx context.Context, userID uuid.UUID) ([]Session, error) {
	rows, err := s.queries.ListSessionsForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	sessions := make([]Session, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, Session{
			ID:         row.ID,
			UserAgent:  row.UserAgent,
			IP:         row.Ip,
			CreatedAt:  row.CreatedAt,
			LastSeenAt: row.LastSeenAt,
			ExpiresAt:  row.ExpiresAt,
		})
	}
	return sessions, nil
}

// DeleteSessionForUser ends one of a user's sessions. It returns
// ErrSessionNotFound if the user has no such session.
func (s *Store) DeleteSessionForUser(ctx context.Context, userID, sessionID uuid.UUID) error {
	rows, err := s.queries.DeleteSessionForUser(ctx, dbgen.DeleteSessionForUserParams{UserID: userID, ID: sessionID})
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	if rows == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// DeleteAllSessionsForUser signs a user out everywhere.
func (s *Store) DeleteAllSessionsForUser(ctx context.Context, userID uuid.UUID) error {
	if err := s.queries.DeleteAllSessionsForUser(ctx, userID); err != nil {
		return fmt.Errorf("delete sessions: %w", err)
	}
	return nil
}

// DeleteOtherSessionsForUser ends all of a user's sessions except one.
func (s *Store) DeleteOtherSessionsForUser(ctx context.Context, userID, keepSessionID uuid.UUID) error {
	err := s.queries.DeleteOtherSessionsForUser(ctx, dbgen.DeleteOtherSessionsForUserParams{UserID: userID, ID: keepSessionID})
	if err != nil {
		return fmt.Errorf("delete other sessions: %w", err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolationID
}
