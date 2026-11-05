package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/swayam5342/dextro/internal/admin"
	"github.com/swayam5342/dextro/internal/db"
	"github.com/swayam5342/dextro/internal/db/dbtest"
	"github.com/swayam5342/dextro/internal/httpx"
)

const goodPassword = "correct horse battery"

var client = Client{IP: "203.0.113.7", UserAgent: "test-browser"}

type fixture struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	service *Service
}

func (f *fixture) register(t *testing.T, email string) Issued {
	t.Helper()
	issued, err := f.service.Register(f.ctx, email, goodPassword, client)
	if err != nil {
		t.Fatalf("Register(%s): %v", email, err)
	}
	return issued
}

func (f *fixture) closeRegistration(t *testing.T) {
	t.Helper()
	if err := admin.NewStore(f.pool).SetSetting(f.ctx, "allow_registration", []byte("false")); err != nil {
		t.Fatalf("close registration: %v", err)
	}
}

func (f *fixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(f.ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func (f *fixture) auditCount(t *testing.T, action string) int {
	t.Helper()
	return f.count(t, `SELECT count(*) FROM audit_events WHERE action = $1`, action)
}

func googleID(subject, email string) GoogleIdentity {
	return GoogleIdentity{Subject: subject, Email: email, EmailVerified: true}
}

// TestService shares one Postgres between its subtests, emptying it each time.
func TestService(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	tests := []struct {
		name string
		run  func(*testing.T, *fixture)
	}{
		{"first account becomes admin and later ones do not", testFirstAccountIsAdmin},
		{"concurrent first sign-ups produce one admin", testConcurrentFirstSignUps},
		{"emails are unique whatever their case", testEmailsAreUnique},
		{"closed registration blocks sign-up", testClosedRegistration},
		{"the first account ignores closed registration", testFirstAccountIgnoresClosedRegistration},
		{"bad input is refused", testRegisterValidates},
		{"login", testLogin},
		{"failed logins look alike and are audited", testFailedLogins},
		{"google-only accounts have no password to guess", testGoogleOnlyAccountsCannotUsePasswords},
		{"session tokens are stored hashed", testSessionTokensAreHashed},
		{"resolving sessions", testResolveSession},
		{"expired sessions stop working", testExpiredSessions},
		{"role changes apply to live sessions", testRoleChangeAppliesToLiveSessions},
		{"logout ends only the current session", testLogout},
		{"users only see their own sessions", testListSessionsIsScoped},
		{"users cannot revoke each other's sessions", testRevokeSessionIsScoped},
		{"changing a password", testChangePassword},
		{"changing a password needs the current one", testChangePasswordChecksCurrent},
		{"accounts without a password can't change it", testChangePasswordWithoutOne},
		{"google sign-in creates an account", testGoogleCreatesAccount},
		{"the first account can come from google", testGoogleFirstAccountIsAdmin},
		{"google accounts with unverified emails are refused", testGoogleUnverifiedIsRefused},
		{"returning google users sign in by subject", testGoogleReturningUser},
		{"google never takes over a password account", testGoogleNeverAutoLinks},
		{"closed registration blocks first-time google sign-ins", testGoogleClosedRegistration},
		{"closed registration lets returning google users in", testGoogleClosedRegistrationReturning},
		{"password sign-up can't claim a google account's email", testPasswordCannotClaimGoogleEmail},
		{"linking google", testLinkGoogle},
		{"linking needs a verified email", testLinkGoogleNeedsVerifiedEmail},
		{"a google account links to one user", testLinkGoogleOwnedByAnother},
		{"a user links one google account", testLinkGoogleTwice},
		{"relinking the same google account is harmless", testLinkGoogleSameAgain},
		{"setting roles", testSetRole},
		{"setting a role refuses bad input", testSetRoleValidates},
		{"registration status", testRegistrationOpen},
		{"the role command", testRunCommandSetsRole},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, `TRUNCATE users, settings, audit_events CASCADE`)
			if err != nil {
				t.Fatalf("reset database: %v", err)
			}
			tt.run(t, &fixture{ctx: ctx, pool: pool, service: NewService(NewStore(pool))})
		})
	}
}

