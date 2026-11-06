package cli

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/swayam5342/dextro/internal/admin"
	"github.com/swayam5342/dextro/internal/auth"
	"github.com/swayam5342/dextro/internal/db"
	"github.com/swayam5342/dextro/internal/db/dbtest"
	"github.com/swayam5342/dextro/internal/files"
	"github.com/swayam5342/dextro/internal/secrets"
	"github.com/swayam5342/dextro/internal/storage"
)

type fixture struct {
	ctx  context.Context
	pool *pgxpool.Pool
	deps Deps
}

func (f *fixture) run(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := Run(f.ctx, f.deps, args, &out); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	return out.String()
}

func (f *fixture) count(t *testing.T, query string) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(f.ctx, query).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func (f *fixture) seed(t *testing.T, email, role string) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO users (email, role) VALUES ($1, $2)`, email, role); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestCommands(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	keyring, err := secrets.NewKeyring(map[string][]byte{"k1": bytes.Repeat([]byte{7}, 32)}, "k1")
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	service := storage.NewService(storage.Deps{Store: storage.NewStore(pool), Keyring: keyring, OpenDrive: storage.NewOpenDrive(nil)})
	f := &fixture{ctx: ctx, pool: pool, deps: Deps{
		Auth:  auth.NewService(auth.NewStore(pool)),
		Admin: admin.NewService(admin.NewStore(pool), service, nil),
		Files: files.NewService(files.NewStore(pool), service),
	}}
	tests := []struct {
		name string
		run  func(*testing.T, *fixture)
	}{
		{"help lists the commands", testHelp},
		{"anything else gets the usage", testUsage},
		{"users shows counts, not files", testUsers},
		{"a new user gets a password that has to change", testCreateUser},
		{"the first account can't come from here", testCreateFirstUser},
		{"a taken email is refused", testCreateTakenEmail},
		{"deleting asks first", testDeleteAsksFirst},
		{"a user can be deleted", testDeleteUser},
		{"the admin can't be deleted", testDeleteAdmin},
		{"someone who doesn't exist can't be deleted", testDeleteNobody},
		{"a password can be reset", testResetPassword},
		{"the admin role can move", testTransferAdmin},
		{"links can be shown and set", testLinks},
		{"a bad links value is refused", testLinksBadValue},
		{"the upload limit can be shown and set", testUploadLimit},
		{"a bad upload limit is refused", testUploadLimitBadValue},
		{"status reports on the server", testStatus},
		{"sync with nothing linked is fine", testSync},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `TRUNCATE users, settings, audit_events, job_runs CASCADE`); err != nil {
				t.Fatalf("reset database: %v", err)
			}
			tt.run(t, f)
		})
	}
}

func testHelp(t *testing.T, f *fixture) {
	for _, flag := range []string{"help", "--help", "-h"} {
		out := f.run(t, flag)
		for _, want := range []string{"status", "create-user", "delete-user", "reset-password", "transfer-admin", "links", "upload-limit", "sync"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s doesn't mention %s", flag, want)
			}
		}
	}
}

func testUsage(t *testing.T, f *fixture) {
	for _, args := range [][]string{
		{"bogus"},
		{"set-role", "a@example.org"},
		{"transfer-admin"},
		{"reset-password"},
		{"transfer-admin", "a@example.org", "extra"},
		{"create-user"},
		{"delete-user", "a@example.org", "--force"},
		{"users", "extra"},
	} {
		err := Run(f.ctx, f.deps, args, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Errorf("%v: error = %v, want usage", args, err)
		}
	}
}

func testUsers(t *testing.T, f *fixture) {
	f.seed(t, "boss@example.org", "admin")
	f.seed(t, "person@example.org", "user")
	out := f.run(t, "users")
	for _, want := range []string{"boss@example.org", "admin", "person@example.org", "ACCOUNTS"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want %q", out, want)
		}
	}
}

func testCreateUser(t *testing.T, f *fixture) {
	f.seed(t, "boss@example.org", "admin")
	out := f.run(t, "create-user", "new@example.org")
	match := regexp.MustCompile(`Temporary password for \S+: (\S+)`).FindStringSubmatch(out)
	if match == nil {
		t.Fatalf("output = %q", out)
	}
	issued, err := f.deps.Auth.Login(f.ctx, "new@example.org", match[1], auth.Client{})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !issued.User.MustChangePassword || issued.User.Role == auth.RoleAdmin {
		t.Errorf("user = %+v", issued.User)
	}
	if got := f.count(t, `SELECT count(*) FROM audit_events WHERE action = 'user.created' AND ip = 'cli' AND actor_id IS NULL`); got != 1 {
		t.Errorf("%d audit events", got)
	}
}

func testCreateFirstUser(t *testing.T, f *fixture) {
	err := Run(f.ctx, f.deps, []string{"create-user", "first@example.org"}, &bytes.Buffer{})
	if !errors.Is(err, auth.ErrNoAdmin) {
		t.Errorf("error = %v, want ErrNoAdmin", err)
	}
	if got := f.count(t, `SELECT count(*) FROM users`); got != 0 {
		t.Errorf("%d users: sign-up would now be closed with no admin", got)
	}
}

func testCreateTakenEmail(t *testing.T, f *fixture) {
	f.seed(t, "boss@example.org", "admin")
	err := Run(f.ctx, f.deps, []string{"create-user", "boss@example.org"}, &bytes.Buffer{})
	if !errors.Is(err, auth.ErrEmailTaken) {
		t.Errorf("error = %v, want ErrEmailTaken", err)
	}
}

func testDeleteAsksFirst(t *testing.T, f *fixture) {
	f.seed(t, "boss@example.org", "admin")
	f.seed(t, "person@example.org", "user")
	out := f.run(t, "delete-user", "person@example.org")
	if !strings.Contains(out, "--yes") || !strings.Contains(out, "stay in Google Drive") {
		t.Errorf("output = %q", out)
	}
	if got := f.count(t, `SELECT count(*) FROM users WHERE email = 'person@example.org'`); got != 1 {
		t.Error("it deleted without --yes")
	}
}

func testDeleteUser(t *testing.T, f *fixture) {
	f.seed(t, "boss@example.org", "admin")
	f.seed(t, "person@example.org", "user")
	f.run(t, "delete-user", "person@example.org", "--yes")
	if got := f.count(t, `SELECT count(*) FROM users WHERE email = 'person@example.org'`); got != 0 {
		t.Error("the user is still there")
	}
	if got := f.count(t, `SELECT count(*) FROM audit_events WHERE action = 'user.deleted' AND ip = 'cli'`); got != 1 {
		t.Errorf("%d audit events", got)
	}
}

func testDeleteAdmin(t *testing.T, f *fixture) {
	f.seed(t, "boss@example.org", "admin")
	err := Run(f.ctx, f.deps, []string{"delete-user", "boss@example.org", "--yes"}, &bytes.Buffer{})
	if !errors.Is(err, auth.ErrAdminAccount) {
		t.Errorf("error = %v, want ErrAdminAccount", err)
	}
	if got := f.count(t, `SELECT count(*) FROM users`); got != 1 {
		t.Error("the only admin went")
	}
}

func testDeleteNobody(t *testing.T, f *fixture) {
	err := Run(f.ctx, f.deps, []string{"delete-user", "nobody@example.org", "--yes"}, &bytes.Buffer{})
	if !errors.Is(err, auth.ErrUserNotFound) {
		t.Errorf("error = %v, want ErrUserNotFound", err)
	}
}

func testResetPassword(t *testing.T, f *fixture) {
	f.seed(t, "boss@example.org", "admin")
	out := f.run(t, "reset-password", "Boss@Example.org")
	match := regexp.MustCompile(`Temporary password for \S+: (\S+)`).FindStringSubmatch(out)
	if match == nil {
		t.Fatalf("output = %q", out)
	}
	if _, err := f.deps.Auth.Login(f.ctx, "boss@example.org", match[1], auth.Client{}); err != nil {
		t.Errorf("Login: %v", err)
	}
}

func testTransferAdmin(t *testing.T, f *fixture) {
	f.seed(t, "boss@example.org", "admin")
	f.seed(t, "person@example.org", "user")
	if out := f.run(t, "transfer-admin", "person@example.org"); !strings.Contains(out, "person@example.org") {
		t.Errorf("output = %q", out)
	}
	if got := f.count(t, `SELECT count(*) FROM users WHERE role = 'admin' AND email = 'person@example.org'`); got != 1 {
		t.Error("the role didn't move")
	}
}

func testLinks(t *testing.T, f *fixture) {
	if out := f.run(t, "links"); !strings.Contains(out, "off") {
		t.Errorf("output = %q", out)
	}
	if out := f.run(t, "links", "on"); !strings.Contains(out, "are on") {
		t.Errorf("output = %q", out)
	}
	if on, err := f.deps.Admin.Settings(f.ctx); err != nil || !on.GoogleLinks {
		t.Errorf("settings = %+v, error %v", on, err)
	}
	if got := f.count(t, `SELECT count(*) FROM audit_events WHERE action = 'settings.updated' AND ip = 'cli'`); got != 1 {
		t.Errorf("%d audit events", got)
	}
	if out := f.run(t, "links", "off"); !strings.Contains(out, "are off") {
		t.Errorf("output = %q", out)
	}
}

func testLinksBadValue(t *testing.T, f *fixture) {
	err := Run(f.ctx, f.deps, []string{"links", "maybe"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Errorf("error = %v, want usage", err)
	}
}

func testUploadLimit(t *testing.T, f *fixture) {
	if out := f.run(t, "upload-limit"); !strings.Contains(out, "10.0 GiB") {
		t.Errorf("output = %q", out)
	}
	if out := f.run(t, "upload-limit", "2.5"); !strings.Contains(out, "2.5 GiB") {
		t.Errorf("output = %q", out)
	}
}

func testUploadLimitBadValue(t *testing.T, f *fixture) {
	if err := Run(f.ctx, f.deps, []string{"upload-limit", "lots"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Errorf("not a number: error = %v", err)
	}
	if err := Run(f.ctx, f.deps, []string{"upload-limit", "-1"}, &bytes.Buffer{}); !errors.Is(err, admin.ErrInvalidSettings) {
		t.Errorf("negative: error = %v", err)
	}
}

func testStatus(t *testing.T, f *fixture) {
	f.seed(t, "boss@example.org", "admin")
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO job_runs (name, finished_at, succeeded) VALUES ('file_sync', now(), true), ('quota_sync', now(), false)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	out := f.run(t, "status")
	for _, want := range []string{"Users", "1", "Linked accounts", "Google links", "off", "file_sync", "working", "quota_sync", "failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want %q", out, want)
		}
	}
}

func testSync(t *testing.T, f *fixture) {
	if out := f.run(t, "sync"); !strings.Contains(out, "0 found") {
		t.Errorf("output = %q", out)
	}
}
