// Package httpx builds the HTTP router and holds what every handler shares.
package httpx

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/swayam5342/dextro/internal/httpx/apigen"
)

// Deps is everything the router needs.
type Deps struct {
	Logger *slog.Logger
	DB     Pinger
}

// NewRouter returns the handler for the whole server.
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(assignRequestID, accessLog(d.Logger), recoverPanics(d.Logger), securityHeaders)

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, http.StatusNotFound, CodeNotFound, "Not found.")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "Method not allowed.")
	})

	apigen.HandlerWithOptions(&api{db: d.DB, logger: d.Logger}, apigen.ChiServerOptions{
		BaseRouter: r,
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			d.Logger.WarnContext(r.Context(), "invalid request",
				"request_id", RequestID(r.Context()), "error", err)
			WriteError(w, r, http.StatusBadRequest, CodeBadRequest, "The request is not valid.")
		},
	})
	return r
}