func testFirstAccountIsAdmin(t *testing.T, f *fixture) {
	first := f.register(t, "first@example.org")
	if first.User.Role != RoleAdmin {
		t.Errorf("first role = %q, want admin", first.User.Role)
	}
	second := f.register(t, "second@example.org")
	if second.User.Role != RoleUser {
		t.Errorf("second role = %q, want user", second.User.Role)
	}
	if got := f.auditCount(t, "user.registered"); got != 2 {
		t.Errorf("audit events = %d, want 2", got)
	}
}

func testConcurrentFirstSignUps(t *testing.T, f *fixture) {
	const signUps = 12
	var wg sync.WaitGroup
	errs := make([]error, signUps)
	for i := range signUps {
		wg.Go(func() {
			_, errs[i] = f.service.Register(f.ctx, fmt.Sprintf("user%d@example.org", i), goodPassword, client)
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	if got := f.count(t, `SELECT count(*) FROM users WHERE role = 'admin'`); got != 1 {
		t.Errorf("admins = %d, want 1", got)
	}
}

func testEmailsAreUnique(t *testing.T, f *fixture) {
	f.register(t, "person@example.org")
	_, err := f.service.Register(f.ctx, "PERSON@Example.org", goodPassword, client)
	if !errors.Is(err, ErrEmailTaken) {
		t.Errorf("error = %v, want ErrEmailTaken", err)
	}
}

func testClosedRegistration(t *testing.T, f *fixture) {
	f.register(t, "first@example.org")
	f.closeRegistration(t)
	_, err := f.service.Register(f.ctx, "second@example.org", goodPassword, client)
	if !errors.Is(err, ErrRegistrationClosed) {
		t.Errorf("error = %v, want ErrRegistrationClosed", err)
	}
}

func testFirstAccountIgnoresClosedRegistration(t *testing.T, f *fixture) {
	f.closeRegistration(t)
	first := f.register(t, "first@example.org")
	if first.User.Role != RoleAdmin {
		t.Errorf("role = %q, want admin", first.User.Role)
	}
}

func testRegisterValidates(t *testing.T, f *fixture) {
	if _, err := f.service.Register(f.ctx, "not an email", goodPassword, client); !errors.Is(err, ErrInvalidEmail) {
		t.Errorf("bad email: error = %v", err)
	}
	if _, err := f.service.Register(f.ctx, "person@example.org", "short", client); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("short password: error = %v", err)
	}
	if got := f.count(t, `SELECT count(*) FROM users`); got != 0 {
		t.Errorf("users = %d, want 0", got)
	}
}

func testLogin(t *testing.T, f *fixture) {
	f.register(t, "person@example.org")
	issued, err := f.service.Login(f.ctx, " Person@Example.org", goodPassword, client)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if issued.Token == "" || issued.User.Email != "person@example.org" {
		t.Errorf("issued = %+v", issued)
	}
	if got := f.auditCount(t, "auth.login"); got != 1 {
		t.Errorf("audit events = %d, want 1", got)
	}
}

func testFailedLogins(t *testing.T, f *fixture) {
	f.register(t, "person@example.org")
	attempts := []struct{ email, password string }{
		{"person@example.org", "wrong password here"},
		{"nobody@example.org", goodPassword},
		{"not an email", goodPassword},
	}
	for _, a := range attempts {
		if _, err := f.service.Login(f.ctx, a.email, a.password, client); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("Login(%q): error = %v, want ErrInvalidCredentials", a.email, err)
		}
	}
	if got := f.auditCount(t, "auth.login_failed"); got != len(attempts) {
		t.Errorf("audit events = %d, want %d", got, len(attempts))
	}
	known := f.count(t, `SELECT count(*) FROM audit_events WHERE action = 'auth.login_failed' AND actor_id IS NOT NULL`)
	if known != 1 {
		t.Errorf("events naming a user = %d, want 1", known)
	}
}

func testGoogleOnlyAccountsCannotUsePasswords(t *testing.T, f *fixture) {
	if _, err := f.service.SignInWithGoogle(f.ctx, googleID("sub-1", "person@example.org"), client); err != nil {
		t.Fatalf("SignInWithGoogle: %v", err)
	}
	_, err := f.service.Login(f.ctx, "person@example.org", goodPassword, client)
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("error = %v, want ErrInvalidCredentials", err)
	}
}

