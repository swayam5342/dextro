package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

type fakeDB struct{ err error }

func (f fakeDB) Ping(context.Context) error { return f.err }

func newTestRouter(t *testing.T, db Pinger) (http.Handler, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	return NewRouter(Deps{Logger: logger, DB: db}), &logs
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
		t.Errorf("logs are missing the failure: %s", logs.String())
	}
}

func TestUnknownPathsReturnJSON404(t *testing.T) {
	h, _ := newTestRouter(t, fakeDB{})
	for _, target := range []string{"/", "/api", "/api/v1/nope", "/settings"} {
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

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	h, _ := newTestRouter(t, fakeDB{})
	for _, target := range []string{"/health", "/api/v1/nope"} {
		rec := do(h, http.MethodGet, target)
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q", target, got)
		}
		if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'none'") {
			t.Errorf("%s: Content-Security-Policy = %q", target, got)
		}
	}
}

func TestAccessLogOmitsQueryStrings(t *testing.T) {
	h, logs := newTestRouter(t, fakeDB{})
	do(h, http.MethodGet, "/health?token=super-secret-value")
	if strings.Contains(logs.String(), "super-secret-value") {
		t.Errorf("query string was logged: %s", logs.String())
	}
	for _, want := range []string{`"path":"/health"`, `"status":200`, `"request_id"`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log %s is missing %s", logs.String(), want)
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
