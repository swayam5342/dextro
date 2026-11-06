package files

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/db"
	"github.com/swayam5342/dextro/internal/db/dbtest"
	"github.com/swayam5342/dextro/internal/httpx"
	"github.com/swayam5342/dextro/internal/httpx/apigen"
	"github.com/swayam5342/dextro/internal/provider"
)

// unzip reads what was written, and fails the test if it isn't a complete archive.
func unzip(t *testing.T, body []byte) map[string]string {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	found := map[string]string{}
	for _, entry := range reader.File {
		opened, err := entry.Open()
		if err != nil {
			t.Fatalf("open %s: %v", entry.Name, err)
		}
		content, err := io.ReadAll(opened)
		_ = opened.Close()
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name, err)
		}
		found[entry.Name] = string(content)
	}
	return found
}

func (f *uploadFixture) zipped(t *testing.T, userID uuid.UUID, files, folders []uuid.UUID) map[string]string {
	t.Helper()
	plan, err := f.service.PlanZip(f.ctx, userID, files, folders)
	if err != nil {
		t.Fatalf("PlanZip: %v", err)
	}
	var out bytes.Buffer
	if err := f.service.WriteZip(f.ctx, userID, plan, &out); err != nil {
		t.Fatalf("WriteZip: %v", err)
	}
	return unzip(t, out.Bytes())
}

func zipNames(found map[string]string) string {
	list := make([]string, 0, len(found))
	for name := range found {
		list = append(list, name)
	}
	slices.Sort(list)
	return strings.Join(list, " ")
}

