package httpx

import (
	"encoding/json"
	"net/http"

	"github.com/swayam5342/tributary/internal/httpx/apigen"
)

// Stable error codes. Clients may depend on these, so don't rename them.
const (
	CodeBadRequest             = "bad_request"
	CodeNotFound               = "not_found"
	CodeMethodNotAllowed       = "method_not_allowed"
	CodeInternal               = "internal_error"
	CodeUnauthorized           = "unauthorized"
	CodeForbidden              = "forbidden"
	CodeOriginNotAllowed       = "origin_not_allowed"
	CodeRateLimited            = "rate_limited"
	CodePasswordChangeRequired = "password_change_required"
)

// WriteError sends the standard error body. The message reaches the client, so
// it must be safe to show; put the details in the logs instead.
func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	var body apigen.Error
	body.Error.Code = code
	body.Error.Message = message
	body.Error.RequestId = RequestID(r.Context())
	WriteJSON(w, status, body)
}

// WriteJSON sends body as JSON with the given status.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status is already sent, and an encode failure here means the client
	// went away, so there is nothing useful left to do with the error.
	_ = json.NewEncoder(w).Encode(body)
}

// maxJSONBody bounds JSON request bodies. No JSON endpoint carries file data.
const maxJSONBody = 64 << 10

// DecodeJSON reads the request body into dst. If it can't, it sends the error
// response and returns false.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		WriteError(w, r, http.StatusBadRequest, CodeBadRequest, "The request is not valid.")
		return false
	}
	return true
}
