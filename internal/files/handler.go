package files

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/oapi-codegen/runtime/types"

	"github.com/swayam5342/dextro/internal/httpx"
	"github.com/swayam5342/dextro/internal/httpx/apigen"
	"github.com/swayam5342/dextro/internal/provider"
	"github.com/swayam5342/dextro/internal/storage"
)

// Error codes the files API adds to the shared ones. Clients may depend on these.
const (
	codeInvalidUpload       = "invalid_upload"
	codeInvalidName         = "invalid_name"
	codeInvalidMove         = "invalid_move"
	codeNotInTrash          = "not_in_trash"
	codeFileTooLarge        = "file_too_large"
	codeConnectionNotFound  = "connection_not_found"
	codeProviderUnavailable = "provider_unavailable"
	codeFileUnavailable     = "file_unavailable"
	codeInvalidSearch       = "invalid_search"
	codeNoStorage           = "no_storage"
	codeStorageFull         = "storage_full"
)

// contentPolicy goes on every response that carries a user's file, so that even
// a file a browser decides to render can't run anything on this origin.
const contentPolicy = "sandbox"

// inlineTypes are the only types shown in the browser, and how. It's a list of
// exact types rather than image/* because an SVG is an image that can run scripts.
var inlineTypes = map[string]apigen.FilePreview{
	"image/png":       apigen.Image,
	"image/jpeg":      apigen.Image,
	"image/gif":       apigen.Image,
	"image/webp":      apigen.Image,
	"image/avif":      apigen.Image,
	"image/bmp":       apigen.Image,
	"video/mp4":       apigen.Video,
	"video/webm":      apigen.Video,
	"video/ogg":       apigen.Video,
	"application/pdf": apigen.Pdf,
}

func previewOf(contentType string) apigen.FilePreview {
	if preview, ok := inlineTypes[contentType]; ok {
		return preview
	}
	return apigen.None
}

// Handler serves the file routes.
type Handler struct {
	service *Service
	logger  *slog.Logger
}

// NewHandler returns a Handler for service.
func NewHandler(service *Service, logger *slog.Logger) *Handler {
	return &Handler{service: service, logger: logger}
}

func userID(r *http.Request) types.UUID {
	principal, _ := httpx.PrincipalFrom(r.Context())
	return principal.UserID
}

// ListFolder implements apigen.ServerInterface.
func (h *Handler) ListFolder(w http.ResponseWriter, r *http.Request, params apigen.ListFolderParams) {
	contents, err := h.service.List(r.Context(), userID(r), params.FolderId)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, apigen.FolderContents{
		Path:    toAPIFolders(contents.Path),
		Folders: toAPIFolders(contents.Folders),
		Files:   toAPIFiles(contents.Files),
	})
}

