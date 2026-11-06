package files

import (
	"net/http"

	"github.com/oapi-codegen/runtime/types"

	"github.com/swayam5342/dextro/internal/httpx"
	"github.com/swayam5342/dextro/internal/httpx/apigen"
)

func toAPIShare(s Share) apigen.Share {
	return apigen.Share{
		FileId:    s.FileID,
		Name:      s.Name,
		Role:      apigen.ShareRole(s.Role),
		Url:       s.URL,
		ExpiresAt: s.ExpiresAt,
		CreatedAt: s.CreatedAt,
	}
}

// ListShares implements apigen.ServerInterface.
func (h *Handler) ListShares(w http.ResponseWriter, r *http.Request) {
	list, err := h.service.ListShares(r.Context(), userID(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	body := apigen.ShareList{Enabled: list.Enabled, Shares: make([]apigen.Share, 0, len(list.Shares))}
	for _, share := range list.Shares {
		body.Shares = append(body.Shares, toAPIShare(share))
	}
	httpx.WriteJSON(w, http.StatusOK, body)
}

// PutShare implements apigen.ServerInterface.
func (h *Handler) PutShare(w http.ResponseWriter, r *http.Request, id types.UUID) {
	var body apigen.NewShare
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	share, err := h.service.ShareFile(r.Context(), userID(r), id, ShareInput{Role: string(body.Role), ExpiresAt: body.ExpiresAt}, httpx.ClientIP(r.Context()))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAPIShare(share))
}

// DeleteShare implements apigen.ServerInterface.
func (h *Handler) DeleteShare(w http.ResponseWriter, r *http.Request, id types.UUID) {
	h.noContent(w, r, h.service.UnshareFile(r.Context(), userID(r), id, httpx.ClientIP(r.Context())))
}
