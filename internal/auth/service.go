// Package auth handles accounts, sessions and sign-in.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/audit"
	"github.com/swayam5342/dextro/internal/httpx"
)

// Roles a user can have.
const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

const (
	sessionLifetime = 30 * 24 * time.Hour
	// sessionTouchInterval limits last_seen_at to one write per session per
	// interval, rather than one per request.
	sessionTouchInterval = 5 * time.Minute
	secretBytes          = 32
)

// Errors the handlers turn into stable API error codes.
var (
	ErrInvalidEmail          = errors.New("invalid email")
	ErrWeakPassword          = errors.New("weak password")
	ErrPasswordUnchanged     = errors.New("new password is the old password")
	ErrEmailTaken            = errors.New("email taken")
	ErrRegistrationClosed    = errors.New("registration closed")
	ErrInvalidCredentials    = errors.New("invalid credentials")
	ErrNoPassword            = errors.New("account has no password")
	ErrSessionNotFound       = errors.New("session not found")
	ErrUserNotFound          = errors.New("user not found")
	ErrAccountExists         = errors.New("a password account uses this email")
	ErrGoogleEmailUnverified = errors.New("google email not verified")
	ErrGoogleAlreadyLinked   = errors.New("google account already linked")
)

// User is an account, safe to send to its owner.
type User struct {
	ID           uuid.UUID
	Email        string
	Role         string
	HasPassword  bool
	GoogleLinked bool
	// MustChangePassword is set when someone else chose the password.
	MustChangePassword bool
	CreatedAt          time.Time
}

