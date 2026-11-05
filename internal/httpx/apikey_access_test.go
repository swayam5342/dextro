package httpx

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/httpx/apigen"
)

// fakeKeys maps bearer tokens to principals.
type fakeKeys struct {
	keys map[string]Principal
	err  error
}

func (f fakeKeys) ResolveAPIKey(_ context.Context, token string) (Principal, error) {
	if f.err != nil {
		return Principal{}, f.err
	}
	p, ok := f.keys[token]
	if !ok {
		return Principal{}, ErrNoSession
	}
	return p, nil
}

func keyPrincipal(scopes ...string) Principal {
	return Principal{UserID: uuid.New(), Key: &KeyAccess{ID: uuid.New(), Scopes: scopes}}
}

func newKeyRouter(t *testing.T, keys APIKeyResolver) http.Handler {
	t.Helper()
	return NewRouter(Deps{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		DB:           fakeDB{},
		PublicOrigin: testOrigin,
		Sessions:     defaultSessions(),
		APIKeys:      keys,
		Auth:         apigen.Unimplemented{},
		Admin:        apigen.Unimplemented{},
		Storage:      apigen.Unimplemented{},
		Files:        apigen.Unimplemented{},
	})
}

func defaultKeys() fakeKeys {
	admin := keyPrincipal(ScopeFilesRead, ScopeFilesWrite)
	admin.IsAdmin = true
	return fakeKeys{keys: map[string]Principal{
		"read":  keyPrincipal(ScopeFilesRead),
		"write": keyPrincipal(ScopeFilesWrite),
		"both":  keyPrincipal(ScopeFilesRead, ScopeFilesWrite),
		"admin": admin,
		"none":  keyPrincipal(),
	}}
}

// keyRequest calls the router as a script would: a bearer token and no Origin.
func keyRequest(h http.Handler, method, target, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func patternOf(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if _, err := uuid.Parse(segment); err == nil {
			segments[i] = "{id}"
		}
	}
	return strings.Join(segments, "/")
}

// The list of what a key can reach is written out here on purpose, separately
// from the code, so widening access takes a change in two places.
var wantKeyRoutes = map[string]string{
	"GET /api/v1/files":                 ScopeFilesRead,
	"POST /api/v1/files":                ScopeFilesWrite,
	"GET /api/v1/files/{id}":            ScopeFilesRead,
	"PATCH /api/v1/files/{id}":          ScopeFilesWrite,
	"GET /api/v1/files/{id}/content":    ScopeFilesRead,
	"POST /api/v1/files/{id}/move":      ScopeFilesWrite,
	"POST /api/v1/files/{id}/trash":     ScopeFilesWrite,
	"POST /api/v1/files/{id}/restore":   ScopeFilesWrite,
	"POST /api/v1/folders":              ScopeFilesWrite,
	"PATCH /api/v1/folders/{id}":        ScopeFilesWrite,
	"POST /api/v1/folders/{id}/move":    ScopeFilesWrite,
	"POST /api/v1/folders/{id}/trash":   ScopeFilesWrite,
	"POST /api/v1/folders/{id}/restore": ScopeFilesWrite,
	"GET /api/v1/trash":                 ScopeFilesRead,
}

func TestAPIKeysReachOnlyTheirListedRoutes(t *testing.T) {
	h := newKeyRouter(t, defaultKeys())
	for _, rt := range allRoutes(t, h) {
		name := rt.method + " " + patternOf(rt.path)
		rec := keyRequest(h, rt.method, rt.path, "both")
		_, listed := wantKeyRoutes[name]
		if isPublic(rt.path) {
			if rec.Code == http.StatusForbidden && listed {
				t.Errorf("%s: public route refused a key", name)
			}
			continue
		}
		if listed && rec.Code != http.StatusNotImplemented {
			t.Errorf("%s: status = %d, want the handler to run", name, rec.Code)
		}
		if !listed && rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", name, rec.Code)
		}
	}
}

