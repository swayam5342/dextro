package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/swayam5342/dextro/internal/db"
	"github.com/swayam5342/dextro/internal/db/dbtest"
	"github.com/swayam5342/dextro/internal/netguard"
	"github.com/swayam5342/dextro/internal/provider"
	"github.com/swayam5342/dextro/internal/provider/gdrive"
	"github.com/swayam5342/dextro/internal/provider/providertest"
	"github.com/swayam5342/dextro/internal/provider/s3"
	"github.com/swayam5342/dextro/internal/secrets"
)

const (
	testIP       = "203.0.113.7"
	testEndpoint = "https://s3.dextro.test"
	secretKey    = "the-secret-access-key"
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
	ctx      context.Context
	pool     *pgxpool.Pool
	service  *Service
	fake     *providertest.Fake
	drive    *fakeDriveAuth
	gotS3    []s3.Credentials
	gotDrive []gdrive.Credentials
}

func newFixture(t *testing.T, pool *pgxpool.Pool) *fixture {
	t.Helper()
	f := &fixture{
		ctx:   context.Background(),
		pool:  pool,
		fake:  providertest.NewFake(providertest.ListPageSize),
		drive: &fakeDriveAuth{email: "person@example.org", token: refreshToken},
	}
	f.service = NewService(Deps{
		Store:   NewStore(pool),
		Keyring: testKeyring(t),
		Guard:   netguard.New([]string{"s3.dextro.test"}),
		Openers: Openers{
			S3: func(_ s3.Settings, c s3.Credentials) (provider.Provider, error) {
				f.gotS3 = append(f.gotS3, c)
				return f.fake, nil
			},
			Drive: func(_ context.Context, _ gdrive.Settings, c gdrive.Credentials) (provider.Provider, error) {
				f.gotDrive = append(f.gotDrive, c)
				return f.fake, nil
			},
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

func s3Input(name string) S3Input {
	return S3Input{
		Name:            name,
		Endpoint:        testEndpoint,
		Bucket:          "my-bucket",
		AccessKeyID:     "the-access-key",
		SecretAccessKey: secretKey,
	}
}

func (f *fixture) connect(t *testing.T, userID uuid.UUID, name string) Connection {
	t.Helper()
	connection, err := f.service.CreateS3(f.ctx, userID, s3Input(name), testIP)
	if err != nil {
		t.Fatalf("CreateS3: %v", err)
	}
	return connection
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
		{"connecting s3 saves it", testCreateS3},
		{"credentials are stored encrypted", testCredentialsAreEncrypted},
		{"the region defaults", testRegionDefaults},
		{"the region is kept when given", testRegionKept},
		{"endpoints are checked before anything else happens", testEndpointRefused},
		{"details that don't work are not saved", testFailedCheckSavesNothing},
		{"bad input is refused", testValidation},
		{"names are unique per user", testNamesAreUniquePerUser},
		{"users see only their own connections", testListIsScoped},
		{"testing a connection decrypts its credentials", testConnectionTest},
		{"a failing test is reported", testConnectionTestFailure},
		{"users can't use each other's connections", testUseIsScoped},
		{"users can't remove each other's connections", testDeleteIsScoped},
		{"removing a connection forgets it", testDelete},
		{"a secret copied onto another row won't open", testSecretsAreBoundToTheirRow},
		{"old keys keep working after rotation", testRotation},
		{"a missing key is an error", testUnknownKey},
		{"connecting google drive", testConnectDrive},
		{"reconnecting replaces the old token", testReconnectDrive},
		{"two users can connect the same google account", testDrivePerUser},
		{"a failed google connection saves nothing", testDriveFailure},
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

func testCreateS3(t *testing.T, f *fixture) {
	userID := f.user(t)
	connection := f.connect(t, userID, "Backups")
	if connection.Name != "Backups" || connection.Provider != ProviderS3 {
		t.Errorf("connection = %+v", connection)
	}
	if connection.Summary != "my-bucket on s3.dextro.test" {
		t.Errorf("Summary = %q", connection.Summary)
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

func testCredentialsAreEncrypted(t *testing.T, f *fixture) {
	connection := f.connect(t, f.user(t), "Backups")
	var settings, ciphertext []byte
	var keyID string
	err := f.pool.QueryRow(f.ctx, `SELECT settings, secret_key_id, secret_ciphertext FROM storage_connections WHERE id = $1`, connection.ID).
		Scan(&settings, &keyID, &ciphertext)
	if err != nil {
		t.Fatalf("read row: %v", err)
	}
	if keyID != "k1" {
		t.Errorf("key ID = %q, want the active key", keyID)
	}
	for _, secret := range []string{secretKey, "the-access-key"} {
		if bytes.Contains(ciphertext, []byte(secret)) || bytes.Contains(settings, []byte(secret)) {
			t.Errorf("%q is stored in the clear", secret)
		}
	}
}

func testRegionDefaults(t *testing.T, f *fixture) {
	connection := f.connect(t, f.user(t), "Backups")
	var region string
	if err := f.pool.QueryRow(f.ctx, `SELECT settings->>'region' FROM storage_connections WHERE id = $1`, connection.ID).Scan(&region); err != nil {
		t.Fatalf("read region: %v", err)
	}
	if region != "us-east-1" {
		t.Errorf("region = %q", region)
	}
}

func testRegionKept(t *testing.T, f *fixture) {
	in := s3Input("Backups")
	in.Region = "eu-west-3"
	connection, err := f.service.CreateS3(f.ctx, f.user(t), in, testIP)
	if err != nil {
		t.Fatalf("CreateS3: %v", err)
	}
	var region string
	if err := f.pool.QueryRow(f.ctx, `SELECT settings->>'region' FROM storage_connections WHERE id = $1`, connection.ID).Scan(&region); err != nil {
		t.Fatalf("read region: %v", err)
	}
	if region != "eu-west-3" {
		t.Errorf("region = %q", region)
	}
}

func testEndpointRefused(t *testing.T, f *fixture) {
	for _, endpoint := range []string{"https://127.0.0.1", "https://169.254.169.254", "http://s3.public.example", "not a url"} {
		in := s3Input("Backups")
		in.Endpoint = endpoint
		if _, err := f.service.CreateS3(f.ctx, f.user(t), in, testIP); !errors.Is(err, ErrEndpointNotAllowed) {
			t.Errorf("%s: error = %v, want ErrEndpointNotAllowed", endpoint, err)
		}
	}
	if len(f.gotS3) != 0 {
		t.Error("a refused endpoint was still contacted")
	}
}

func testFailedCheckSavesNothing(t *testing.T, f *fixture) {
	f.fake.CheckErr = errors.New("access denied")
	_, err := f.service.CreateS3(f.ctx, f.user(t), s3Input("Backups"), testIP)
	if !errors.Is(err, ErrConnectionFailed) {
		t.Fatalf("error = %v, want ErrConnectionFailed", err)
	}
	if got := f.count(t, `SELECT count(*) FROM storage_connections`); got != 0 {
		t.Errorf("connections = %d, want 0", got)
	}
}

func testValidation(t *testing.T, f *fixture) {
	userID := f.user(t)
	tests := []struct {
		name   string
		change func(*S3Input)
	}{
		{"no name", func(in *S3Input) { in.Name = "   " }},
		{"a name too long", func(in *S3Input) { in.Name = strings.Repeat("x", 101) }},
		{"a control character in the name", func(in *S3Input) { in.Name = "a\nb" }},
		{"no bucket", func(in *S3Input) { in.Bucket = "" }},
		{"a bucket with slashes", func(in *S3Input) { in.Bucket = "../other" }},
		{"a bad region", func(in *S3Input) { in.Region = "us east" }},
		{"no access key", func(in *S3Input) { in.AccessKeyID = "" }},
		{"no secret key", func(in *S3Input) { in.SecretAccessKey = "" }},
		{"an access key too long", func(in *S3Input) { in.AccessKeyID = strings.Repeat("x", 513) }},
		{"a secret key too long", func(in *S3Input) { in.SecretAccessKey = strings.Repeat("x", 513) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := s3Input("Backups")
			tt.change(&in)
			if _, err := f.service.CreateS3(f.ctx, userID, in, testIP); !errors.Is(err, ErrInvalidConnection) {
				t.Errorf("error = %v, want ErrInvalidConnection", err)
			}
		})
	}
}

func testNamesAreUniquePerUser(t *testing.T, f *fixture) {
	first, second := f.user(t), f.user(t)
	f.connect(t, first, "Backups")
	if _, err := f.service.CreateS3(f.ctx, first, s3Input(" Backups "), testIP); !errors.Is(err, ErrNameTaken) {
		t.Errorf("same user: error = %v, want ErrNameTaken", err)
	}
	f.connect(t, second, "Backups")
}

func testListIsScoped(t *testing.T, f *fixture) {
	mine, theirs := f.user(t), f.user(t)
	f.connect(t, mine, "Mine")
	f.connect(t, theirs, "Theirs")
	list, err := f.service.List(f.ctx, mine)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Name != "Mine" {
		t.Errorf("list = %+v", list)
	}
}

func testConnectionTest(t *testing.T, f *fixture) {
	userID := f.user(t)
	connection := f.connect(t, userID, "Backups")
	f.gotS3 = nil
	if err := f.service.Test(f.ctx, userID, connection.ID); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(f.gotS3) != 1 || f.gotS3[0].SecretAccessKey != secretKey {
		t.Errorf("credentials = %+v", f.gotS3)
	}
}

func testConnectionTestFailure(t *testing.T, f *fixture) {
	userID := f.user(t)
	connection := f.connect(t, userID, "Backups")
	f.fake.CheckErr = errors.New("expired")
	if err := f.service.Test(f.ctx, userID, connection.ID); !errors.Is(err, ErrConnectionFailed) {
		t.Errorf("error = %v, want ErrConnectionFailed", err)
	}
}

func testUseIsScoped(t *testing.T, f *fixture) {
	owner, other := f.user(t), f.user(t)
	connection := f.connect(t, owner, "Backups")
	f.gotS3 = nil
	if err := f.service.Test(f.ctx, other, connection.ID); !errors.Is(err, ErrConnectionNotFound) {
		t.Errorf("Test: error = %v, want ErrConnectionNotFound", err)
	}
	if _, err := f.service.Provider(f.ctx, other, connection.ID); !errors.Is(err, ErrConnectionNotFound) {
		t.Errorf("Provider: error = %v, want ErrConnectionNotFound", err)
	}
	if len(f.gotS3) != 0 {
		t.Error("someone else's credentials were decrypted")
	}
}

func testDeleteIsScoped(t *testing.T, f *fixture) {
	owner, other := f.user(t), f.user(t)
	connection := f.connect(t, owner, "Backups")
	if err := f.service.Delete(f.ctx, other, connection.ID, testIP); !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("error = %v, want ErrConnectionNotFound", err)
	}
	if got := f.count(t, `SELECT count(*) FROM storage_connections`); got != 1 {
		t.Errorf("connections = %d, want 1", got)
	}
}

func testDelete(t *testing.T, f *fixture) {
	userID := f.user(t)
	connection := f.connect(t, userID, "Backups")
	object, err := f.fake.Put(f.ctx, provider.Upload{Name: "keep.txt", Body: strings.NewReader("x")})
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
	if _, err := f.fake.Get(f.ctx, object.ID, nil); err != nil {
		t.Errorf("the account's files must stay: %v", err)
	}
}

func testSecretsAreBoundToTheirRow(t *testing.T, f *fixture) {
	victim, attacker := f.user(t), f.user(t)
	stolen := f.connect(t, victim, "Theirs")
	own := f.connect(t, attacker, "Mine")
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
	connection := f.connect(t, userID, "Backups")
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
	next := f.connect(t, userID, "Newer")
	if got := f.count(t, `SELECT count(*) FROM storage_connections WHERE id = $1 AND secret_key_id = 'k2'`, next.ID); got != 1 {
		t.Error("a new value didn't use the active key")
	}
}

func testUnknownKey(t *testing.T, f *fixture) {
	userID := f.user(t)
	connection := f.connect(t, userID, "Backups")
	if _, err := f.pool.Exec(f.ctx, `UPDATE storage_connections SET secret_key_id = 'retired'`); err != nil {
		t.Fatalf("change key: %v", err)
	}
	if err := f.service.Test(f.ctx, userID, connection.ID); !errors.Is(err, secrets.ErrUnknownKey) {
		t.Errorf("error = %v, want ErrUnknownKey", err)
	}
}

func testConnectDrive(t *testing.T, f *fixture) {
	userID := f.user(t)
	connection, err := f.service.ConnectDrive(f.ctx, userID, "code", "verifier", testIP)
	if err != nil {
		t.Fatalf("ConnectDrive: %v", err)
	}
	if connection.Provider != ProviderGoogleDrive || connection.Summary != "person@example.org" {
		t.Errorf("connection = %+v", connection)
	}
	if err := f.service.Test(f.ctx, userID, connection.ID); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if len(f.gotDrive) == 0 || f.gotDrive[len(f.gotDrive)-1].RefreshToken != refreshToken {
		t.Errorf("credentials = %+v", f.gotDrive)
	}
	var ciphertext, settings []byte
	row := f.pool.QueryRow(f.ctx, `SELECT secret_ciphertext, settings FROM storage_connections`)
	if err := row.Scan(&ciphertext, &settings); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if bytes.Contains(ciphertext, []byte(refreshToken)) || bytes.Contains(settings, []byte(refreshToken)) {
		t.Error("the refresh token is stored in the clear")
	}
}

func testReconnectDrive(t *testing.T, f *fixture) {
	userID := f.user(t)
	first, err := f.service.ConnectDrive(f.ctx, userID, "code", "verifier", testIP)
	if err != nil {
		t.Fatalf("ConnectDrive: %v", err)
	}
	f.drive.token = "a-newer-token"
	second, err := f.service.ConnectDrive(f.ctx, userID, "code", "verifier", testIP)
	if err != nil {
		t.Fatalf("ConnectDrive again: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("reconnecting created a new connection")
	}
	if got := f.count(t, `SELECT count(*) FROM storage_connections`); got != 1 {
		t.Errorf("connections = %d, want 1", got)
	}
	if err := f.service.Test(f.ctx, userID, second.ID); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if got := f.gotDrive[len(f.gotDrive)-1].RefreshToken; got != "a-newer-token" {
		t.Errorf("token = %q", got)
	}
}

func testDrivePerUser(t *testing.T, f *fixture) {
	for range 2 {
		if _, err := f.service.ConnectDrive(f.ctx, f.user(t), "code", "verifier", testIP); err != nil {
			t.Fatalf("ConnectDrive: %v", err)
		}
	}
	if got := f.count(t, `SELECT count(*) FROM storage_connections`); got != 2 {
		t.Errorf("connections = %d, want 2", got)
	}
}

func testDriveFailure(t *testing.T, f *fixture) {
	f.drive.err = errors.New("bad code")
	if _, err := f.service.ConnectDrive(f.ctx, f.user(t), "code", "verifier", testIP); err == nil {
		t.Fatal("ConnectDrive succeeded, want error")
	}
	if got := f.count(t, `SELECT count(*) FROM storage_connections`); got != 0 {
		t.Errorf("connections = %d, want 0", got)
	}
}

func testAuditHasNoSecrets(t *testing.T, f *fixture) {
	userID := f.user(t)
	connection := f.connect(t, userID, "Backups")
	if _, err := f.service.ConnectDrive(f.ctx, userID, "code", "verifier", testIP); err != nil {
		t.Fatalf("ConnectDrive: %v", err)
	}
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
		for _, secret := range []string{secretKey, refreshToken, "the-access-key"} {
			if bytes.Contains(metadata, []byte(secret)) {
				t.Errorf("audit event contains %q: %s", secret, metadata)
			}
		}
	}
}