func TestZip(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	tests := []struct {
		name string
		run  func(*testing.T, *uploadFixture)
	}{
		{"selected files go in a zip", testZipFiles},
		{"a folder keeps its shape", testZipFolder},
		{"an empty folder is included", testZipEmptyFolder},
		{"the zip is named after a single folder", testZipName},
		{"trashed items are left out", testZipLeavesOutTrash},
		{"names that clash are kept apart", testZipDuplicateNames},
		{"names that differ by case are kept apart", testZipCaseClash},
		{"a file gone from drive is listed, not fatal", testZipMissingFile},
		{"an added file is included", testZipExternalFile},
		{"nothing to zip is refused", testZipNothing},
		{"too many items are refused", testZipTooManyItems},
		{"too many files in a folder are refused", testZipTooManyFiles},
		{"someone else's file can't be zipped", testZipForeignFile},
		{"someone else's folder can't be zipped", testZipForeignFolder},
		{"a trashed folder can't be zipped", testZipTrashedFolder},
		{"a drive failure stops the zip", testZipProviderFailure},
		{"memory doesn't grow with the zip", testZipMemoryStaysFlat},
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

func testZipFiles(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	one := f.put1(t, user, nil, "one.txt")
	two := f.put1(t, user, nil, "two.txt")
	got := f.zipped(t, user, []uuid.UUID{one.ID, two.ID}, nil)
	if len(got) != 2 || got["one.txt"] != "hello" || got["two.txt"] != "hello" {
		t.Errorf("zip = %v", got)
	}
}

func testZipFolder(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	trip := f.folder(t, user, nil, "Trip")
	day := f.folder(t, user, &trip.ID, "Day 1")
	f.put1(t, user, &trip.ID, "plan.txt")
	f.put1(t, user, &day.ID, "beach.txt")
	got := f.zipped(t, user, nil, []uuid.UUID{trip.ID})
	if got["Trip/plan.txt"] != "hello" || got["Trip/Day 1/beach.txt"] != "hello" {
		t.Errorf("zip = %s", zipNames(got))
	}
}

func testZipEmptyFolder(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	folder := f.folder(t, user, nil, "Empty")
	got := f.zipped(t, user, nil, []uuid.UUID{folder.ID})
	if _, ok := got["Empty/"]; !ok {
		t.Errorf("zip = %s", zipNames(got))
	}
}

func testZipName(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	folder := f.folder(t, user, nil, "Trip")
	file := f.put1(t, user, nil, "a.txt")
	one, err := f.service.PlanZip(f.ctx, user, nil, []uuid.UUID{folder.ID})
	if err != nil {
		t.Fatalf("PlanZip: %v", err)
	}
	if one.Name != "Trip.zip" {
		t.Errorf("name = %q", one.Name)
	}
	both, err := f.service.PlanZip(f.ctx, user, []uuid.UUID{file.ID}, []uuid.UUID{folder.ID})
	if err != nil {
		t.Fatalf("PlanZip: %v", err)
	}
	if both.Name != "dextro.zip" {
		t.Errorf("name = %q", both.Name)
	}
}

func testZipLeavesOutTrash(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	folder := f.folder(t, user, nil, "Trip")
	gone := f.put1(t, user, &folder.ID, "gone.txt")
	f.put1(t, user, &folder.ID, "kept.txt")
	inner := f.folder(t, user, &folder.ID, "Gone folder")
	if err := f.service.TrashFile(f.ctx, user, gone.ID, ""); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if err := f.service.TrashFolder(f.ctx, user, inner.ID, ""); err != nil {
		t.Fatalf("TrashFolder: %v", err)
	}
	got := f.zipped(t, user, nil, []uuid.UUID{folder.ID})
	if len(got) != 2 || got["Trip/kept.txt"] != "hello" {
		t.Errorf("zip = %s", zipNames(got))
	}
}

func testZipDuplicateNames(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	one := f.put1(t, user, nil, "same.txt")
	two := f.put1(t, user, nil, "same.txt")
	three := f.put1(t, user, nil, "same.txt")
	got := f.zipped(t, user, []uuid.UUID{one.ID, two.ID, three.ID}, nil)
	for _, name := range []string{"same.txt", "same (2).txt", "same (3).txt"} {
		if _, ok := got[name]; !ok {
			t.Errorf("%q is missing: %s", name, zipNames(got))
		}
	}
}

func testZipCaseClash(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	one := f.put1(t, user, nil, "Photo.txt")
	two := f.put1(t, user, nil, "photo.txt")
	got := f.zipped(t, user, []uuid.UUID{one.ID, two.ID}, nil)
	if len(got) != 2 || got["Photo.txt"] == "" || got["photo (2).txt"] == "" {
		t.Errorf("zip = %s, Windows would overwrite one with the other", zipNames(got))
	}
}

func testZipMissingFile(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	gone := f.put1(t, user, nil, "gone.txt")
	kept := f.put1(t, user, nil, "kept.txt")
	if err := f.account("a@gmail.com").Delete(f.ctx, f.objectID(t, user, gone.ID)); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got := f.zipped(t, user, []uuid.UUID{gone.ID, kept.ID}, nil)
	if got["kept.txt"] != "hello" {
		t.Errorf("zip = %s", zipNames(got))
	}
	if _, ok := got["gone.txt"]; ok {
		t.Errorf("zip = %s", zipNames(got))
	}
	if !strings.Contains(got[missingFilesNote], "gone.txt") {
		t.Errorf("note = %q, the zip should say what's missing", got[missingFilesNote])
	}
}

func testZipExternalFile(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	id, _ := f.addedFile(t, user, connection)
	got := f.zipped(t, user, []uuid.UUID{id}, nil)
	if got["mine.jpg"] != "mine" {
		t.Errorf("zip = %v", got)
	}
}

func testZipNothing(t *testing.T, f *uploadFixture) {
	if _, err := f.service.PlanZip(f.ctx, f.user(t), nil, nil); !errors.Is(err, ErrInvalidZip) {
		t.Errorf("error = %v, want ErrInvalidZip", err)
	}
}

func testZipTooManyItems(t *testing.T, f *uploadFixture) {
	ids := make([]uuid.UUID, maxZipItems+1)
	if _, err := f.service.PlanZip(f.ctx, f.user(t), ids, nil); !errors.Is(err, ErrInvalidZip) {
		t.Errorf("error = %v, want ErrInvalidZip", err)
	}
}

func testZipTooManyFiles(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	folder := f.folder(t, user, nil, "Big")
	_, err := f.pool.Exec(f.ctx, `INSERT INTO files (user_id, connection_id, folder_id, object_id, name, size, content_type)
		SELECT $1, $2, $3, 'o' || n, 'f' || n, 1, 'text/plain' FROM generate_series(1, $4::int) n`, user, connection, folder.ID, maxZipFiles+1)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := f.service.PlanZip(f.ctx, user, nil, []uuid.UUID{folder.ID}); !errors.Is(err, ErrZipTooLarge) {
		t.Errorf("error = %v, want ErrZipTooLarge", err)
	}
}

func testZipForeignFile(t *testing.T, f *uploadFixture) {
	owner := f.user(t)
	f.link(t, owner, "a@gmail.com", 1<<30)
	file := f.put1(t, owner, nil, "secret.txt")
	if _, err := f.service.PlanZip(f.ctx, f.user(t), []uuid.UUID{file.ID}, nil); !errors.Is(err, ErrFileNotFound) {
		t.Errorf("error = %v, want ErrFileNotFound", err)
	}
}

func testZipForeignFolder(t *testing.T, f *uploadFixture) {
	owner := f.user(t)
	f.link(t, owner, "a@gmail.com", 1<<30)
	folder := f.folder(t, owner, nil, "Secret")
	if _, err := f.service.PlanZip(f.ctx, f.user(t), nil, []uuid.UUID{folder.ID}); !errors.Is(err, ErrFolderNotFound) {
		t.Errorf("error = %v, want ErrFolderNotFound", err)
	}
}

func testZipTrashedFolder(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	folder := f.folder(t, user, nil, "Gone")
	if err := f.service.TrashFolder(f.ctx, user, folder.ID, ""); err != nil {
		t.Fatalf("TrashFolder: %v", err)
	}
	if _, err := f.service.PlanZip(f.ctx, user, nil, []uuid.UUID{folder.ID}); !errors.Is(err, ErrFolderNotFound) {
		t.Errorf("error = %v, want ErrFolderNotFound", err)
	}
}

// failingGet is an account that stops answering.
type failingGet struct{ provider.Provider }

func (failingGet) Get(context.Context, string, *provider.ByteRange) (io.ReadCloser, error) {
	return nil, errors.New("backend error")
}

func testZipProviderFailure(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	file := f.put1(t, user, nil, "a.txt")
	plan, err := f.service.PlanZip(f.ctx, user, []uuid.UUID{file.ID}, nil)
	if err != nil {
		t.Fatalf("PlanZip: %v", err)
	}
	f.wrap = func(p provider.Provider) provider.Provider { return failingGet{p} }
	if err := f.service.WriteZip(f.ctx, user, plan, io.Discard); !errors.Is(err, ErrProviderFailed) {
		t.Errorf("error = %v, want ErrProviderFailed", err)
	}
}

// bigFile is an account where every file is huge.
type bigFile struct {
	provider.Provider
	size int64
}

func (b bigFile) Get(context.Context, string, *provider.ByteRange) (io.ReadCloser, error) {
	return io.NopCloser(io.LimitReader(zeros{}, b.size)), nil
}

// Rule 7 applies to what goes out too: a zip streams, however large it is.
func testZipMemoryStaysFlat(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	one := f.put1(t, user, nil, "one.bin")
	two := f.put1(t, user, nil, "two.bin")
	const size = 256 << 20
	const limit = 32 << 20
	f.wrap = func(p provider.Provider) provider.Provider { return bigFile{p, size} }
	plan, err := f.service.PlanZip(f.ctx, user, []uuid.UUID{one.ID, two.ID}, nil)
	if err != nil {
		t.Fatalf("PlanZip: %v", err)
	}

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
			case <-time.After(5 * time.Millisecond):
				var now runtime.MemStats
				runtime.ReadMemStats(&now)
				if now.HeapAlloc > peak.Load() {
					peak.Store(now.HeapAlloc)
				}
			}
		}
	}()
	err = f.service.WriteZip(f.ctx, user, plan, io.Discard)
	close(done)
	<-sampled
	if err != nil {
		t.Fatalf("WriteZip: %v", err)
	}
	if grew := peak.Load() - min(peak.Load(), before.HeapAlloc); grew > limit {
		t.Errorf("heap grew by %d MiB for a 512 MiB zip", grew>>20)
	}
}

