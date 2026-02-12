package api

import (
	"net/http"

	"github.com/xraph/ctrlplane/telemetry"
)

// QueryMetrics handles GET /v1/instances/{instanceID}/metrics.
func (a *API) QueryMetrics(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	q := telemetry.MetricQuery{
		InstanceID: instanceID,
	}

	metrics, err := a.cp.Telemetry.QueryMetrics(r.Context(), q)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, metrics)
}

// QueryLogs handles GET /v1/instances/{instanceID}/logs.
func (a *API) QueryLogs(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	q := telemetry.LogQuery{
		InstanceID: instanceID,
	}

	logs, err := a.cp.Telemetry.QueryLogs(r.Context(), q)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, logs)
}

// QueryTraces handles GET /v1/instances/{instanceID}/traces.
func (a *API) QueryTraces(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	q := telemetry.TraceQuery{
		InstanceID: instanceID,
	}

	traces, err := a.cp.Telemetry.QueryTraces(r.Context(), q)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, traces)
}

// GetResources handles GET /v1/instances/{instanceID}/resources.
func (a *API) GetResources(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	snap, err := a.cp.Telemetry.GetCurrentResources(r.Context(), instanceID)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, snap)
}

// GetDashboard handles GET /v1/instances/{instanceID}/dashboard.
func (a *API) GetDashboard(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	dash, err := a.cp.Telemetry.GetDashboard(r.Context(), instanceID)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, dash)
}
