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

	"github.com/google/uuid"

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
	results, err := f.service.Search(f.ctx, a.id, query, "")
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
		if _, err := s.service.Search(s.ctx, a.id, query, ""); !errors.Is(err, ErrInvalidSearch) {
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

func TestSearchFilters(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	f := newUploadFixture(t, pool)
	user := f.user(t)
	stranger := f.user(t)
	home := f.link(t, user, "home@gmail.com", 1<<30)
	work := f.link(t, user, "work@gmail.com", 1<<30)
	f.link(t, stranger, "theirs@gmail.com", 1<<30)

	put := func(owner uuid.UUID, connection *uuid.UUID, folder *uuid.UUID, name, contentType string, size int, age time.Duration) File {
		t.Helper()
		file, err := f.service.Upload(ctx, owner, UploadInput{
			ConnectionID: connection, FolderID: folder, Name: name, ContentType: contentType,
			Size: int64(size), Body: strings.NewReader(strings.Repeat("x", size)),
		})
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE files SET created_at = $2 WHERE id = $1`, file.ID, time.Now().Add(-age)); err != nil {
			t.Fatalf("age: %v", err)
		}
		return file
	}
	const day = 24 * time.Hour
	trip, err := f.service.CreateFolder(ctx, user, nil, "Summer Trip")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	beach, err := f.service.CreateFolder(ctx, user, &trip.ID, "Beach")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	vault := f.vault(t, user, "Vault")
	photo := put(user, &home, &beach.ID, "sunset.jpg", "image/jpeg", 3000, 2*day)
	put(user, &work, nil, "report final.pdf", "application/pdf", 2000, 40*day)
	put(user, &work, nil, "report draft.pdf", "application/pdf", 100, 400*day)
	clip := put(user, &home, nil, "clip.mp4", "video/mp4", 5000, 10*day)
	put(user, &home, nil, "trip plan.txt", "text/plain", 10, 1*day)
	put(user, &home, &vault.ID, "locked.txt", "text/plain", 20, 1*day)
	put(stranger, nil, nil, "report stranger.pdf", "application/pdf", 2000, day)
	if err := f.service.SetStarred(ctx, user, photo.ID, true); err != nil {
		t.Fatalf("SetStarred: %v", err)
	}
	f.allowLinks(t)
	if _, err := f.service.ShareFile(ctx, user, clip.ID, ShareInput{Role: "viewer"}, ""); err != nil {
		t.Fatalf("ShareFile: %v", err)
	}

	names := func(query, sort string) string {
		t.Helper()
		found, err := f.service.Search(ctx, user, query, sort)
		if err != nil {
			t.Fatalf("Search(%q): %v", query, err)
		}
		return fileNames(found.Files)
	}
	tests := []struct {
		query string
		want  string
	}{
		{"report", "report draft.pdf,report final.pdf"},
		{"final report", "report final.pdf"},
		{`"report final"`, "report final.pdf"},
		{`"final report"`, ""},
		{"report -draft", "report final.pdf"},
		{"type:image", "sunset.jpg"},
		{"type:video", "clip.mp4"},
		{"type:pdf", "report draft.pdf,report final.pdf"},
		{"type:document report", "report draft.pdf,report final.pdf"},
		{"type:image type:video", "clip.mp4,sunset.jpg"},
		{"larger:2500", "clip.mp4,sunset.jpg"},
		{"size:>2500", "clip.mp4,sunset.jpg"},
		{"size:<50", "locked.txt,trip plan.txt"},
		{"smaller:50 type:text", "locked.txt,trip plan.txt"},
		{"newer_than:5d type:image", "sunset.jpg"},
		{"older_than:1y", "report draft.pdf"},
		{"older_than:30d type:pdf", "report draft.pdf,report final.pdf"},
		{"before:2020-01-01", ""},
		{"after:2020-01-01 type:video", "clip.mp4"},
		{"account:work", "report draft.pdf,report final.pdf"},
		{"owner:WORK@gmail", "report draft.pdf,report final.pdf"},
		{"owner:me type:video", "clip.mp4"},
		{"account:nobody", ""},
		{"in:summer", "sunset.jpg"},
		{`in:"Summer Trip"`, "sunset.jpg"},
		{"in:beach", "sunset.jpg"},
		{"is:starred", "sunset.jpg"},
		{"is:encrypted", "locked.txt"},
		{"is:shared", "clip.mp4"},
		{"report type:pdf larger:1000 owner:work -draft", "report final.pdf"},
		{"100%", ""},
	}
	for _, tt := range tests {
		if got := names(tt.query, ""); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.query, got, tt.want)
		}
	}

	// A folder can't have a type, and a word still finds one.
	withWords, err := f.service.Search(ctx, user, "trip", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(withWords.Folders) != 1 || len(withWords.Files) != 1 {
		t.Errorf("trip = %+v", withWords)
	}
	withType, err := f.service.Search(ctx, user, "trip type:text", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(withType.Folders) != 0 || len(withType.Files) != 1 {
		t.Errorf("trip type:text = %+v", withType)
	}

	sorts := map[string]string{
		"":          "report draft.pdf,report final.pdf",
		"name-asc":  "report draft.pdf,report final.pdf",
		"name-desc": "report final.pdf,report draft.pdf",
		"size-desc": "report final.pdf,report draft.pdf",
		"size-asc":  "report draft.pdf,report final.pdf",
		"date-desc": "report final.pdf,report draft.pdf",
		"date-asc":  "report draft.pdf,report final.pdf",
		"; DROP":    "report draft.pdf,report final.pdf",
	}
	for sort, want := range sorts {
		if got := names("type:pdf", sort); got != want {
			t.Errorf("sort %q: got %q, want %q", sort, got, want)
		}
	}
}
