package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/swayam5342/dextro/internal/db"
	"github.com/swayam5342/dextro/internal/db/dbtest"
	"github.com/swayam5342/dextro/internal/provider"
	"github.com/swayam5342/dextro/internal/provider/gdrive"
	"github.com/swayam5342/dextro/internal/provider/providertest"
	"github.com/swayam5342/dextro/internal/secrets"
)

const (
	testIP       = "203.0.113.7"
	refreshToken = "the-refresh-token"
)

func testKeyring(t *testing.T) *secrets.Keyring {
	t.Helper()
	k, err := secrets.NewKeyring(map[string][]byte{
		"k1": bytes.Repeat([]byte{1}, 32),
		"k2": bytes.Repeat([]byte{2}, 32),
	}, "k1")
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	return k
}

// fakeDriveAuth stands in for Google's side of connecting an account.
type fakeDriveAuth struct {
	email string
	token string
	err   error
}

func (f *fakeDriveAuth) AuthCodeURL(state, _ string) string {
	return "https://accounts.example/auth?state=" + state
}

func (f *fakeDriveAuth) Connect(context.Context, string, string) (gdrive.Settings, gdrive.Credentials, error) {
	if f.err != nil {
		return gdrive.Settings{}, gdrive.Credentials{}, f.err
	}
	return gdrive.Settings{Email: f.email, FolderID: "folder-1"}, gdrive.Credentials{RefreshToken: f.token}, nil
}

type fixture struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	service *Service
	drive   *fakeDriveAuth

	mu       sync.Mutex
	accounts map[string]*providertest.Fake
	opened   []gdrive.Credentials
	// wrap, if set, changes the provider an account opens to.
	wrap func(provider.Provider) provider.Provider
}

// account returns the in-memory Drive behind an email address.
func (f *fixture) account(email string) *providertest.Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.accounts[email] == nil {
		f.accounts[email] = providertest.NewFake(providertest.ListPageSize)
	}
	return f.accounts[email]
}

func newFixture(t *testing.T, pool *pgxpool.Pool) *fixture {
	t.Helper()
	f := &fixture{
		ctx:      context.Background(),
		pool:     pool,
		drive:    &fakeDriveAuth{email: "person@example.org", token: refreshToken},
		accounts: map[string]*providertest.Fake{},
	}
	f.service = NewService(Deps{
		Store:   NewStore(pool),
		Keyring: testKeyring(t),
		OpenDrive: func(_ context.Context, s gdrive.Settings, c gdrive.Credentials) (provider.Provider, error) {
			f.mu.Lock()
			f.opened = append(f.opened, c)
			f.mu.Unlock()
			if f.wrap != nil {
				return f.wrap(f.account(s.Email)), nil
			}
			return f.account(s.Email), nil
		},
		DriveAuth: f.drive,
	})
	return f
}

