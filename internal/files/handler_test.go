package files

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/swayam5342/tributary/internal/db"
	"github.com/swayam5342/tributary/internal/db/dbtest"
	"github.com/swayam5342/tributary/internal/httpx"
	"github.com/swayam5342/tributary/internal/httpx/apigen"
	"github.com/swayam5342/tributary/internal/provider"
)

const testOrigin = "https://files.example.org"

var errMissing = provider.ErrObjectNotFound

// sessions signs requests in by treating the cookie as the user's ID.
type sessions struct{}

func (sessions) ResolveSession(_ context.Context, token string) (httpx.Principal, error) {
	id, err := uuid.Parse(token)
	if err != nil {
		return httpx.Principal{}, httpx.ErrNoSession
	}
	return httpx.Principal{UserID: id, SessionID: id}, nil
}

// keys signs requests in by treating "user-id:scope" as an API key.
type keys struct{}

func (keys) ResolveAPIKey(_ context.Context, token string) (httpx.Principal, error) {
	raw, scope, _ := strings.Cut(token, ":")
	id, err := uuid.Parse(raw)
	if err != nil {
		return httpx.Principal{}, httpx.ErrNoSession
	}
	return httpx.Principal{UserID: id, Key: &httpx.KeyAccess{ID: id, Scopes: []string{scope}}}, nil
}

type server struct {
	*fixture
	handler http.Handler
}

type client struct {
	s *server
	t *testing.T
	a account
}

func (s *server) client(t *testing.T) *client {
	t.Helper()
	return &client{s: s, t: t, a: s.account(t)}
}

func (c *client) request(method, target string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	c.t.Helper()
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Origin", testOrigin)
	req.AddCookie(&http.Cookie{Name: httpx.SessionCookieName, Value: c.a.id.String()})
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	c.s.handler.ServeHTTP(rec, req)
	return rec
}

func (c *client) json(method, target string, body any) *httptest.ResponseRecorder {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("encode body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	return c.request(method, target, reader, nil)
}

func (c *client) uploadURL(name string) string {
	q := url.Values{"connection_id": {c.a.connection.String()}, "name": {name}}
	return "/api/v1/files?" + q.Encode()
}

func (c *client) upload(name, contentType, body string) apigen.File {
	c.t.Helper()
	rec := c.request(http.MethodPost, c.uploadURL(name), strings.NewReader(body), map[string]string{"Content-Type": contentType})
	if rec.Code != http.StatusCreated {
		c.t.Fatalf("upload %q: status = %d, body %s", name, rec.Code, rec.Body)
	}
	return decode[apigen.File](c.t, rec)
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(rec.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode %q: %v", rec.Body, err)
	}
	return value
}

func wantError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, status, rec.Body)
	}
	body := decode[struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}](t, rec)
	if body.Error.Code != code {
		t.Errorf("code = %q, want %q", body.Error.Code, code)
	}
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, status, rec.Body)
	}
}

func contentURL(file apigen.File, query string) string {
	return "/api/v1/files/" + file.Id.String() + "/content" + query
}

