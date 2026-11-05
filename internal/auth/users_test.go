package auth

import (
	"bytes"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/audit"
	"github.com/swayam5342/dextro/internal/httpx/apigen"
)

const otherPassword = "a different long password"

func testCreateUser(t *testing.T, f *fixture) {
	admin := f.register(t, "admin@example.org")
	created, err := f.service.CreateUser(f.ctx, admin.User.ID, "New@Example.org", goodPassword, client)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if created.Email != "new@example.org" || created.Role != RoleUser || !created.MustChangePassword {
		t.Errorf("created = %+v", created)
	}
	got := f.count(t, `SELECT count(*) FROM audit_events WHERE action = 'user.created'
		AND actor_id = $1 AND metadata->>'email' = 'new@example.org'`, admin.User.ID)
	if got != 1 {
		t.Errorf("audit events = %d, want 1", got)
	}
	if got := f.count(t, `SELECT count(*) FROM audit_events WHERE metadata::text LIKE '%' || $1 || '%'`, goodPassword); got != 0 {
		t.Error("the password reached the audit log")
	}
}

func testCreateUserValidates(t *testing.T, f *fixture) {
	admin := f.register(t, "admin@example.org")
	if _, err := f.service.CreateUser(f.ctx, admin.User.ID, "nope", goodPassword, client); !errors.Is(err, ErrInvalidEmail) {
		t.Errorf("bad email: error = %v", err)
	}
	if _, err := f.service.CreateUser(f.ctx, admin.User.ID, "a@example.org", "short", client); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("weak password: error = %v", err)
	}
	if _, err := f.service.CreateUser(f.ctx, admin.User.ID, "ADMIN@example.org", goodPassword, client); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("taken email: error = %v", err)
	}
}

func testListUsers(t *testing.T, f *fixture) {
	f.register(t, "admin@example.org")
	person := f.register(t, "person@example.org")
	_, err := f.pool.Exec(f.ctx, `INSERT INTO storage_connections (id, user_id, provider, name, settings, secret_key_id, secret_ciphertext)
		VALUES ($1, $2, 'google_drive', 'Drive', '{}', 'k1', '\x00')`, uuid.New(), person.User.ID)
	if err != nil {
		t.Fatalf("seed connection: %v", err)
	}
	users, err := f.service.ListUsers(f.ctx)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 2 || users[0].Email != "admin@example.org" || users[0].Role != RoleAdmin {
		t.Fatalf("users = %+v", users)
	}
	if users[0].LinkedAccounts != 0 || users[1].LinkedAccounts != 1 {
		t.Errorf("linked accounts = %d and %d, want 0 and 1", users[0].LinkedAccounts, users[1].LinkedAccounts)
	}
}

func testResetUserPassword(t *testing.T, f *fixture) {
	admin := f.register(t, "admin@example.org")
	person := f.register(t, "person@example.org")

	if err := f.service.ResetUserPassword(f.ctx, admin.User.ID, person.User.ID, otherPassword, client); err != nil {
		t.Fatalf("ResetUserPassword: %v", err)
	}
	if _, err := f.service.ResolveSession(f.ctx, person.Token); err == nil {
		t.Error("the old session still works after a reset")
	}
	if _, err := f.service.Login(f.ctx, "person@example.org", goodPassword, client); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("old password: error = %v", err)
	}
	issued, err := f.service.Login(f.ctx, "person@example.org", otherPassword, client)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !issued.User.MustChangePassword {
		t.Error("MustChangePassword = false after a reset")
	}
	got, err := f.service.ResolveSession(f.ctx, issued.Token)
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}
	if !got.MustChangePassword {
		t.Error("the session doesn't carry MustChangePassword")
	}
	if got := f.auditCount(t, "user.password_reset"); got != 1 {
		t.Errorf("audit events = %d, want 1", got)
	}
}

