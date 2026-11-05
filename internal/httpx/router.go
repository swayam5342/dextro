// Package httpx builds the HTTP router and holds what every handler shares.
package httpx

import (
	"log/slog"
	"net/http"
	"net/netip"

	"github.com/go-chi/chi/v5"
	"github.com/oapi-codegen/runtime/types"

	"github.com/swayam5342/dextro/internal/httpx/apigen"
)

// AuthHandlers is the part of the API the auth feature implements.
type AuthHandlers interface {
	GetAuthConfig(w http.ResponseWriter, r *http.Request)
	Register(w http.ResponseWriter, r *http.Request)
	Login(w http.ResponseWriter, r *http.Request)
	Logout(w http.ResponseWriter, r *http.Request)
	GetMe(w http.ResponseWriter, r *http.Request)
	ChangePassword(w http.ResponseWriter, r *http.Request)
	ListSessions(w http.ResponseWriter, r *http.Request)
	RevokeSession(w http.ResponseWriter, r *http.Request, id types.UUID)
	StartGoogleSignIn(w http.ResponseWriter, r *http.Request)
	LinkGoogle(w http.ResponseWriter, r *http.Request)
	GoogleCallback(w http.ResponseWriter, r *http.Request, params apigen.GoogleCallbackParams)
}

// StorageHandlers is the part of the API the storage feature implements.
type StorageHandlers interface {
	ListConnections(w http.ResponseWriter, r *http.Request)
	CreateS3Connection(w http.ResponseWriter, r *http.Request)
	DeleteConnection(w http.ResponseWriter, r *http.Request, id types.UUID)
	TestConnection(w http.ResponseWriter, r *http.Request, id types.UUID)
	ConnectGoogleDrive(w http.ResponseWriter, r *http.Request)
	GoogleDriveCallback(w http.ResponseWriter, r *http.Request, params apigen.GoogleDriveCallbackParams)
}

// AdminHandlers is the part of the API the admin feature implements.
type AdminHandlers interface {
	GetSettings(w http.ResponseWriter, r *http.Request)
	UpdateSettings(w http.ResponseWriter, r *http.Request)
}

// Deps is everything the router needs.
type Deps struct {
	Logger *slog.Logger
	DB     Pinger
	// PublicOrigin is the only Origin allowed to make changing requests.
	PublicOrigin   string
	TrustedProxies []netip.Prefix
	Sessions       SessionResolver
	Auth           AuthHandlers
	Admin          AdminHandlers
	Storage        StorageHandlers
}

// NewRouter returns the handler for the whole server.
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(assignRequestID, accessLog(d.Logger), recoverPanics(d.Logger), securityHeaders, resolveClientIP(d.TrustedProxies))

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, http.StatusNotFound, CodeNotFound, "Not found.")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "Method not allowed.")
	})

	server := &api{AuthHandlers: d.Auth, AdminHandlers: d.Admin, StorageHandlers: d.Storage, db: d.DB, logger: d.Logger}
	apigen.HandlerWithOptions(server, apigen.ChiServerOptions{
		BaseRouter: r,
		// These wrap each generated route, and the last one listed runs first.
		Middlewares: []apigen.MiddlewareFunc{
			requireAdmin,
			requireSession,
			loadSession(d.Sessions, d.Logger),
			requireOrigin(d.PublicOrigin),
		},
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			d.Logger.WarnContext(r.Context(), "invalid request",
				"request_id", RequestID(r.Context()), "error", err)
			WriteError(w, r, http.StatusBadRequest, CodeBadRequest, "The request is not valid.")
		},
	})
	return r
}