func (f *fixture) user(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := f.pool.QueryRow(f.ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`, uuid.NewString()+"@example.org").Scan(&id)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return id
}

// connect links the Drive account with this email to a user.
func (f *fixture) connect(t *testing.T, userID uuid.UUID, email string) Connection {
	t.Helper()
	f.drive.email = email
	connection, err := f.service.ConnectDrive(f.ctx, userID, "code", "verifier", testIP)
	if err != nil {
		t.Fatalf("ConnectDrive: %v", err)
	}
	return connection
}

func (f *fixture) lastOpened(t *testing.T) gdrive.Credentials {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.opened) == 0 {
		t.Fatal("nothing was opened")
	}
	return f.opened[len(f.opened)-1]
}

func (f *fixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(f.ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
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
		{"connecting google drive", testConnectDrive},
		{"tokens are stored encrypted", testTokensAreEncrypted},
		{"reconnecting replaces the old token", testReconnectDrive},
		{"two users can connect the same google account", testDrivePerUser},
		{"a user can connect several accounts", testSeveralAccounts},
		{"a failed connection saves nothing", testDriveFailure},
		{"users see only their own connections", testListIsScoped},
		{"testing a connection decrypts its token", testConnectionTest},
		{"a failing test is reported", testConnectionTestFailure},
		{"users can't use each other's connections", testUseIsScoped},
		{"users can't remove each other's connections", testDeleteIsScoped},
		{"removing a connection forgets it", testDelete},
		{"a secret copied onto another row won't open", testSecretsAreBoundToTheirRow},
		{"old keys keep working after rotation", testRotation},
		{"a missing key is an error", testUnknownKey},
		{"audit events carry no secrets", testAuditHasNoSecrets},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `TRUNCATE users, audit_events CASCADE`); err != nil {
				t.Fatalf("reset database: %v", err)
			}
			tt.run(t, newFixture(t, pool))
		})
	}
}

func testConnectDrive(t *testing.T, f *fixture) {
	userID := f.user(t)
	connection := f.connect(t, userID, "person@example.org")
	if connection.Provider != ProviderGoogleDrive || connection.Summary != "person@example.org" {
		t.Errorf("connection = %+v", connection)
	}
	list, err := f.service.List(f.ctx, userID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != connection.ID {
		t.Errorf("list = %+v", list)
	}
	if got := f.count(t, `SELECT count(*) FROM audit_events WHERE action = 'storage.connected' AND actor_id = $1`, userID); got != 1 {
		t.Errorf("audit events = %d, want 1", got)
	}
}

func testTokensAreEncrypted(t *testing.T, f *fixture) {
	f.connect(t, f.user(t), "person@example.org")
	var ciphertext, settings []byte
	var keyID string
	row := f.pool.QueryRow(f.ctx, `SELECT secret_ciphertext, settings, secret_key_id FROM storage_connections`)
	if err := row.Scan(&ciphertext, &settings, &keyID); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if keyID != "k1" {
		t.Errorf("key ID = %q, want the active key", keyID)
	}
	if bytes.Contains(ciphertext, []byte(refreshToken)) || bytes.Contains(settings, []byte(refreshToken)) {
		t.Error("the refresh token is stored in the clear")
	}
}

func testReconnectDrive(t *testing.T, f *fixture) {
	userID := f.user(t)
	first := f.connect(t, userID, "person@example.org")
	f.drive.token = "a-newer-token"
	second := f.connect(t, userID, "person@example.org")
	if first.ID != second.ID {
		t.Errorf("reconnecting created a new connection")
	}
	if got := f.count(t, `SELECT count(*) FROM storage_connections`); got != 1 {
		t.Errorf("connections = %d, want 1", got)
	}
	if err := f.service.Test(f.ctx, userID, second.ID); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if got := f.lastOpened(t).RefreshToken; got != "a-newer-token" {
		t.Errorf("token = %q", got)
	}
}

func testDrivePerUser(t *testing.T, f *fixture) {
	f.connect(t, f.user(t), "person@example.org")
	f.connect(t, f.user(t), "person@example.org")
	if got := f.count(t, `SELECT count(*) FROM storage_connections`); got != 2 {
		t.Errorf("connections = %d, want 2", got)
	}
}

func testSeveralAccounts(t *testing.T, f *fixture) {
	userID := f.user(t)
	f.connect(t, userID, "one@example.org")
	f.connect(t, userID, "two@example.org")
	list, err := f.service.List(f.ctx, userID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("list = %+v", list)
	}
}

func testDriveFailure(t *testing.T, f *fixture) {
	f.drive.err = errors.New("bad code")
	_, err := f.service.ConnectDrive(f.ctx, f.user(t), "code", "verifier", testIP)
	if !errors.Is(err, ErrConnectionFailed) {
		t.Fatalf("error = %v, want ErrConnectionFailed", err)
	}
	if got := f.count(t, `SELECT count(*) FROM storage_connections`); got != 0 {
		t.Errorf("connections = %d, want 0", got)
	}
}

func testListIsScoped(t *testing.T, f *fixture) {
	mine, theirs := f.user(t), f.user(t)
	f.connect(t, mine, "mine@example.org")
	f.connect(t, theirs, "theirs@example.org")
	list, err := f.service.List(f.ctx, mine)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Summary != "mine@example.org" {
		t.Errorf("list = %+v", list)
	}
}

func testConnectionTest(t *testing.T, f *fixture) {
	userID := f.user(t)
	connection := f.connect(t, userID, "person@example.org")
	if err := f.service.Test(f.ctx, userID, connection.ID); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if got := f.lastOpened(t).RefreshToken; got != refreshToken {
		t.Errorf("token = %q", got)
	}
}

func testConnectionTestFailure(t *testing.T, f *fixture) {
	userID := f.user(t)
	connection := f.connect(t, userID, "person@example.org")
	f.account("person@example.org").CheckErr = errors.New("expired")
	if err := f.service.Test(f.ctx, userID, connection.ID); !errors.Is(err, ErrConnectionFailed) {
		t.Errorf("error = %v, want ErrConnectionFailed", err)
	}
}

func testUseIsScoped(t *testing.T, f *fixture) {
	owner, other := f.user(t), f.user(t)
	connection := f.connect(t, owner, "person@example.org")
	if err := f.service.Test(f.ctx, other, connection.ID); !errors.Is(err, ErrConnectionNotFound) {
		t.Errorf("Test: error = %v, want ErrConnectionNotFound", err)
	}
	if _, err := f.service.Provider(f.ctx, other, connection.ID); !errors.Is(err, ErrConnectionNotFound) {
		t.Errorf("Provider: error = %v, want ErrConnectionNotFound", err)
	}
	if len(f.opened) != 0 {
		t.Error("someone else's credentials were decrypted")
	}
}

func testDeleteIsScoped(t *testing.T, f *fixture) {
	owner, other := f.user(t), f.user(t)
	connection := f.connect(t, owner, "person@example.org")
	if err := f.service.Delete(f.ctx, other, connection.ID, testIP); !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("error = %v, want ErrConnectionNotFound", err)
	}
	if got := f.count(t, `SELECT count(*) FROM storage_connections`); got != 1 {
		t.Errorf("connections = %d, want 1", got)
	}
}

func testDelete(t *testing.T, f *fixture) {
	userID := f.user(t)
	connection := f.connect(t, userID, "person@example.org")
	stored := f.account("person@example.org")
	object, err := stored.Put(f.ctx, provider.Upload{Name: "keep.txt", Body: strings.NewReader("x")})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := f.service.Delete(f.ctx, userID, connection.ID, testIP); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := f.count(t, `SELECT count(*) FROM storage_connections`); got != 0 {
		t.Errorf("connections = %d, want 0", got)
	}
	if got := f.count(t, `SELECT count(*) FROM audit_events WHERE action = 'storage.removed'`); got != 1 {
		t.Errorf("audit events = %d, want 1", got)
	}
	if _, err := stored.Get(f.ctx, object.ID, nil); err != nil {
		t.Errorf("the account's files must stay: %v", err)
	}
}

func testSecretsAreBoundToTheirRow(t *testing.T, f *fixture) {
	victim, attacker := f.user(t), f.user(t)
	stolen := f.connect(t, victim, "victim@example.org")
	own := f.connect(t, attacker, "attacker@example.org")
	// Someone who can write to the database moves a secret onto their own row.
	_, err := f.pool.Exec(f.ctx, `UPDATE storage_connections
		SET secret_ciphertext = (SELECT secret_ciphertext FROM storage_connections WHERE id = $1)
		WHERE id = $2`, stolen.ID, own.ID)
	if err != nil {
		t.Fatalf("swap secrets: %v", err)
	}
	if err := f.service.Test(f.ctx, attacker, own.ID); !errors.Is(err, secrets.ErrDecrypt) {
		t.Errorf("error = %v, want ErrDecrypt", err)
	}
}

func testRotation(t *testing.T, f *fixture) {
	userID := f.user(t)
	connection := f.connect(t, userID, "old@example.org")
	rotated, err := secrets.NewKeyring(map[string][]byte{
		"k1": bytes.Repeat([]byte{1}, 32),
		"k2": bytes.Repeat([]byte{2}, 32),
	}, "k2")
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	f.service.keyring = rotated
	if err := f.service.Test(f.ctx, userID, connection.ID); err != nil {
		t.Errorf("value sealed with the old key: %v", err)
	}
	next := f.connect(t, userID, "new@example.org")
	if got := f.count(t, `SELECT count(*) FROM storage_connections WHERE id = $1 AND secret_key_id = 'k2'`, next.ID); got != 1 {
		t.Error("a new value didn't use the active key")
	}
}

func testUnknownKey(t *testing.T, f *fixture) {
	userID := f.user(t)
	connection := f.connect(t, userID, "person@example.org")
	if _, err := f.pool.Exec(f.ctx, `UPDATE storage_connections SET secret_key_id = 'retired'`); err != nil {
		t.Fatalf("change key: %v", err)
	}
	if err := f.service.Test(f.ctx, userID, connection.ID); !errors.Is(err, secrets.ErrUnknownKey) {
		t.Errorf("error = %v, want ErrUnknownKey", err)
	}
}

func testAuditHasNoSecrets(t *testing.T, f *fixture) {
	userID := f.user(t)
	connection := f.connect(t, userID, "person@example.org")
	if err := f.service.Delete(f.ctx, userID, connection.ID, testIP); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	rows, err := f.pool.Query(f.ctx, `SELECT metadata FROM audit_events`)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var metadata json.RawMessage
		if err := rows.Scan(&metadata); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if bytes.Contains(metadata, []byte(refreshToken)) {
			t.Errorf("audit event contains the token: %s", metadata)
		}
	}
}
