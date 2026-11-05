package admin

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/swayam5342/dextro/internal/audit"
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

// GetServerStatus implements apigen.ServerInterface.
func (h *Handler) GetServerStatus(w http.ResponseWriter, r *http.Request) {
	status, err := h.service.Status(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	body := apigen.ServerStatus{
		Jobs:                make([]apigen.JobRun, 0, len(status.Jobs)),
		Accounts:            status.Health.Accounts,
		UsedBytes:           status.Health.UsedBytes,
		UnreachableAccounts: make([]apigen.UnreachableAccount, 0, len(status.Health.Unreachable)),
	}
	for _, job := range status.Jobs {
		body.Jobs = append(body.Jobs, apigen.JobRun{Name: job.Name, FinishedAt: job.FinishedAt, Succeeded: job.Succeeded})
	}
	for _, account := range status.Health.Unreachable {
		body.UnreachableAccounts = append(body.UnreachableAccounts, apigen.UnreachableAccount{
			OwnerEmail: account.OwnerEmail,
			Summary:    account.Summary,
			CheckedAt:  account.CheckedAt,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, body)
}

// ListAuditEvents implements apigen.ServerInterface.
func (h *Handler) ListAuditEvents(w http.ResponseWriter, r *http.Request, params apigen.ListAuditEventsParams) {
	filter := audit.Filter{}
	if params.Action != nil {
		filter.Action = *params.Action
	}
	if params.Before != nil {
		before, err := strconv.ParseInt(*params.Before, 10, 64)
		if err != nil {
			httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "The request is not valid.")
			return
		}
		filter.Before = before
	}
	page, err := h.service.AuditLog(r.Context(), filter)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	body := apigen.AuditPage{Events: make([]apigen.AuditEvent, 0, len(page.Events))}
	for _, event := range page.Events {
		body.Events = append(body.Events, apigen.AuditEvent{
			Id:         event.ID,
			Action:     event.Action,
			ActorEmail: event.ActorEmail,
			Ip:         event.IP,
			Metadata:   event.Metadata,
			CreatedAt:  event.CreatedAt,
		})
	}
	if page.NextBefore != 0 {
		next := strconv.FormatInt(page.NextBefore, 10)
		body.NextBefore = &next
	}
	httpx.WriteJSON(w, http.StatusOK, body)
}

func toAPI(s Settings) apigen.Settings {
	return apigen.Settings{MaxUploadBytes: s.MaxUploadBytes}
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	h.logger.ErrorContext(r.Context(), "admin request failed",
		"request_id", httpx.RequestID(r.Context()), "error", err)
	httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "Something went wrong.")
}