// TestHandlers shares one Postgres between its subtests, emptying it each time.
func TestHandlers(t *testing.T) {
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
		{"uploading and listing", testHandlerUpload},
		{"responses never carry the provider's object id", testObjectIDsStayHidden},
		{"an upload needs a name", testUploadNeedsParams},
		{"bad uploads have stable errors", testUploadErrors},
		{"too-large uploads are refused", testUploadTooLarge},
		{"downloads are safe by default", testDownloadHeaders},
		{"only images, video and pdf display inline", testInlineAllowlist},
		{"inline types are served with their real type", testInlineHeaders},
		{"filenames can't break the header", testContentDisposition},
		{"byte ranges", testRanges},
		{"a range that can't be met is refused", testUnsatisfiableRange},
		{"odd range headers are ignored", testIgnoredRanges},
		{"an empty file downloads", testEmptyFile},
		{"a missing object and a failed provider are reported", testDownloadFailures},
		{"a failed stream is cut off, not passed off as complete", testBrokenStream},
		{"folders over http", testHandlerFolders},
		{"the trash over http", testHandlerTrash},
		{"permanent delete needs the trash", testHandlerDeleteNeedsTrash},
		{"moving a folder into itself is refused", testHandlerInvalidMove},
		{"users can't reach each other's files over http", testHandlerIsolation},
		{"an api key can upload and read", testKeyUse},
		{"an api key can't destroy anything", testKeyCannotDestroy},
		{"bad ids are refused", testBadIDs},
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

func testHandlerUpload(t *testing.T, s *server) {
	c := s.client(t)
	file := c.upload("notes.txt", "text/plain; charset=utf-8", "hello")
	if file.Name != "notes.txt" || file.Size != 5 || file.ContentType != "text/plain" || file.Preview != apigen.None {
		t.Errorf("file = %+v", file)
	}
	contents := decode[apigen.FolderContents](t, c.json(http.MethodGet, "/api/v1/files", nil))
	if len(contents.Files) != 1 || contents.Files[0].Id != file.Id {
		t.Errorf("contents = %+v", contents)
	}
	if got := decode[apigen.File](t, c.json(http.MethodGet, "/api/v1/files/"+file.Id.String(), nil)); got.Id != file.Id {
		t.Errorf("file = %+v", got)
	}
}

func testObjectIDsStayHidden(t *testing.T, s *server) {
	c := s.client(t)
	file := c.upload("x.txt", "text/plain", "x")
	var objectID string
	if err := s.pool.QueryRow(s.ctx, `SELECT object_id FROM files`).Scan(&objectID); err != nil {
		t.Fatalf("read object id: %v", err)
	}
	// Long enough to be sure a match isn't a coincidence.
	if _, err := s.pool.Exec(s.ctx, `UPDATE files SET object_id = 'object-marker-1234'`); err != nil {
		t.Fatalf("set object id: %v", err)
	}
	for _, target := range []string{"/api/v1/files", "/api/v1/files/" + file.Id.String()} {
		if body := c.json(http.MethodGet, target, nil).Body.String(); strings.Contains(body, "object-marker") {
			t.Errorf("%s leaks the object id: %s", target, body)
		}
	}
}

func testUploadNeedsParams(t *testing.T, s *server) {
	c := s.client(t)
	for _, target := range []string{
		"/api/v1/files",
		"/api/v1/files?connection_id=" + c.a.connection.String(),
	} {
		wantError(t, c.request(http.MethodPost, target, strings.NewReader("x"), nil), http.StatusBadRequest, "invalid_upload")
	}
}

func testUploadErrors(t *testing.T, s *server) {
	c := s.client(t)
	other := s.client(t)
	wantError(t, c.request(http.MethodPost, c.uploadURL("a/b.txt"), strings.NewReader("x"), nil), http.StatusBadRequest, "invalid_name")

	stolen := "/api/v1/files?name=x.txt&connection_id=" + other.a.connection.String()
	wantError(t, c.request(http.MethodPost, stolen, strings.NewReader("x"), nil), http.StatusNotFound, "connection_not_found")

	missing := c.uploadURL("x.txt") + "&folder_id=" + uuid.NewString()
	wantError(t, c.request(http.MethodPost, missing, strings.NewReader("x"), nil), http.StatusNotFound, "not_found")

	s.providers.forConnection(c.a.connection).putErr = errors.New("dial tcp 10.1.2.3:9000: connection refused")
	rec := c.request(http.MethodPost, c.uploadURL("x.txt"), strings.NewReader("x"), nil)
	wantError(t, rec, http.StatusBadGateway, "provider_unavailable")
	if strings.Contains(rec.Body.String(), "10.1.2.3") {
		t.Errorf("response leaks the failure: %s", rec.Body)
	}
}

func testUploadTooLarge(t *testing.T, s *server) {
	c := s.client(t)
	setLimit(t, s.fixture, 4)
	wantError(t, c.request(http.MethodPost, c.uploadURL("big.bin"), strings.NewReader("hello"), nil),
		http.StatusRequestEntityTooLarge, "file_too_large")

	// No length declared, as with a chunked upload.
	req := httptest.NewRequest(http.MethodPost, c.uploadURL("big.bin"), io.MultiReader(strings.NewReader("hello")))
	req.ContentLength = -1
	req.Header.Set("Origin", testOrigin)
	req.AddCookie(&http.Cookie{Name: httpx.SessionCookieName, Value: c.a.id.String()})
	rec := httptest.NewRecorder()
	c.s.handler.ServeHTTP(rec, req)
	wantError(t, rec, http.StatusRequestEntityTooLarge, "file_too_large")
}

func testDownloadHeaders(t *testing.T, s *server) {
	c := s.client(t)
	file := c.upload("report.html", "text/html", "<script>alert(1)</script>")
	rec := c.json(http.MethodGet, contentURL(file, ""), nil)
	wantStatus(t, rec, http.StatusOK)
	h := rec.Header()
	want := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "sandbox",
		"Content-Type":            "application/octet-stream",
		"Accept-Ranges":           "bytes",
		"Content-Length":          "25",
	}
	for name, value := range want {
		if got := h.Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
	if !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") {
		t.Errorf("Content-Disposition = %q", h.Get("Content-Disposition"))
	}
	if rec.Body.String() != "<script>alert(1)</script>" {
		t.Errorf("body = %q", rec.Body)
	}
}

