package files

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/httpx"
	"github.com/swayam5342/dextro/internal/httpx/apigen"
)

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
	header.Set("Content-Security-Policy", contentPolicy)
	if err := h.service.WriteZip(r.Context(), userID(r), plan, w); err != nil {
		// Someone cancelling their download is nothing to report.
		if r.Context().Err() == nil {
			h.logger.WarnContext(r.Context(), "zip failed partway",
				"request_id", httpx.RequestID(r.Context()), "error", err)
		}
		panic(http.ErrAbortHandler)
	}
}
