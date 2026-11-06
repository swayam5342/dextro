package files

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/swayam5342/dextro/internal/db"
	"github.com/swayam5342/dextro/internal/db/dbtest"
	"github.com/swayam5342/dextro/internal/httpx"
	"github.com/swayam5342/dextro/internal/httpx/apigen"
	"github.com/swayam5342/dextro/internal/provider"
	"github.com/swayam5342/dextro/internal/provider/gdrive"
	"github.com/swayam5342/dextro/internal/provider/providertest"
	"github.com/swayam5342/dextro/internal/secrets"
	"github.com/swayam5342/dextro/internal/storage"
)

const align = provider.ChunkAlignment

// connectFake stands in for Google's side of linking an account.
type connectFake struct{ email string }

func (c *connectFake) AuthCodeURL(string, string) string { return "" }

func (c *connectFake) Connect(context.Context, string, string) (gdrive.Settings, gdrive.Credentials, error) {
	return gdrive.Settings{Email: c.email, FolderID: "folder"}, gdrive.Credentials{RefreshToken: "token"}, nil
}

// uploadFixture uses the real storage service, because reservations and sealing
// are what these tests are about. Only Google is fake.
type uploadFixture struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	service *Service
	storage *storage.Service
	auth    *connectFake

	mu       sync.Mutex
	accounts map[string]*providertest.Fake
	// wrap, if set, changes the provider an account opens to.
	wrap func(provider.Provider) provider.Provider
}

func (f *uploadFixture) account(email string) *providertest.Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.accounts[email] == nil {
		f.accounts[email] = providertest.NewFake(100)
	}
	return f.accounts[email]
}

func newUploadFixture(t *testing.T, pool *pgxpool.Pool) *uploadFixture {
	t.Helper()
	keyring, err := secrets.NewKeyring(map[string][]byte{"k1": bytes.Repeat([]byte{7}, 32)}, "k1")
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	f := &uploadFixture{ctx: context.Background(), pool: pool, auth: &connectFake{}, accounts: map[string]*providertest.Fake{}}
	f.storage = storage.NewService(storage.Deps{
		Store:   storage.NewStore(pool),
		Keyring: keyring,
		OpenDrive: func(_ context.Context, s gdrive.Settings, _ gdrive.Credentials) (provider.Provider, error) {
			if f.wrap != nil {
				return f.wrap(f.account(s.Email)), nil
			}
			return f.account(s.Email), nil
		},
		DriveAuth: f.auth,
	})
	f.service = NewService(NewStore(pool), f.storage)
	return f
}

