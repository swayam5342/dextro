package files

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/db"
	"github.com/swayam5342/dextro/internal/db/dbtest"
	"github.com/swayam5342/dextro/internal/httpx"
	"github.com/swayam5342/dextro/internal/httpx/apigen"
	"github.com/swayam5342/dextro/internal/provider"
	"github.com/swayam5342/dextro/internal/storage"
)

const longAgo = 2 * syncGrace

// stored puts an object in an account, as an earlier upload would have.
func (f *uploadFixture) stored(t *testing.T, email, name, contentType string) provider.Object {
	t.Helper()
	object, err := f.account(email).Put(f.ctx, provider.Upload{Name: name, ContentType: contentType, Body: strings.NewReader("hello")})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	f.account(email).Age(longAgo)
	return object
}

// recorded uploads a file through dextro and makes it old enough to be judged.
func (f *uploadFixture) recorded(t *testing.T, userID uuid.UUID, email, name string) File {
	t.Helper()
	file, err := f.service.Upload(f.ctx, userID, UploadInput{Name: name, ContentType: "text/plain", Size: 5, Body: strings.NewReader("hello")})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	f.account(email).Age(longAgo)
	if _, err := f.pool.Exec(f.ctx, `UPDATE files SET created_at = $2 WHERE id = $1`, file.ID, time.Now().Add(-longAgo)); err != nil {
		t.Fatalf("age file: %v", err)
	}
	return file
}

func (f *uploadFixture) sync(t *testing.T, userID, connection uuid.UUID) SyncResult {
	t.Helper()
	result, err := f.service.SyncConnection(f.ctx, userID, connection, "203.0.113.7")
	if err != nil {
		t.Fatalf("SyncConnection: %v", err)
	}
	return result
}

func (f *uploadFixture) files(t *testing.T, userID uuid.UUID) int {
	t.Helper()
	return f.count(t, `SELECT count(*) FROM files WHERE user_id = $1`, userID)
}

