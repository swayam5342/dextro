package auth

import (
	"net/http"

	"github.com/oapi-codegen/runtime/types"

	"github.com/swayam5342/tributary/internal/httpx"
	"github.com/swayam5342/tributary/internal/httpx/apigen"
)

// These sit under /api/v1/admin, so requireAdmin has run before any of them.

// ListUsers implements apigen.ServerInterface.
func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.service.ListUsers(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	body := make([]apigen.ManagedUser, 0, len(users))
	for _, user := range users {
		body = append(body, toAPIManagedUser(user))
	}
	httpx.WriteJSON(w, http.StatusOK, body)
}

// CreateUser implements apigen.ServerInterface.
func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var body apigen.Credentials
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	principal, _ := httpx.PrincipalFrom(r.Context())
	created, err := h.service.CreateUser(r.Context(), principal.UserID, body.Email, body.Password, clientFrom(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toAPIManagedUser(created))
}

// DeleteUser implements apigen.ServerInterface.
func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request, id types.UUID) {
	principal, _ := httpx.PrincipalFrom(r.Context())
	if err := h.service.DeleteUser(r.Context(), principal.UserID, id, clientFrom(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ResetUserPassword implements apigen.ServerInterface.
func (h *Handler) ResetUserPassword(w http.ResponseWriter, r *http.Request, id types.UUID) {
	var body apigen.NewPassword
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	principal, _ := httpx.PrincipalFrom(r.Context())
	err := h.service.ResetUserPassword(r.Context(), principal.UserID, id, body.Password, clientFrom(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toAPIManagedUser(u ManagedUser) apigen.ManagedUser {
	return apigen.ManagedUser{
		Id:                 u.ID,
		Email:              u.Email,
		Role:               apigen.ManagedUserRole(u.Role),
		HasPassword:        u.HasPassword,
		GoogleLinked:       u.GoogleLinked,
		MustChangePassword: u.MustChangePassword,
		LinkedAccounts:     int(u.LinkedAccounts),
		CreatedAt:          u.CreatedAt,
	}
}
