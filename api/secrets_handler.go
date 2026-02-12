package api

import (
	"net/http"

	"github.com/xraph/ctrlplane/secrets"
)

// SetSecret creates or updates a secret for an instance.
func (a *API) SetSecret(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	var req secrets.SetRequest

	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	req.InstanceID = instanceID

	secret, err := a.cp.Secrets.Set(r.Context(), req)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusCreated, secret)
}

// ListSecrets returns all secrets for an instance.
func (a *API) ListSecrets(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	list, err := a.cp.Secrets.List(r.Context(), instanceID)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, list)
}

// DeleteSecret removes a secret from an instance.
func (a *API) DeleteSecret(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	key := r.PathValue("key")

	if err := a.cp.Secrets.Delete(r.Context(), instanceID, key); err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