func testInlineAllowlist(t *testing.T, s *server) {
	c := s.client(t)
	tests := []struct {
		contentType string
		preview     apigen.FilePreview
	}{
		{"image/png", apigen.Image},
		{"image/jpeg", apigen.Image},
		{"image/gif", apigen.Image},
		{"image/webp", apigen.Image},
		{"video/mp4", apigen.Video},
		{"video/webm", apigen.Video},
		{"application/pdf", apigen.Pdf},
		// These can all run scripts, or be made to.
		{"image/svg+xml", apigen.None},
		{"text/html", apigen.None},
		{"application/xhtml+xml", apigen.None},
		{"text/xml", apigen.None},
		{"application/javascript", apigen.None},
		{"text/plain", apigen.None},
		{"application/octet-stream", apigen.None},
	}
	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			file := c.upload("file", tt.contentType, "data")
			if file.Preview != tt.preview {
				t.Errorf("preview = %q, want %q", file.Preview, tt.preview)
			}
			rec := c.json(http.MethodGet, contentURL(file, "?disposition=inline"), nil)
			wantStatus(t, rec, http.StatusOK)
			disposition := rec.Header().Get("Content-Disposition")
			if tt.preview == apigen.None {
				if !strings.HasPrefix(disposition, "attachment") || rec.Header().Get("Content-Type") != "application/octet-stream" {
					t.Errorf("headers = %v", rec.Header())
				}
				return
			}
			if !strings.HasPrefix(disposition, "inline") {
				t.Errorf("Content-Disposition = %q", disposition)
			}
		})
	}
}

func testInlineHeaders(t *testing.T, s *server) {
	c := s.client(t)
	file := c.upload("photo.png", "image/png", "data")
	rec := c.json(http.MethodGet, contentURL(file, "?disposition=inline"), nil)
	h := rec.Header()
	if h.Get("Content-Type") != "image/png" || h.Get("Content-Security-Policy") != "sandbox" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("headers = %v", h)
	}
	attachment := c.json(http.MethodGet, contentURL(file, "?disposition=attachment"), nil)
	if !strings.HasPrefix(attachment.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("Content-Disposition = %q", attachment.Header().Get("Content-Disposition"))
	}
}