func (f *uploadFixture) user(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := f.pool.QueryRow(f.ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`, uuid.NewString()+"@example.org").Scan(&id)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return id
}

// link connects an account with this much free space.
func (f *uploadFixture) link(t *testing.T, userID uuid.UUID, email string, free int64) uuid.UUID {
	t.Helper()
	account := f.account(email)
	account.Limit = free
	f.auth.email = email
	connection, err := f.storage.ConnectDrive(f.ctx, userID, "code", "verifier", "203.0.113.7")
	if err != nil {
		t.Fatalf("ConnectDrive: %v", err)
	}
	return connection.ID
}

func (f *uploadFixture) begin(t *testing.T, userID uuid.UUID, size int64) UploadSession {
	t.Helper()
	session, err := f.service.BeginUpload(f.ctx, userID, BeginUploadInput{Name: "big.bin", ContentType: "text/plain", Size: size, LastModified: 1234})
	if err != nil {
		t.Fatalf("BeginUpload: %v", err)
	}
	return session
}

func (f *uploadFixture) put(t *testing.T, userID uuid.UUID, id uuid.UUID, offset int64, body string) UploadSession {
	t.Helper()
	session, err := f.service.PutUploadChunk(f.ctx, userID, id, offset, int64(len(body)), strings.NewReader(body))
	if err != nil {
		t.Fatalf("PutUploadChunk(%d): %v", offset, err)
	}
	return session
}

func (f *uploadFixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(f.ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// address reads an upload's real session address, as only the server can.
func (f *uploadFixture) address(t *testing.T, userID, id uuid.UUID) string {
	t.Helper()
	r, err := NewStore(f.pool).UploadSessionForOwner(f.ctx, userID, id)
	if err != nil {
		t.Fatalf("UploadSessionForOwner: %v", err)
	}
	address, err := f.storage.OpenUploadSession(id, *r.sealed)
	if err != nil {
		t.Fatalf("OpenUploadSession: %v", err)
	}
	return address
}

func TestChunkedUploads(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	tests := []struct {
		name string
		run  func(*testing.T, *uploadFixture)
	}{
		{"chunks become a file", testChunksBecomeAFile},
		{"a file lands in the chosen folder", testChunkedUploadFolder},
		{"the input is checked", testBeginValidates},
		{"chunks are checked", testChunkValidation},
		{"a session address is stored sealed", testAddressIsSealed},
		{"a sealed address can't move to another upload", testAddressIsBoundToItsRow},
		{"a finished upload forgets its address", testFinishedUploadForgetsAddress},
		{"the space is held while an upload runs", testReservationHoldsSpace},
		{"racing uploads can't share the same space", testConcurrentReservations},
		{"a second upload goes to the next account", testReservationSpreadsUploads},
		{"a one-request upload respects reservations", testChooseCountsReservations},
		{"a picked account must have room left", testPickedAccountCountsReservations},
		{"a finished upload stops holding space", testFinishFreesReservation},
		{"a failed start gives the space back", testFailedBeginFreesReservation},
		{"keeping less than was sent is reported", testKeptLess},
		{"a resumed upload continues from what the account has", testResumeSyncsWithAccount},
		{"a finished upload is found after losing the answer", testLostFinish},
		{"asking after it's finished finds the file", testProgressAfterFinish},
		{"a repeated last chunk doesn't make a second file", testRepeatedLastChunk},
		{"an expired session frees its space", testExpiredSession},
		{"a failed chunk loses nothing", testChunkFailure},
		{"a failed save removes the stored object", testFailedFinishRemovesObject},
		{"cancelling frees the space and tells the account", testCancel},
		{"a finished upload can't be cancelled", testCancelFinished},
		{"unfinished uploads are listed", testListUploads},
		{"users can't reach each other's uploads", testUploadsAreIsolated},
		{"cleanup removes abandoned uploads", testCleanupAbandoned},
		{"cleanup leaves recent uploads", testCleanupKeepsRecent},
		{"cleanup removes old records of finished uploads", testCleanupFinished},
		{"removing an account removes its uploads", testRemovingAccount},
		{"a chunk's memory doesn't grow with its size", testChunkMemoryStaysFlat},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `TRUNCATE users, settings, audit_events CASCADE`); err != nil {
				t.Fatalf("reset database: %v", err)
			}
			tt.run(t, newUploadFixture(t, pool))
		})
	}
}

func testChunksBecomeAFile(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	body := strings.Repeat("a", align) + strings.Repeat("b", align) + "tail"
	session := f.begin(t, user, int64(len(body)))
	if session.ConnectionID != connection || session.Received != 0 {
		t.Errorf("session = %+v", session)
	}

	one := f.put(t, user, session.ID, 0, body[:align])
	if one.Received != align || one.File != nil {
		t.Fatalf("after one chunk: %+v", one)
	}
	two := f.put(t, user, session.ID, align, body[align:2*align])
	if two.Received != 2*align {
		t.Fatalf("after two chunks: %+v", two)
	}
	done := f.put(t, user, session.ID, 2*align, body[2*align:])
	if done.File == nil {
		t.Fatalf("the last chunk finished nothing: %+v", done)
	}
	if done.File.Name != "big.bin" || done.File.Size != int64(len(body)) || done.File.ContentType != "text/plain" {
		t.Errorf("file = %+v", done.File)
	}

	opened, err := f.service.Open(f.ctx, user, done.File.ID, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = opened.Close() }()
	stored, err := io.ReadAll(opened)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(stored) != body {
		t.Errorf("stored %d bytes that differ from the %d sent", len(stored), len(body))
	}
}

func testChunkedUploadFolder(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	folder, err := f.service.CreateFolder(f.ctx, user, nil, "Photos")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	session, err := f.service.BeginUpload(f.ctx, user, BeginUploadInput{FolderID: &folder.ID, Name: "a.txt", Size: 3})
	if err != nil {
		t.Fatalf("BeginUpload: %v", err)
	}
	done := f.put(t, user, session.ID, 0, "abc")
	if done.File == nil || done.File.FolderID == nil || *done.File.FolderID != folder.ID {
		t.Errorf("file = %+v", done.File)
	}
}

func testBeginValidates(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<40)
	stranger := f.user(t)
	theirFolder, err := f.service.CreateFolder(f.ctx, stranger, nil, "Theirs")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	theirConnection := f.link(t, stranger, "b@gmail.com", 1<<40)
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO settings (key, value) VALUES ('max_upload_bytes', '1000')`); err != nil {
		t.Fatalf("set limit: %v", err)
	}
	tests := []struct {
		name string
		in   BeginUploadInput
		want error
	}{
		{"no name", BeginUploadInput{Size: 5}, ErrInvalidName},
		{"a slash in the name", BeginUploadInput{Name: "a/b", Size: 5}, ErrInvalidName},
		{"nothing to send", BeginUploadInput{Name: "a", Size: 0}, ErrInvalidUpload},
		{"a negative size", BeginUploadInput{Name: "a", Size: -1}, ErrInvalidUpload},
		{"over the limit", BeginUploadInput{Name: "a", Size: 1001}, ErrFileTooLarge},
		{"someone else's folder", BeginUploadInput{Name: "a", Size: 5, FolderID: &theirFolder.ID}, ErrFolderNotFound},
		{"someone else's account", BeginUploadInput{Name: "a", Size: 5, ConnectionID: &theirConnection}, storage.ErrConnectionNotFound},
	}
	for _, tt := range tests {
		if _, err := f.service.BeginUpload(f.ctx, user, tt.in); !errors.Is(err, tt.want) {
			t.Errorf("%s: error = %v, want %v", tt.name, err, tt.want)
		}
	}
	if _, err := f.service.BeginUpload(f.ctx, user, BeginUploadInput{Name: "a", Size: 1000, ConnectionID: &connection}); err != nil {
		t.Errorf("exactly at the limit: %v", err)
	}
	if got := f.count(t, `SELECT count(*) FROM upload_sessions`); got != 1 {
		t.Errorf("sessions = %d, refused uploads must leave nothing behind", got)
	}
}