func testSessionTokensAreHashed(t *testing.T, f *fixture) {
	issued := f.register(t, "person@example.org")
	sum := sha256.Sum256([]byte(issued.Token))
	if got := f.count(t, `SELECT count(*) FROM sessions WHERE token_hash = $1`, sum[:]); got != 1 {
		t.Errorf("sessions with the hashed token = %d, want 1", got)
	}
	if got := f.count(t, `SELECT count(*) FROM sessions WHERE token_hash = $1`, []byte(issued.Token)); got != 0 {
		t.Error("the raw token is stored")
	}
}

func testResolveSession(t *testing.T, f *fixture) {
	admin := f.register(t, "admin@example.org")
	user := f.register(t, "user@example.org")

	got, err := f.service.ResolveSession(f.ctx, admin.Token)
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}
	if got.UserID != admin.User.ID || !got.IsAdmin || got.SessionID == uuid.Nil {
		t.Errorf("admin principal = %+v", got)
	}

	got, err = f.service.ResolveSession(f.ctx, user.Token)
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}
	if got.UserID != user.User.ID || got.IsAdmin {
		t.Errorf("user principal = %+v", got)
	}

	if _, err := f.service.ResolveSession(f.ctx, "not-a-real-token"); !errors.Is(err, httpx.ErrNoSession) {
		t.Errorf("unknown token: error = %v, want ErrNoSession", err)
	}
}