func testResetUserPasswordValidates(t *testing.T, f *fixture) {
	admin := f.register(t, "admin@example.org")
	person := f.register(t, "person@example.org")
	if err := f.service.ResetUserPassword(f.ctx, admin.User.ID, admin.User.ID, otherPassword, client); !errors.Is(err, ErrOwnAccount) {
		t.Errorf("own account: error = %v", err)
	}
	if err := f.service.ResetUserPassword(f.ctx, admin.User.ID, person.User.ID, "short", client); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("weak password: error = %v", err)
	}
	if err := f.service.ResetUserPassword(f.ctx, admin.User.ID, uuid.New(), otherPassword, client); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("unknown user: error = %v", err)
	}
}

// The password an admin chose has to go, and only replacing it lifts the block.
func testChangingPasswordLiftsTheBlock(t *testing.T, f *fixture) {
	admin := f.register(t, "admin@example.org")
	if _, err := f.service.CreateUser(f.ctx, admin.User.ID, "new@example.org", goodPassword, client); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	issued, err := f.service.Login(f.ctx, "new@example.org", goodPassword, client)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	principal, err := f.service.ResolveSession(f.ctx, issued.Token)
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}
	if err := f.service.ChangePassword(f.ctx, principal, goodPassword, goodPassword, client); !errors.Is(err, ErrPasswordUnchanged) {
		t.Fatalf("same password: error = %v", err)
	}
	if err := f.service.ChangePassword(f.ctx, principal, goodPassword, otherPassword, client); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	after, err := f.service.ResolveSession(f.ctx, issued.Token)
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}
	if after.MustChangePassword {
		t.Error("MustChangePassword is still set")
	}
}

