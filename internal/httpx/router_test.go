package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"
)

type fakeDB struct{ err error }

func (f fakeDB) Ping(context.Context) error { return f.err }

var testUI = fstest.MapFS{
	"index.html":       {Data: []byte("<!doctype html><title>app</title>")},
	"assets/app-1.js":  {Data: []byte("console.log(1)")},
	"favicon.svg":      {Data: []byte("<svg/>")},
	".gitkeep":         {Data: []byte("")},
	"assets/.internal": {Data: []byte("hidden")},
}

func newTestRouter(t *testing.T, db Pinger) (http.Handler, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	return NewRouter(Deps{Logger: logger, DB: db, UI: testUI}), &logs
}

func do(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

type errorBody struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorBody {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body, err)
	}
	return body
}

func TestHealth(t *testing.T) {
	tests := []struct {
		name       string
		db         fakeDB
		wantStatus int
		wantBody   string
	}{
		{"database reachable", fakeDB{}, http.StatusOK, "ok"},
		{"database down", fakeDB{err: errors.New("dial tcp 10.0.0.5:5432: refused")}, http.StatusServiceUnavailable, "unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := newTestRouter(t, tt.db)
			rec := do(h, http.MethodGet, "/health")
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			var body struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Status != tt.wantBody {
				t.Errorf("status field = %q, want %q", body.Status, tt.wantBody)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
		})
	}
}

func TestHealthHidesFailureDetailsFromClientsButLogsThem(t *testing.T) {
	h, logs := newTestRouter(t, fakeDB{err: errors.New("dial tcp 10.0.0.5:5432: refused")})
	rec := do(h, http.MethodGet, "/health")
	if strings.Contains(rec.Body.String(), "10.0.0.5") {
		t.Errorf("response leaks the failure: %s", rec.Body)
	}
	if !strings.Contains(logs.String(), "10.0.0.5") {
		t.Errorf("logs are missing the failure: %s", logs)
	}
}

func TestUnknownAPIPathsReturnJSON404(t *testing.T) {
	h, _ := newTestRouter(t, fakeDB{})
	for _, target := range []string{"/api", "/api/", "/api/v1/nope", "/api/v1/../v1/nope"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			rec := do(h, method, target)
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s %s: status = %d, want 404", method, target, rec.Code)
				continue
			}
			if body := decodeError(t, rec); body.Error.Code != CodeNotFound {
				t.Errorf("%s %s: code = %q", method, target, body.Error.Code)
			}
		}
	}
}

func TestWrongMethodOnKnownRoute(t *testing.T) {
	h, _ := newTestRouter(t, fakeDB{})
	rec := do(h, http.MethodPost, "/health")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if body := decodeError(t, rec); body.Error.Code != CodeMethodNotAllowed {
		t.Errorf("code = %q", body.Error.Code)
	}
}

func TestErrorBodyCarriesTheRequestID(t *testing.T) {
	h, _ := newTestRouter(t, fakeDB{})
	rec := do(h, http.MethodGet, "/api/v1/nope")
	body := decodeError(t, rec)
	if body.Error.RequestID == "" || body.Error.RequestID != rec.Header().Get("X-Request-ID") {
		t.Errorf("body id %q does not match header %q", body.Error.RequestID, rec.Header().Get("X-Request-ID"))
	}
}

func TestClientSuppliedRequestIDIsIgnored(t *testing.T) {
	h, _ := newTestRouter(t, fakeDB{})
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("X-Request-ID", "forged")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Request-ID"); got == "" || got == "forged" {
		t.Errorf("X-Request-ID = %q, want a server-generated ID", got)
	}
}