func testContentDisposition(t *testing.T, s *server) {
	c := s.client(t)
	for _, name := range []string{
		`plain.txt`,
		`with space.txt`,
		`quo"te.txt`,
		`semi;colon=1.txt`,
		`ünï-cødé ✓.txt`,
		`trailing%0d%0aSet-Cookie:x=1.txt`,
	} {
		t.Run(name, func(t *testing.T) {
			file := c.upload(name, "text/plain", "x")
			header := c.json(http.MethodGet, contentURL(file, ""), nil).Header().Get("Content-Disposition")
			if strings.ContainsAny(header, "\r\n") {
				t.Fatalf("header has a line break: %q", header)
			}
			kind, params, err := mime.ParseMediaType(header)
			if err != nil {
				t.Fatalf("parse %q: %v", header, err)
			}
			if kind != "attachment" || params["filename"] != name {
				t.Errorf("parsed %q %v from %q, want filename %q", kind, params, header, name)
			}
		})
	}
}

func testRanges(t *testing.T, s *server) {
	c := s.client(t)
	file := c.upload("x.bin", "application/octet-stream", "0123456789")
	tests := []struct {
		header, body, contentRange string
	}{
		{"bytes=0-3", "0123", "bytes 0-3/10"},
		{"bytes=3-5", "345", "bytes 3-5/10"},
		{"bytes=7-", "789", "bytes 7-9/10"},
		{"bytes=-2", "89", "bytes 8-9/10"},
		{"bytes=8-100", "89", "bytes 8-9/10"},
		{"bytes=-100", "0123456789", "bytes 0-9/10"},
	}
	for _, tt := range tests {
		t.Run(tt.header, func(t *testing.T) {
			rec := c.request(http.MethodGet, contentURL(file, ""), nil, map[string]string{"Range": tt.header})
			wantStatus(t, rec, http.StatusPartialContent)
			if rec.Body.String() != tt.body {
				t.Errorf("body = %q, want %q", rec.Body, tt.body)
			}
			if got := rec.Header().Get("Content-Range"); got != tt.contentRange {
				t.Errorf("Content-Range = %q, want %q", got, tt.contentRange)
			}
			if rec.Header().Get("Content-Security-Policy") != "sandbox" {
				t.Error("a partial response lost the sandbox")
			}
		})
	}
}

func testUnsatisfiableRange(t *testing.T, s *server) {
	c := s.client(t)
	file := c.upload("x.bin", "application/octet-stream", "0123456789")
	for _, header := range []string{"bytes=10-", "bytes=100-200", "bytes=-0"} {
		rec := c.request(http.MethodGet, contentURL(file, ""), nil, map[string]string{"Range": header})
		wantStatus(t, rec, http.StatusRequestedRangeNotSatisfiable)
		if got := rec.Header().Get("Content-Range"); got != "bytes */10" {
			t.Errorf("%s: Content-Range = %q", header, got)
		}
	}
}

func testIgnoredRanges(t *testing.T, s *server) {
	c := s.client(t)
	file := c.upload("x.bin", "application/octet-stream", "0123456789")
	for _, header := range []string{"bytes=0-1,3-4", "items=0-1", "bytes=abc", "bytes=5-2", "bytes="} {
		rec := c.request(http.MethodGet, contentURL(file, ""), nil, map[string]string{"Range": header})
		wantStatus(t, rec, http.StatusOK)
		if rec.Body.String() != "0123456789" {
			t.Errorf("%s: body = %q", header, rec.Body)
		}
	}
}

func testEmptyFile(t *testing.T, s *server) {
	c := s.client(t)
	file := c.upload("empty.txt", "text/plain", "")
	wantStatus(t, c.json(http.MethodGet, contentURL(file, ""), nil), http.StatusOK)
	rec := c.request(http.MethodGet, contentURL(file, ""), nil, map[string]string{"Range": "bytes=0-"})
	wantStatus(t, rec, http.StatusOK)
}