func TestSync(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	tests := []struct {
		name string
		run  func(*testing.T, *uploadFixture)
	}{
		{"files in the account come back after losing the database", testSyncImports},
		{"names that can't be used are replaced", testSyncCleansNames},
		{"syncing again changes nothing", testSyncIsRepeatable},
		{"syncing at the same time makes no duplicates", testConcurrentSyncs},
		{"a file gone from the account leaves the app", testSyncRemoves},
		{"a trashed file gone from the account leaves too", testSyncRemovesTrashed},
		{"a file put back in the account returns", testSyncBringsBack},
		{"files still in the account are left alone", testSyncKeepsWhatsThere},
		{"a new object isn't imported while its upload finishes", testSyncSkipsNewObjects},
		{"a file just uploaded and deleted leaves straight away", testSyncRemovesNewFiles},
		{"a file the listing missed but the account has stays", testSyncKeepsLaggingFiles},
		{"a failed lookup removes nothing", testFailedLookupRemovesNothing},
		{"a listing that fails partway removes nothing", testPartialListingRemovesNothing},
		{"a missing folder removes nothing", testMissingFolderRemovesNothing},
		{"an unreachable account removes nothing", testUnreachableRemovesNothing},
		{"someone else's account can't be synced", testSyncIsScoped},
		{"changes are audited", testSyncAudited},
		{"nothing changing isn't audited", testQuietSyncNotAudited},
		{"the job covers everyone and survives a broken account", testSyncAll},
		{"more files than one page all count", testSyncAcrossPages},
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

func testSyncImports(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	object := f.stored(t, "a@gmail.com", "photo.png", "image/png")

	if got := f.sync(t, user, connection); got.Added != 1 || got.Removed != 0 {
		t.Fatalf("result = %+v", got)
	}
	contents, err := f.service.List(f.ctx, user, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(contents.Files) != 1 {
		t.Fatalf("files = %+v", contents.Files)
	}
	file := contents.Files[0]
	if file.Name != "photo.png" || file.Size != object.Size || file.ContentType != "image/png" || file.ConnectionID != connection {
		t.Errorf("file = %+v", file)
	}
	opened, err := f.service.Open(f.ctx, user, file.ID, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = opened.Close() }()
	if body, _ := io.ReadAll(opened); string(body) != "hello" {
		t.Errorf("body = %q", body)
	}
}

func testSyncCleansNames(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	f.stored(t, "a@gmail.com", "a/b.txt", "text/plain")
	if got := f.sync(t, user, connection); got.Added != 1 {
		t.Fatalf("result = %+v", got)
	}
	contents, err := f.service.List(f.ctx, user, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if name := contents.Files[0].Name; strings.ContainsAny(name, `/\`) || name == "" {
		t.Errorf("name = %q", name)
	}
}

func testSyncIsRepeatable(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	f.stored(t, "a@gmail.com", "a.txt", "text/plain")
	f.sync(t, user, connection)
	if got := f.sync(t, user, connection); got.Added != 0 || got.Removed != 0 {
		t.Errorf("second sync = %+v", got)
	}
	if got := f.files(t, user); got != 1 {
		t.Errorf("files = %d", got)
	}
}

// listGate holds every listing until all of them have started, so that every sync
// has read the same records before any of them adds one.
type listGate struct {
	provider.Provider
	arrived *sync.WaitGroup
}

func (g listGate) List(ctx context.Context, token string) (provider.ObjectPage, error) {
	g.arrived.Done()
	g.arrived.Wait()
	return g.Provider.List(ctx, token)
}

func testConcurrentSyncs(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	f.stored(t, "a@gmail.com", "a.txt", "text/plain")
	f.stored(t, "a@gmail.com", "b.txt", "text/plain")
	const syncs = 6
	var arrived sync.WaitGroup
	arrived.Add(syncs)
	f.wrap = func(p provider.Provider) provider.Provider { return listGate{p, &arrived} }
	var wg sync.WaitGroup
	for range syncs {
		wg.Go(func() {
			if _, err := f.service.SyncConnection(f.ctx, user, connection, "job"); err != nil {
				t.Errorf("SyncConnection: %v", err)
			}
		})
	}
	wg.Wait()
	if got := f.files(t, user); got != 2 {
		t.Errorf("files = %d, want 2", got)
	}
}

func testSyncRemoves(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	gone := f.recorded(t, user, "a@gmail.com", "gone.txt")
	f.recorded(t, user, "a@gmail.com", "kept.txt")
	record, err := NewStore(f.pool).FileForOwner(f.ctx, user, gone.ID)
	if err != nil {
		t.Fatalf("FileForOwner: %v", err)
	}
	if err := f.account("a@gmail.com").Delete(f.ctx, record.objectID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := f.sync(t, user, connection); got.Removed != 1 || got.Added != 0 {
		t.Fatalf("result = %+v", got)
	}
	if _, err := f.service.Get(f.ctx, user, gone.ID); !errors.Is(err, ErrFileNotFound) {
		t.Errorf("error = %v, want ErrFileNotFound", err)
	}
	if got := f.files(t, user); got != 1 {
		t.Errorf("files = %d", got)
	}
}

func testSyncRemovesTrashed(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	file := f.recorded(t, user, "a@gmail.com", "gone.txt")
	if err := f.service.TrashFile(f.ctx, user, file.ID); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	record, err := NewStore(f.pool).FileForOwner(f.ctx, user, file.ID)
	if err != nil {
		t.Fatalf("FileForOwner: %v", err)
	}
	if err := f.account("a@gmail.com").Delete(f.ctx, record.objectID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := f.sync(t, user, connection); got.Removed != 1 {
		t.Errorf("result = %+v", got)
	}
}

// Trashing in Drive and then restoring there is the same file going and coming back.
func testSyncBringsBack(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	object := f.stored(t, "a@gmail.com", "a.txt", "text/plain")
	f.sync(t, user, connection)
	account := f.account("a@gmail.com")
	account.Hide(object.ID)
	if _, err := f.pool.Exec(f.ctx, `UPDATE files SET created_at = $1`, time.Now().Add(-longAgo)); err != nil {
		t.Fatalf("age: %v", err)
	}
	if got := f.sync(t, user, connection); got.Removed != 1 {
		t.Fatalf("after hiding: %+v", got)
	}
	account.Unhide(object.ID)
	if got := f.sync(t, user, connection); got.Added != 1 {
		t.Errorf("after restoring: %+v", got)
	}
}

func testSyncKeepsWhatsThere(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	folder, err := f.service.CreateFolder(f.ctx, user, nil, "Photos")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	moved := f.recorded(t, user, "a@gmail.com", "moved.txt")
	if _, err := f.service.MoveFile(f.ctx, user, moved.ID, &folder.ID); err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	trashed := f.recorded(t, user, "a@gmail.com", "trashed.txt")
	if err := f.service.TrashFile(f.ctx, user, trashed.ID); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if got := f.sync(t, user, connection); got.Added != 0 || got.Removed != 0 {
		t.Fatalf("result = %+v", got)
	}
	still, err := f.service.Get(f.ctx, user, moved.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if still.FolderID == nil || *still.FolderID != folder.ID {
		t.Errorf("the sync moved a file: %+v", still)
	}
	if got := f.count(t, `SELECT count(*) FROM files WHERE id = $1 AND trashed_at IS NOT NULL`, trashed.ID); got != 1 {
		t.Error("the sync took a file out of the trash")
	}
}

// An upload stores the object a moment before saving its record. Importing in that
// moment would make the upload's own save fail, and it would delete the object.
func testSyncSkipsNewObjects(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	if _, err := f.account("a@gmail.com").Put(f.ctx, provider.Upload{Name: "a.txt", Body: strings.NewReader("x")}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if got := f.sync(t, user, connection); got.Added != 0 {
		t.Errorf("result = %+v", got)
	}
}

func (f *uploadFixture) objectID(t *testing.T, userID, fileID uuid.UUID) string {
	t.Helper()
	record, err := NewStore(f.pool).FileForOwner(f.ctx, userID, fileID)
	if err != nil {
		t.Fatalf("FileForOwner: %v", err)
	}
	return record.objectID
}

// What someone does to try this out: upload, delete it in Drive, press Sync.
func testSyncRemovesNewFiles(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	file, err := f.service.Upload(f.ctx, user, UploadInput{Name: "a.txt", Size: 1, Body: strings.NewReader("x")})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if err := f.account("a@gmail.com").Delete(f.ctx, f.objectID(t, user, file.ID)); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := f.sync(t, user, connection); got.Removed != 1 {
		t.Errorf("result = %+v", got)
	}
}

// A listing can lag behind a new file, so missing from it doesn't prove anything.
func testSyncKeepsLaggingFiles(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	file, err := f.service.Upload(f.ctx, user, UploadInput{Name: "a.txt", Size: 1, Body: strings.NewReader("x")})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	f.account("a@gmail.com").Unlist(f.objectID(t, user, file.ID))
	if got := f.sync(t, user, connection); got.Removed != 0 {
		t.Errorf("result = %+v", got)
	}
}

func testFailedLookupRemovesNothing(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	file := f.recorded(t, user, "a@gmail.com", "a.txt")
	account := f.account("a@gmail.com")
	account.Hide(f.objectID(t, user, file.ID))
	account.ExistsErr = errors.New("backend error")
	if _, err := f.service.SyncConnection(f.ctx, user, connection, "job"); !errors.Is(err, ErrProviderFailed) {
		t.Fatalf("error = %v, want ErrProviderFailed", err)
	}
	if got := f.files(t, user); got != 1 {
		t.Errorf("files = %d", got)
	}
}

func testPartialListingRemovesNothing(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	account := f.account("a@gmail.com")
	f.recorded(t, user, "a@gmail.com", "first.txt")
	f.recorded(t, user, "a@gmail.com", "second.txt")
	file := f.recorded(t, user, "a@gmail.com", "third.txt")
	record, err := NewStore(f.pool).FileForOwner(f.ctx, user, file.ID)
	if err != nil {
		t.Fatalf("FileForOwner: %v", err)
	}
	account.SetPageSize(1)
	account.ListFailsAfter = 1
	account.Hide(record.objectID)
	if _, err := f.service.SyncConnection(f.ctx, user, connection, "job"); !errors.Is(err, ErrProviderFailed) {
		t.Fatalf("error = %v, want ErrProviderFailed", err)
	}
	if got := f.files(t, user); got != 3 {
		t.Errorf("files = %d, a failed listing must remove nothing", got)
	}
}

func testMissingFolderRemovesNothing(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	f.recorded(t, user, "a@gmail.com", "a.txt")
	account := f.account("a@gmail.com")
	// A trashed dextro folder lists as empty. That mustn't read as everything deleted.
	account.Hide("1")
	account.CheckErr = errFolderMissing
	if _, err := f.service.SyncConnection(f.ctx, user, connection, "job"); !errors.Is(err, ErrProviderFailed) {
		t.Fatalf("error = %v, want ErrProviderFailed", err)
	}
	if got := f.files(t, user); got != 1 {
		t.Errorf("files = %d", got)
	}
}

var errFolderMissing = errors.New("dextro's drive folder is missing")

func testUnreachableRemovesNothing(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	f.recorded(t, user, "a@gmail.com", "a.txt")
	f.account("a@gmail.com").CheckErr = errors.New("token expired")
	if _, err := f.service.SyncConnection(f.ctx, user, connection, "job"); !errors.Is(err, ErrProviderFailed) {
		t.Fatalf("error = %v, want ErrProviderFailed", err)
	}
	if got := f.files(t, user); got != 1 {
		t.Errorf("files = %d", got)
	}
}

func testSyncIsScoped(t *testing.T, f *uploadFixture) {
	owner := f.user(t)
	connection := f.link(t, owner, "a@gmail.com", 1<<30)
	f.stored(t, "a@gmail.com", "a.txt", "text/plain")
	stranger := f.user(t)
	if _, err := f.service.SyncConnection(f.ctx, stranger, connection, "job"); !errors.Is(err, storage.ErrConnectionNotFound) {
		t.Errorf("error = %v, want ErrConnectionNotFound", err)
	}
	if got := f.files(t, stranger) + f.files(t, owner); got != 0 {
		t.Errorf("files = %d", got)
	}
	f.sync(t, owner, connection)
	objects, err := NewStore(f.pool).ObjectsForConnection(f.ctx, stranger, connection)
	if err != nil {
		t.Fatalf("ObjectsForConnection: %v", err)
	}
	if len(objects) != 0 {
		t.Errorf("a stranger read %d of someone's objects", len(objects))
	}
}

func testSyncAudited(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	f.stored(t, "a@gmail.com", "a.txt", "text/plain")
	f.sync(t, user, connection)
	got := f.count(t, `SELECT count(*) FROM audit_events WHERE action = 'storage.synced' AND actor_id = $1
		AND metadata->>'added' = '1' AND metadata->>'removed' = '0'`, user)
	if got != 1 {
		t.Errorf("audit events = %d, want 1", got)
	}
}

func testQuietSyncNotAudited(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	f.sync(t, user, connection)
	if got := f.count(t, `SELECT count(*) FROM audit_events WHERE action = 'storage.synced'`); got != 0 {
		t.Errorf("audit events = %d", got)
	}
}

func testSyncAll(t *testing.T, f *uploadFixture) {
	first := f.user(t)
	f.link(t, first, "a@gmail.com", 1<<30)
	f.stored(t, "a@gmail.com", "a.txt", "text/plain")
	second := f.user(t)
	f.link(t, second, "broken@gmail.com", 1<<30)
	f.account("broken@gmail.com").CheckErr = errors.New("token expired")
	third := f.user(t)
	f.link(t, third, "c@gmail.com", 1<<30)
	f.stored(t, "c@gmail.com", "c.txt", "text/plain")

	result, err := f.service.SyncAll(f.ctx)
	if err != nil {
		t.Fatalf("SyncAll: %v", err)
	}
	if result.Added != 2 || result.Skipped != 1 {
		t.Errorf("result = %+v", result)
	}
	if f.files(t, first) != 1 || f.files(t, third) != 1 {
		t.Errorf("files: %d and %d", f.files(t, first), f.files(t, third))
	}
}

func testSyncAcrossPages(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	f.account("a@gmail.com").SetPageSize(2)
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		f.stored(t, "a@gmail.com", name, "text/plain")
	}
	if got := f.sync(t, user, connection); got.Added != 5 {
		t.Fatalf("first: %+v", got)
	}
	if _, err := f.pool.Exec(f.ctx, `UPDATE files SET created_at = $1`, time.Now().Add(-longAgo)); err != nil {
		t.Fatalf("age: %v", err)
	}
	if got := f.sync(t, user, connection); got.Added != 0 || got.Removed != 0 {
		t.Errorf("files past the first page were treated as gone: %+v", got)
	}
}

func TestSyncHandler(t *testing.T) {
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
	connection := f.link(t, owner, "a@gmail.com", 1<<30)
	f.stored(t, "a@gmail.com", "a.txt", "text/plain")
	sync := func(user uuid.UUID) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/storage/connections/"+connection.String()+"/sync", nil)
		req.Header.Set("Origin", testOrigin)
		req.AddCookie(&http.Cookie{Name: httpx.SessionCookieName, Value: user.String()})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	if rec := sync(f.user(t)); rec.Code != http.StatusNotFound {
		t.Errorf("a stranger: status = %d", rec.Code)
	}
	rec := sync(owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if got := decode[apigen.SyncResult](t, rec); got.Added != 1 || got.Removed != 0 {
		t.Errorf("result = %+v", got)
	}
	f.account("a@gmail.com").CheckErr = errors.New("token expired")
	if rec := sync(owner); rec.Code != http.StatusBadGateway {
		t.Errorf("unreachable: status = %d", rec.Code)
	}
}
