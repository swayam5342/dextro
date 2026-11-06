package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// SessionCookieName is the cookie that carries the session token.
const SessionCookieName = "dextro_session"

// adminPrefix is the one route group for instance-wide settings and maintenance.
const adminPrefix = "/api/v1/admin/"

// Scopes an API key can hold. These are stored, so don't rename them.
const (
	ScopeFilesRead  = "files:read"
	ScopeFilesWrite = "files:write"
)

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

// keyRoutes are the only routes an API key can reach, and the scope each needs.
// Everything else is refused, so a new route is closed to keys by default.
// Permanent deletes are left out on purpose: a leaked key can move files to the
// trash but can't destroy them.
var keyRoutes = map[string]string{
	"GET /api/v1/files":                 ScopeFilesRead,
	"POST /api/v1/files":                ScopeFilesWrite,
	"GET /api/v1/files/{id}":            ScopeFilesRead,
	"PATCH /api/v1/files/{id}":          ScopeFilesWrite,
	"GET /api/v1/files/{id}/content":    ScopeFilesRead,
	"GET /api/v1/files/{id}/thumbnail":  ScopeFilesRead,
	"POST /api/v1/files/{id}/move":      ScopeFilesWrite,
	"POST /api/v1/files/{id}/trash":     ScopeFilesWrite,
	"POST /api/v1/files/{id}/restore":   ScopeFilesWrite,
	"POST /api/v1/folders":              ScopeFilesWrite,
	"PATCH /api/v1/folders/{id}":        ScopeFilesWrite,
	"POST /api/v1/folders/{id}/move":    ScopeFilesWrite,
	"POST /api/v1/folders/{id}/trash":   ScopeFilesWrite,
	"POST /api/v1/folders/{id}/restore": ScopeFilesWrite,
	"GET /api/v1/trash":                 ScopeFilesRead,
	"GET /api/v1/search":                ScopeFilesRead,
	"GET /api/v1/uploads":               ScopeFilesWrite,
	"POST /api/v1/uploads":              ScopeFilesWrite,
	"GET /api/v1/uploads/{id}":          ScopeFilesWrite,
	"DELETE /api/v1/uploads/{id}":       ScopeFilesWrite,
	"PUT /api/v1/uploads/{id}/chunk":    ScopeFilesWrite,
}

// passwordChangeRoutes are all that's left while someone has to replace a password
// the admin chose. Everything else is refused, so a new route is covered by default.
var passwordChangeRoutes = map[string]bool{
	"GET /api/v1/auth/config":    true,
	"GET /api/v1/auth/me":        true,
	"POST /api/v1/auth/logout":   true,
	"POST /api/v1/auth/password": true,
}

// KeyAccess describes the API key behind a request.
type KeyAccess struct {
	ID     uuid.UUID
	Scopes []string
}

// Principal is the user making a request. Key is set when the request came with
// an API key rather than a session, in which case SessionID is empty.
type Principal struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
	IsAdmin   bool
	Key       *KeyAccess
	// MustChangePassword limits the user to passwordChangeRoutes.
	MustChangePassword bool
}

// ErrNoSession means a token doesn't match a live session or API key.
var ErrNoSession = errors.New("no session")

// SessionResolver turns a session token into the user it belongs to. It returns
// ErrNoSession for a token that is unknown or expired.
type SessionResolver interface {
	ResolveSession(ctx context.Context, token string) (Principal, error)
}

// APIKeyResolver turns an API key into the user it belongs to. It returns
// ErrNoSession for a key that is unknown or revoked.
type APIKeyResolver interface {
	ResolveAPIKey(ctx context.Context, token string) (Principal, error)
}

type principalKey struct{}

// PrincipalFrom returns the user making the request, if there is one.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

func bearerToken(r *http.Request) (string, bool) {
	return strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func routeKey(r *http.Request) string {
	return r.Method + " " + chi.RouteContext(r.Context()).RoutePattern()
}

func isKeyRequest(r *http.Request) bool {
	_, hasToken := bearerToken(r)
	_, listed := keyRoutes[routeKey(r)]
	return hasToken && listed
}

// requireOrigin stops other sites from making a browser send changing requests
// here. Every browser sends Origin on such requests, so a missing one is refused.
// Requests with an API key are exempt on routes keys can use: a browser won't
// attach that header for another site, so there is nothing to forge.
func requireOrigin(origin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			safe := r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions
			if !safe && !isKeyRequest(r) && r.Header.Get("Origin") != origin {
				WriteError(w, r, http.StatusForbidden, CodeOriginNotAllowed, "This request is not allowed from this origin.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// loadSession puts the user making the request in the context. It doesn't refuse
// anyone; that is for the middleware after it. An API key, good or bad, takes
// the place of any session cookie. /health skips the lookup so it can still
// report a database outage.
func loadSession(sessions SessionResolver, keys APIKeyResolver, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/api/") {
				next.ServeHTTP(w, r)
				return
			}
			principal, err := resolvePrincipal(r, sessions, keys)
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

func resolvePrincipal(r *http.Request, sessions SessionResolver, keys APIKeyResolver) (Principal, error) {
	if token, ok := bearerToken(r); ok {
		if keys == nil {
			return Principal{}, ErrNoSession
		}
		return keys.ResolveAPIKey(r.Context(), token)
	}
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		return Principal{}, ErrNoSession
	}
	return sessions.ResolveSession(r.Context(), cookie.Value)
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

// requireKeyScope limits API keys to the routes in keyRoutes, and to the routes
// their scopes cover.
func requireKeyScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := PrincipalFrom(r.Context()); ok && p.Key != nil {
			scope, listed := keyRoutes[routeKey(r)]
			if !listed || !slices.Contains(p.Key.Scopes, scope) {
				WriteError(w, r, http.StatusForbidden, CodeForbidden, "This API key can't do that.")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requirePasswordChange holds someone at the password change until they've done
// it, so a password the admin knows can't be used for anything else.
func requirePasswordChange(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := PrincipalFrom(r.Context()); ok && p.MustChangePassword && !passwordChangeRoutes[routeKey(r)] {
			WriteError(w, r, http.StatusForbidden, CodePasswordChangeRequired, "Choose a new password to continue.")
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
