package files

import (
	"net/http"

	"github.com/oapi-codegen/runtime/types"

	"github.com/swayam5342/tributary/internal/httpx"
	"github.com/swayam5342/tributary/internal/httpx/apigen"
)

// ListDuplicates implements apigen.ServerInterface.
func (h *Handler) ListDuplicates(w http.ResponseWriter, r *http.Request) {
	found, err := h.service.Duplicates(r.Context(), userID(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	groups := make([]apigen.DuplicateGroup, 0, len(found.Groups))
	for _, group := range found.Groups {
		copies := make([]apigen.DuplicateFile, 0, len(group.Files))
		for _, copied := range group.Files {
			copies = append(copies, apigen.DuplicateFile{File: toAPIFile(copied.File), Location: copied.Location})
		}
		groups = append(groups, apigen.DuplicateGroup{Hash: group.Hash, Size: group.Size, Files: copies})
	}
	httpx.WriteJSON(w, http.StatusOK, apigen.DuplicateList{Groups: groups, ReclaimableBytes: found.ReclaimableBytes})
}

// GetFileChecks implements apigen.ServerInterface.
func (h *Handler) GetFileChecks(w http.ResponseWriter, r *http.Request) {
	checks, err := h.service.FileChecks(r.Context(), userID(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	problems := make([]apigen.FileProblem, 0, len(checks.Problems))
	for _, problem := range checks.Problems {
		problems = append(problems, apigen.FileProblem{
			Id:           problem.ID,
			FileId:       problem.FileID,
			ConnectionId: problem.ConnectionID,
			Name:         problem.Name,
			Size:         problem.Size,
			Kind:         apigen.FileProblemKind(problem.Kind),
			FoundAt:      problem.FoundAt,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, apigen.FileChecks{
		Enabled:        checks.Enabled,
		CheckableFiles: checks.Checkable,
		CheckedFiles:   checks.Checked,
		Problems:       problems,
	})
}

// DismissFileProblem implements apigen.ServerInterface.
func (h *Handler) DismissFileProblem(w http.ResponseWriter, r *http.Request, id types.UUID) {
	h.noContent(w, r, h.service.DismissProblem(r.Context(), userID(r), id))
}
