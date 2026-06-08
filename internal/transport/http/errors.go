// Package http wires the HTTP transport: router, middleware, and handlers. It
// translates domain results and errors into consistent JSON responses.
package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/shaurya703/linkforge/internal/domain"
)

// ErrorResponse is the single JSON shape for every error the API returns.
type ErrorResponse struct {
	Error string `json:"error"`          // human-readable message
	Code  string `json:"code,omitempty"` // stable machine-readable code
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, ErrorResponse{Error: msg, Code: code})
}

// writeDomainError maps a domain sentinel error to its HTTP response.
func writeDomainError(w http.ResponseWriter, log *slog.Logger, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidURL):
		writeError(w, http.StatusBadRequest, "invalid_url", "the provided url is invalid; must be http(s) with a host")
	case errors.Is(err, domain.ErrInvalidCode):
		writeError(w, http.StatusBadRequest, "invalid_code", "alias must be 3-32 chars of [A-Za-z0-9_-] and not reserved")
	case errors.Is(err, domain.ErrAliasTaken):
		writeError(w, http.StatusConflict, "alias_taken", "that custom alias is already in use")
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no link found for that code")
	case errors.Is(err, domain.ErrExpired):
		writeError(w, http.StatusGone, "expired", "this link has expired")
	default:
		log.Error("unhandled error", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "something went wrong")
	}
}