func testDownloadFailures(t *testing.T, s *server) {
	c := s.client(t)
	file := c.upload("x.txt", "text/plain", "x")
	stored := s.providers.forConnection(c.a.connection)

	stored.getErr = errors.New("dial tcp 10.1.2.3:9000: connection refused")
	rec := c.json(http.MethodGet, contentURL(file, ""), nil)
	wantError(t, rec, http.StatusBadGateway, "provider_unavailable")
	if strings.Contains(rec.Body.String(), "10.1.2.3") {
		t.Errorf("response leaks the failure: %s", rec.Body)
	}
	if rec.Header().Get("Content-Disposition") != "" {
		t.Errorf("an error carries a file header: %v", rec.Header())
	}

	stored.getErr = errMissing
	wantError(t, c.json(http.MethodGet, contentURL(file, ""), nil), http.StatusNotFound, "file_unavailable")
}

// brokenBody fails after sending part of a file.
type brokenBody struct{ sent bool }

func (b *brokenBody) Read(p []byte) (int, error) {
	if b.sent {
		return 0, errors.New("connection reset")
	}
	b.sent = true
	return copy(p, "partial"), nil
}

func (b *brokenBody) Close() error { return nil }

func testBrokenStream(t *testing.T, s *server) {
	handler := NewHandler(s.service, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer func() {
		// http.ErrAbortHandler is how a handler makes the server drop the
		// connection, so the client can tell the file is incomplete.
		if got := recover(); got != http.ErrAbortHandler {
			t.Errorf("recovered %v, want http.ErrAbortHandler", got)
		}
	}()
	handler.stream(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), &brokenBody{})
	t.Error("stream returned normally after the body failed")
}

func testHandlerFolders(t *testing.T, s *server) {
	c := s.client(t)
	rec := c.json(http.MethodPost, "/api/v1/folders", map[string]any{"name": "Top", "parent_id": nil})
	wantStatus(t, rec, http.StatusCreated)
	top := decode[apigen.Folder](t, rec)

	rec = c.json(http.MethodPost, "/api/v1/folders", map[string]any{"name": "Child", "parent_id": top.Id})
	wantStatus(t, rec, http.StatusCreated)
	child := decode[apigen.Folder](t, rec)

	rec = c.json(http.MethodPatch, "/api/v1/folders/"+child.Id.String(), map[string]string{"name": "Renamed"})
	wantStatus(t, rec, http.StatusOK)
	if got := decode[apigen.Folder](t, rec); got.Name != "Renamed" {
		t.Errorf("folder = %+v", got)
	}

	contents := decode[apigen.FolderContents](t, c.json(http.MethodGet, "/api/v1/files?folder_id="+child.Id.String(), nil))
	if len(contents.Path) != 2 || contents.Path[0].Name != "Top" || contents.Path[1].Name != "Renamed" {
		t.Errorf("path = %+v", contents.Path)
	}

	file := c.upload("x.txt", "text/plain", "x")
	rec = c.json(http.MethodPost, "/api/v1/files/"+file.Id.String()+"/move", map[string]any{"folder_id": child.Id})
	wantStatus(t, rec, http.StatusOK)
	rec = c.json(http.MethodPatch, "/api/v1/files/"+file.Id.String(), map[string]string{"name": "y.txt"})
	wantStatus(t, rec, http.StatusOK)
	contents = decode[apigen.FolderContents](t, c.json(http.MethodGet, "/api/v1/files?folder_id="+child.Id.String(), nil))
	if len(contents.Files) != 1 || contents.Files[0].Name != "y.txt" {
		t.Errorf("files = %+v", contents.Files)
	}

	rec = c.json(http.MethodPost, "/api/v1/folders/"+child.Id.String()+"/move", map[string]any{"parent_id": nil})
	wantStatus(t, rec, http.StatusOK)
	if got := decode[apigen.Folder](t, rec); got.ParentId != nil {
		t.Errorf("folder = %+v", got)
	}
}