func testChunkValidation(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<40)
	session := f.begin(t, user, 2*align+10)
	tests := []struct {
		name           string
		offset, length int64
		want           error
	}{
		{"before the start", -1, align, ErrInvalidChunk},
		{"nothing in it", 0, 0, ErrInvalidChunk},
		{"unknown length", 0, -1, ErrInvalidChunk},
		{"past the end", 0, 2*align + 11, ErrInvalidChunk},
		{"not a multiple of the alignment", 0, align + 1, ErrInvalidChunk},
		{"not where the upload is", align, align, ErrOffsetMismatch},
	}
	for _, tt := range tests {
		_, err := f.service.PutUploadChunk(f.ctx, user, session.ID, tt.offset, tt.length, strings.NewReader(""))
		if !errors.Is(err, tt.want) {
			t.Errorf("%s: error = %v, want %v", tt.name, err, tt.want)
		}
	}
	big := f.begin(t, user, 2*maxChunkBytes)
	if _, err := f.service.PutUploadChunk(f.ctx, user, big.ID, 0, maxChunkBytes+align, strings.NewReader("")); !errors.Is(err, ErrInvalidChunk) {
		t.Errorf("over the largest chunk: error = %v", err)
	}
}

func testAddressIsSealed(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	session := f.begin(t, user, 10)
	address := f.address(t, user, session.ID)
	if address == "" {
		t.Fatal("no address")
	}
	var ciphertext []byte
	var keyID string
	err := f.pool.QueryRow(f.ctx, `SELECT secret_key_id, secret_ciphertext FROM upload_sessions WHERE id = $1`, session.ID).Scan(&keyID, &ciphertext)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if keyID != "k1" || bytes.Contains(ciphertext, []byte(address)) {
		t.Errorf("key %q, ciphertext holds the address: %v", keyID, bytes.Contains(ciphertext, []byte(address)))
	}
}

func testAddressIsBoundToItsRow(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	first := f.begin(t, user, 10)
	second := f.begin(t, user, 10)
	_, err := f.pool.Exec(f.ctx, `UPDATE upload_sessions SET secret_ciphertext = (SELECT secret_ciphertext FROM upload_sessions WHERE id = $1) WHERE id = $2`, first.ID, second.ID)
	if err != nil {
		t.Fatalf("swap: %v", err)
	}
	if _, err := f.service.UploadProgress(f.ctx, user, second.ID); err == nil {
		t.Error("an address moved to another row still opened")
	}
}

func testFinishedUploadForgetsAddress(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	session := f.begin(t, user, 3)
	f.put(t, user, session.ID, 0, "abc")
	if got := f.count(t, `SELECT count(*) FROM upload_sessions WHERE secret_ciphertext IS NOT NULL OR secret_key_id IS NOT NULL`); got != 0 {
		t.Errorf("addresses kept = %d", got)
	}
}

