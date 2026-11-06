package files

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/swayam5342/tributary/internal/db"
	"github.com/swayam5342/tributary/internal/db/dbtest"
	"github.com/swayam5342/tributary/internal/httpx"
	"github.com/swayam5342/tributary/internal/httpx/apigen"
	"github.com/swayam5342/tributary/internal/storage"
)

// TestSearchAndPool shares one Postgres between its subtests, emptying it each time.
func TestSearchAndPool(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tests := []struct {
		name string
		run  func(*testing.T, *server)
	}{
		{"search matches part of a name, in any case", testSearchMatches},
		{"search finds folders too", testSearchFolders},
		{"search leaves out the trash", testSearchSkipsTrash},
		{"search leaves out what's inside a trashed folder", testSearchSkipsTrashedFolders},
		{"search stays inside the user's own files", testSearchIsScoped},
		{"wildcards in a search are taken literally", testSearchEscapesWildcards},
		{"a search needs words", testSearchValidates},
		{"search over http", testHandlerSearch},
		{"an api key can search", testKeySearch},
		{"an upload without a connection goes to the pool", testPoolUpload},
		{"an upload can pick its account", testChosenUpload},
		{"nowhere to upload has stable errors", testPoolErrors},
		{"the pool sees the file's declared size", testPoolSeesSize},
		{"thumbnails", testThumbnails},
		{"a thumbnail of an unsafe type is refused", testThumbnailTypes},
		{"users can.t reach each other.s thumbnails", testThumbnailIsolation},
		{"upload memory stays flat through the whole server", testUploadEndpointMemory},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `TRUNCATE users, settings, audit_events CASCADE`); err != nil {
				t.Fatalf("reset database: %v", err)
			}
			f := newFixture(pool)
			tt.run(t, &server{fixture: f, handler: httpx.NewRouter(httpx.Deps{
				Logger:       logger,
				DB:           pool,
				PublicOrigin: testOrigin,
				Sessions:     sessions{},
				APIKeys:      keys{},
				Files:        NewHandler(f.service, logger),
			})})
		})
	}
}

func (f *fixture) search(t *testing.T, a account, query string) Listing {
	t.Helper()
	results, err := f.service.Search(f.ctx, a.id, query)
	if err != nil {
		t.Fatalf("Search(%q): %v", query, err)
	}
	return results
}

func testSearchMatches(t *testing.T, s *server) {
	a := s.account(t)
	s.put(t, a, nil, "Holiday Photos 2026.zip")
	s.put(t, a, nil, "tax-return.pdf")
	folder := s.folder(t, a, nil, "Deep")
	s.put(t, a, &folder.ID, "holiday-notes.txt")
	if got := fileNames(s.search(t, a, "HOLIDAY").Files); got != "Holiday Photos 2026.zip,holiday-notes.txt" {
		t.Errorf("results = %q", got)
	}
	if got := fileNames(s.search(t, a, "  tax ").Files); got != "tax-return.pdf" {
		t.Errorf("results = %q", got)
	}
	if got := fileNames(s.search(t, a, "nothing-like-this").Files); got != "" {
		t.Errorf("results = %q", got)
	}
}

func testSearchFolders(t *testing.T, s *server) {
	a := s.account(t)
	s.folder(t, a, nil, "Invoices")
	parent := s.folder(t, a, nil, "Work")
	s.folder(t, a, &parent.ID, "Old invoices")
	if got := folderNames(s.search(t, a, "invoice").Folders); got != "Invoices,Old invoices" {
		t.Errorf("results = %q", got)
	}
}

func testSearchSkipsTrash(t *testing.T, s *server) {
	a := s.account(t)
	file := s.put(t, a, nil, "match.txt")
	folder := s.folder(t, a, nil, "match folder")
	if err := s.service.TrashFile(s.ctx, a.id, file.ID, ""); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if err := s.service.TrashFolder(s.ctx, a.id, folder.ID, ""); err != nil {
		t.Fatalf("TrashFolder: %v", err)
	}
	results := s.search(t, a, "match")
	if len(results.Files) != 0 || len(results.Folders) != 0 {
		t.Errorf("results = %+v", results)
	}
}