func testHandlerTrash(t *testing.T, s *server) {
	c := s.client(t)
	file := c.upload("x.txt", "text/plain", "x")
	folder := decode[apigen.Folder](t, c.json(http.MethodPost, "/api/v1/folders", map[string]any{"name": "F", "parent_id": nil}))

	wantStatus(t, c.json(http.MethodPost, "/api/v1/files/"+file.Id.String()+"/trash", nil), http.StatusNoContent)
	wantStatus(t, c.json(http.MethodPost, "/api/v1/folders/"+folder.Id.String()+"/trash", nil), http.StatusNoContent)

	trash := decode[apigen.Trash](t, c.json(http.MethodGet, "/api/v1/trash", nil))
	if len(trash.Files) != 1 || len(trash.Folders) != 1 || trash.Files[0].TrashedAt == nil {
		t.Fatalf("trash = %+v", trash)
	}

	wantStatus(t, c.json(http.MethodPost, "/api/v1/files/"+file.Id.String()+"/restore", nil), http.StatusNoContent)
	wantStatus(t, c.json(http.MethodPost, "/api/v1/folders/"+folder.Id.String()+"/restore", nil), http.StatusNoContent)
	contents := decode[apigen.FolderContents](t, c.json(http.MethodGet, "/api/v1/files", nil))
	if len(contents.Files) != 1 || len(contents.Folders) != 1 {
		t.Fatalf("contents = %+v", contents)
	}

	wantStatus(t, c.json(http.MethodPost, "/api/v1/files/"+file.Id.String()+"/trash", nil), http.StatusNoContent)
	wantStatus(t, c.json(http.MethodDelete, "/api/v1/files/"+file.Id.String(), nil), http.StatusNoContent)
	wantStatus(t, c.json(http.MethodPost, "/api/v1/folders/"+folder.Id.String()+"/trash", nil), http.StatusNoContent)
	wantStatus(t, c.json(http.MethodDelete, "/api/v1/folders/"+folder.Id.String(), nil), http.StatusNoContent)

	other := c.upload("y.txt", "text/plain", "y")
	wantStatus(t, c.json(http.MethodPost, "/api/v1/files/"+other.Id.String()+"/trash", nil), http.StatusNoContent)
	wantStatus(t, c.json(http.MethodDelete, "/api/v1/trash", nil), http.StatusNoContent)
	if got := s.objects(t, c.a.connection); got != 0 {
		t.Errorf("objects = %d, want 0", got)
	}
}

func testHandlerDeleteNeedsTrash(t *testing.T, s *server) {
	c := s.client(t)
	file := c.upload("x.txt", "text/plain", "x")
	wantError(t, c.json(http.MethodDelete, "/api/v1/files/"+file.Id.String(), nil), http.StatusConflict, "not_in_trash")
	folder := decode[apigen.Folder](t, c.json(http.MethodPost, "/api/v1/folders", map[string]any{"name": "F", "parent_id": nil}))
	wantError(t, c.json(http.MethodDelete, "/api/v1/folders/"+folder.Id.String(), nil), http.StatusConflict, "not_in_trash")
}

func testHandlerInvalidMove(t *testing.T, s *server) {
	c := s.client(t)
	folder := decode[apigen.Folder](t, c.json(http.MethodPost, "/api/v1/folders", map[string]any{"name": "F", "parent_id": nil}))
	wantError(t, c.json(http.MethodPost, "/api/v1/folders/"+folder.Id.String()+"/move", map[string]any{"parent_id": folder.Id}),
		http.StatusBadRequest, "invalid_move")
}