func TestSPA(t *testing.T) {
	h, _ := newTestRouter(t, fakeDB{})

	t.Run("page paths get index.html, uncached", func(t *testing.T) {
		for _, target := range []string{"/", "/index.html", "/files/some/folder", "/settings"} {
			rec := do(h, http.MethodGet, target)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<title>app</title>") {
				t.Errorf("GET %s: status %d, body %q", target, rec.Code, rec.Body)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
				t.Errorf("GET %s: Cache-Control = %q", target, got)
			}
		}
	})

	t.Run("hashed assets are cached for good", func(t *testing.T) {
		rec := do(h, http.MethodGet, "/assets/app-1.js")
		if rec.Code != http.StatusOK || rec.Body.String() != "console.log(1)" {
			t.Fatalf("status %d, body %q", rec.Code, rec.Body)
		}
		if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
			t.Errorf("Cache-Control = %q", got)
		}
	})

	t.Run("other files are served without long caching", func(t *testing.T) {
		rec := do(h, http.MethodGet, "/favicon.svg")
		if rec.Code != http.StatusOK || rec.Body.String() != "<svg/>" {
			t.Fatalf("status %d, body %q", rec.Code, rec.Body)
		}
		if got := rec.Header().Get("Cache-Control"); got != "" {
			t.Errorf("Cache-Control = %q, want none", got)
		}
	})

	t.Run("a missing asset is a real 404", func(t *testing.T) {
		rec := do(h, http.MethodGet, "/assets/old-hash.js")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		decodeError(t, rec)
	})

	t.Run("hidden files are not served", func(t *testing.T) {
		if rec := do(h, http.MethodGet, "/.gitkeep"); strings.Contains(rec.Header().Get("Content-Type"), "text/plain") {
			t.Errorf("served the hidden file: %q", rec.Body)
		}
		if rec := do(h, http.MethodGet, "/assets/.internal"); rec.Code != http.StatusNotFound {
			t.Errorf("assets/.internal: status = %d, want 404", rec.Code)
		}
	})

	t.Run("writes to page paths are rejected", func(t *testing.T) {
		rec := do(h, http.MethodPost, "/settings")
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", rec.Code)
		}
	})

	t.Run("path traversal stays inside the UI", func(t *testing.T) {
		rec := do(h, http.MethodGet, "/assets/../../../etc/passwd")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<title>app</title>") {
			t.Errorf("status %d, body %q", rec.Code, rec.Body)
		}
	})
}

func TestMissingUIBuildReportsUnavailable(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := NewRouter(Deps{Logger: logger, DB: fakeDB{}, UI: fstest.MapFS{}})
	rec := do(h, http.MethodGet, "/")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if body := decodeError(t, rec); body.Error.Code != CodeUIUnavailable {
		t.Errorf("code = %q", body.Error.Code)
	}
}

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	h, _ := newTestRouter(t, fakeDB{})
	for _, target := range []string{"/", "/health", "/api/v1/nope", "/assets/app-1.js"} {
		rec := do(h, http.MethodGet, target)
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q", target, got)
		}
		if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "frame-ancestors 'none'") {
			t.Errorf("%s: Content-Security-Policy = %q", target, got)
		}
	}
}

func TestAccessLogOmitsQueryStrings(t *testing.T) {
	h, logs := newTestRouter(t, fakeDB{})
	do(h, http.MethodGet, "/settings?token=super-secret-value")
	if strings.Contains(logs.String(), "super-secret-value") {
		t.Errorf("query string was logged: %s", logs)
	}
	for _, want := range []string{`"path":"/settings"`, `"status":200`, `"request_id"`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log %s is missing %s", logs, want)
		}
	}
}

func TestPanicBecomesGeneric500(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	r := chi.NewRouter()
	r.Use(assignRequestID, accessLog(logger), recoverPanics(logger))
	r.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("db password is hunter2") })

	rec := do(r, http.MethodGet, "/boom")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Errorf("response leaks the panic: %s", rec.Body)
	}
	if body := decodeError(t, rec); body.Error.Code != CodeInternal {
		t.Errorf("code = %q", body.Error.Code)
	}
	if !strings.Contains(logs.String(), "handler panicked") {
		t.Errorf("panic was not logged: %s", logs.String())
	}
	if !strings.Contains(logs.String(), `"status":500`) {
		t.Errorf("access log did not record the 500: %s", logs.String())
	}
}
