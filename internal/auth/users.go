package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/audit"
)

// cliActor stands in for a client address on events from the command line.
const cliActor = "cli"

// ErrOwnAccount means the admin tried something on the panel that would cut off
// their own account. Their password belongs on the account page, and the admin
// role moves with transfer-admin.
var ErrOwnAccount = errors.New("not on your own account")

// ManagedUser is an account as the admin sees it. It carries nothing about what
// the person stores.
type ManagedUser struct {
	User
	LinkedAccounts int64
}

// ListUsers returns every account, oldest first. Only the admin routes call it.
func (s *Service) ListUsers(ctx context.Context) ([]ManagedUser, error) {
	return s.store.ListUsersForAdmin(ctx)
}

// CreateUser adds an account with a password the admin chose, which the person
// must replace when they first sign in.
func (s *Service) CreateUser(ctx context.Context, actor uuid.UUID, rawEmail, password string, c Client) (ManagedUser, error) {
	email, ok := normalizeEmail(rawEmail)
	if !ok {
		return ManagedUser{}, ErrInvalidEmail
	}
	if err := validatePassword(password); err != nil {
		return ManagedUser{}, err
	}
	hash, err := s.hasher.hash(ctx, password)
	if err != nil {
		return ManagedUser{}, fmt.Errorf("hash password: %w", err)
	}
	var created ManagedUser
	err = s.store.InTx(ctx, func(tx *Store) error {
		userID, err := tx.CreateUser(ctx, email, hash, RoleUser, true)
		if err != nil {
			return err
		}
		found, err := tx.Account(ctx, userID)
		if err != nil {
			return err
		}
		created = ManagedUser{User: found.User}
		return tx.Audit().Record(ctx, audit.Event{
			ActorID:  actor,
			Action:   audit.ActionUserCreated,
			IP:       c.IP,
			Metadata: map[string]any{"email": email},
		})
	})
	if err != nil {
		return ManagedUser{}, err
	}
	return created, nil
}

// ResetUserPassword sets a password the person must replace, and signs them out
// everywhere so that nobody stays in on the old one.
func (s *Service) ResetUserPassword(ctx context.Context, actor, target uuid.UUID, password string, c Client) error {
	if actor == target {
		return ErrOwnAccount
	}
	if err := validatePassword(password); err != nil {
		return err
	}
	hash, err := s.hasher.hash(ctx, password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	return s.store.InTx(ctx, func(tx *Store) error {
		return setTemporaryPassword(ctx, tx, target, hash, actor, c.IP)
	})
}

func setTemporaryPassword(ctx context.Context, tx *Store, target uuid.UUID, hash string, actor uuid.UUID, ip string) error {
	found, err := tx.Account(ctx, target)
	if err != nil {
		return err
	}
	if err := tx.SetTemporaryPassword(ctx, target, hash); err != nil {
		return err
	}
	if err := tx.DeleteAllSessionsForUser(ctx, target); err != nil {
		return err
	}
	return tx.Audit().Record(ctx, audit.Event{
		ActorID:  actor,
		Action:   audit.ActionPasswordReset,
		IP:       ip,
		Metadata: map[string]any{"email": found.Email},
	})
}

// DeleteUser removes an account. Its sessions, keys, linked accounts and file
// records go with it through foreign keys, but the files stay in the person's
// Google Drive.
func (s *Service) DeleteUser(ctx context.Context, actor, target uuid.UUID, c Client) error {
	if actor == target {
		return ErrOwnAccount
	}
	return s.store.InTx(ctx, func(tx *Store) error {
		found, err := tx.Account(ctx, target)
		if err != nil {
			return err
		}
		if err := tx.DeleteUser(ctx, target); err != nil {
			return err
		}
		return tx.Audit().Record(ctx, audit.Event{
			ActorID:  actor,
			Action:   audit.ActionUserDeleted,
			IP:       c.IP,
			Metadata: map[string]any{"email": found.Email},
		})
	})
}

// TransferAdmin moves the admin role to another account. It is the only way the
// role moves, and it's reachable only from the command line.
func (s *Service) TransferAdmin(ctx context.Context, rawEmail string) error {
	email, ok := normalizeEmail(rawEmail)
	if !ok {
		return ErrUserNotFound
	}
	return s.store.InTx(ctx, func(tx *Store) error {
		// Taken first so that two transfers at once run one after the other.
		if err := tx.LockUserCreation(ctx); err != nil {
			return err
		}
		if err := tx.MakeAdmin(ctx, email); err != nil {
			return err
		}
		return tx.Audit().Record(ctx, audit.Event{
			Action:   audit.ActionRoleChanged,
			IP:       cliActor,
			Metadata: map[string]any{"email": email, "role": RoleAdmin},
		})
	})
}

// ResetPasswordFromCLI gets someone back in when nobody can sign in, the admin
// included. It returns a random password the person must replace.
func (s *Service) ResetPasswordFromCLI(ctx context.Context, rawEmail string) (string, error) {
	email, ok := normalizeEmail(rawEmail)
	if !ok {
		return "", ErrUserNotFound
	}
	password := rand.Text()
	hash, err := s.hasher.hash(ctx, password)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	err = s.store.InTx(ctx, func(tx *Store) error {
		found, err := tx.AccountForLogin(ctx, email)
		if err != nil {
			return err
		}
		return setTemporaryPassword(ctx, tx, found.ID, hash, uuid.Nil, cliActor)
	})
	if err != nil {
		return "", err
	}
	return password, nil
}