func testSearchSkipsTrashedFolders(t *testing.T, s *server) {
	a := s.account(t)
	top := s.folder(t, a, nil, "Top")
	middle := s.folder(t, a, &top.ID, "Middle match")
	low := s.folder(t, a, &middle.ID, "Low")
	s.put(t, a, &top.ID, "match-one.txt")
	s.put(t, a, &low.ID, "match-two.txt")
	s.put(t, a, nil, "match-kept.txt")
	if err := s.service.TrashFolder(s.ctx, a.id, top.ID, ""); err != nil {
		t.Fatalf("TrashFolder: %v", err)
	}
	results := s.search(t, a, "match")
	if got := fileNames(results.Files); got != "match-kept.txt" {
		t.Errorf("files = %q", got)
	}
	if got := folderNames(results.Folders); got != "" {
		t.Errorf("folders = %q", got)
	}
}

func testSearchIsScoped(t *testing.T, s *server) {
	mine, theirs := s.account(t), s.account(t)
	s.put(t, theirs, nil, "their-secret.txt")
	s.folder(t, theirs, nil, "their-secret folder")
	results := s.search(t, mine, "secret")
	if len(results.Files) != 0 || len(results.Folders) != 0 {
		t.Errorf("results = %+v", results)
	}
}

func testSearchEscapesWildcards(t *testing.T, s *server) {
	a := s.account(t)
	s.put(t, a, nil, "100% done.txt")
	s.put(t, a, nil, "plain.txt")
	s.put(t, a, nil, "under_score.txt")
	if got := fileNames(s.search(t, a, "%").Files); got != "100% done.txt" {
		t.Errorf("%% matched %q", got)
	}
	if got := fileNames(s.search(t, a, "_").Files); got != "under_score.txt" {
		t.Errorf("_ matched %q", got)
	}
}

func testSearchValidates(t *testing.T, s *server) {
	a := s.account(t)
	for _, query := range []string{"", "   ", strings.Repeat("x", 256)} {
		if _, err := s.service.Search(s.ctx, a.id, query); !errors.Is(err, ErrInvalidSearch) {
			t.Errorf("Search(%q): error = %v, want ErrInvalidSearch", query, err)
		}
	}
}

func testHandlerSearch(t *testing.T, s *server) {
	c := s.client(t)
	file := c.upload("report.pdf", "application/pdf", "x")
	results := decode[apigen.SearchResults](t, c.json(http.MethodGet, "/api/v1/search?q=report", nil))
	if len(results.Files) != 1 || results.Files[0].Id != file.Id || results.Files[0].Preview != apigen.Pdf {
		t.Errorf("results = %+v", results)
	}
	wantError(t, c.json(http.MethodGet, "/api/v1/search", nil), http.StatusBadRequest, "invalid_search")
	other := s.client(t)
	if got := decode[apigen.SearchResults](t, other.json(http.MethodGet, "/api/v1/search?q=report", nil)); len(got.Files) != 0 {
		t.Errorf("another user found %+v", got.Files)
	}
}

func testKeySearch(t *testing.T, s *server) {
	c := s.client(t)
	c.upload("report.pdf", "application/pdf", "x")
	wantStatus(t, c.keyRequest(http.MethodGet, "/api/v1/search?q=report", httpx.ScopeFilesRead, nil), http.StatusOK)
	wantStatus(t, c.keyRequest(http.MethodGet, "/api/v1/search?q=report", httpx.ScopeFilesWrite, nil), http.StatusForbidden)
}

func testPoolUpload(t *testing.T, s *server) {
	c := s.client(t)
	rec := c.request(http.MethodPost, "/api/v1/files?name=pooled.txt", strings.NewReader("hello"), nil)
	wantStatus(t, rec, http.StatusCreated)
	file := decode[apigen.File](t, rec)
	if file.ConnectionId != c.a.connection {
		t.Errorf("connection = %s", file.ConnectionId)
	}
	if got := s.objects(t, c.a.connection); got != 1 {
		t.Errorf("objects = %d, want 1", got)
	}
}

func testChosenUpload(t *testing.T, s *server) {
	c := s.client(t)
	second := s.connection(t, c.a.id)
	rec := c.request(http.MethodPost, "/api/v1/files?name=x.txt&connection_id="+second.String(), strings.NewReader("hello"), nil)
	wantStatus(t, rec, http.StatusCreated)
	if got := decode[apigen.File](t, rec).ConnectionId; got != second {
		t.Errorf("connection = %s, want the one asked for", got)
	}
	if got := s.objects(t, second); got != 1 {
		t.Errorf("objects = %d, want 1", got)
	}
}

