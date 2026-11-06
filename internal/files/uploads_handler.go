package files

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/oapi-codegen/runtime/types"

	"github.com/swayam5342/dextro/internal/httpx"
	"github.com/swayam5342/dextro/internal/httpx/apigen"
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

func parseIDs(raw *[]string) ([]uuid.UUID, error) {
	if raw == nil {
		return nil, nil
	}
	ids := make([]uuid.UUID, 0, len(*raw))
	for _, value := range *raw {
		id, err := uuid.Parse(value)
		if err != nil {
			return nil, ErrInvalidZip
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// DownloadZip implements apigen.ServerInterface. Once the first byte has gone it's
// too late for an error response, so a failure cuts the connection: ending normally
// would leave a broken zip that looks finished.
func (h *Handler) DownloadZip(w http.ResponseWriter, r *http.Request, params apigen.DownloadZipParams) {
	files, err := parseIDs(params.File)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	folders, err := parseIDs(params.Folder)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	plan, err := h.service.PlanZip(r.Context(), userID(r), files, folders)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if params.Check != nil && *params.Check == "true" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	header := w.Header()
	header.Set("Content-Type", "application/zip")
	header.Set("Content-Disposition", contentDisposition(false, plan.Name))
	header.Set("Content-Security-Policy", "sandbox")
	if err := h.service.WriteZip(r.Context(), userID(r), plan, w); err != nil {
		// Someone cancelling their download is nothing to report.
		if r.Context().Err() == nil {
			h.logger.WarnContext(r.Context(), "zip failed partway",
				"request_id", httpx.RequestID(r.Context()), "error", err)
		}
		panic(http.ErrAbortHandler)
	}
}
