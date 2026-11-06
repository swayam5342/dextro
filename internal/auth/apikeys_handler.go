package auth

import (
	"net/http"

	"github.com/oapi-codegen/runtime/types"

	"github.com/swayam5342/tributary/internal/httpx"
	"github.com/swayam5342/tributary/internal/httpx/apigen"
)

const codeInvalidAPIKey = "invalid_api_key"

// ListAPIKeys implements apigen.ServerInterface.
func (h *Handler) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	principal, _ := httpx.PrincipalFrom(r.Context())
	keys, err := h.service.ListAPIKeys(r.Context(), principal.UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	body := make([]apigen.APIKey, 0, len(keys))
	for _, key := range keys {
		body = append(body, toAPIKeyBody(key))
	}
	httpx.WriteJSON(w, http.StatusOK, body)
}

// CreateAPIKey implements apigen.ServerInterface.
func (h *Handler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	principal, _ := httpx.PrincipalFrom(r.Context())
	var body struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	created, token, err := h.service.CreateAPIKey(r.Context(), principal.UserID, body.Name, body.Scopes, clientFrom(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, apigen.APIKeyCreated{Key: toAPIKeyBody(created), Token: token})
}

// RevokeAPIKey implements apigen.ServerInterface.
func (h *Handler) RevokeAPIKey(w http.ResponseWriter, r *http.Request, id types.UUID) {
	principal, _ := httpx.PrincipalFrom(r.Context())
	if err := h.service.RevokeAPIKey(r.Context(), principal.UserID, id, clientFrom(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toAPIKeyBody(key APIKey) apigen.APIKey {
	scopes := make([]apigen.APIKeyScopes, 0, len(key.Scopes))
	for _, scope := range key.Scopes {
		scopes = append(scopes, apigen.APIKeyScopes(scope))
	}
	return apigen.APIKey{
		Id:         key.ID,
		Name:       key.Name,
		Prefix:     key.Prefix,
		Scopes:     scopes,
		CreatedAt:  key.CreatedAt,
		LastUsedAt: key.LastUsedAt,
	}
}