func testPoolErrors(t *testing.T, s *server) {
	c := s.client(t)
	upload := func() *httptest.ResponseRecorder {
		return c.request(http.MethodPost, "/api/v1/files?name=x.txt", strings.NewReader("x"), nil)
	}
	s.providers.chooseErr = storage.ErrNoStorage
	wantError(t, upload(), http.StatusConflict, "no_storage")
	s.providers.chooseErr = storage.ErrStorageFull
	wantError(t, upload(), http.StatusInsufficientStorage, "storage_full")
	s.providers.chooseErr = errors.Join(storage.ErrStorageUnavailable, errors.New("token for person@example.org expired"))
	rec := upload()
	wantError(t, rec, http.StatusBadGateway, "provider_unavailable")
	if strings.Contains(rec.Body.String(), "person@example.org") {
		t.Errorf("response leaks the failure: %s", rec.Body)
	}
}

func testPoolSeesSize(t *testing.T, s *server) {
	c := s.client(t)
	wantStatus(t, c.request(http.MethodPost, "/api/v1/files?name=x.txt", strings.NewReader("hello"), nil), http.StatusCreated)
	if s.providers.lastSize != 5 {
		t.Errorf("the pool was told size %d, want 5", s.providers.lastSize)
	}
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// Rule 7, end to end: through the router, the handler and the service, a file is
// never held in memory, whatever its size.
func testUploadEndpointMemory(t *testing.T, s *server) {
	c := s.client(t)
	s.providers.forConnection(c.a.connection).discard = true

	const size = 512 << 20
	const limit = 32 << 20
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
	rec := c.request(http.MethodPost, c.uploadURL("big.bin"), io.LimitReader(zeros{}, size), nil)
	close(done)
	<-sampled
	wantStatus(t, rec, http.StatusCreated)
	if got := decode[apigen.File](t, rec).Size; got != size {
		t.Errorf("Size = %d, want %d", got, size)
	}
	if grew := peak.Load() - min(peak.Load(), before.HeapAlloc); grew > limit {
		t.Errorf("heap grew by %d MiB for a %d MiB file", grew>>20, size>>20)
	}
}

func testThumbnails(t *testing.T, s *server) {
	c := s.client(t)
	file := c.upload("photo.png", "image/png", "data")
	target := "/api/v1/files/" + file.Id.String() + "/thumbnail"
	wantError(t, c.json(http.MethodGet, target, nil), http.StatusNotFound, "no_thumbnail")

	s.providers.forConnection(c.a.connection).SetThumbnail("image/jpeg", []byte("small image"))
	rec := c.json(http.MethodGet, target, nil)
	wantStatus(t, rec, http.StatusOK)
	if rec.Body.String() != "small image" {
		t.Errorf("body = %q", rec.Body)
	}
	h := rec.Header()
	want := map[string]string{
		"Content-Type":            "image/jpeg",
		"Content-Security-Policy": "sandbox",
		"X-Content-Type-Options":  "nosniff",
		"Cache-Control":           "private, max-age=3600",
	}
	for name, value := range want {
		if got := h.Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
}

func testThumbnailTypes(t *testing.T, s *server) {
	c := s.client(t)
	file := c.upload("x.png", "image/png", "data")
	target := "/api/v1/files/" + file.Id.String() + "/thumbnail"
	for _, contentType := range []string{"image/svg+xml", "text/html", ""} {
		s.providers.forConnection(c.a.connection).SetThumbnail(contentType, []byte("<script>x</script>"))
		wantError(t, c.json(http.MethodGet, target, nil), http.StatusNotFound, "no_thumbnail")
	}
}

func testThumbnailIsolation(t *testing.T, s *server) {
	owner, other := s.client(t), s.client(t)
	file := owner.upload("photo.png", "image/png", "data")
	s.providers.forConnection(owner.a.connection).SetThumbnail("image/jpeg", []byte("small image"))
	wantStatus(t, other.json(http.MethodGet, "/api/v1/files/"+file.Id.String()+"/thumbnail", nil), http.StatusNotFound)
}
