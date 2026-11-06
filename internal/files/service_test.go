package files

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/swayam5342/dextro/internal/admin"
	"github.com/swayam5342/dextro/internal/db"
	"github.com/swayam5342/dextro/internal/db/dbtest"
	"github.com/swayam5342/dextro/internal/provider"
	"github.com/swayam5342/dextro/internal/provider/providertest"
	"github.com/swayam5342/dextro/internal/secrets"
	"github.com/swayam5342/dextro/internal/storage"
)

const testIP = "203.0.113.7"

// flaky is a provider whose failures a test can switch on.
type flaky struct {
	*providertest.Fake
	mu        sync.Mutex
	putErr    error
	deleteErr error
	getErr    error
	// discard throws uploads away, to keep the fake out of a memory measurement.
	discard bool
}

func (f *flaky) Put(ctx context.Context, u provider.Upload) (provider.Object, error) {
	f.mu.Lock()
	err := f.putErr
	f.mu.Unlock()
	if err != nil {
		return provider.Object{}, err
	}
	if f.discard {
		size, err := io.Copy(io.Discard, u.Body)
		return provider.Object{ID: "discarded", Name: u.Name, Size: size}, err
	}
	return f.Fake.Put(ctx, u)
}

func (f *flaky) Delete(ctx context.Context, id string) error {
	f.mu.Lock()
	err := f.deleteErr
	f.mu.Unlock()
	if err != nil {
		return err
	}
	return f.Fake.Delete(ctx, id)
}

func (f *flaky) Get(ctx context.Context, id string, rng *provider.ByteRange) (io.ReadCloser, error) {
	f.mu.Lock()
	err := f.getErr
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return f.Fake.Get(ctx, id, rng)
}

func (f *flaky) setDeleteErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteErr = err
}

// providers checks ownership against the real connections table, as the storage
// service does, and hands back an in-memory provider per connection.
type providers struct {
	pool *pgxpool.Pool
	mu   sync.Mutex
	all  map[uuid.UUID]*flaky
	// chooseErr, if set, is what Choose returns.
	chooseErr error
	lastSize  int64
}