func testHandlerIsolation(t *testing.T, s *server) {
	owner, other := s.client(t), s.client(t)
	file := owner.upload("secret.txt", "text/plain", "the secret")
	folder := decode[apigen.Folder](t, owner.json(http.MethodPost, "/api/v1/folders", map[string]any{"name": "F", "parent_id": nil}))
	f, d := file.Id.String(), folder.Id.String()

	for _, tt := range []struct {
		method, target string
		body           any
	}{
		{http.MethodGet, "/api/v1/files/" + f, nil},
		{http.MethodGet, "/api/v1/files/" + f + "/content", nil},
		{http.MethodPatch, "/api/v1/files/" + f, map[string]string{"name": "x"}},
		{http.MethodPost, "/api/v1/files/" + f + "/move", map[string]any{"folder_id": nil}},
		{http.MethodPost, "/api/v1/files/" + f + "/trash", nil},
		{http.MethodPost, "/api/v1/files/" + f + "/restore", nil},
		{http.MethodDelete, "/api/v1/files/" + f, nil},
		{http.MethodGet, "/api/v1/files?folder_id=" + d, nil},
		{http.MethodPatch, "/api/v1/folders/" + d, map[string]string{"name": "x"}},
		{http.MethodPost, "/api/v1/folders/" + d + "/move", map[string]any{"parent_id": nil}},
		{http.MethodPost, "/api/v1/folders/" + d + "/trash", nil},
		{http.MethodPost, "/api/v1/folders/" + d + "/restore", nil},
		{http.MethodDelete, "/api/v1/folders/" + d, nil},
	} {
		rec := other.json(tt.method, tt.target, tt.body)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: status = %d, want 404", tt.method, tt.target, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "the secret") {
			t.Errorf("%s %s leaks content", tt.method, tt.target)
		}
	}
	got := owner.json(http.MethodGet, contentURL(file, ""), nil)
	if got.Body.String() != "the secret" {
		t.Errorf("the owner's file changed: %q", got.Body)
	}
}

func (c *client) keyRequest(method, target, scope string, body io.Reader) *httptest.ResponseRecorder {
	c.t.Helper()
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Authorization", "Bearer "+c.a.id.String()+":"+scope)
	rec := httptest.NewRecorder()
	c.s.handler.ServeHTTP(rec, req)
	return rec
}

func testKeyUse(t *testing.T, s *server) {
	c := s.client(t)
	rec := c.keyRequest(http.MethodPost, c.uploadURL("x.txt"), httpx.ScopeFilesWrite, strings.NewReader("from a script"))
	wantStatus(t, rec, http.StatusCreated)
	file := decode[apigen.File](t, rec)

	rec = c.keyRequest(http.MethodGet, contentURL(file, ""), httpx.ScopeFilesRead, nil)
	wantStatus(t, rec, http.StatusOK)
	if rec.Body.String() != "from a script" {
		t.Errorf("body = %q", rec.Body)
	}
	wantStatus(t, c.keyRequest(http.MethodPost, c.uploadURL("y.txt"), httpx.ScopeFilesRead, strings.NewReader("x")), http.StatusForbidden)
}

func testKeyCannotDestroy(t *testing.T, s *server) {
	c := s.client(t)
	file := c.upload("x.txt", "text/plain", "x")
	wantStatus(t, c.keyRequest(http.MethodPost, "/api/v1/files/"+file.Id.String()+"/trash", httpx.ScopeFilesWrite, nil), http.StatusNoContent)
	wantStatus(t, c.keyRequest(http.MethodDelete, "/api/v1/files/"+file.Id.String(), httpx.ScopeFilesWrite, nil), http.StatusForbidden)
	wantStatus(t, c.keyRequest(http.MethodDelete, "/api/v1/trash", httpx.ScopeFilesWrite, nil), http.StatusForbidden)
	if got := s.objects(t, c.a.connection); got != 1 {
		t.Errorf("objects = %d, want 1", got)
	}
}

func testBadIDs(t *testing.T, s *server) {
	c := s.client(t)
	wantStatus(t, c.json(http.MethodGet, "/api/v1/files/not-a-uuid", nil), http.StatusBadRequest)
	wantStatus(t, c.json(http.MethodGet, "/api/v1/files?folder_id=not-a-uuid", nil), http.StatusBadRequest)
}