func TestAKeyIsNeverAdmin(t *testing.T) {
	h := newKeyRouter(t, defaultKeys())
	checked := 0
	for _, rt := range allRoutes(t, h) {
		if !strings.HasPrefix(rt.path, "/api/v1/admin/") {
			continue
		}
		checked++
		if rec := keyRequest(h, rt.method, rt.path, "admin"); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want 403", rt.method, rt.path, rec.Code)
		}
	}
	if checked == 0 {
		t.Fatal("no admin routes found")
	}
}

func TestPermanentDeletesNeedASession(t *testing.T) {
	h := newKeyRouter(t, defaultKeys())
	id := uuid.NewString()
	for _, target := range []string{"/api/v1/files/" + id, "/api/v1/folders/" + id, "/api/v1/trash"} {
		if rec := keyRequest(h, http.MethodDelete, target, "both"); rec.Code != http.StatusForbidden {
			t.Errorf("DELETE %s: status = %d, want 403", target, rec.Code)
		}
	}
}

func TestKeysCanOnlyDoWhatTheirScopesSay(t *testing.T) {
	h := newKeyRouter(t, defaultKeys())
	id := uuid.NewString()
	tests := []struct {
		token, method, target string
		want                  int
	}{
		{"read", http.MethodGet, "/api/v1/files", http.StatusNotImplemented},
		{"read", http.MethodGet, "/api/v1/files/" + id + "/content", http.StatusNotImplemented},
		{"read", http.MethodPost, "/api/v1/files/" + id + "/trash", http.StatusForbidden},
		{"write", http.MethodPost, "/api/v1/files/" + id + "/trash", http.StatusNotImplemented},
		{"write", http.MethodGet, "/api/v1/files", http.StatusForbidden},
		{"none", http.MethodGet, "/api/v1/files", http.StatusForbidden},
	}
	for _, tt := range tests {
		if rec := keyRequest(h, tt.method, tt.target, tt.token); rec.Code != tt.want {
			t.Errorf("%s %s with %s: status = %d, want %d", tt.method, tt.target, tt.token, rec.Code, tt.want)
		}
	}
}

func TestAnUnknownKeyIsUnauthorized(t *testing.T) {
	h := newKeyRouter(t, defaultKeys())
	rec := keyRequest(h, http.MethodGet, "/api/v1/files", "stolen-or-revoked")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// A bad key must not fall back to the session sent alongside it, or a request
// could look key-authenticated while using the browser's identity.
func TestABadKeyDoesNotFallBackToTheCookie(t *testing.T) {
	h := newKeyRouter(t, defaultKeys())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer stolen-or-revoked")
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "user"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestAKeyLookupFailureIsAnError(t *testing.T) {
	h := newKeyRouter(t, fakeKeys{err: errors.New("connection refused")})
	rec := keyRequest(h, http.MethodGet, "/api/v1/files", "read")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "refused") {
		t.Errorf("response leaks the failure: %s", rec.Body)
	}
}

func TestKeyWritesDoNotNeedAnOrigin(t *testing.T) {
	h := newKeyRouter(t, defaultKeys())
	rec := keyRequest(h, http.MethodPost, "/api/v1/folders", "write")
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want the handler to run", rec.Code)
	}
}

// Skipping the Origin check is only safe for routes a key may use at all.
func TestABearerHeaderDoesNotSkipTheOriginCheckElsewhere(t *testing.T) {
	h := newKeyRouter(t, defaultKeys())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer anything")
	req.Header.Set("Origin", "https://evil.example")
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "user"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if body := decodeError(t, rec); rec.Code != http.StatusForbidden || body.Error.Code != CodeOriginNotAllowed {
		t.Errorf("status = %d, code = %q", rec.Code, body.Error.Code)
	}
}

func TestCookieRequestsToFileRoutesStillNeedAnOrigin(t *testing.T) {
	h := newKeyRouter(t, defaultKeys())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/folders", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "user"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestOtherAuthorizationSchemesAreIgnored(t *testing.T) {
	h := newKeyRouter(t, defaultKeys())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/files", nil)
	req.Header.Set("Authorization", "Basic cmVhZDo=")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