func testExpiredSessions(t *testing.T, f *fixture) {
	issued := f.register(t, "person@example.org")
	if _, err := f.pool.Exec(f.ctx, `UPDATE sessions SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatalf("expire session: %v", err)
	}
	if _, err := f.service.ResolveSession(f.ctx, issued.Token); !errors.Is(err, httpx.ErrNoSession) {
		t.Errorf("error = %v, want ErrNoSession", err)
	}
}

func testRoleChangeAppliesToLiveSessions(t *testing.T, f *fixture) {
	f.register(t, "first@example.org")
	user := f.register(t, "user@example.org")
	if err := f.service.SetRole(f.ctx, "user@example.org", RoleAdmin); err != nil {
		t.Fatalf("SetRole: %v", err)
	}
	got, err := f.service.ResolveSession(f.ctx, user.Token)
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}
	if !got.IsAdmin {
		t.Error("IsAdmin = false after promotion")
	}
}

func testLogout(t *testing.T, f *fixture) {
	first := f.register(t, "person@example.org")
	second, err := f.service.Login(f.ctx, "person@example.org", goodPassword, client)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	principal, err := f.service.ResolveSession(f.ctx, second.Token)
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}
	if err := f.service.Logout(f.ctx, principal, client); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := f.service.ResolveSession(f.ctx, second.Token); !errors.Is(err, httpx.ErrNoSession) {
		t.Errorf("logged-out session: error = %v", err)
	}
	if _, err := f.service.ResolveSession(f.ctx, first.Token); err != nil {
		t.Errorf("other session: error = %v", err)
	}
	if got := f.auditCount(t, "auth.logout"); got != 1 {
		t.Errorf("audit events = %d, want 1", got)
	}
}

func testListSessionsIsScoped(t *testing.T, f *fixture) {
	mine := f.register(t, "mine@example.org")
	f.register(t, "theirs@example.org")
	sessions, err := f.service.ListSessions(f.ctx, mine.User.ID)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	if sessions[0].UserAgent != client.UserAgent || sessions[0].IP != client.IP {
		t.Errorf("session = %+v", sessions[0])
	}
}

func testRevokeSessionIsScoped(t *testing.T, f *fixture) {
	mine := f.register(t, "mine@example.org")
	theirs := f.register(t, "theirs@example.org")
	target, err := f.service.ResolveSession(f.ctx, theirs.Token)
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}

	err = f.service.RevokeSession(f.ctx, mine.User.ID, target.SessionID, client)
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("error = %v, want ErrSessionNotFound", err)
	}
	if _, err := f.service.ResolveSession(f.ctx, theirs.Token); err != nil {
		t.Errorf("their session stopped working: %v", err)
	}

	if err := f.service.RevokeSession(f.ctx, theirs.User.ID, target.SessionID, client); err != nil {
		t.Fatalf("RevokeSession(own): %v", err)
	}
	if _, err := f.service.ResolveSession(f.ctx, theirs.Token); !errors.Is(err, httpx.ErrNoSession) {
		t.Errorf("revoked session: error = %v", err)
	}
	if got := f.auditCount(t, "auth.session_revoked"); got != 1 {
		t.Errorf("audit events = %d, want 1", got)
	}
}

func testChangePassword(t *testing.T, f *fixture) {
	current := f.register(t, "person@example.org")
	other, err := f.service.Login(f.ctx, "person@example.org", goodPassword, client)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	principal, err := f.service.ResolveSession(f.ctx, current.Token)
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}

	const next = "a different long password"
	if err := f.service.ChangePassword(f.ctx, principal, goodPassword, next, client); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	if _, err := f.service.ResolveSession(f.ctx, current.Token); err != nil {
		t.Errorf("current session stopped working: %v", err)
	}
	if _, err := f.service.ResolveSession(f.ctx, other.Token); !errors.Is(err, httpx.ErrNoSession) {
		t.Errorf("other session: error = %v, want ErrNoSession", err)
	}
	if _, err := f.service.Login(f.ctx, "person@example.org", goodPassword, client); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("old password: error = %v", err)
	}
	if _, err := f.service.Login(f.ctx, "person@example.org", next, client); err != nil {
		t.Errorf("new password: %v", err)
	}
	if got := f.auditCount(t, "auth.password_changed"); got != 1 {
		t.Errorf("audit events = %d, want 1", got)
	}
}

func testChangePasswordChecksCurrent(t *testing.T, f *fixture) {
	issued := f.register(t, "person@example.org")
	principal, err := f.service.ResolveSession(f.ctx, issued.Token)
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}
	err = f.service.ChangePassword(f.ctx, principal, "wrong password here", "a different long password", client)
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("wrong current: error = %v", err)
	}
	if err := f.service.ChangePassword(f.ctx, principal, goodPassword, "short", client); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("weak new password: error = %v", err)
	}
}

func testChangePasswordWithoutOne(t *testing.T, f *fixture) {
	issued, err := f.service.SignInWithGoogle(f.ctx, googleID("sub-1", "person@example.org"), client)
	if err != nil {
		t.Fatalf("SignInWithGoogle: %v", err)
	}
	principal, err := f.service.ResolveSession(f.ctx, issued.Token)
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}
	err = f.service.ChangePassword(f.ctx, principal, "", "a different long password", client)
	if !errors.Is(err, ErrNoPassword) {
		t.Errorf("error = %v, want ErrNoPassword", err)
	}
}

func testGoogleCreatesAccount(t *testing.T, f *fixture) {
	f.register(t, "first@example.org")
	issued, err := f.service.SignInWithGoogle(f.ctx, googleID("sub-1", "Person@Example.org"), client)
	if err != nil {
		t.Fatalf("SignInWithGoogle: %v", err)
	}
	u := issued.User
	if u.Email != "person@example.org" || u.Role != RoleUser || u.HasPassword || !u.GoogleLinked {
		t.Errorf("user = %+v", u)
	}
}

func testGoogleFirstAccountIsAdmin(t *testing.T, f *fixture) {
	issued, err := f.service.SignInWithGoogle(f.ctx, googleID("sub-1", "person@example.org"), client)
	if err != nil {
		t.Fatalf("SignInWithGoogle: %v", err)
	}
	if issued.User.Role != RoleAdmin {
		t.Errorf("role = %q, want admin", issued.User.Role)
	}
}

func testGoogleUnverifiedIsRefused(t *testing.T, f *fixture) {
	id := GoogleIdentity{Subject: "sub-1", Email: "person@example.org", EmailVerified: false}
	_, err := f.service.SignInWithGoogle(f.ctx, id, client)
	if !errors.Is(err, ErrGoogleEmailUnverified) {
		t.Errorf("error = %v, want ErrGoogleEmailUnverified", err)
	}
	if got := f.count(t, `SELECT count(*) FROM users`); got != 0 {
		t.Errorf("users = %d, want 0", got)
	}
}

func testGoogleReturningUser(t *testing.T, f *fixture) {
	first, err := f.service.SignInWithGoogle(f.ctx, googleID("sub-1", "old@example.org"), client)
	if err != nil {
		t.Fatalf("SignInWithGoogle: %v", err)
	}
	// The person changed their Google address since signing up.
	again, err := f.service.SignInWithGoogle(f.ctx, googleID("sub-1", "new@example.org"), client)
	if err != nil {
		t.Fatalf("SignInWithGoogle: %v", err)
	}
	if again.User.ID != first.User.ID {
		t.Errorf("signed in as %s, want %s", again.User.ID, first.User.ID)
	}
}

// Password accounts never prove they own their address, so someone can register
// a stranger's email in advance. If the stranger's Google sign-in then landed in
// that account, it would share an account with whoever set it up.
func testGoogleNeverAutoLinks(t *testing.T, f *fixture) {
	f.register(t, "first@example.org")
	preClaimed := f.register(t, "victim@example.org")

	_, err := f.service.SignInWithGoogle(f.ctx, googleID("victim-sub", "victim@example.org"), client)
	if !errors.Is(err, ErrAccountExists) {
		t.Fatalf("error = %v, want ErrAccountExists", err)
	}
	if got := f.count(t, `SELECT count(*) FROM user_identities`); got != 0 {
		t.Errorf("identities = %d, want 0", got)
	}
	if got := f.count(t, `SELECT count(*) FROM sessions WHERE user_id = $1`, preClaimed.User.ID); got != 1 {
		t.Errorf("sessions = %d, want only the one from registering", got)
	}
}

func testGoogleClosedRegistration(t *testing.T, f *fixture) {
	f.register(t, "first@example.org")
	f.closeRegistration(t)
	_, err := f.service.SignInWithGoogle(f.ctx, googleID("sub-1", "person@example.org"), client)
	if !errors.Is(err, ErrRegistrationClosed) {
		t.Errorf("error = %v, want ErrRegistrationClosed", err)
	}
}

func testGoogleClosedRegistrationReturning(t *testing.T, f *fixture) {
	if _, err := f.service.SignInWithGoogle(f.ctx, googleID("sub-1", "person@example.org"), client); err != nil {
		t.Fatalf("SignInWithGoogle: %v", err)
	}
	f.closeRegistration(t)
	if _, err := f.service.SignInWithGoogle(f.ctx, googleID("sub-1", "person@example.org"), client); err != nil {
		t.Errorf("returning user: %v", err)
	}
}

func testPasswordCannotClaimGoogleEmail(t *testing.T, f *fixture) {
	if _, err := f.service.SignInWithGoogle(f.ctx, googleID("sub-1", "person@example.org"), client); err != nil {
		t.Fatalf("SignInWithGoogle: %v", err)
	}
	_, err := f.service.Register(f.ctx, "person@example.org", goodPassword, client)
	if !errors.Is(err, ErrEmailTaken) {
		t.Errorf("error = %v, want ErrEmailTaken", err)
	}
}

func testLinkGoogle(t *testing.T, f *fixture) {
	issued := f.register(t, "person@example.org")
	id := googleID("sub-1", "other-address@example.org")
	if err := f.service.LinkGoogle(f.ctx, issued.User.ID, id, client); err != nil {
		t.Fatalf("LinkGoogle: %v", err)
	}
	signedIn, err := f.service.SignInWithGoogle(f.ctx, id, client)
	if err != nil {
		t.Fatalf("SignInWithGoogle: %v", err)
	}
	if signedIn.User.ID != issued.User.ID {
		t.Errorf("signed in as %s, want %s", signedIn.User.ID, issued.User.ID)
	}
	if !signedIn.User.GoogleLinked || !signedIn.User.HasPassword {
		t.Errorf("user = %+v", signedIn.User)
	}
	if got := f.auditCount(t, "auth.google_linked"); got != 1 {
		t.Errorf("audit events = %d, want 1", got)
	}
}

func testLinkGoogleNeedsVerifiedEmail(t *testing.T, f *fixture) {
	issued := f.register(t, "person@example.org")
	id := GoogleIdentity{Subject: "sub-1", Email: "person@example.org", EmailVerified: false}
	err := f.service.LinkGoogle(f.ctx, issued.User.ID, id, client)
	if !errors.Is(err, ErrGoogleEmailUnverified) {
		t.Errorf("error = %v, want ErrGoogleEmailUnverified", err)
	}
	if got := f.count(t, `SELECT count(*) FROM user_identities`); got != 0 {
		t.Errorf("identities = %d, want 0", got)
	}
}

func testLinkGoogleOwnedByAnother(t *testing.T, f *fixture) {
	owner := f.register(t, "owner@example.org")
	other := f.register(t, "other@example.org")
	id := googleID("sub-1", "owner@example.org")
	if err := f.service.LinkGoogle(f.ctx, owner.User.ID, id, client); err != nil {
		t.Fatalf("LinkGoogle: %v", err)
	}
	if err := f.service.LinkGoogle(f.ctx, other.User.ID, id, client); !errors.Is(err, ErrGoogleAlreadyLinked) {
		t.Errorf("error = %v, want ErrGoogleAlreadyLinked", err)
	}
}

func testLinkGoogleTwice(t *testing.T, f *fixture) {
	issued := f.register(t, "person@example.org")
	if err := f.service.LinkGoogle(f.ctx, issued.User.ID, googleID("sub-1", "a@example.org"), client); err != nil {
		t.Fatalf("LinkGoogle: %v", err)
	}
	err := f.service.LinkGoogle(f.ctx, issued.User.ID, googleID("sub-2", "b@example.org"), client)
	if !errors.Is(err, ErrGoogleAlreadyLinked) {
		t.Errorf("error = %v, want ErrGoogleAlreadyLinked", err)
	}
}

func testLinkGoogleSameAgain(t *testing.T, f *fixture) {
	issued := f.register(t, "person@example.org")
	id := googleID("sub-1", "a@example.org")
	for range 2 {
		if err := f.service.LinkGoogle(f.ctx, issued.User.ID, id, client); err != nil {
			t.Fatalf("LinkGoogle: %v", err)
		}
	}
}

func testSetRole(t *testing.T, f *fixture) {
	f.register(t, "first@example.org")
	f.register(t, "person@example.org")
	if err := f.service.SetRole(f.ctx, "Person@Example.org", RoleAdmin); err != nil {
		t.Fatalf("SetRole: %v", err)
	}
	if got := f.count(t, `SELECT count(*) FROM users WHERE email = 'person@example.org' AND role = 'admin'`); got != 1 {
		t.Error("role was not changed")
	}
	got := f.count(t, `SELECT count(*) FROM audit_events WHERE action = 'user.role_changed'
		AND metadata->>'email' = 'person@example.org' AND metadata->>'role' = 'admin'`)
	if got != 1 {
		t.Errorf("audit events = %d, want 1", got)
	}
}

func testSetRoleValidates(t *testing.T, f *fixture) {
	f.register(t, "first@example.org")
	if err := f.service.SetRole(f.ctx, "first@example.org", "owner"); !errors.Is(err, ErrInvalidRole) {
		t.Errorf("bad role: error = %v", err)
	}
	if err := f.service.SetRole(f.ctx, "nobody@example.org", RoleAdmin); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("unknown user: error = %v", err)
	}
}

func testRegistrationOpen(t *testing.T, f *fixture) {
	check := func(want bool) {
		t.Helper()
		got, err := f.service.RegistrationOpen(f.ctx)
		if err != nil {
			t.Fatalf("RegistrationOpen: %v", err)
		}
		if got != want {
			t.Errorf("RegistrationOpen = %v, want %v", got, want)
		}
	}
	f.closeRegistration(t)
	check(true) // An empty instance can always create its first account.
	f.register(t, "first@example.org")
	check(false)
}
