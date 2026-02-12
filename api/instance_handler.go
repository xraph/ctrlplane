package api

import (
	"net/http"

	"github.com/xraph/ctrlplane/instance"
)

// CreateInstance handles POST /v1/instances.
func (a *API) CreateInstance(w http.ResponseWriter, r *http.Request) {
	var req instance.CreateRequest

	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	inst, err := a.cp.Instances.Create(r.Context(), req)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusCreated, inst)
}

// ListInstances handles GET /v1/instances.
func (a *API) ListInstances(w http.ResponseWriter, r *http.Request) {
	opts := instance.ListOptions{
		State:    r.URL.Query().Get("state"),
		Label:    r.URL.Query().Get("label"),
		Provider: r.URL.Query().Get("provider"),
		Cursor:   r.URL.Query().Get("cursor"),
		Limit:    parseIntQuery(r, "limit", 20),
	}

	result, err := a.cp.Instances.List(r.Context(), opts)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, result)
}

// GetInstance handles GET /v1/instances/{instanceID}.
func (a *API) GetInstance(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	inst, err := a.cp.Instances.Get(r.Context(), instanceID)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, inst)
}

// UpdateInstance handles PATCH /v1/instances/{instanceID}.
func (a *API) UpdateInstance(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	var req instance.UpdateRequest

	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	inst, err := a.cp.Instances.Update(r.Context(), instanceID, req)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, inst)
}

// DeleteInstance handles DELETE /v1/instances/{instanceID}.
func (a *API) DeleteInstance(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	if err := a.cp.Instances.Delete(r.Context(), instanceID); err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// StartInstance handles POST /v1/instances/{instanceID}/start.
func (a *API) StartInstance(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	if err := a.cp.Instances.Start(r.Context(), instanceID); err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// StopInstance handles POST /v1/instances/{instanceID}/stop.
func (a *API) StopInstance(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	if err := a.cp.Instances.Stop(r.Context(), instanceID); err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// RestartInstance handles POST /v1/instances/{instanceID}/restart.
func (a *API) RestartInstance(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	if err := a.cp.Instances.Restart(r.Context(), instanceID); err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ScaleInstance handles POST /v1/instances/{instanceID}/scale.
func (a *API) ScaleInstance(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	var req instance.ScaleRequest

	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	if err := a.cp.Instances.Scale(r.Context(), instanceID, req); err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// SuspendInstance handles POST /v1/instances/{instanceID}/suspend.
func (a *API) SuspendInstance(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	var body struct {
		Reason string `json:"reason"`
	}

	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	if err := a.cp.Instances.Suspend(r.Context(), instanceID, body.Reason); err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// UnsuspendInstance handles POST /v1/instances/{instanceID}/unsuspend.
func (a *API) UnsuspendInstance(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	if err := a.cp.Instances.Unsuspend(r.Context(), instanceID); err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
