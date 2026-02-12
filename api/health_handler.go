package api

import (
	"net/http"

	"github.com/xraph/ctrlplane/health"
)

// ConfigureHealthCheck handles POST /v1/instances/{instanceID}/health/checks.
func (a *API) ConfigureHealthCheck(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	var req health.ConfigureRequest

	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	req.InstanceID = instanceID

	check, err := a.cp.Health.Configure(r.Context(), req)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusCreated, check)
}

// GetInstanceHealth handles GET /v1/instances/{instanceID}/health.
func (a *API) GetInstanceHealth(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	ih, err := a.cp.Health.GetHealth(r.Context(), instanceID)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, ih)
}

// ListHealthChecks handles GET /v1/instances/{instanceID}/health/checks.
func (a *API) ListHealthChecks(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	checks, err := a.cp.Health.ListChecks(r.Context(), instanceID)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, checks)
}

// RunHealthCheck handles POST /v1/health/checks/{checkID}/run.
func (a *API) RunHealthCheck(w http.ResponseWriter, r *http.Request) {
	checkID, err := parseID(r, "checkID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	result, err := a.cp.Health.RunCheck(r.Context(), checkID)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, result)
}

// RemoveHealthCheck handles DELETE /v1/health/checks/{checkID}.
func (a *API) RemoveHealthCheck(w http.ResponseWriter, r *http.Request) {
	checkID, err := parseID(r, "checkID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	if err := a.cp.Health.Remove(r.Context(), checkID); err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