func testReservationHoldsSpace(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 100)
	f.begin(t, user, 60)
	_, err := f.service.BeginUpload(f.ctx, user, BeginUploadInput{Name: "b", Size: 60})
	if !errors.Is(err, storage.ErrStorageFull) {
		t.Fatalf("error = %v, want ErrStorageFull", err)
	}
	f.begin(t, user, 40)
	pool, err := f.storage.Pool(f.ctx, user)
	if err != nil {
		t.Fatalf("Pool: %v", err)
	}
	if pool.Free != 0 || pool.Accounts[0].Reserved != 100 || pool.Used != 0 {
		t.Errorf("pool = %+v", pool)
	}
}

// gate holds every caller of Quota until all of them have arrived, so that they
// all reach what comes next at the same moment.
type gate struct {
	provider.Provider
	arrived *sync.WaitGroup
}

func (g gate) Quota(ctx context.Context) (provider.Quota, error) {
	g.arrived.Done()
	g.arrived.Wait()
	return g.Provider.Quota(ctx)
}

// CreateFolder takes a moment, as it does on a real account, which is the gap two
// uploads could both slip through.
func (g gate) CreateFolder(ctx context.Context, parent, name string) (string, error) {
	time.Sleep(30 * time.Millisecond)
	return g.Provider.CreateFolder(ctx, parent, name)
}

func testConcurrentReservations(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 100)
	const uploads = 8
	var arrived sync.WaitGroup
	arrived.Add(uploads)
	f.wrap = func(p provider.Provider) provider.Provider { return gate{p, &arrived} }
	errs := make([]error, uploads)
	var wg sync.WaitGroup
	for i := range uploads {
		wg.Go(func() {
			_, errs[i] = f.service.BeginUpload(f.ctx, user, BeginUploadInput{Name: "a", Size: 60})
		})
	}
	wg.Wait()
	started := 0
	for _, err := range errs {
		switch {
		case err == nil:
			started++
		case !errors.Is(err, storage.ErrStorageFull):
			t.Errorf("BeginUpload: %v", err)
		}
	}
	if started != 1 {
		t.Errorf("started = %d, want 1", started)
	}
}

func testReservationSpreadsUploads(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	first := f.link(t, user, "a@gmail.com", 100)
	second := f.link(t, user, "b@gmail.com", 90)
	one := f.begin(t, user, 60)
	two := f.begin(t, user, 60)
	if one.ConnectionID != first || two.ConnectionID != second {
		t.Errorf("went to %s then %s", one.ConnectionID, two.ConnectionID)
	}
}

func testChooseCountsReservations(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 100)
	f.begin(t, user, 60)
	if _, err := f.storage.Choose(f.ctx, user, nil, 60); !errors.Is(err, storage.ErrStorageFull) {
		t.Errorf("error = %v, want ErrStorageFull", err)
	}
}

func testPickedAccountCountsReservations(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 100)
	f.link(t, user, "b@gmail.com", 1000)
	in := BeginUploadInput{Name: "a", Size: 60, ConnectionID: &connection}
	if _, err := f.service.BeginUpload(f.ctx, user, in); err != nil {
		t.Fatalf("BeginUpload: %v", err)
	}
	if _, err := f.service.BeginUpload(f.ctx, user, in); !errors.Is(err, storage.ErrStorageFull) {
		t.Errorf("error = %v, a picked account is refused, never swapped", err)
	}
}

func testFinishFreesReservation(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 100)
	session := f.begin(t, user, 3)
	f.put(t, user, session.ID, 0, "abc")
	pool, err := f.storage.Pool(f.ctx, user)
	if err != nil {
		t.Fatalf("Pool: %v", err)
	}
	if pool.Accounts[0].Reserved != 0 || pool.Used != 3 || pool.Free != 97 {
		t.Errorf("the file's space is counted twice or not at all: %+v", pool)
	}
}

// failingBegin is an account that won't open sessions.
type failingBegin struct{ provider.Provider }

func (failingBegin) BeginResumable(context.Context, provider.Resumable) (string, error) {
	return "", errors.New("drive said no")
}

func testFailedBeginFreesReservation(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 100)
	f.wrap = func(p provider.Provider) provider.Provider { return failingBegin{p} }
	_, err := f.service.BeginUpload(f.ctx, user, BeginUploadInput{Name: "a", Size: 60})
	if !errors.Is(err, ErrProviderFailed) {
		t.Fatalf("error = %v, want ErrProviderFailed", err)
	}
	if got := f.count(t, `SELECT count(*) FROM upload_sessions`); got != 0 {
		t.Errorf("sessions = %d", got)
	}
}