func testDeleteUser(t *testing.T, f *fixture) {
	admin := f.register(t, "admin@example.org")
	person := f.register(t, "person@example.org")
	connection, folder := uuid.New(), uuid.New()
	_, err := f.pool.Exec(f.ctx, `INSERT INTO storage_connections (id, user_id, provider, name, settings, secret_key_id, secret_ciphertext)
		VALUES ($1, $2, 'google_drive', 'Drive', '{}', 'k1', '\x00')`, connection, person.User.ID)
	if err != nil {
		t.Fatalf("seed connection: %v", err)
	}
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO folders (id, user_id, name) VALUES ($1, $2, 'Photos')`, folder, person.User.ID); err != nil {
		t.Fatalf("seed folder: %v", err)
	}
	_, err = f.pool.Exec(f.ctx, `INSERT INTO files (user_id, connection_id, folder_id, object_id, name, size, content_type)
		VALUES ($1, $2, $3, 'o1', 'a.txt', 1, 'text/plain')`, person.User.ID, connection, folder)
	if err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if err := f.service.DeleteUser(f.ctx, admin.User.ID, person.User.ID, client); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	for _, table := range []string{"storage_connections", "folders", "files", "sessions"} {
		if got := f.count(t, `SELECT count(*) FROM `+table+` WHERE user_id = $1`, person.User.ID); got != 0 {
			t.Errorf("%s rows left = %d", table, got)
		}
	}
	if _, err := f.service.ResolveSession(f.ctx, person.Token); err == nil {
		t.Error("a deleted user's session still works")
	}
	got := f.count(t, `SELECT count(*) FROM audit_events WHERE action = 'user.deleted'
		AND actor_id = $1 AND metadata->>'email' = 'person@example.org'`, admin.User.ID)
	if got != 1 {
		t.Errorf("audit events = %d, want 1", got)
	}
}

// The log has to say who did what, even after the person's account has gone.
func testDeletedUsersStayInTheAuditLog(t *testing.T, f *fixture) {
	admin := f.register(t, "admin@example.org")
	person := f.register(t, "person@example.org")
	if _, err := f.service.Login(f.ctx, "person@example.org", goodPassword, client); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if err := f.service.DeleteUser(f.ctx, admin.User.ID, person.User.ID, client); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	entries, err := audit.NewStore(f.pool).List(f.ctx, audit.Filter{Action: audit.ActionLogin}, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].ActorEmail == nil || *entries[0].ActorEmail != "person@example.org" {
		t.Errorf("entries = %+v", entries)
	}
}

func testDeleteUserValidates(t *testing.T, f *fixture) {
	admin := f.register(t, "admin@example.org")
	if err := f.service.DeleteUser(f.ctx, admin.User.ID, admin.User.ID, client); !errors.Is(err, ErrOwnAccount) {
		t.Errorf("own account: error = %v", err)
	}
	if err := f.service.DeleteUser(f.ctx, admin.User.ID, uuid.New(), client); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("unknown user: error = %v", err)
	}
	if got := f.count(t, `SELECT count(*) FROM users`); got != 1 {
		t.Errorf("users = %d, want 1", got)
	}
}

func testTransferAdmin(t *testing.T, f *fixture) {
	old := f.register(t, "first@example.org")
	next := f.register(t, "person@example.org")
	if err := f.service.TransferAdmin(f.ctx, "Person@Example.org"); err != nil {
		t.Fatalf("TransferAdmin: %v", err)
	}
	if got := f.count(t, `SELECT count(*) FROM users WHERE role = 'admin' AND email = 'person@example.org'`); got != 1 {
		t.Error("the role didn't move")
	}
	if got := f.count(t, `SELECT count(*) FROM users WHERE role = 'admin'`); got != 1 {
		t.Errorf("admins = %d, want 1", got)
	}
	// Both sessions stay signed in, and the change applies to them straight away.
	was, err := f.service.ResolveSession(f.ctx, old.Token)
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}
	now, err := f.service.ResolveSession(f.ctx, next.Token)
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}
	if was.IsAdmin || !now.IsAdmin {
		t.Errorf("IsAdmin: old %v, new %v", was.IsAdmin, now.IsAdmin)
	}
	got := f.count(t, `SELECT count(*) FROM audit_events WHERE action = 'user.role_changed'
		AND metadata->>'email' = 'person@example.org' AND metadata->>'role' = 'admin'`)
	if got != 1 {
		t.Errorf("audit events = %d, want 1", got)
	}
}

// A typo in the email mustn't leave the server with no admin.
func testTransferAdminToNobodyKeepsTheAdmin(t *testing.T, f *fixture) {
	f.register(t, "first@example.org")
	if err := f.service.TransferAdmin(f.ctx, "nobody@example.org"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("error = %v, want ErrUserNotFound", err)
	}
	if got := f.count(t, `SELECT count(*) FROM users WHERE role = 'admin' AND email = 'first@example.org'`); got != 1 {
		t.Error("the admin lost the role")
	}
}

func testTransferAdminToTheAdmin(t *testing.T, f *fixture) {
	f.register(t, "first@example.org")
	if err := f.service.TransferAdmin(f.ctx, "first@example.org"); err != nil {
		t.Fatalf("TransferAdmin: %v", err)
	}
	if got := f.count(t, `SELECT count(*) FROM users WHERE role = 'admin'`); got != 1 {
		t.Errorf("admins = %d, want 1", got)
	}
}

func testConcurrentTransfers(t *testing.T, f *fixture) {
	f.register(t, "first@example.org")
	emails := []string{"a@example.org", "b@example.org", "c@example.org", "d@example.org"}
	for _, email := range emails {
		f.register(t, email)
	}
	errs := make([]error, len(emails))
	var wg sync.WaitGroup
	for i, email := range emails {
		wg.Go(func() { errs[i] = f.service.TransferAdmin(f.ctx, email) })
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Errorf("TransferAdmin: %v", err)
		}
	}
	if got := f.count(t, `SELECT count(*) FROM users WHERE role = 'admin'`); got != 1 {
		t.Errorf("admins = %d, want 1", got)
	}
}

// The rule holds even for code that forgets it.
func testDatabaseRefusesASecondAdmin(t *testing.T, f *fixture) {
	f.register(t, "first@example.org")
	f.register(t, "person@example.org")
	_, err := f.pool.Exec(f.ctx, `UPDATE users SET role = 'admin' WHERE email = 'person@example.org'`)
	if !isUniqueViolation(err) {
		t.Errorf("error = %v, want a unique violation", err)
	}
}

func testResetPasswordCommand(t *testing.T, f *fixture) {
	admin := f.register(t, "admin@example.org")
	var out bytes.Buffer
	if err := RunCommand(f.ctx, f.service, []string{"reset-password", "Admin@Example.org"}, &out); err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	match := regexp.MustCompile(`Temporary password for \S+: (\S+)`).FindStringSubmatch(out.String())
	if match == nil {
		t.Fatalf("output = %q", out.String())
	}
	if _, err := f.service.ResolveSession(f.ctx, admin.Token); err == nil {
		t.Error("the old session still works")
	}
	issued, err := f.service.Login(f.ctx, "admin@example.org", match[1], client)
	if err != nil {
		t.Fatalf("Login with the printed password: %v", err)
	}
	if !issued.User.MustChangePassword || issued.User.Role != RoleAdmin {
		t.Errorf("user = %+v", issued.User)
	}
	got := f.count(t, `SELECT count(*) FROM audit_events WHERE action = 'user.password_reset' AND ip = 'cli' AND actor_id IS NULL`)
	if got != 1 {
		t.Errorf("audit events = %d, want 1", got)
	}
}

func testResetPasswordCommandUnknownUser(t *testing.T, f *fixture) {
	err := RunCommand(f.ctx, f.service, []string{"reset-password", "nobody@example.org"}, &bytes.Buffer{})
	if !errors.Is(err, ErrUserNotFound) {
		t.Errorf("error = %v, want ErrUserNotFound", err)
	}
}

func testDeleteExpiredSessions(t *testing.T, f *fixture) {
	f.register(t, "admin@example.org")
	stale := f.register(t, "stale@example.org")
	live := f.register(t, "live@example.org")
	if _, err := f.pool.Exec(f.ctx, `UPDATE sessions SET expires_at = now() - interval '1 minute' WHERE user_id = $1`, stale.User.ID); err != nil {
		t.Fatalf("expire session: %v", err)
	}
	deleted, err := f.service.DeleteExpiredSessions(f.ctx)
	if err != nil {
		t.Fatalf("DeleteExpiredSessions: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}
	if got := f.count(t, `SELECT count(*) FROM sessions WHERE user_id = $1`, stale.User.ID); got != 0 {
		t.Errorf("expired sessions left = %d", got)
	}
	if _, err := f.service.ResolveSession(f.ctx, live.Token); err != nil {
		t.Errorf("a live session stopped working: %v", err)
	}
}

func testAPIKeyWaitsForPasswordChange(t *testing.T, f *fixture) {
	admin := f.register(t, "admin@example.org")
	person := f.register(t, "person@example.org")
	_, token, err := f.service.CreateAPIKey(f.ctx, person.User.ID, "script", []string{"files:read"}, client)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if err := f.service.ResetUserPassword(f.ctx, admin.User.ID, person.User.ID, otherPassword, client); err != nil {
		t.Fatalf("ResetUserPassword: %v", err)
	}
	got, err := f.service.ResolveAPIKey(f.ctx, token)
	if err != nil {
		t.Fatalf("ResolveAPIKey: %v", err)
	}
	if !got.MustChangePassword {
		t.Error("an API key carries on while its owner's password needs replacing")
	}
}

func testAdminManagesUsers(t *testing.T, s *server) {
	admin := s.signUp("admin@example.org")

	rec := admin.do(http.MethodPost, "/api/v1/admin/users", credentials("new@example.org"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, body %s", rec.Code, rec.Body)
	}
	created := decode[apigen.ManagedUser](t, rec)
	if !created.MustChangePassword || created.Role != apigen.ManagedUserRoleUser {
		t.Errorf("created = %+v", created)
	}
	wantError(t, admin.do(http.MethodPost, "/api/v1/admin/users", credentials("new@example.org")),
		http.StatusConflict, "email_taken")

	list := admin.do(http.MethodGet, "/api/v1/admin/users", nil)
	if strings.Contains(list.Body.String(), "password_hash") || strings.Contains(list.Body.String(), "argon2") {
		t.Errorf("the list leaks password data: %s", list.Body)
	}
	if users := decode[[]apigen.ManagedUser](t, list); len(users) != 2 {
		t.Errorf("users = %+v", users)
	}

	person := s.browser()
	if rec := person.do(http.MethodPost, "/api/v1/auth/login", credentials("new@example.org")); rec.Code != http.StatusOK {
		t.Fatalf("login: status = %d, body %s", rec.Code, rec.Body)
	}
	// The real router holds them here until the password is replaced.
	wantError(t, person.do(http.MethodGet, "/api/v1/auth/sessions", nil), http.StatusForbidden, "password_change_required")
	me := decode[apigen.User](t, person.do(http.MethodGet, "/api/v1/auth/me", nil))
	if !me.MustChangePassword {
		t.Errorf("me = %+v", me)
	}
	change := map[string]string{"current_password": goodPassword, "new_password": goodPassword}
	wantError(t, person.do(http.MethodPost, "/api/v1/auth/password", change), http.StatusBadRequest, "password_unchanged")
	change["new_password"] = otherPassword
	if rec := person.do(http.MethodPost, "/api/v1/auth/password", change); rec.Code != http.StatusNoContent {
		t.Fatalf("change: status = %d, body %s", rec.Code, rec.Body)
	}
	if rec := person.do(http.MethodGet, "/api/v1/auth/sessions", nil); rec.Code != http.StatusOK {
		t.Errorf("after changing: status = %d, body %s", rec.Code, rec.Body)
	}

	path := "/api/v1/admin/users/" + created.Id.String()
	wantError(t, person.do(http.MethodGet, "/api/v1/admin/users", nil), http.StatusForbidden, "forbidden")
	wantError(t, person.do(http.MethodPost, "/api/v1/admin/users", credentials("x@example.org")), http.StatusForbidden, "forbidden")
	wantError(t, person.do(http.MethodDelete, path, nil), http.StatusForbidden, "forbidden")

	reset := map[string]string{"password": "yet another long password"}
	if rec := admin.do(http.MethodPost, path+"/password", reset); rec.Code != http.StatusNoContent {
		t.Fatalf("reset: status = %d, body %s", rec.Code, rec.Body)
	}
	if rec := person.do(http.MethodGet, "/api/v1/auth/me", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("after a reset: status = %d", rec.Code)
	}

	if rec := admin.do(http.MethodDelete, path, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d, body %s", rec.Code, rec.Body)
	}
	wantError(t, admin.do(http.MethodDelete, path, nil), http.StatusNotFound, "not_found")
}

func testAdminCannotRemoveThemselves(t *testing.T, s *server) {
	admin := s.signUp("admin@example.org")
	me := decode[apigen.User](t, admin.do(http.MethodGet, "/api/v1/auth/me", nil))
	path := "/api/v1/admin/users/" + me.Id.String()
	wantError(t, admin.do(http.MethodDelete, path, nil), http.StatusBadRequest, "own_account")
	wantError(t, admin.do(http.MethodPost, path+"/password", map[string]string{"password": otherPassword}),
		http.StatusBadRequest, "own_account")
}

// Sign-up is public, so it must not reveal which addresses have accounts.
func testSignUpClosesAfterTheFirstAccount(t *testing.T, s *server) {
	s.signUp("admin@example.org")
	config := decode[map[string]bool](t, s.browser().do(http.MethodGet, "/api/v1/auth/config", nil))
	if config["registration_open"] {
		t.Errorf("config = %v", config)
	}
	for _, email := range []string{"new@example.org", "admin@example.org"} {
		wantError(t, s.browser().do(http.MethodPost, "/api/v1/auth/register", credentials(email)),
			http.StatusForbidden, "registration_closed")
	}
}