func TestZipHandler(t *testing.T) {
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
	f.link(t, owner, "a@gmail.com", 1<<30)
	folder := f.folder(t, owner, nil, `Trip "2026"`)
	file := f.put1(t, owner, &folder.ID, "a.txt")
	get := func(user uuid.UUID, query url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/zip?"+query.Encode(), nil)
		req.AddCookie(&http.Cookie{Name: httpx.SessionCookieName, Value: user.String()})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	rec := get(owner, url.Values{"folder": {folder.ID.String()}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if got := unzip(t, rec.Body.Bytes()); got[`Trip "2026"/a.txt`] != "hello" {
		t.Errorf("zip = %s", zipNames(got))
	}
	head := rec.Header()
	if head.Get("Content-Type") != "application/zip" || !strings.HasPrefix(head.Get("Content-Disposition"), "attachment") {
		t.Errorf("headers = %v", head)
	}
	if head.Get("X-Content-Type-Options") != "nosniff" || head.Get("Content-Security-Policy") != "sandbox" {
		t.Errorf("headers = %v", head)
	}

	checked := get(owner, url.Values{"folder": {folder.ID.String()}, "check": {"true"}})
	if checked.Code != http.StatusNoContent || checked.Body.Len() != 0 {
		t.Errorf("check: status = %d, %d bytes", checked.Code, checked.Body.Len())
	}
	refused := get(f.user(t), url.Values{"file": {file.ID.String()}, "check": {"true"}})
	if refused.Code != http.StatusNotFound {
		t.Errorf("check by a stranger: status = %d", refused.Code)
	}

	if rec := get(owner, url.Values{}); rec.Code != http.StatusBadRequest {
		t.Errorf("nothing: status = %d", rec.Code)
	}
	if rec := get(owner, url.Values{"file": {"not-an-id"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id: status = %d", rec.Code)
	}
	// Leaving out what can't be read would hand back less than was asked for.
	if rec := get(owner, url.Values{"file": {file.ID.String(), "not-an-id"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("one bad id among good: status = %d", rec.Code)
	}
	if rec := get(f.user(t), url.Values{"file": {file.ID.String()}}); rec.Code != http.StatusNotFound {
		t.Errorf("a stranger: status = %d", rec.Code)
	}

	// Headers have gone by the time Drive fails, so the connection has to break, or
	// the browser would call a broken zip finished.
	f.wrap = func(p provider.Provider) provider.Provider { return failingGet{p} }
	defer func() {
		if got := recover(); got != http.ErrAbortHandler {
			t.Errorf("recovered %v, want the connection to be cut", got)
		}
	}()
	get(owner, url.Values{"file": {file.ID.String()}})
	t.Error("a failed zip finished normally")
}