func testKeptLess(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	body := strings.Repeat("x", 3*align)
	session := f.begin(t, user, int64(len(body)))
	f.account("a@gmail.com").DropBytes = align

	got := f.put(t, user, session.ID, 0, body[:2*align])
	if got.Received != align {
		t.Fatalf("Received = %d, want what the account kept", got.Received)
	}
	f.account("a@gmail.com").DropBytes = 0
	// Sending from what was sent, not what was kept, would leave a hole in the file.
	_, err := f.service.PutUploadChunk(f.ctx, user, session.ID, 2*align, align, strings.NewReader(body[2*align:]))
	if !errors.Is(err, ErrOffsetMismatch) {
		t.Fatalf("error = %v, want ErrOffsetMismatch", err)
	}
	f.put(t, user, session.ID, align, body[align:2*align])
	done := f.put(t, user, session.ID, 2*align, body[2*align:])
	if done.File == nil || done.File.Size != int64(len(body)) {
		t.Errorf("done = %+v", done)
	}
}

func testResumeSyncsWithAccount(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	body := strings.Repeat("x", 2*align)
	session := f.begin(t, user, int64(len(body)))
	// The chunk reached the account, but the answer never got back to Tributary.
	_, err := f.account("a@gmail.com").PutChunk(f.ctx, f.address(t, user, session.ID), provider.Chunk{
		Length: align,
		Total:  int64(len(body)),
		Body:   strings.NewReader(body[:align]),
	})
	if err != nil {
		t.Fatalf("PutChunk: %v", err)
	}
	progress, err := f.service.UploadProgress(f.ctx, user, session.ID)
	if err != nil {
		t.Fatalf("UploadProgress: %v", err)
	}
	if progress.Received != align {
		t.Fatalf("Received = %d, want %d", progress.Received, align)
	}
	if done := f.put(t, user, session.ID, align, body[align:]); done.File == nil {
		t.Errorf("done = %+v", done)
	}
}

func testLostFinish(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	session := f.begin(t, user, 3)
	_, err := f.account("a@gmail.com").PutChunk(f.ctx, f.address(t, user, session.ID), provider.Chunk{
		Length: 3,
		Total:  3,
		Body:   strings.NewReader("abc"),
	})
	if err != nil {
		t.Fatalf("PutChunk: %v", err)
	}
	progress, err := f.service.UploadProgress(f.ctx, user, session.ID)
	if err != nil {
		t.Fatalf("UploadProgress: %v", err)
	}
	if progress.File == nil {
		t.Fatalf("progress = %+v", progress)
	}
	if got := f.count(t, `SELECT count(*) FROM files WHERE user_id = $1`, user); got != 1 {
		t.Errorf("files = %d", got)
	}
}

func testProgressAfterFinish(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	session := f.begin(t, user, 3)
	done := f.put(t, user, session.ID, 0, "abc")
	again, err := f.service.UploadProgress(f.ctx, user, session.ID)
	if err != nil {
		t.Fatalf("UploadProgress: %v", err)
	}
	if again.File == nil || again.File.ID != done.File.ID || again.Received != 3 {
		t.Errorf("again = %+v", again)
	}
}

func testRepeatedLastChunk(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	session := f.begin(t, user, 3)
	first := f.put(t, user, session.ID, 0, "abc")
	second := f.put(t, user, session.ID, 0, "abc")
	if second.File == nil || second.File.ID != first.File.ID {
		t.Errorf("second = %+v", second)
	}
	if got := f.count(t, `SELECT count(*) FROM files WHERE user_id = $1`, user); got != 1 {
		t.Errorf("files = %d", got)
	}
}

func testExpiredSession(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 100)
	first := f.begin(t, user, 60)
	second := f.begin(t, user, 40)
	f.account("a@gmail.com").ExpireSessions()

	if _, err := f.service.UploadProgress(f.ctx, user, first.ID); !errors.Is(err, ErrUploadExpired) {
		t.Errorf("progress: error = %v, want ErrUploadExpired", err)
	}
	if _, err := f.service.PutUploadChunk(f.ctx, user, second.ID, 0, 40, strings.NewReader(strings.Repeat("x", 40))); !errors.Is(err, ErrUploadExpired) {
		t.Errorf("chunk: error = %v, want ErrUploadExpired", err)
	}
	if got := f.count(t, `SELECT count(*) FROM upload_sessions`); got != 0 {
		t.Errorf("sessions = %d", got)
	}
	f.begin(t, user, 100)
}

