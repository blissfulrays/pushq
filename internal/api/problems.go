package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/blissfulrays/pushq/internal/store"
)

// Stable machine-readable error codes (RFC 9457 problem+json `code` extension).
const (
	CodeAlreadyExists      = "already_exists"
	CodeNotFound           = "not_found"
	CodeFailedPrecondition = "failed_precondition"
	CodeInvalidArgument    = "invalid_argument"
	CodeUnauthenticated    = "unauthenticated"
	CodePermissionDenied   = "permission_denied"
	CodePayloadTooLarge    = "payload_too_large"
	CodeRateLimited        = "rate_limited"
	CodeInternal           = "internal"
)

type problem struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Code     string `json:"code"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
}

func writeProblem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(problem{
		Type:     "about:blank",
		Title:    http.StatusText(status),
		Status:   status,
		Code:     code,
		Detail:   scrub(r, detail), // never reveal a team's internal namespace
		Instance: r.URL.Path,
	})
}

// writeStoreErr maps store sentinel errors onto problem responses.
func writeStoreErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeProblem(w, r, http.StatusNotFound, CodeNotFound, err.Error())
	case errors.Is(err, store.ErrAlreadyExists):
		writeProblem(w, r, http.StatusConflict, CodeAlreadyExists, err.Error())
	case errors.Is(err, store.ErrFailedPrecondition):
		writeProblem(w, r, http.StatusBadRequest, CodeFailedPrecondition, err.Error())
	default:
		writeProblem(w, r, http.StatusInternalServerError, CodeInternal, "internal error")
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
