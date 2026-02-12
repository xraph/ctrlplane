package api

import (
	"net/http"

	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/id"
)

// Deploy handles POST /v1/instances/{instanceID}/deploy.
func (a *API) Deploy(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	var req deploy.DeployRequest

	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	req.InstanceID = instanceID

	deployment, err := a.cp.Deploys.Deploy(r.Context(), req)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusCreated, deployment)
}

// ListDeployments handles GET /v1/instances/{instanceID}/deployments.
func (a *API) ListDeployments(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	opts := deploy.ListOptions{
		Cursor: r.URL.Query().Get("cursor"),
		Limit:  parseIntQuery(r, "limit", 20),
	}

	result, err := a.cp.Deploys.ListDeployments(r.Context(), instanceID, opts)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, result)
}

// GetDeployment handles GET /v1/deployments/{deploymentID}.
func (a *API) GetDeployment(w http.ResponseWriter, r *http.Request) {
	deploymentID, err := parseID(r, "deploymentID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	deployment, err := a.cp.Deploys.GetDeployment(r.Context(), deploymentID)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, deployment)
}

// CancelDeployment handles POST /v1/deployments/{deploymentID}/cancel.
func (a *API) CancelDeployment(w http.ResponseWriter, r *http.Request) {
	deploymentID, err := parseID(r, "deploymentID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	if err := a.cp.Deploys.Cancel(r.Context(), deploymentID); err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Rollback handles POST /v1/instances/{instanceID}/rollback.
func (a *API) Rollback(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	var body struct {
		ReleaseID id.ID `json:"release_id"`
	}

	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	deployment, err := a.cp.Deploys.Rollback(r.Context(), instanceID, body.ReleaseID)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusCreated, deployment)
}

// ListReleases handles GET /v1/instances/{instanceID}/releases.
func (a *API) ListReleases(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	opts := deploy.ListOptions{
		Cursor: r.URL.Query().Get("cursor"),
		Limit:  parseIntQuery(r, "limit", 20),
	}

	result, err := a.cp.Deploys.ListReleases(r.Context(), instanceID, opts)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, result)
}

// GetRelease handles GET /v1/releases/{releaseID}.
func (a *API) GetRelease(w http.ResponseWriter, r *http.Request) {
	releaseID, err := parseID(r, "releaseID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	release, err := a.cp.Deploys.GetRelease(r.Context(), releaseID)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, release)
}
