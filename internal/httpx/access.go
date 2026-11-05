package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// SessionCookieName is the cookie that carries the session token.
const SessionCookieName = "dextro_session"

// adminPrefix is the one route group for instance-wide settings and maintenance.
const adminPrefix = "/api/v1/admin/"

// publicRoutes are the only routes reachable without a session. Everything else
// is refused by default, so a new route can't be left open by accident.
var publicRoutes = map[string]bool{
	"/health":                      true,
	"/api/v1/auth/config":          true,
	"/api/v1/auth/register":        true,
	"/api/v1/auth/login":           true,
	"/api/v1/auth/google/start":    true,
	"/api/v1/auth/google/callback": true,
}

// Principal is the signed-in user making a request.
type Principal struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
	IsAdmin   bool
}

// ErrNoSession means a token doesn't match a live session.
var ErrNoSession = errors.New("no session")

// SessionResolver turns a session token into the user it belongs to. It returns
// ErrNoSession for a token that is unknown or expired.
type SessionResolver interface {
	ResolveSession(ctx context.Context, token string) (Principal, error)
}

type principalKey struct{}

// PrincipalFrom returns the signed-in user, if there is one.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// requireOrigin stops other sites from making a browser send changing requests
// here. Every browser sends Origin on such requests, so a missing one is refused.
func requireOrigin(origin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			safe := r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions
			if !safe && r.Header.Get("Origin") != origin {
				WriteError(w, r, http.StatusForbidden, CodeOriginNotAllowed, "This request is not allowed from this origin.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// loadSession puts the signed-in user in the context. It doesn't refuse anyone;
// that is for the middleware after it. /health skips the lookup so it can still
// report a database outage.
func loadSession(sessions SessionResolver, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(SessionCookieName)
			if err != nil || !strings.HasPrefix(r.URL.Path, "/api/") {
				next.ServeHTTP(w, r)
				return
			}
			principal, err := sessions.ResolveSession(r.Context(), cookie.Value)
			switch {
			case errors.Is(err, ErrNoSession):
				next.ServeHTTP(w, r)
			case err != nil:
				logger.ErrorContext(r.Context(), "resolve session",
					"request_id", RequestID(r.Context()), "error", err)
				WriteError(w, r, http.StatusInternalServerError, CodeInternal, "Something went wrong.")
			default:
				ctx := context.WithValue(r.Context(), principalKey{}, principal)
				next.ServeHTTP(w, r.WithContext(ctx))
			}
		})
	}
}

// requireSession refuses anonymous requests to anything not in publicRoutes.
func requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, signedIn := PrincipalFrom(r.Context())
		if !signedIn && !publicRoutes[chi.RouteContext(r.Context()).RoutePattern()] {
			WriteError(w, r, http.StatusUnauthorized, CodeUnauthorized, "Sign in to continue.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireAdmin guards the admin route group. The generated router registers
// every route at once, so the group is defined by its path prefix.
func requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(chi.RouteContext(r.Context()).RoutePattern(), adminPrefix) {
			if p, _ := PrincipalFrom(r.Context()); !p.IsAdmin {
				WriteError(w, r, http.StatusForbidden, CodeForbidden, "You don't have access to this.")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

type clientIPKey struct{}

// ClientIP returns the address of the client making the request.
func ClientIP(ctx context.Context) string {
	ip, _ := ctx.Value(clientIPKey{}).(string)
	return ip
}

// resolveClientIP works out the client's address. X-Forwarded-For is believed
// only from a trusted proxy, because anyone else can write what they like in it.
func resolveClientIP(trusted []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), clientIPKey{}, clientIP(r, trusted))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func clientIP(r *http.Request, trusted []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	peer = peer.Unmap()
	for _, prefix := range trusted {
		if !prefix.Contains(peer) {
			continue
		}
		if forwarded, err := netip.ParseAddr(r.Header.Get("X-Forwarded-For")); err == nil {
			return forwarded.Unmap().String()
		}
		break
	}
	return peer.String()
}