func testChunkFailure(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	body := strings.Repeat("x", 2*align)
	session := f.begin(t, user, int64(len(body)))
	account := f.account("a@gmail.com")
	account.ChunkErr = errors.New("backend error")
	_, err := f.service.PutUploadChunk(f.ctx, user, session.ID, 0, align, strings.NewReader(body[:align]))
	if !errors.Is(err, ErrProviderFailed) {
		t.Fatalf("error = %v, want ErrProviderFailed", err)
	}
	account.ChunkErr = nil
	f.put(t, user, session.ID, 0, body[:align])
	if done := f.put(t, user, session.ID, align, body[align:]); done.File == nil {
		t.Errorf("done = %+v", done)
	}
}

// folderVanishes deletes the upload's folder while the last chunk is finishing,
// which removes the upload's record too.
type folderVanishes struct {
	provider.Provider
	f *uploadFixture
}

func (v folderVanishes) PutChunk(ctx context.Context, session string, chunk provider.Chunk) (provider.ResumeState, error) {
	state, err := v.Provider.PutChunk(ctx, session, chunk)
	if _, execErr := v.f.pool.Exec(ctx, `DELETE FROM folders`); execErr != nil {
		return state, execErr
	}
	return state, err
}

func testFailedFinishRemovesObject(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	folder, err := f.service.CreateFolder(f.ctx, user, nil, "Photos")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	session, err := f.service.BeginUpload(f.ctx, user, BeginUploadInput{FolderID: &folder.ID, Name: "a", Size: 3})
	if err != nil {
		t.Fatalf("BeginUpload: %v", err)
	}
	f.wrap = func(p provider.Provider) provider.Provider { return folderVanishes{p, f} }
	if _, err := f.service.PutUploadChunk(f.ctx, user, session.ID, 0, 3, strings.NewReader("abc")); err == nil {
		t.Fatal("error = nil")
	}
	quota, err := f.account("a@gmail.com").Quota(f.ctx)
	if err != nil {
		t.Fatalf("Quota: %v", err)
	}
	if quota.Used != 0 {
		t.Errorf("an object nothing can find was left behind: %d bytes", quota.Used)
	}
}

func testCancel(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 100)
	session := f.begin(t, user, 60)
	if err := f.service.CancelUpload(f.ctx, user, session.ID); err != nil {
		t.Fatalf("CancelUpload: %v", err)
	}
	if got := f.account("a@gmail.com").OpenSessions(); got != 0 {
		t.Errorf("sessions left on the account = %d", got)
	}
	if err := f.service.CancelUpload(f.ctx, user, session.ID); !errors.Is(err, ErrUploadNotFound) {
		t.Errorf("cancelling twice: error = %v", err)
	}
	f.begin(t, user, 100)
}

func testCancelFinished(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 100)
	session := f.begin(t, user, 3)
	f.put(t, user, session.ID, 0, "abc")
	if err := f.service.CancelUpload(f.ctx, user, session.ID); !errors.Is(err, ErrUploadNotFound) {
		t.Errorf("error = %v", err)
	}
	if got := f.count(t, `SELECT count(*) FROM files WHERE user_id = $1`, user); got != 1 {
		t.Errorf("files = %d, cancelling must not touch a finished file", got)
	}
}

func testListUploads(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	finished := f.begin(t, user, 3)
	f.put(t, user, finished.ID, 0, "abc")
	running := f.begin(t, user, 2*align)
	f.put(t, user, running.ID, 0, strings.Repeat("x", align))

	uploads, err := f.service.ListUploads(f.ctx, user)
	if err != nil {
		t.Fatalf("ListUploads: %v", err)
	}
	if len(uploads) != 1 || uploads[0].ID != running.ID || uploads[0].Received != align || uploads[0].LastModified != 1234 {
		t.Errorf("uploads = %+v", uploads)
	}
}

func testUploadsAreIsolated(t *testing.T, f *uploadFixture) {
	owner := f.user(t)
	f.link(t, owner, "a@gmail.com", 1<<30)
	stranger := f.user(t)
	f.link(t, stranger, "b@gmail.com", 1<<30)
	session := f.begin(t, owner, 3)

	if _, err := f.service.UploadProgress(f.ctx, stranger, session.ID); !errors.Is(err, ErrUploadNotFound) {
		t.Errorf("progress: error = %v", err)
	}
	if _, err := f.service.PutUploadChunk(f.ctx, stranger, session.ID, 0, 3, strings.NewReader("abc")); !errors.Is(err, ErrUploadNotFound) {
		t.Errorf("chunk: error = %v", err)
	}
	if err := f.service.CancelUpload(f.ctx, stranger, session.ID); !errors.Is(err, ErrUploadNotFound) {
		t.Errorf("cancel: error = %v", err)
	}
	uploads, err := f.service.ListUploads(f.ctx, stranger)
	if err != nil {
		t.Fatalf("ListUploads: %v", err)
	}
	if len(uploads) != 0 {
		t.Errorf("uploads = %+v", uploads)
	}
	if done := f.put(t, owner, session.ID, 0, "abc"); done.File == nil {
		t.Errorf("the owner's upload was disturbed: %+v", done)
	}
}