// UploadFile implements apigen.ServerInterface. The body goes straight to the
// provider as a stream.
func (h *Handler) UploadFile(w http.ResponseWriter, r *http.Request, params apigen.UploadFileParams) {
	if params.Name == nil {
		httpx.WriteError(w, r, http.StatusBadRequest, codeInvalidUpload, "An upload needs a file name.")
		return
	}
	file, err := h.service.Upload(r.Context(), userID(r), UploadInput{
		ConnectionID: params.ConnectionId,
		FolderID:     params.FolderId,
		Name:         *params.Name,
		ContentType:  r.Header.Get("Content-Type"),
		Size:         r.ContentLength,
		Body:         r.Body,
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toAPIFile(file))
}

// GetFile implements apigen.ServerInterface.
func (h *Handler) GetFile(w http.ResponseWriter, r *http.Request, id types.UUID) {
	file, err := h.service.Get(r.Context(), userID(r), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAPIFile(file))
}

// DownloadFile implements apigen.ServerInterface.
func (h *Handler) DownloadFile(w http.ResponseWriter, r *http.Request, id types.UUID, params apigen.DownloadFileParams) {
	file, err := h.service.Get(r.Context(), userID(r), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	rng, satisfiable := parseRange(r.Header.Get("Range"), file.Size)
	if !satisfiable {
		w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(file.Size, 10))
		httpx.WriteError(w, r, http.StatusRequestedRangeNotSatisfiable, httpx.CodeBadRequest, "The requested range isn't in this file.")
		return
	}
	reader, err := h.service.Open(r.Context(), userID(r), id, rng)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	defer func() { _ = reader.Close() }()

	inline := params.Disposition != nil && *params.Disposition == apigen.Inline && previewOf(file.ContentType) != apigen.None
	header := w.Header()
	header.Set("Content-Security-Policy", contentPolicy)
	header.Set("Content-Disposition", contentDisposition(inline, file.Name))
	header.Set("Accept-Ranges", "bytes")
	if inline {
		header.Set("Content-Type", file.ContentType)
	} else {
		header.Set("Content-Type", defaultType)
	}

	length, status := file.Size, http.StatusOK
	if rng != nil {
		length, status = rng.End-rng.Start+1, http.StatusPartialContent
		header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", rng.Start, rng.End, file.Size))
	}
	header.Set("Content-Length", strconv.FormatInt(length, 10))
	w.WriteHeader(status)
	h.stream(w, r, reader)
}

// stream copies a body to the client. Headers are already sent by now, so the
// only way left to report a failure is to cut the connection, which tells the
// client the file is incomplete instead of letting it keep a truncated copy.
func (h *Handler) stream(w http.ResponseWriter, r *http.Request, body io.Reader) {
	if _, err := io.Copy(w, body); err != nil {
		h.logger.WarnContext(r.Context(), "stream file",
			"request_id", httpx.RequestID(r.Context()), "error", err)
		panic(http.ErrAbortHandler)
	}
}

// contentDisposition builds the header. The mime package quotes and encodes the
// name, which is what stops one from breaking out of the header.
func contentDisposition(inline bool, name string) string {
	kind := "attachment"
	if inline {
		kind = "inline"
	}
	if header := mime.FormatMediaType(kind, map[string]string{"filename": name}); header != "" {
		return header
	}
	return kind
}

// parseRange reads a Range header. It returns a nil range for the whole file,
// and false if the range can't be met. Anything it doesn't understand, including
// multiple ranges, is ignored, which the HTTP spec allows.
func parseRange(header string, size int64) (*provider.ByteRange, bool) {
	spec, ok := strings.CutPrefix(header, "bytes=")
	if !ok || strings.Contains(spec, ",") || size == 0 {
		return nil, true
	}
	first, last, ok := strings.Cut(spec, "-")
	if !ok {
		return nil, true
	}
	if first == "" {
		suffix, err := strconv.ParseInt(last, 10, 64)
		if err != nil || suffix < 0 {
			return nil, true
		}
		if suffix == 0 {
			return nil, false
		}
		return &provider.ByteRange{Start: max(size-suffix, 0), End: size - 1}, true
	}
	start, err := strconv.ParseInt(first, 10, 64)
	if err != nil || start < 0 {
		return nil, true
	}
	end := size - 1
	if last != "" {
		if end, err = strconv.ParseInt(last, 10, 64); err != nil || end < start {
			return nil, true
		}
	}
	if start >= size {
		return nil, false
	}
	return &provider.ByteRange{Start: start, End: min(end, size-1)}, true
}

// RenameFile implements apigen.ServerInterface.
func (h *Handler) RenameFile(w http.ResponseWriter, r *http.Request, id types.UUID) {
	var body apigen.NameInput
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	file, err := h.service.RenameFile(r.Context(), userID(r), id, body.Name)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAPIFile(file))
}

// MoveFile implements apigen.ServerInterface.
func (h *Handler) MoveFile(w http.ResponseWriter, r *http.Request, id types.UUID) {
	var body apigen.MoveFileInput
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	file, err := h.service.MoveFile(r.Context(), userID(r), id, body.FolderId)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAPIFile(file))
}

// TrashFile implements apigen.ServerInterface.
func (h *Handler) TrashFile(w http.ResponseWriter, r *http.Request, id types.UUID) {
	h.noContent(w, r, h.service.TrashFile(r.Context(), userID(r), id))
}

// RestoreFile implements apigen.ServerInterface.
func (h *Handler) RestoreFile(w http.ResponseWriter, r *http.Request, id types.UUID) {
	h.noContent(w, r, h.service.RestoreFile(r.Context(), userID(r), id))
}

// DeleteFile implements apigen.ServerInterface.
func (h *Handler) DeleteFile(w http.ResponseWriter, r *http.Request, id types.UUID) {
	h.noContent(w, r, h.service.DeleteFile(r.Context(), userID(r), id, httpx.ClientIP(r.Context())))
}

// CreateFolder implements apigen.ServerInterface.
func (h *Handler) CreateFolder(w http.ResponseWriter, r *http.Request) {
	var body apigen.FolderInput
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	folder, err := h.service.CreateFolder(r.Context(), userID(r), body.ParentId, body.Name)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toAPIFolder(folder))
}

// RenameFolder implements apigen.ServerInterface.
func (h *Handler) RenameFolder(w http.ResponseWriter, r *http.Request, id types.UUID) {
	var body apigen.NameInput
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	folder, err := h.service.RenameFolder(r.Context(), userID(r), id, body.Name)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAPIFolder(folder))
}

// MoveFolder implements apigen.ServerInterface.
func (h *Handler) MoveFolder(w http.ResponseWriter, r *http.Request, id types.UUID) {
	var body apigen.MoveFolderInput
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	folder, err := h.service.MoveFolder(r.Context(), userID(r), id, body.ParentId)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAPIFolder(folder))
}

// TrashFolder implements apigen.ServerInterface.
func (h *Handler) TrashFolder(w http.ResponseWriter, r *http.Request, id types.UUID) {
	h.noContent(w, r, h.service.TrashFolder(r.Context(), userID(r), id))
}

// RestoreFolder implements apigen.ServerInterface.
func (h *Handler) RestoreFolder(w http.ResponseWriter, r *http.Request, id types.UUID) {
	h.noContent(w, r, h.service.RestoreFolder(r.Context(), userID(r), id))
}

