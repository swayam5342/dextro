package files

import (
	"net/http"
	"strconv"

	"github.com/oapi-codegen/runtime/types"

	"github.com/swayam5342/tributary/internal/httpx"
	"github.com/swayam5342/tributary/internal/httpx/apigen"
)

// ListUploads implements apigen.ServerInterface.
func (h *Handler) ListUploads(w http.ResponseWriter, r *http.Request) {
	sessions, err := h.service.ListUploads(r.Context(), userID(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	body := make([]apigen.UploadSession, 0, len(sessions))
	for _, session := range sessions {
		body = append(body, toAPIUpload(session))
	}
	httpx.WriteJSON(w, http.StatusOK, body)
}

// BeginUpload implements apigen.ServerInterface.
func (h *Handler) BeginUpload(w http.ResponseWriter, r *http.Request) {
	var body apigen.NewUpload
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	in := BeginUploadInput{
		ConnectionID: body.ConnectionId,
		FolderID:     body.FolderId,
		Name:         body.Name,
		Size:         body.Size,
	}
	if body.ContentType != nil {
		in.ContentType = *body.ContentType
	}
	if body.LastModified != nil {
		in.LastModified = *body.LastModified
	}
	if body.BusyConnectionIds != nil {
		in.Busy = *body.BusyConnectionIds
	}
	session, err := h.service.BeginUpload(r.Context(), userID(r), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toAPIUpload(session))
}

// GetUpload implements apigen.ServerInterface.
func (h *Handler) GetUpload(w http.ResponseWriter, r *http.Request, id types.UUID) {
	session, err := h.service.UploadProgress(r.Context(), userID(r), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAPIUpload(session))
}

// CancelUpload implements apigen.ServerInterface.
func (h *Handler) CancelUpload(w http.ResponseWriter, r *http.Request, id types.UUID) {
	if err := h.service.CancelUpload(r.Context(), userID(r), id); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PutUploadChunk implements apigen.ServerInterface. The length comes from the
// request, because a chunk has to say up front how long it is.
func (h *Handler) PutUploadChunk(w http.ResponseWriter, r *http.Request, id types.UUID, params apigen.PutUploadChunkParams) {
	if params.Offset == nil {
		h.fail(w, r, ErrInvalidChunk)
		return
	}
	offset, err := strconv.ParseInt(*params.Offset, 10, 64)
	if err != nil {
		h.fail(w, r, ErrInvalidChunk)
		return
	}
	session, err := h.service.PutUploadChunk(r.Context(), userID(r), id, offset, r.ContentLength, r.Body)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAPIUpload(session))
}

func toAPIUpload(s UploadSession) apigen.UploadSession {
	body := apigen.UploadSession{
		Id:           s.ID,
		ConnectionId: s.ConnectionID,
		FolderId:     s.FolderID,
		Name:         s.Name,
		Size:         s.Size,
		Received:     s.Received,
		LastModified: s.LastModified,
		CreatedAt:    s.CreatedAt,
	}
	if s.File != nil {
		file := toAPIFile(*s.File)
		body.File = &file
	}
	if s.Encrypted && s.File == nil && s.Received < s.Size {
		piece := nextSealedPiece(s.Received, s.Size)
		body.PieceBytes = &piece
	}
	return body
}

// SyncConnection implements apigen.ServerInterface. It lives here, not with the
// other connection routes, because what it changes is files.
func (h *Handler) SyncConnection(w http.ResponseWriter, r *http.Request, id types.UUID) {
	result, err := h.service.SyncConnection(r.Context(), userID(r), id, httpx.ClientIP(r.Context()))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, apigen.SyncResult{Added: result.Added, Removed: result.Removed})
}

// ImportPicked implements apigen.ServerInterface.
func (h *Handler) ImportPicked(w http.ResponseWriter, r *http.Request, id types.UUID) {
	var body apigen.PickedFiles
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	result, err := h.service.ImportPicked(r.Context(), userID(r), id, body.FileIds, httpx.ClientIP(r.Context()))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, apigen.ImportResult{Added: result.Added, Skipped: result.Skipped})
}