func (f *uploadFixture) age(t *testing.T, id uuid.UUID, by time.Duration) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, `UPDATE upload_sessions SET updated_at = $2 WHERE id = $1`, id, time.Now().Add(-by)); err != nil {
		t.Fatalf("age: %v", err)
	}
}

func testCleanupAbandoned(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 100)
	session := f.begin(t, user, 60)
	f.age(t, session.ID, uploadAbandonedAfter+time.Minute)

	removed, err := f.service.CleanupUploads(f.ctx)
	if err != nil {
		t.Fatalf("CleanupUploads: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d", removed)
	}
	if got := f.account("a@gmail.com").OpenSessions(); got != 0 {
		t.Errorf("sessions left on the account = %d", got)
	}
	f.begin(t, user, 100)
}

func testCleanupKeepsRecent(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1000)
	session := f.begin(t, user, 60)
	f.age(t, session.ID, uploadAbandonedAfter-time.Minute)
	removed, err := f.service.CleanupUploads(f.ctx)
	if err != nil {
		t.Fatalf("CleanupUploads: %v", err)
	}
	if removed != 0 || f.account("a@gmail.com").OpenSessions() != 1 {
		t.Errorf("removed = %d, sessions = %d", removed, f.account("a@gmail.com").OpenSessions())
	}
}

func testCleanupFinished(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1000)
	session := f.begin(t, user, 3)
	f.put(t, user, session.ID, 0, "abc")
	f.age(t, session.ID, uploadAbandonedAfter+time.Minute)
	if _, err := f.service.CleanupUploads(f.ctx); err != nil {
		t.Fatalf("CleanupUploads: %v", err)
	}
	if got := f.count(t, `SELECT count(*) FROM upload_sessions`); got != 0 {
		t.Errorf("sessions = %d", got)
	}
	if got := f.count(t, `SELECT count(*) FROM files WHERE user_id = $1`, user); got != 1 {
		t.Errorf("files = %d, cleanup must not touch finished files", got)
	}
}

func testRemovingAccount(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1000)
	f.begin(t, user, 60)
	if err := f.storage.Delete(f.ctx, user, connection, "203.0.113.7"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := f.count(t, `SELECT count(*) FROM upload_sessions`); got != 0 {
		t.Errorf("sessions = %d", got)
	}
}

// discardingChunks reads what it's sent without keeping it, so that it doesn't count
// towards the memory the test measures.
type discardingChunks struct{ provider.Provider }

func (discardingChunks) PutChunk(_ context.Context, _ string, chunk provider.Chunk) (provider.ResumeState, error) {
	read, err := io.Copy(io.Discard, chunk.Body)
	return provider.ResumeState{Received: chunk.Offset + read}, err
}

// Rule 7, at the service: a chunk streams through it.
func testChunkMemoryStaysFlat(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<40)
	const size = maxChunkBytes
	session := f.begin(t, user, 2*size)
	f.wrap = func(p provider.Provider) provider.Provider { return discardingChunks{p} }

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	var peak atomic.Uint64
	done := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		for {
			select {
			case <-done:
				return
			case <-time.After(time.Millisecond):
				var now runtime.MemStats
				runtime.ReadMemStats(&now)
				if now.HeapAlloc > peak.Load() {
					peak.Store(now.HeapAlloc)
				}
			}
		}
	}()
	got, err := f.service.PutUploadChunk(f.ctx, user, session.ID, 0, size, zeros{})
	close(done)
	<-sampled
	if err != nil {
		t.Fatalf("PutUploadChunk: %v", err)
	}
	if got.Received != size {
		t.Errorf("Received = %d", got.Received)
	}
	if grew := peak.Load() - min(peak.Load(), before.HeapAlloc); grew > size/4 {
		t.Errorf("heap grew by %s for a %s chunk", fmt.Sprint(grew>>20, " MiB"), fmt.Sprint(size>>20, " MiB"))
	}
}

