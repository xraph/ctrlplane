package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
)

// writeJSON serializes v as JSON and writes it to w with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if encErr := json.NewEncoder(w).Encode(map[string]string{
		"error": err.Error(),
	}); encErr != nil {
		http.Error(w, encErr.Error(), http.StatusInternalServerError)
	}
}

// readJSON decodes the request body into dst.
func readJSON(r *http.Request, dst any) error {
	return json.NewDecoder(r.Body).Decode(dst)
}

// parseID extracts and parses an id.ID from a path value.
func parseID(r *http.Request, key string) (id.ID, error) {
	raw := r.PathValue(key)
	if raw == "" {
		return id.Nil, errors.New("missing path parameter: " + key)
	}

	return id.Parse(raw)
}

// parseIntQuery extracts an int query parameter with a default value.
func parseIntQuery(r *http.Request, key string, defaultVal int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return defaultVal
	}

	val, err := strconv.Atoi(raw)
	if err != nil {
		return defaultVal
	}

	return val
}

// errorStatus maps sentinel errors to HTTP status codes.
func errorStatus(err error) int {
	switch {
	case errors.Is(err, ctrlplane.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ctrlplane.ErrAlreadyExists):
		return http.StatusConflict
	case errors.Is(err, ctrlplane.ErrInvalidState):
		return http.StatusConflict
	case errors.Is(err, ctrlplane.ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, ctrlplane.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, ctrlplane.ErrQuotaExceeded):
		return http.StatusTooManyRequests
	case errors.Is(err, ctrlplane.ErrProviderNotFound):
		return http.StatusBadRequest
	case errors.Is(err, ctrlplane.ErrProviderUnavail):
		return http.StatusServiceUnavailable
	case errors.Is(err, ctrlplane.ErrDeploymentFailed):
		return http.StatusInternalServerError
	case errors.Is(err, ctrlplane.ErrInvalidConfig):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
