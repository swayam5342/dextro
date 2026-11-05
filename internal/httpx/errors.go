package httpx

import (
	"encoding/json"
	"net/http"

	"github.com/swayam5342/dextro/internal/httpx/apigen"
)

// Stable error codes. Clients may depend on these, so don't rename them.
const (
	CodeBadRequest       = "bad_request"
	CodeNotFound         = "not_found"
	CodeMethodNotAllowed = "method_not_allowed"
	CodeInternal         = "internal_error"
	CodeUIUnavailable    = "ui_unavailable"
)

// WriteError sends the standard error body. The message reaches the client, so
// it must be safe to show; put the details in the logs instead.
func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	var body apigen.Error
	body.Error.Code = code
	body.Error.Message = message
	body.Error.RequestId = RequestID(r.Context())
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status is already sent, and an encode failure here means the client
	// went away, so there is nothing useful left to do with the error.
	_ = json.NewEncoder(w).Encode(body)
}