// TestChunkedUploadHandlers goes through the real router, as the browser does.
func TestChunkedUploadHandlers(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	f := newUploadFixture(t, pool)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := httpx.NewRouter(httpx.Deps{
		Logger:       logger,
		DB:           pool,
		PublicOrigin: testOrigin,
		Sessions:     sessions{},
		APIKeys:      keys{},
		Auth:         apigen.Unimplemented{},
		Admin:        apigen.Unimplemented{},
		Storage:      apigen.Unimplemented{},
		Files:        NewHandler(f.service, logger),
	})
	owner := f.user(t)
	f.link(t, owner, "a@gmail.com", 100)
	stranger := f.user(t)

	do := func(user uuid.UUID, method, target, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Origin", testOrigin)
		req.AddCookie(&http.Cookie{Name: httpx.SessionCookieName, Value: user.String()})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	wantCode := func(rec *httptest.ResponseRecorder, status int, code string) {
		t.Helper()
		if rec.Code != status || !strings.Contains(rec.Body.String(), `"`+code+`"`) {
			t.Errorf("status = %d, body %s, want %d %s", rec.Code, rec.Body, status, code)
		}
	}

	wantCode(do(owner, http.MethodPost, "/api/v1/uploads", `{"name":"a.txt","size":0}`), http.StatusBadRequest, "invalid_upload")
	wantCode(do(owner, http.MethodPost, "/api/v1/uploads", `{"name":"a.txt","size":101}`), http.StatusInsufficientStorage, "storage_full")

	rec := do(owner, http.MethodPost, "/api/v1/uploads", `{"name":"a.txt","size":3,"content_type":"text/plain","last_modified":99}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("begin: status = %d, body %s", rec.Code, rec.Body)
	}
	created := decode[apigen.UploadSession](t, rec)
	if created.LastModified != 99 || strings.Contains(rec.Body.String(), "session-") {
		t.Errorf("begin answered %s", rec.Body)
	}
	path := "/api/v1/uploads/" + created.Id.String()

	wantCode(do(owner, http.MethodPut, path+"/chunk", "abc"), http.StatusBadRequest, "invalid_chunk")
	wantCode(do(owner, http.MethodPut, path+"/chunk?offset=abc", "abc"), http.StatusBadRequest, "invalid_chunk")
	wantCode(do(owner, http.MethodPut, path+"/chunk?offset=1", "ab"), http.StatusConflict, "offset_mismatch")

	wantCode(do(stranger, http.MethodGet, path, ""), http.StatusNotFound, "not_found")
	wantCode(do(stranger, http.MethodPut, path+"/chunk?offset=0", "abc"), http.StatusNotFound, "not_found")
	wantCode(do(stranger, http.MethodDelete, path, ""), http.StatusNotFound, "not_found")

	if list := decode[[]apigen.UploadSession](t, do(owner, http.MethodGet, "/api/v1/uploads", "")); len(list) != 1 {
		t.Errorf("list = %+v", list)
	}
	if list := decode[[]apigen.UploadSession](t, do(stranger, http.MethodGet, "/api/v1/uploads", "")); len(list) != 0 {
		t.Errorf("a stranger's list = %+v", list)
	}

	rec = do(owner, http.MethodPut, path+"/chunk?offset=0", "abc")
	if rec.Code != http.StatusOK {
		t.Fatalf("chunk: status = %d, body %s", rec.Code, rec.Body)
	}
	if done := decode[apigen.UploadSession](t, rec); done.File == nil || done.File.ContentType != "text/plain" {
		t.Errorf("done = %s", rec.Body)
	}

	expiring := decode[apigen.UploadSession](t, do(owner, http.MethodPost, "/api/v1/uploads", `{"name":"b.txt","size":3}`))
	f.account("a@gmail.com").ExpireSessions()
	wantCode(do(owner, http.MethodGet, "/api/v1/uploads/"+expiring.Id.String(), ""), http.StatusGone, "upload_expired")

	cancelled := decode[apigen.UploadSession](t, do(owner, http.MethodPost, "/api/v1/uploads", `{"name":"c.txt","size":3}`))
	if rec := do(owner, http.MethodDelete, "/api/v1/uploads/"+cancelled.Id.String(), ""); rec.Code != http.StatusNoContent {
		t.Errorf("cancel: status = %d, body %s", rec.Code, rec.Body)
	}

	// Scripts keep the one-request upload, so these stay closed to keys.
	for _, route := range []struct{ method, target string }{
		{http.MethodGet, "/api/v1/uploads"},
		{http.MethodPost, "/api/v1/uploads"},
		{http.MethodGet, path},
		{http.MethodDelete, path},
		{http.MethodPut, path + "/chunk?offset=0"},
	} {
		req := httptest.NewRequest(route.method, route.target, strings.NewReader("{}"))
		req.Header.Set("Origin", testOrigin)
		req.Header.Set("Authorization", "Bearer "+owner.String()+":files:write")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s with a key: status = %d", route.method, route.target, rec.Code)
		}
	}
}
