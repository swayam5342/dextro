package httpx

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/httpx/apigen"
)

const testOrigin = "https://files.example.org"

// fakeSessions maps cookie values to principals.
type fakeSessions struct {
	sessions map[string]Principal
	err      error
}

func (f fakeSessions) ResolveSession(_ context.Context, token string) (Principal, error) {
	if f.err != nil {
		return Principal{}, f.err
	}
	p, ok := f.sessions[token]
	if !ok {
		return Principal{}, ErrNoSession
	}
	return p, nil
}

var (
	userPrincipal  = Principal{UserID: uuid.New(), SessionID: uuid.New()}
	adminPrincipal = Principal{UserID: uuid.New(), SessionID: uuid.New(), IsAdmin: true}
)

func newAccessRouter(t *testing.T, sessions SessionResolver) http.Handler {
	t.Helper()
	return NewRouter(Deps{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		DB:           fakeDB{},
		PublicOrigin: testOrigin,
		Sessions:     sessions,
		Auth:         apigen.Unimplemented{},
		Admin:        apigen.Unimplemented{},
	})
}

func defaultSessions() fakeSessions {
	return fakeSessions{sessions: map[string]Principal{"user": userPrincipal, "admin": adminPrincipal}}
}

// request calls the router the way a browser on the right origin would.
func request(h http.Handler, method, target, cookie string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set("Origin", testOrigin)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: cookie})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type route struct{ method, path string }

// allRoutes lists what the router really serves, so a route added later is
// covered by the tests below without anyone remembering to add it.
func allRoutes(t *testing.T, h http.Handler) []route {
	t.Helper()
	var routes []route
	err := chi.Walk(h.(chi.Routes), func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes = append(routes, route{method, strings.ReplaceAll(pattern, "{id}", uuid.NewString())})
		return nil
	})
	if err != nil {
		t.Fatalf("walk routes: %v", err)
	}
	if len(routes) < 10 {
		t.Fatalf("found only %d routes", len(routes))
	}
	return routes
}

func isPublic(path string) bool {
	switch path {
	case "/health", "/api/v1/auth/config", "/api/v1/auth/register", "/api/v1/auth/login",
		"/api/v1/auth/google/start", "/api/v1/auth/google/callback":
		return true
	}
	return false
}

func TestEveryRouteNeedsASessionUnlessItIsPublic(t *testing.T) {
	h := newAccessRouter(t, defaultSessions())
	for _, rt := range allRoutes(t, h) {
		rec := request(h, rt.method, rt.path, "")
		if isPublic(rt.path) {
			if rec.Code == http.StatusUnauthorized {
				t.Errorf("%s %s: public route returned 401", rt.method, rt.path)
			}
			continue
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, want 401", rt.method, rt.path, rec.Code)
			continue
		}
		if body := decodeError(t, rec); body.Error.Code != CodeUnauthorized {
			t.Errorf("%s %s: code = %q", rt.method, rt.path, body.Error.Code)
		}
	}
}

func TestAnUnknownSessionCookieIsTreatedAsNoSession(t *testing.T) {
	h := newAccessRouter(t, defaultSessions())
	if rec := request(h, http.MethodGet, "/api/v1/auth/me", "stolen-or-expired"); rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestEveryAdminRouteRefusesNonAdmins(t *testing.T) {
	h := newAccessRouter(t, defaultSessions())
	checked := 0
	for _, rt := range allRoutes(t, h) {
		if !strings.HasPrefix(rt.path, "/api/v1/admin/") {
			continue
		}
		checked++
		rec := request(h, rt.method, rt.path, "user")
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want 403", rt.method, rt.path, rec.Code)
			continue
		}
		if body := decodeError(t, rec); body.Error.Code != CodeForbidden {
			t.Errorf("%s %s: code = %q", rt.method, rt.path, body.Error.Code)
		}
		if rec := request(h, rt.method, rt.path, "admin"); rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s as admin: status = %d, want the handler to run", rt.method, rt.path, rec.Code)
		}
	}
	if checked == 0 {
		t.Fatal("no admin routes found")
	}
}