// Session is one signed-in device, as shown to the user who owns it.
type Session struct {
	ID         uuid.UUID
	UserAgent  string
	IP         string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// Client describes who is making a request.
type Client struct {
	IP        string
	UserAgent string
}

// Issued is a new session. Token is the only copy of the secret; the database
// keeps just its hash.
type Issued struct {
	User      User
	Token     string
	ExpiresAt time.Time
}

// GoogleIdentity is what Google reports about the person signing in. Subject is
// their permanent ID; an email address can change hands.
type GoogleIdentity struct {
	Subject       string
	Email         string
	EmailVerified bool
}

// Service holds the rules for accounts, sessions and sign-in.
type Service struct {
	store  *Store
	hasher *hasher
	// decoyHash gives logins for unknown emails the same cost as real ones, so
	// response times don't reveal which addresses have accounts.
	decoyHash func() (string, error)
}

// NewService returns a Service backed by store.
func NewService(store *Store) *Service {
	h := newHasher()
	return &Service{
		store:  store,
		hasher: h,
		decoyHash: sync.OnceValues(func() (string, error) {
			return h.hash(context.Background(), rand.Text())
		}),
	}
}

// RegistrationOpen reports whether anyone can sign up. Only the first account can,
// and it becomes the admin. After that the admin creates accounts.
func (s *Service) RegistrationOpen(ctx context.Context) (bool, error) {
	exists, err := s.store.AnyUsers(ctx)
	if err != nil {
		return false, err
	}
	return !exists, nil
}

// Register creates a password account and signs it in.
func (s *Service) Register(ctx context.Context, rawEmail, password string, c Client) (Issued, error) {
	email, ok := normalizeEmail(rawEmail)
	if !ok {
		return Issued{}, ErrInvalidEmail
	}
	if err := validatePassword(password); err != nil {
		return Issued{}, err
	}
	hash, err := s.hasher.hash(ctx, password)
	if err != nil {
		return Issued{}, fmt.Errorf("hash password: %w", err)
	}

	var issued Issued
	err = s.store.InTx(ctx, func(tx *Store) error {
		userID, err := createAccount(ctx, tx, email, hash, "password", c)
		if err != nil {
			return err
		}
		issued, err = issueSession(ctx, tx, userID, c)
		return err
	})
	if err != nil {
		return Issued{}, err
	}
	return issued, nil
}

// createAccount is how the first account comes to exist, whichever way it signs
// up. It must run in a transaction: the lock makes the "is this the first account"
// check safe under concurrent sign-ups. Everyone after that is refused here before
// the email is looked at, so sign-up can't show which addresses have accounts.
func createAccount(ctx context.Context, tx *Store, email, passwordHash, method string, c Client) (uuid.UUID, error) {
	if err := tx.LockUserCreation(ctx); err != nil {
		return uuid.Nil, err
	}
	exists, err := tx.AnyUsers(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	if exists {
		return uuid.Nil, ErrRegistrationClosed
	}
	userID, err := tx.CreateUser(ctx, email, passwordHash, RoleAdmin, false)
	if err != nil {
		return uuid.Nil, err
	}
	err = tx.Audit().Record(ctx, audit.Event{
		ActorID:  userID,
		Action:   audit.ActionUserRegistered,
		IP:       c.IP,
		Metadata: map[string]any{"method": method, "role": RoleAdmin},
	})
	if err != nil {
		return uuid.Nil, err
	}
	return userID, nil
}

// Login checks a password and starts a session. Every failure looks the same to
// the caller, so it can't be used to find out which emails have accounts.
func (s *Service) Login(ctx context.Context, rawEmail, password string, c Client) (Issued, error) {
	found, err := s.findForLogin(ctx, rawEmail)
	if err != nil {
		return Issued{}, err
	}

	hash := found.passwordHash
	if hash == "" {
		if hash, err = s.decoyHash(); err != nil {
			return Issued{}, fmt.Errorf("hash decoy password: %w", err)
		}
	}
	matches, err := s.hasher.verify(ctx, hash, password)
	if err != nil {
		return Issued{}, fmt.Errorf("verify password: %w", err)
	}
	if !matches || found.passwordHash == "" {
		err := s.store.Audit().Record(ctx, audit.Event{
			ActorID:  found.ID,
			Action:   audit.ActionLoginFailed,
			IP:       c.IP,
			Metadata: map[string]any{"method": "password"},
		})
		if err != nil {
			return Issued{}, err
		}
		return Issued{}, ErrInvalidCredentials
	}
	return s.signIn(ctx, found.ID, "password", c)
}

// findForLogin returns a zero account when the email has none.
func (s *Service) findForLogin(ctx context.Context, rawEmail string) (account, error) {
	email, ok := normalizeEmail(rawEmail)
	if !ok {
		return account{}, nil
	}
	found, err := s.store.AccountForLogin(ctx, email)
	if errors.Is(err, ErrUserNotFound) {
		return account{}, nil
	}
	return found, err
}

func (s *Service) signIn(ctx context.Context, userID uuid.UUID, method string, c Client) (Issued, error) {
	var issued Issued
	err := s.store.InTx(ctx, func(tx *Store) error {
		var err error
		issued, err = signInTx(ctx, tx, userID, method, c)
		return err
	})
	if err != nil {
		return Issued{}, err
	}
	return issued, nil
}

func signInTx(ctx context.Context, tx *Store, userID uuid.UUID, method string, c Client) (Issued, error) {
	err := tx.Audit().Record(ctx, audit.Event{
		ActorID:  userID,
		Action:   audit.ActionLogin,
		IP:       c.IP,
		Metadata: map[string]any{"method": method},
	})
	if err != nil {
		return Issued{}, err
	}
	return issueSession(ctx, tx, userID, c)
}

func issueSession(ctx context.Context, tx *Store, userID uuid.UUID, c Client) (Issued, error) {
	token := newSecret()
	expiresAt := time.Now().Add(sessionLifetime)

	if err := tx.CreateSession(ctx, userID, hashToken(token), c, expiresAt); err != nil {
		return Issued{}, err
	}
	found, err := tx.Account(ctx, userID)
	if err != nil {
		return Issued{}, err
	}
	return Issued{User: found.User, Token: token, ExpiresAt: expiresAt}, nil
}

// newSecret returns 256 random bits in a form safe for cookies and URLs.
func newSecret() string {
	secret := make([]byte, secretBytes)
	_, _ = rand.Read(secret) // crypto/rand never returns an error.
	return base64.RawURLEncoding.EncodeToString(secret)
}

// hashToken needs no salt or slow hash: the token is 256 random bits, so there
// is nothing to guess.
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// ResolveSession implements httpx.SessionResolver.
func (s *Service) ResolveSession(ctx context.Context, token string) (httpx.Principal, error) {
	session, err := s.store.SessionByTokenHash(ctx, hashToken(token))
	if errors.Is(err, ErrSessionNotFound) {
		return httpx.Principal{}, httpx.ErrNoSession
	}
	if err != nil {
		return httpx.Principal{}, err
	}
	if time.Since(session.lastSeenAt) > sessionTouchInterval {
		if err := s.store.TouchSession(ctx, session.id); err != nil {
			return httpx.Principal{}, err
		}
	}
	return httpx.Principal{
		UserID:             session.userID,
		SessionID:          session.id,
		IsAdmin:            session.role == RoleAdmin,
		MustChangePassword: session.mustChangePassword,
	}, nil
}

// Me returns the signed-in user.
func (s *Service) Me(ctx context.Context, userID uuid.UUID) (User, error) {
	found, err := s.store.Account(ctx, userID)
	if err != nil {
		return User{}, err
	}
	return found.User, nil
}

// Logout ends the session making the request.
func (s *Service) Logout(ctx context.Context, p httpx.Principal, c Client) error {
	return s.store.InTx(ctx, func(tx *Store) error {
		if err := tx.DeleteSessionForUser(ctx, p.UserID, p.SessionID); err != nil {
			return err
		}
		return tx.Audit().Record(ctx, audit.Event{ActorID: p.UserID, Action: audit.ActionLogout, IP: c.IP})
	})
}

// ListSessions returns a user's live sessions.
func (s *Service) ListSessions(ctx context.Context, userID uuid.UUID) ([]Session, error) {
	return s.store.ListSessionsForUser(ctx, userID)
}

// RevokeSession ends one of a user's own sessions.
func (s *Service) RevokeSession(ctx context.Context, userID, sessionID uuid.UUID, c Client) error {
	return s.store.InTx(ctx, func(tx *Store) error {
		if err := tx.DeleteSessionForUser(ctx, userID, sessionID); err != nil {
			return err
		}
		return tx.Audit().Record(ctx, audit.Event{ActorID: userID, Action: audit.ActionSessionRevoked, IP: c.IP})
	})
}

// ChangePassword replaces the password and ends the user's other sessions, so a
// change made because of a stolen password locks the thief out. It's also how a
// password the admin chose gets replaced, which is why the two can't match.
func (s *Service) ChangePassword(ctx context.Context, p httpx.Principal, current, next string, c Client) error {
	found, err := s.store.Account(ctx, p.UserID)
	if err != nil {
		return err
	}
	if found.passwordHash == "" {
		return ErrNoPassword
	}
	matches, err := s.hasher.verify(ctx, found.passwordHash, current)
	if err != nil {
		return fmt.Errorf("verify password: %w", err)
	}
	if !matches {
		return ErrInvalidCredentials
	}
	if next == current {
		return ErrPasswordUnchanged
	}
	if err := validatePassword(next); err != nil {
		return err
	}
	hash, err := s.hasher.hash(ctx, next)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	return s.store.InTx(ctx, func(tx *Store) error {
		if err := tx.UpdatePassword(ctx, p.UserID, hash); err != nil {
			return err
		}
		if err := tx.DeleteOtherSessionsForUser(ctx, p.UserID, p.SessionID); err != nil {
			return err
		}
		return tx.Audit().Record(ctx, audit.Event{ActorID: p.UserID, Action: audit.ActionPasswordChanged, IP: c.IP})
	})
}

// SignInWithGoogle signs in the account tied to a Google identity, creating one
// if this is the person's first visit.
//
// It never ties a Google identity to an existing account, even when the emails
// match and Google has verified its own. Password accounts never prove they own
// their address, so the account could have been set up in advance by someone
// else. Linking happens only through LinkGoogle, by a signed-in user.
func (s *Service) SignInWithGoogle(ctx context.Context, id GoogleIdentity, c Client) (Issued, error) {
	var issued Issued
	err := s.store.InTx(ctx, func(tx *Store) error {
		userID, err := tx.GoogleIdentityOwner(ctx, id.Subject)
		if err == nil {
			issued, err = signInTx(ctx, tx, userID, "google", c)
			return err
		}
		if !errors.Is(err, errIdentityNotFound) {
			return err
		}
		issued, err = createGoogleAccount(ctx, tx, id, c)
		return err
	})
	if err == nil {
		return issued, nil
	}
	if errors.Is(err, ErrAccountExists) || errors.Is(err, ErrGoogleEmailUnverified) || errors.Is(err, ErrRegistrationClosed) {
		auditErr := s.store.Audit().Record(ctx, audit.Event{
			Action:   audit.ActionLoginFailed,
			IP:       c.IP,
			Metadata: map[string]any{"method": "google", "reason": err.Error()},
		})
		if auditErr != nil {
			return Issued{}, auditErr
		}
	}
	return Issued{}, err
}

func createGoogleAccount(ctx context.Context, tx *Store, id GoogleIdentity, c Client) (Issued, error) {
	if !id.EmailVerified {
		return Issued{}, ErrGoogleEmailUnverified
	}
	email, ok := normalizeEmail(id.Email)
	if !ok {
		return Issued{}, ErrGoogleEmailUnverified
	}
	// Google has shown this person owns the address, so telling them it has an
	// account reveals nothing, and it points them at linking from the account page.
	switch _, err := tx.AccountForLogin(ctx, email); {
	case err == nil:
		return Issued{}, ErrAccountExists
	case !errors.Is(err, ErrUserNotFound):
		return Issued{}, err
	}
	userID, err := createAccount(ctx, tx, email, "", "google", c)
	if errors.Is(err, ErrEmailTaken) {
		return Issued{}, ErrAccountExists
	}
	if err != nil {
		return Issued{}, err
	}
	if err := tx.CreateGoogleIdentity(ctx, userID, id.Subject, id.Email); err != nil {
		return Issued{}, err
	}
	return signInTx(ctx, tx, userID, "google", c)
}

// LinkGoogle ties a Google identity to a signed-in user, who has shown they
// control both sides.
func (s *Service) LinkGoogle(ctx context.Context, userID uuid.UUID, id GoogleIdentity, c Client) error {
	if !id.EmailVerified {
		return ErrGoogleEmailUnverified
	}
	return s.store.InTx(ctx, func(tx *Store) error {
		owner, err := tx.GoogleIdentityOwner(ctx, id.Subject)
		if err == nil {
			if owner == userID {
				return nil
			}
			return ErrGoogleAlreadyLinked
		}
		if !errors.Is(err, errIdentityNotFound) {
			return err
		}
		if err := tx.CreateGoogleIdentity(ctx, userID, id.Subject, id.Email); err != nil {
			return err
		}
		return tx.Audit().Record(ctx, audit.Event{ActorID: userID, Action: audit.ActionGoogleLinked, IP: c.IP})
	})
}