func (p *providers) Provider(ctx context.Context, userID, connectionID uuid.UUID) (provider.Provider, error) {
	var exists bool
	err := p.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM storage_connections WHERE user_id = $1 AND id = $2)`,
		userID, connectionID).Scan(&exists)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, storage.ErrConnectionNotFound
	}
	return p.forConnection(connectionID), nil
}

// Choose stands in for the pool, whose rules are tested in the storage package.
func (p *providers) Choose(ctx context.Context, userID uuid.UUID, preferred *uuid.UUID, size int64) (storage.Chosen, error) {
	p.lastSize = size
	if p.chooseErr != nil {
		return storage.Chosen{}, p.chooseErr
	}
	var id uuid.UUID
	if preferred != nil {
		id = *preferred
	} else if err := p.pool.QueryRow(ctx, `SELECT id FROM storage_connections WHERE user_id = $1 ORDER BY created_at LIMIT 1`, userID).Scan(&id); err != nil {
		return storage.Chosen{}, storage.ErrNoStorage
	}
	stored, err := p.Provider(ctx, userID, id)
	if err != nil {
		return storage.Chosen{}, err
	}
	return storage.Chosen{ConnectionID: id, Provider: stored}, nil
}

// uploads_test.go uses the real storage service for chunked uploads.
func (p *providers) Reserve(context.Context, uuid.UUID, *uuid.UUID, storage.Reservation) (storage.Chosen, error) {
	return storage.Chosen{}, errors.New("not used by these tests")
}

func (p *providers) SealUploadSession(uuid.UUID, string) secrets.Sealed { return secrets.Sealed{} }

func (p *providers) OpenUploadSession(uuid.UUID, secrets.Sealed) (string, error) {
	return "", errors.New("not used by these tests")
}

func (p *providers) AccountsFor(ctx context.Context, userID uuid.UUID) ([]storage.Account, error) {
	rows, err := p.pool.Query(ctx, `SELECT id FROM storage_connections WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var accounts []storage.Account
	for rows.Next() {
		account := storage.Account{UserID: userID}
		if err := rows.Scan(&account.ID); err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

func (p *providers) AllAccounts(context.Context) ([]storage.Account, error) {
	return nil, errors.New("not used by these tests")
}

func (p *providers) forConnection(id uuid.UUID) *flaky {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.all[id] == nil {
		p.all[id] = &flaky{Fake: providertest.NewFake(1000)}
	}
	return p.all[id]
}

type fixture struct {
	ctx       context.Context
	pool      *pgxpool.Pool
	service   *Service
	providers *providers
}

func newFixture(pool *pgxpool.Pool) *fixture {
	p := &providers{pool: pool, all: map[uuid.UUID]*flaky{}}
	return &fixture{ctx: context.Background(), pool: pool, providers: p, service: NewService(NewStore(pool), p)}
}

type account struct {
	id         uuid.UUID
	connection uuid.UUID
}

func (f *fixture) connection(t *testing.T, userID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := f.pool.Exec(f.ctx, `INSERT INTO storage_connections (id, user_id, provider, name, settings, secret_key_id, secret_ciphertext)
		VALUES ($1, $2, 'google_drive', $3, '{}', 'k1', '\x00')`, id, userID, id.String())
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	return id
}

func (f *fixture) account(t *testing.T) account {
	t.Helper()
	var id uuid.UUID
	err := f.pool.QueryRow(f.ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`, uuid.NewString()+"@example.org").Scan(&id)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return account{id: id, connection: f.connection(t, id)}
}

func upload(a account, folder *uuid.UUID, name, body string) UploadInput {
	return UploadInput{
		ConnectionID: &a.connection,
		FolderID:     folder,
		Name:         name,
		ContentType:  "text/plain",
		Size:         int64(len(body)),
		Body:         strings.NewReader(body),
	}
}

func (f *fixture) put(t *testing.T, a account, folder *uuid.UUID, name string) File {
	t.Helper()
	file, err := f.service.Upload(f.ctx, a.id, upload(a, folder, name, "hello"))
	if err != nil {
		t.Fatalf("Upload(%s): %v", name, err)
	}
	return file
}

func (f *fixture) folder(t *testing.T, a account, parent *uuid.UUID, name string) Folder {
	t.Helper()
	folder, err := f.service.CreateFolder(f.ctx, a.id, parent, name)
	if err != nil {
		t.Fatalf("CreateFolder(%s): %v", name, err)
	}
	return folder
}

func (f *fixture) list(t *testing.T, a account, folder *uuid.UUID) Contents {
	t.Helper()
	contents, err := f.service.List(f.ctx, a.id, folder)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return contents
}

func (f *fixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(f.ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func (f *fixture) objects(t *testing.T, connection uuid.UUID) int {
	t.Helper()
	page, err := f.providers.forConnection(connection).List(f.ctx, "")
	if err != nil {
		t.Fatalf("List objects: %v", err)
	}
	return files(page.Objects)
}

// files counts the files in a listing, which also holds folders.
func files(objects []provider.Object) int {
	n := 0
	for _, object := range objects {
		if !object.Folder {
			n++
		}
	}
	return n
}

func names[T any](items []T, name func(T) string) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, name(item))
	}
	return strings.Join(parts, ",")
}

func fileNames(items []File) string     { return names(items, func(f File) string { return f.Name }) }
func folderNames(items []Folder) string { return names(items, func(f Folder) string { return f.Name }) }

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
		{"an upload is stored and listed", testUpload},
		{"content types are normalized", testContentTypes},
		{"names are validated", testNameValidation},
		{"a file can't go in someone else's folder", testUploadForeignFolder},
		{"a file can't go in a trashed folder", testUploadTrashedFolder},
		{"a file can't go through someone else's connection", testUploadForeignConnection},
		{"a declared size over the limit is refused unread", testDeclaredSizeOverLimit},
		{"a stream over the limit stops and stores nothing", testStreamOverLimit},
		{"a file exactly at the limit is allowed", testExactlyAtLimit},
		{"a provider failure stores nothing", testUploadProviderFailure},
		{"a failed save removes the stored object", testOrphanCleanup},
		{"folders list with their path", testFolderPath},
		{"users only see their own items", testListIsScoped},
		{"users can't reach each other's files", testFilesAreIsolated},
		{"users can't reach each other's folders", testFoldersAreIsolated},
		{"renaming a file", testRenameFile},
		{"moving a file", testMoveFile},
		{"a file can't move to someone else's folder", testMoveFileForeignFolder},
		{"trashing and restoring a file", testTrashFile},
		{"a restored file goes to the top if its folder is gone", testRestoreFileToTop},
		{"trashed files can't be renamed or moved", testTrashedFileIsFrozen},
		{"permanent delete needs the trash first", testDeleteNeedsTrash},
		{"permanent delete removes the object", testDeleteFile},
		{"a failed provider delete keeps the record", testDeleteProviderFailure},
		{"an object already gone still clears the record", testDeleteObjectGone},
		{"trashing a folder hides everything in it", testTrashFolder},
		{"a restored folder goes to the top if its parent is gone", testRestoreFolderToTop},
		{"a folder can't move into itself", testMoveFolderCycle},
		{"concurrent folder moves can't make a loop", testConcurrentFolderMoves},
		{"deleting a folder removes everything under it", testDeleteFolder},
		{"a half-finished folder delete can be retried", testDeleteFolderRetry},
		{"a folder delete needs the trash first", testDeleteFolderNeedsTrash},
		{"emptying the trash", testEmptyTrash},
		{"the trash lists only what was trashed directly", testTrashListing},
		{"removing a connection forgets its files", testConnectionRemoval},
		{"reading a file's content", testOpen},
		{"a loop in the folder tree can.t hang the server", testFolderLoopCannotHang},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `TRUNCATE users, settings, audit_events CASCADE`); err != nil {
				t.Fatalf("reset database: %v", err)
			}
			tt.run(t, newFixture(pool))
		})
	}
}

func testUpload(t *testing.T, f *fixture) {
	a := f.account(t)
	file := f.put(t, a, nil, "notes.txt")
	if file.Name != "notes.txt" || file.Size != 5 || file.ContentType != "text/plain" || file.ConnectionID != a.connection {
		t.Errorf("file = %+v", file)
	}
	if got := fileNames(f.list(t, a, nil).Files); got != "notes.txt" {
		t.Errorf("listing = %q", got)
	}
	if got := f.objects(t, a.connection); got != 1 {
		t.Errorf("objects = %d, want 1", got)
	}
}

func testContentTypes(t *testing.T, f *fixture) {
	a := f.account(t)
	tests := []struct{ raw, want string }{
		{"text/plain; charset=utf-8", "text/plain"},
		{"IMAGE/PNG", "image/png"},
		{"", "application/octet-stream"},
		{"not a type at all", "application/octet-stream"},
	}
	for _, tt := range tests {
		in := upload(a, nil, "x.bin", "x")
		in.ContentType = tt.raw
		file, err := f.service.Upload(f.ctx, a.id, in)
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}
		if file.ContentType != tt.want {
			t.Errorf("type for %q = %q, want %q", tt.raw, file.ContentType, tt.want)
		}
	}
}

func testNameValidation(t *testing.T, f *fixture) {
	a := f.account(t)
	for _, name := range []string{"", "   ", ".", "..", "a/b", `a\b`, "a\nb", "a\x00b", strings.Repeat("x", 256)} {
		if _, err := f.service.Upload(f.ctx, a.id, upload(a, nil, name, "x")); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Upload(%q): error = %v, want ErrInvalidName", name, err)
		}
		if _, err := f.service.CreateFolder(f.ctx, a.id, nil, name); !errors.Is(err, ErrInvalidName) {
			t.Errorf("CreateFolder(%q): error = %v, want ErrInvalidName", name, err)
		}
	}
	if got := f.objects(t, a.connection); got != 0 {
		t.Errorf("objects = %d, want 0", got)
	}
	file := f.put(t, a, nil, "  spaced.txt ")
	if file.Name != "spaced.txt" {
		t.Errorf("name = %q", file.Name)
	}
}

func testUploadForeignFolder(t *testing.T, f *fixture) {
	mine, theirs := f.account(t), f.account(t)
	folder := f.folder(t, theirs, nil, "Theirs")
	_, err := f.service.Upload(f.ctx, mine.id, upload(mine, &folder.ID, "x.txt", "x"))
	if !errors.Is(err, ErrFolderNotFound) {
		t.Fatalf("error = %v, want ErrFolderNotFound", err)
	}
	if got := f.objects(t, mine.connection); got != 0 {
		t.Errorf("objects = %d, want 0", got)
	}
}

func testUploadTrashedFolder(t *testing.T, f *fixture) {
	a := f.account(t)
	parent := f.folder(t, a, nil, "Parent")
	child := f.folder(t, a, &parent.ID, "Child")
	if err := f.service.TrashFolder(f.ctx, a.id, parent.ID); err != nil {
		t.Fatalf("TrashFolder: %v", err)
	}
	for _, id := range []uuid.UUID{parent.ID, child.ID} {
		if _, err := f.service.Upload(f.ctx, a.id, upload(a, &id, "x.txt", "x")); !errors.Is(err, ErrFolderNotFound) {
			t.Errorf("error = %v, want ErrFolderNotFound", err)
		}
	}
}

func testUploadForeignConnection(t *testing.T, f *fixture) {
	mine, theirs := f.account(t), f.account(t)
	in := upload(mine, nil, "x.txt", "x")
	in.ConnectionID = &theirs.connection
	if _, err := f.service.Upload(f.ctx, mine.id, in); !errors.Is(err, storage.ErrConnectionNotFound) {
		t.Fatalf("error = %v, want ErrConnectionNotFound", err)
	}
	if got := f.objects(t, theirs.connection); got != 0 {
		t.Errorf("objects = %d, want 0", got)
	}
}

func setLimit(t *testing.T, f *fixture, limit int64) {
	t.Helper()
	value := fmt.Appendf(nil, "%d", limit)
	if err := admin.NewStore(f.pool).SetSetting(f.ctx, "max_upload_bytes", value); err != nil {
		t.Fatalf("set limit: %v", err)
	}
}

// countingReader records how much of a body was actually read.
type countingReader struct {
	r    io.Reader
	read int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += int64(n)
	return n, err
}

func testDeclaredSizeOverLimit(t *testing.T, f *fixture) {
	a := f.account(t)
	setLimit(t, f, 10)
	body := &countingReader{r: strings.NewReader(strings.Repeat("x", 11))}
	in := upload(a, nil, "big.bin", "")
	in.Size = 11
	in.Body = body
	if _, err := f.service.Upload(f.ctx, a.id, in); !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("error = %v, want ErrFileTooLarge", err)
	}
	if body.read != 0 {
		t.Errorf("read %d bytes of a file already known to be too large", body.read)
	}
}

type endless struct{}

func (endless) Read(p []byte) (int, error) { return len(p), nil }

func testStreamOverLimit(t *testing.T, f *fixture) {
	a := f.account(t)
	setLimit(t, f, 1000)
	body := &countingReader{r: endless{}}
	in := upload(a, nil, "big.bin", "")
	in.Size = -1 // A chunked upload doesn't say how long it is.
	in.Body = body
	if _, err := f.service.Upload(f.ctx, a.id, in); !errors.Is(err, ErrFileTooLarge) {
		t.Fatalf("error = %v, want ErrFileTooLarge", err)
	}
	if body.read > 1<<20 {
		t.Errorf("read %d bytes past a limit of 1000", body.read)
	}
	if got := f.count(t, `SELECT count(*) FROM files`); got != 0 {
		t.Errorf("records = %d, want 0", got)
	}
	if got := f.objects(t, a.connection); got != 0 {
		t.Errorf("objects = %d, want 0", got)
	}
}

func testExactlyAtLimit(t *testing.T, f *fixture) {
	a := f.account(t)
	setLimit(t, f, 5)
	in := upload(a, nil, "fits.txt", "hello")
	in.Size = -1
	if _, err := f.service.Upload(f.ctx, a.id, in); err != nil {
		t.Fatalf("Upload: %v", err)
	}
}

func testUploadProviderFailure(t *testing.T, f *fixture) {
	a := f.account(t)
	f.providers.forConnection(a.connection).putErr = errors.New("connection reset")
	if _, err := f.service.Upload(f.ctx, a.id, upload(a, nil, "x.txt", "x")); !errors.Is(err, ErrProviderFailed) {
		t.Fatalf("error = %v, want ErrProviderFailed", err)
	}
	if got := f.count(t, `SELECT count(*) FROM files`); got != 0 {
		t.Errorf("records = %d, want 0", got)
	}
}

func testOrphanCleanup(t *testing.T, f *fixture) {
	a := f.account(t)
	_, err := f.pool.Exec(f.ctx, `
		CREATE FUNCTION fail_insert() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'database went away'; END $$;
		CREATE TRIGGER fail_files BEFORE INSERT ON files FOR EACH ROW EXECUTE FUNCTION fail_insert();`)
	if err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(f.ctx, `DROP TRIGGER fail_files ON files; DROP FUNCTION fail_insert();`)
	})

	if _, err := f.service.Upload(f.ctx, a.id, upload(a, nil, "x.txt", "x")); err == nil {
		t.Fatal("Upload succeeded, want error")
	}
	if got := f.objects(t, a.connection); got != 0 {
		t.Errorf("objects = %d: the upload left an object no record points to", got)
	}
}

func testFolderPath(t *testing.T, f *fixture) {
	a := f.account(t)
	top := f.folder(t, a, nil, "Top")
	mid := f.folder(t, a, &top.ID, "Mid")
	low := f.folder(t, a, &mid.ID, "Low")
	f.put(t, a, &low.ID, "deep.txt")

	contents := f.list(t, a, &low.ID)
	if got := folderNames(contents.Path); got != "Top,Mid,Low" {
		t.Errorf("path = %q", got)
	}
	if got := fileNames(contents.Files); got != "deep.txt" {
		t.Errorf("files = %q", got)
	}
	if got := folderNames(f.list(t, a, nil).Folders); got != "Top" {
		t.Errorf("top level = %q", got)
	}
}

func testListIsScoped(t *testing.T, f *fixture) {
	mine, theirs := f.account(t), f.account(t)
	f.put(t, theirs, nil, "theirs.txt")
	f.folder(t, theirs, nil, "Theirs")
	contents := f.list(t, mine, nil)
	if len(contents.Files) != 0 || len(contents.Folders) != 0 {
		t.Errorf("contents = %+v", contents)
	}
}

func testFilesAreIsolated(t *testing.T, f *fixture) {
	mine, theirs := f.account(t), f.account(t)
	file := f.put(t, theirs, nil, "theirs.txt")
	trashed := f.put(t, theirs, nil, "trashed.txt")
	if err := f.service.TrashFile(f.ctx, theirs.id, trashed.ID); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}

	calls := map[string]func() error{
		"Get":     func() error { _, err := f.service.Get(f.ctx, mine.id, file.ID); return err },
		"Open":    func() error { _, err := f.service.Open(f.ctx, mine.id, file.ID, nil); return err },
		"Rename":  func() error { _, err := f.service.RenameFile(f.ctx, mine.id, file.ID, "mine.txt"); return err },
		"Move":    func() error { _, err := f.service.MoveFile(f.ctx, mine.id, file.ID, nil); return err },
		"Trash":   func() error { return f.service.TrashFile(f.ctx, mine.id, file.ID) },
		"Restore": func() error { return f.service.RestoreFile(f.ctx, mine.id, trashed.ID) },
		"Delete":  func() error { return f.service.DeleteFile(f.ctx, mine.id, trashed.ID, testIP) },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, ErrFileNotFound) {
			t.Errorf("%s: error = %v, want ErrFileNotFound", name, err)
		}
	}
	if got := fileNames(f.list(t, theirs, nil).Files); got != "theirs.txt" {
		t.Errorf("their files changed: %q", got)
	}
	if got := f.objects(t, theirs.connection); got != 2 {
		t.Errorf("their objects = %d, want 2", got)
	}
}

func testFoldersAreIsolated(t *testing.T, f *fixture) {
	mine, theirs := f.account(t), f.account(t)
	folder := f.folder(t, theirs, nil, "Theirs")
	trashed := f.folder(t, theirs, nil, "Trashed")
	if err := f.service.TrashFolder(f.ctx, theirs.id, trashed.ID); err != nil {
		t.Fatalf("TrashFolder: %v", err)
	}
	own := f.folder(t, mine, nil, "Mine")

	calls := map[string]func() error{
		"List":      func() error { _, err := f.service.List(f.ctx, mine.id, &folder.ID); return err },
		"Create in": func() error { _, err := f.service.CreateFolder(f.ctx, mine.id, &folder.ID, "x"); return err },
		"Rename":    func() error { _, err := f.service.RenameFolder(f.ctx, mine.id, folder.ID, "x"); return err },
		"Move":      func() error { _, err := f.service.MoveFolder(f.ctx, mine.id, folder.ID, nil); return err },
		"Move into": func() error { _, err := f.service.MoveFolder(f.ctx, mine.id, own.ID, &folder.ID); return err },
		"Trash":     func() error { return f.service.TrashFolder(f.ctx, mine.id, folder.ID) },
		"Restore":   func() error { return f.service.RestoreFolder(f.ctx, mine.id, trashed.ID) },
		"Delete":    func() error { return f.service.DeleteFolder(f.ctx, mine.id, trashed.ID, testIP) },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, ErrFolderNotFound) {
			t.Errorf("%s: error = %v, want ErrFolderNotFound", name, err)
		}
	}
	if got := folderNames(f.list(t, theirs, nil).Folders); got != "Theirs" {
		t.Errorf("their folders changed: %q", got)
	}
}

func testRenameFile(t *testing.T, f *fixture) {
	a := f.account(t)
	file := f.put(t, a, nil, "old.txt")
	renamed, err := f.service.RenameFile(f.ctx, a.id, file.ID, "new.txt")
	if err != nil {
		t.Fatalf("RenameFile: %v", err)
	}
	if renamed.Name != "new.txt" {
		t.Errorf("name = %q", renamed.Name)
	}
	if _, err := f.service.RenameFile(f.ctx, a.id, file.ID, "a/b"); !errors.Is(err, ErrInvalidName) {
		t.Errorf("error = %v, want ErrInvalidName", err)
	}
}

func testMoveFile(t *testing.T, f *fixture) {
	a := f.account(t)
	folder := f.folder(t, a, nil, "Folder")
	file := f.put(t, a, nil, "x.txt")
	if _, err := f.service.MoveFile(f.ctx, a.id, file.ID, &folder.ID); err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if got := fileNames(f.list(t, a, &folder.ID).Files); got != "x.txt" {
		t.Errorf("folder = %q", got)
	}
	if got := fileNames(f.list(t, a, nil).Files); got != "" {
		t.Errorf("top level = %q", got)
	}
	if _, err := f.service.MoveFile(f.ctx, a.id, file.ID, nil); err != nil {
		t.Fatalf("MoveFile to top: %v", err)
	}
	if got := fileNames(f.list(t, a, nil).Files); got != "x.txt" {
		t.Errorf("top level = %q", got)
	}
}

func testMoveFileForeignFolder(t *testing.T, f *fixture) {
	mine, theirs := f.account(t), f.account(t)
	folder := f.folder(t, theirs, nil, "Theirs")
	file := f.put(t, mine, nil, "x.txt")
	if _, err := f.service.MoveFile(f.ctx, mine.id, file.ID, &folder.ID); !errors.Is(err, ErrFolderNotFound) {
		t.Fatalf("error = %v, want ErrFolderNotFound", err)
	}
}

func testTrashFile(t *testing.T, f *fixture) {
	a := f.account(t)
	file := f.put(t, a, nil, "x.txt")
	if err := f.service.TrashFile(f.ctx, a.id, file.ID); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if got := fileNames(f.list(t, a, nil).Files); got != "" {
		t.Errorf("listing = %q", got)
	}
	trash, err := f.service.ListTrash(f.ctx, a.id)
	if err != nil {
		t.Fatalf("ListTrash: %v", err)
	}
	if got := fileNames(trash.Files); got != "x.txt" {
		t.Errorf("trash = %q", got)
	}
	if got := f.objects(t, a.connection); got != 1 {
		t.Errorf("objects = %d: trashing must not touch the stored file", got)
	}
	if err := f.service.RestoreFile(f.ctx, a.id, file.ID); err != nil {
		t.Fatalf("RestoreFile: %v", err)
	}
	if got := fileNames(f.list(t, a, nil).Files); got != "x.txt" {
		t.Errorf("listing = %q", got)
	}
}

func testRestoreFileToTop(t *testing.T, f *fixture) {
	a := f.account(t)
	folder := f.folder(t, a, nil, "Folder")
	file := f.put(t, a, &folder.ID, "x.txt")
	if err := f.service.TrashFile(f.ctx, a.id, file.ID); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if err := f.service.TrashFolder(f.ctx, a.id, folder.ID); err != nil {
		t.Fatalf("TrashFolder: %v", err)
	}
	if err := f.service.RestoreFile(f.ctx, a.id, file.ID); err != nil {
		t.Fatalf("RestoreFile: %v", err)
	}
	if got := fileNames(f.list(t, a, nil).Files); got != "x.txt" {
		t.Errorf("top level = %q", got)
	}
}

func testTrashedFileIsFrozen(t *testing.T, f *fixture) {
	a := f.account(t)
	file := f.put(t, a, nil, "x.txt")
	if err := f.service.TrashFile(f.ctx, a.id, file.ID); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if _, err := f.service.RenameFile(f.ctx, a.id, file.ID, "y.txt"); !errors.Is(err, ErrFileNotFound) {
		t.Errorf("Rename: error = %v, want ErrFileNotFound", err)
	}
	if _, err := f.service.MoveFile(f.ctx, a.id, file.ID, nil); !errors.Is(err, ErrFileNotFound) {
		t.Errorf("Move: error = %v, want ErrFileNotFound", err)
	}
}

func testDeleteNeedsTrash(t *testing.T, f *fixture) {
	a := f.account(t)
	file := f.put(t, a, nil, "x.txt")
	if err := f.service.DeleteFile(f.ctx, a.id, file.ID, testIP); !errors.Is(err, ErrNotInTrash) {
		t.Fatalf("error = %v, want ErrNotInTrash", err)
	}
	if got := f.objects(t, a.connection); got != 1 {
		t.Errorf("objects = %d, want 1", got)
	}
}

func (f *fixture) trashAndDelete(t *testing.T, a account, id uuid.UUID) error {
	t.Helper()
	if err := f.service.TrashFile(f.ctx, a.id, id); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	return f.service.DeleteFile(f.ctx, a.id, id, testIP)
}

func testDeleteFile(t *testing.T, f *fixture) {
	a := f.account(t)
	file := f.put(t, a, nil, "x.txt")
	if err := f.trashAndDelete(t, a, file.ID); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if got := f.objects(t, a.connection); got != 0 {
		t.Errorf("objects = %d, want 0", got)
	}
	if _, err := f.service.Get(f.ctx, a.id, file.ID); !errors.Is(err, ErrFileNotFound) {
		t.Errorf("error = %v, want ErrFileNotFound", err)
	}
	if got := f.count(t, `SELECT count(*) FROM audit_events WHERE action = 'file.deleted' AND actor_id = $1`, a.id); got != 1 {
		t.Errorf("audit events = %d, want 1", got)
	}
}

func testDeleteProviderFailure(t *testing.T, f *fixture) {
	a := f.account(t)
	file := f.put(t, a, nil, "x.txt")
	f.providers.forConnection(a.connection).setDeleteErr(errors.New("connection reset"))
	if err := f.trashAndDelete(t, a, file.ID); !errors.Is(err, ErrProviderFailed) {
		t.Fatalf("error = %v, want ErrProviderFailed", err)
	}
	if got := f.count(t, `SELECT count(*) FROM files`); got != 1 {
		t.Errorf("records = %d: the record must stay until the object is really gone", got)
	}
}

func testDeleteObjectGone(t *testing.T, f *fixture) {
	a := f.account(t)
	file := f.put(t, a, nil, "x.txt")
	f.providers.forConnection(a.connection).setDeleteErr(provider.ErrObjectNotFound)
	if err := f.trashAndDelete(t, a, file.ID); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if got := f.count(t, `SELECT count(*) FROM files`); got != 0 {
		t.Errorf("records = %d, want 0", got)
	}
}

func testTrashFolder(t *testing.T, f *fixture) {
	a := f.account(t)
	parent := f.folder(t, a, nil, "Parent")
	child := f.folder(t, a, &parent.ID, "Child")
	f.put(t, a, &child.ID, "x.txt")

	if err := f.service.TrashFolder(f.ctx, a.id, parent.ID); err != nil {
		t.Fatalf("TrashFolder: %v", err)
	}
	if got := folderNames(f.list(t, a, nil).Folders); got != "" {
		t.Errorf("top level = %q", got)
	}
	for _, id := range []uuid.UUID{parent.ID, child.ID} {
		if _, err := f.service.List(f.ctx, a.id, &id); !errors.Is(err, ErrFolderNotFound) {
			t.Errorf("List: error = %v, want ErrFolderNotFound", err)
		}
	}
	if err := f.service.RestoreFolder(f.ctx, a.id, parent.ID); err != nil {
		t.Fatalf("RestoreFolder: %v", err)
	}
	if got := fileNames(f.list(t, a, &child.ID).Files); got != "x.txt" {
		t.Errorf("restored contents = %q", got)
	}
}

func testRestoreFolderToTop(t *testing.T, f *fixture) {
	a := f.account(t)
	parent := f.folder(t, a, nil, "Parent")
	child := f.folder(t, a, &parent.ID, "Child")
	if err := f.service.TrashFolder(f.ctx, a.id, child.ID); err != nil {
		t.Fatalf("TrashFolder child: %v", err)
	}
	if err := f.service.TrashFolder(f.ctx, a.id, parent.ID); err != nil {
		t.Fatalf("TrashFolder parent: %v", err)
	}
	if err := f.service.RestoreFolder(f.ctx, a.id, child.ID); err != nil {
		t.Fatalf("RestoreFolder: %v", err)
	}
	if got := folderNames(f.list(t, a, nil).Folders); got != "Child" {
		t.Errorf("top level = %q", got)
	}
}

func testMoveFolderCycle(t *testing.T, f *fixture) {
	a := f.account(t)
	parent := f.folder(t, a, nil, "Parent")
	child := f.folder(t, a, &parent.ID, "Child")
	if _, err := f.service.MoveFolder(f.ctx, a.id, parent.ID, &parent.ID); !errors.Is(err, ErrInvalidMove) {
		t.Errorf("into itself: error = %v, want ErrInvalidMove", err)
	}
	if _, err := f.service.MoveFolder(f.ctx, a.id, parent.ID, &child.ID); !errors.Is(err, ErrInvalidMove) {
		t.Errorf("into its child: error = %v, want ErrInvalidMove", err)
	}
	if _, err := f.service.MoveFolder(f.ctx, a.id, child.ID, nil); err != nil {
		t.Errorf("a legal move: %v", err)
	}
}

func testConcurrentFolderMoves(t *testing.T, f *fixture) {
	a := f.account(t)
	for range 20 {
		one := f.folder(t, a, nil, "One")
		two := f.folder(t, a, nil, "Two")
		var wg sync.WaitGroup
		wg.Go(func() { _, _ = f.service.MoveFolder(f.ctx, a.id, one.ID, &two.ID) })
		wg.Go(func() { _, _ = f.service.MoveFolder(f.ctx, a.id, two.ID, &one.ID) })
		wg.Wait()
		// With a loop neither folder could be reached from the top any more.
		got := f.count(t, `SELECT count(*) FROM folders WHERE id = ANY($1) AND parent_id IS NULL`, []uuid.UUID{one.ID, two.ID})
		if got != 1 {
			t.Fatalf("folders at the top = %d, want exactly 1", got)
		}
	}
}

func testDeleteFolder(t *testing.T, f *fixture) {
	a := f.account(t)
	second := f.connection(t, a.id)
	parent := f.folder(t, a, nil, "Parent")
	child := f.folder(t, a, &parent.ID, "Child")
	keep := f.folder(t, a, nil, "Keep")
	f.put(t, a, &parent.ID, "a.txt")
	f.put(t, a, &child.ID, "b.txt")
	f.put(t, a, &keep.ID, "keep.txt")
	in := upload(a, &child.ID, "other-account.txt", "x")
	in.ConnectionID = &second
	if _, err := f.service.Upload(f.ctx, a.id, in); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	trashedInside := f.put(t, a, &child.ID, "trashed-inside.txt")
	if err := f.service.TrashFile(f.ctx, a.id, trashedInside.ID); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}

	if err := f.service.TrashFolder(f.ctx, a.id, parent.ID); err != nil {
		t.Fatalf("TrashFolder: %v", err)
	}
	if err := f.service.DeleteFolder(f.ctx, a.id, parent.ID, testIP); err != nil {
		t.Fatalf("DeleteFolder: %v", err)
	}
	if got := f.objects(t, a.connection); got != 1 {
		t.Errorf("objects = %d, want only keep.txt", got)
	}
	if got := f.objects(t, second); got != 0 {
		t.Errorf("objects in the second account = %d, want 0", got)
	}
	if got := f.count(t, `SELECT count(*) FROM folders`); got != 1 {
		t.Errorf("folders = %d, want 1", got)
	}
	if got := fileNames(f.list(t, a, &keep.ID).Files); got != "keep.txt" {
		t.Errorf("kept folder = %q", got)
	}
	if got := f.count(t, `SELECT count(*) FROM audit_events WHERE action = 'file.deleted'`); got != 4 {
		t.Errorf("file audit events = %d, want 4", got)
	}
	if got := f.count(t, `SELECT count(*) FROM audit_events WHERE action = 'folder.deleted'`); got != 1 {
		t.Errorf("folder audit events = %d, want 1", got)
	}
}

func testDeleteFolderRetry(t *testing.T, f *fixture) {
	a := f.account(t)
	folder := f.folder(t, a, nil, "Folder")
	f.put(t, a, &folder.ID, "a.txt")
	if err := f.service.TrashFolder(f.ctx, a.id, folder.ID); err != nil {
		t.Fatalf("TrashFolder: %v", err)
	}
	stored := f.providers.forConnection(a.connection)
	stored.setDeleteErr(errors.New("connection reset"))
	if err := f.service.DeleteFolder(f.ctx, a.id, folder.ID, testIP); !errors.Is(err, ErrProviderFailed) {
		t.Fatalf("error = %v, want ErrProviderFailed", err)
	}
	if got := f.count(t, `SELECT count(*) FROM folders`); got != 1 {
		t.Errorf("folders = %d: the folder must stay so the delete can be retried", got)
	}
	stored.setDeleteErr(nil)
	if err := f.service.DeleteFolder(f.ctx, a.id, folder.ID, testIP); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got := f.objects(t, a.connection); got != 0 {
		t.Errorf("objects = %d, want 0", got)
	}
}

func testDeleteFolderNeedsTrash(t *testing.T, f *fixture) {
	a := f.account(t)
	folder := f.folder(t, a, nil, "Folder")
	f.put(t, a, &folder.ID, "a.txt")
	if err := f.service.DeleteFolder(f.ctx, a.id, folder.ID, testIP); !errors.Is(err, ErrNotInTrash) {
		t.Fatalf("error = %v, want ErrNotInTrash", err)
	}
	if got := f.objects(t, a.connection); got != 1 {
		t.Errorf("objects = %d, want 1", got)
	}
}

func testEmptyTrash(t *testing.T, f *fixture) {
	a, other := f.account(t), f.account(t)
	parent := f.folder(t, a, nil, "Parent")
	child := f.folder(t, a, &parent.ID, "Child")
	f.put(t, a, &child.ID, "in-folder.txt")
	loose := f.put(t, a, nil, "loose.txt")
	f.put(t, a, nil, "keep.txt")
	theirs := f.put(t, other, nil, "theirs.txt")
	for _, call := range []error{
		f.service.TrashFile(f.ctx, a.id, loose.ID),
		f.service.TrashFile(f.ctx, other.id, theirs.ID),
		f.service.TrashFolder(f.ctx, a.id, child.ID),
		f.service.TrashFolder(f.ctx, a.id, parent.ID),
	} {
		if call != nil {
			t.Fatalf("trash: %v", call)
		}
	}
	if err := f.service.EmptyTrash(f.ctx, a.id, testIP); err != nil {
		t.Fatalf("EmptyTrash: %v", err)
	}
	if got := f.objects(t, a.connection); got != 1 {
		t.Errorf("objects = %d, want only keep.txt", got)
	}
	if got := f.objects(t, other.connection); got != 1 {
		t.Errorf("someone else's trash was emptied: objects = %d", got)
	}
	trash, err := f.service.ListTrash(f.ctx, a.id)
	if err != nil {
		t.Fatalf("ListTrash: %v", err)
	}
	if len(trash.Files) != 0 || len(trash.Folders) != 0 {
		t.Errorf("trash = %+v", trash)
	}
}

func testTrashListing(t *testing.T, f *fixture) {
	a := f.account(t)
	folder := f.folder(t, a, nil, "Folder")
	f.put(t, a, &folder.ID, "inside.txt")
	if err := f.service.TrashFolder(f.ctx, a.id, folder.ID); err != nil {
		t.Fatalf("TrashFolder: %v", err)
	}
	trash, err := f.service.ListTrash(f.ctx, a.id)
	if err != nil {
		t.Fatalf("ListTrash: %v", err)
	}
	if got := folderNames(trash.Folders); got != "Folder" {
		t.Errorf("folders = %q", got)
	}
	if got := fileNames(trash.Files); got != "" {
		t.Errorf("files = %q: a trashed folder's contents travel with it", got)
	}
}

func testConnectionRemoval(t *testing.T, f *fixture) {
	a := f.account(t)
	f.put(t, a, nil, "x.txt")
	if _, err := f.pool.Exec(f.ctx, `DELETE FROM storage_connections WHERE id = $1`, a.connection); err != nil {
		t.Fatalf("delete connection: %v", err)
	}
	if got := fileNames(f.list(t, a, nil).Files); got != "" {
		t.Errorf("listing = %q", got)
	}
	if got := f.objects(t, a.connection); got != 1 {
		t.Errorf("objects = %d: removing a connection must leave the account alone", got)
	}
}

func testOpen(t *testing.T, f *fixture) {
	a := f.account(t)
	file, err := f.service.Upload(f.ctx, a.id, upload(a, nil, "x.txt", "0123456789"))
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	read := func(rng *provider.ByteRange) string {
		t.Helper()
		reader, err := f.service.Open(f.ctx, a.id, file.ID, rng)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer func() { _ = reader.Close() }()
		var body bytes.Buffer
		if _, err := body.ReadFrom(reader); err != nil {
			t.Fatalf("read: %v", err)
		}
		return body.String()
	}
	if got := read(nil); got != "0123456789" {
		t.Errorf("body = %q", got)
	}
	if got := read(&provider.ByteRange{Start: 2, End: 4}); got != "234" {
		t.Errorf("range = %q", got)
	}

	stored := f.providers.forConnection(a.connection)
	stored.getErr = provider.ErrObjectNotFound
	if _, err := f.service.Open(f.ctx, a.id, file.ID, nil); !errors.Is(err, ErrObjectMissing) {
		t.Errorf("missing object: error = %v, want ErrObjectMissing", err)
	}
	stored.getErr = errors.New("connection reset")
	if _, err := f.service.Open(f.ctx, a.id, file.ID, nil); !errors.Is(err, ErrProviderFailed) {
		t.Errorf("provider down: error = %v, want ErrProviderFailed", err)
	}
}

// Moves can never make a loop, but one could still come from a manual edit of the
// database. It must not be able to hang the queries that walk the tree.
func testFolderLoopCannotHang(t *testing.T, f *fixture) {
	a := f.account(t)
	one := f.folder(t, a, nil, "One")
	two := f.folder(t, a, &one.ID, "Two")
	if _, err := f.pool.Exec(f.ctx, `UPDATE folders SET parent_id = $1 WHERE id = $2`, two.ID, one.ID); err != nil {
		t.Fatalf("make a loop: %v", err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	if _, err := f.service.List(ctx, a.id, &one.ID); errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		t.Fatalf("List hung: %v", err)
	}
	if err := f.service.TrashFolder(ctx, a.id, one.ID); err != nil {
		t.Fatalf("TrashFolder: %v", err)
	}
	if err := f.service.DeleteFolder(ctx, a.id, one.ID, testIP); err != nil {
		t.Fatalf("DeleteFolder: %v", err)
	}
}

func (p *providers) ReserveTransfers(context.Context, uuid.UUID, storage.TransferRequest) (int, error) {
	return 0, nil
}

func (p *providers) StopDraining(context.Context, uuid.UUID, uuid.UUID) error { return nil }