func TestSignedInUsersReachOrdinaryRoutes(t *testing.T) {
	h := newAccessRouter(t, defaultSessions())
	if rec := request(h, http.MethodGet, "/api/v1/auth/me", "user"); rec.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want the handler to run", rec.Code)
	}
}

func TestHandlersReceiveThePrincipal(t *testing.T) {
	var got Principal
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = PrincipalFrom(r.Context())
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := loadSession(defaultSessions(), logger)(inner)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "admin"})
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got != adminPrincipal {
		t.Errorf("principal = %+v, want %+v", got, adminPrincipal)
	}
}

func TestSessionLookupFailureIsAnErrorNotAnAnonymousRequest(t *testing.T) {
	h := newAccessRouter(t, fakeSessions{err: errors.New("connection refused")})
	rec := request(h, http.MethodPost, "/api/v1/auth/login", "user")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "refused") {
		t.Errorf("response leaks the failure: %s", rec.Body)
	}
}

func TestHealthStillAnswersWhenSessionsAreUnavailable(t *testing.T) {
	h := newAccessRouter(t, fakeSessions{err: errors.New("connection refused")})
	if rec := request(h, http.MethodGet, "/health", "user"); rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestChangingRequestsMustComeFromThePublicOrigin(t *testing.T) {
	h := newAccessRouter(t, defaultSessions())
	tests := []struct {
		name   string
		origin string
	}{
		{"no origin", ""},
		{"another site", "https://evil.example"},
		{"a lookalike suffix", testOrigin + ".evil.example"},
		{"the wrong scheme", "http://files.example.org"},
		{"an opaque origin", "null"},
	}
	for _, rt := range allRoutes(t, h) {
		if rt.method == http.MethodGet || rt.method == http.MethodHead {
			continue
		}
		for _, tt := range tests {
			t.Run(tt.name+" "+rt.method+" "+rt.path, func(t *testing.T) {
				req := httptest.NewRequest(rt.method, rt.path, nil)
				req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "admin"})
				if tt.origin != "" {
					req.Header.Set("Origin", tt.origin)
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("status = %d, want 403", rec.Code)
				}
				if body := decodeError(t, rec); body.Error.Code != CodeOriginNotAllowed {
					t.Errorf("code = %q", body.Error.Code)
				}
			})
		}
	}
}

func TestReadsDoNotNeedAnOrigin(t *testing.T) {
	h := newAccessRouter(t, defaultSessions())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "user"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want the handler to run", rec.Code)
	}
}

func TestAPIResponsesAreNeverCached(t *testing.T) {
	h := newAccessRouter(t, defaultSessions())
	rec := request(h, http.MethodGet, "/api/v1/auth/me", "user")
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func TestClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("172.30.0.10/32")}
	tests := []struct {
		name, remote, forwarded, want string
	}{
		{"direct client", "203.0.113.7:4000", "", "203.0.113.7"},
		{"direct client forging the header", "203.0.113.7:4000", "198.51.100.1", "203.0.113.7"},
		{"through the proxy", "172.30.0.10:4000", "198.51.100.1", "198.51.100.1"},
		{"through the proxy with a mapped address", "172.30.0.10:4000", "::ffff:198.51.100.1", "198.51.100.1"},
		{"proxy sending garbage", "172.30.0.10:4000", "not-an-ip", "172.30.0.10"},
		{"proxy sending nothing", "172.30.0.10:4000", "", "172.30.0.10"},
		{"ipv6 client", "[2001:db8::1]:4000", "", "2001:db8::1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			h := resolveClientIP(trusted)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = ClientIP(r.Context())
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remote
			if tt.forwarded != "" {
				req.Header.Set("X-Forwarded-For", tt.forwarded)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got != tt.want {
				t.Errorf("ClientIP = %q, want %q", got, tt.want)
			}
		})
	}
}