// DeleteFolder implements apigen.ServerInterface.
func (h *Handler) DeleteFolder(w http.ResponseWriter, r *http.Request, id types.UUID) {
	h.noContent(w, r, h.service.DeleteFolder(r.Context(), userID(r), id, httpx.ClientIP(r.Context())))
}

// ListTrash implements apigen.ServerInterface.
func (h *Handler) ListTrash(w http.ResponseWriter, r *http.Request) {
	trash, err := h.service.ListTrash(r.Context(), userID(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, apigen.Trash{Folders: toAPIFolders(trash.Folders), Files: toAPIFiles(trash.Files)})
}

// Search implements apigen.ServerInterface.
func (h *Handler) Search(w http.ResponseWriter, r *http.Request, params apigen.SearchParams) {
	query := ""
	if params.Q != nil {
		query = *params.Q
	}
	results, err := h.service.Search(r.Context(), userID(r), query)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, apigen.SearchResults{Folders: toAPIFolders(results.Folders), Files: toAPIFiles(results.Files)})
}

// EmptyTrash implements apigen.ServerInterface.
func (h *Handler) EmptyTrash(w http.ResponseWriter, r *http.Request) {
	h.noContent(w, r, h.service.EmptyTrash(r.Context(), userID(r), httpx.ClientIP(r.Context())))
}

func (h *Handler) noContent(w http.ResponseWriter, r *http.Request, err error) {
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// fail sends the response for a service error. Why a provider failed goes to the
// logs only, because the reason can name internal addresses.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrFileNotFound), errors.Is(err, ErrFolderNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "Not found.")
	case errors.Is(err, storage.ErrConnectionNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, codeConnectionNotFound, "That storage connection doesn't exist.")
	case errors.Is(err, ErrInvalidName):
		httpx.WriteError(w, r, http.StatusBadRequest, codeInvalidName,
			"Use a name up to 255 characters long, without slashes.")
	case errors.Is(err, ErrInvalidMove):
		httpx.WriteError(w, r, http.StatusBadRequest, codeInvalidMove, "A folder can't move into itself.")
	case errors.Is(err, ErrNotInTrash):
		httpx.WriteError(w, r, http.StatusConflict, codeNotInTrash, "Move this to the trash before deleting it.")
	case errors.Is(err, ErrFileTooLarge):
		httpx.WriteError(w, r, http.StatusRequestEntityTooLarge, codeFileTooLarge, "This file is larger than this server allows.")
	case errors.Is(err, ErrObjectMissing):
		httpx.WriteError(w, r, http.StatusNotFound, codeFileUnavailable, "This file is no longer in its storage account.")
	case errors.Is(err, ErrInvalidSearch):
		httpx.WriteError(w, r, http.StatusBadRequest, codeInvalidSearch, "Enter something to search for.")
	case errors.Is(err, storage.ErrNoStorage):
		httpx.WriteError(w, r, http.StatusConflict, codeNoStorage, "Link a Google Drive account before uploading.")
	case errors.Is(err, storage.ErrStorageFull):
		httpx.WriteError(w, r, http.StatusInsufficientStorage, codeStorageFull, "No linked account has room for this file.")
	case errors.Is(err, ErrProviderFailed), errors.Is(err, storage.ErrStorageUnavailable):
		h.logger.WarnContext(r.Context(), "storage provider failed",
			"request_id", httpx.RequestID(r.Context()), "error", err)
		httpx.WriteError(w, r, http.StatusBadGateway, codeProviderUnavailable, "The storage account couldn't be reached.")
	default:
		h.logger.ErrorContext(r.Context(), "file request failed",
			"request_id", httpx.RequestID(r.Context()), "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "Something went wrong.")
	}
}

func toAPIFile(f File) apigen.File {
	return apigen.File{
		Id:           f.ID,
		Name:         f.Name,
		Size:         f.Size,
		ContentType:  f.ContentType,
		Preview:      previewOf(f.ContentType),
		ConnectionId: f.ConnectionID,
		FolderId:     f.FolderID,
		TrashedAt:    f.TrashedAt,
		CreatedAt:    f.CreatedAt,
		UpdatedAt:    f.UpdatedAt,
	}
}

func toAPIFiles(files []File) []apigen.File {
	body := make([]apigen.File, 0, len(files))
	for _, f := range files {
		body = append(body, toAPIFile(f))
	}
	return body
}

func toAPIFolder(f Folder) apigen.Folder {
	return apigen.Folder{
		Id:        f.ID,
		Name:      f.Name,
		ParentId:  f.ParentID,
		TrashedAt: f.TrashedAt,
		CreatedAt: f.CreatedAt,
		UpdatedAt: f.UpdatedAt,
	}
}

func toAPIFolders(folders []Folder) []apigen.Folder {
	body := make([]apigen.Folder, 0, len(folders))
	for _, f := range folders {
		body = append(body, toAPIFolder(f))
	}
	return body
}
