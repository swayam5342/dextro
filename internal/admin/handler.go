package admin

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/swayam5342/dextro/internal/httpx"
	"github.com/swayam5342/dextro/internal/httpx/apigen"
)

// Handler serves the admin routes. requireAdmin has already run by the time any
// of these are called.
type Handler struct {
	service *Service
	logger  *slog.Logger
}

// NewHandler returns a Handler for service.
func NewHandler(service *Service, logger *slog.Logger) *Handler {
	return &Handler{service: service, logger: logger}
}

// GetSettings implements apigen.ServerInterface.
func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := h.service.Settings(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAPI(settings))
}

// UpdateSettings implements apigen.ServerInterface.
func (h *Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var body apigen.SettingsUpdate
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	if body.MaxUploadBytes == nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "The request is not valid.")
		return
	}
	principal, _ := httpx.PrincipalFrom(r.Context())
	settings, err := h.service.UpdateSettings(r.Context(), principal.UserID, httpx.ClientIP(r.Context()),
		Update{MaxUploadBytes: body.MaxUploadBytes})
	if errors.Is(err, ErrInvalidSettings) {
		httpx.WriteError(w, r, http.StatusBadRequest, "invalid_settings", "Choose an upload limit between 1 MiB and 5 TiB.")
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAPI(settings))
}

func toAPI(s Settings) apigen.Settings {
	return apigen.Settings{MaxUploadBytes: s.MaxUploadBytes}
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	h.logger.ErrorContext(r.Context(), "admin request failed",
		"request_id", httpx.RequestID(r.Context()), "error", err)
	httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "Something went wrong.")
}
