package api

import (
	"net/http"

	"github.com/xraph/ctrlplane/admin"
)

// SystemStats handles GET /v1/admin/stats.
func (a *API) SystemStats(w http.ResponseWriter, r *http.Request) {
	stats, err := a.cp.Admin.SystemStats(r.Context())
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, stats)
}

// ListProviders handles GET /v1/admin/providers.
func (a *API) ListProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := a.cp.Admin.ListProviders(r.Context())
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, providers)
}

// CreateTenant handles POST /v1/admin/tenants.
func (a *API) CreateTenant(w http.ResponseWriter, r *http.Request) {
	var req admin.CreateTenantRequest

	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	tenant, err := a.cp.Admin.CreateTenant(r.Context(), req)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusCreated, tenant)
}

// ListTenants handles GET /v1/admin/tenants.
func (a *API) ListTenants(w http.ResponseWriter, r *http.Request) {
	opts := admin.ListTenantsOptions{
		Status: r.URL.Query().Get("status"),
		Cursor: r.URL.Query().Get("cursor"),
		Limit:  parseIntQuery(r, "limit", 50),
	}

	result, err := a.cp.Admin.ListTenants(r.Context(), opts)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, result)
}

// GetTenant handles GET /v1/admin/tenants/{tenantID}.
func (a *API) GetTenant(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenantID")

	tenant, err := a.cp.Admin.GetTenant(r.Context(), tenantID)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, tenant)
}

// UpdateTenant handles PATCH /v1/admin/tenants/{tenantID}.
func (a *API) UpdateTenant(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenantID")

	var req admin.UpdateTenantRequest

	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	tenant, err := a.cp.Admin.UpdateTenant(r.Context(), tenantID, req)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, tenant)
}

// SuspendTenant handles POST /v1/admin/tenants/{tenantID}/suspend.
func (a *API) SuspendTenant(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenantID")

	var body struct {
		Reason string `json:"reason"`
	}

	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	if err := a.cp.Admin.SuspendTenant(r.Context(), tenantID, body.Reason); err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// UnsuspendTenant handles POST /v1/admin/tenants/{tenantID}/unsuspend.
func (a *API) UnsuspendTenant(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenantID")

	if err := a.cp.Admin.UnsuspendTenant(r.Context(), tenantID); err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// DeleteTenant handles DELETE /v1/admin/tenants/{tenantID}.
func (a *API) DeleteTenant(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenantID")

	if err := a.cp.Admin.DeleteTenant(r.Context(), tenantID); err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GetQuota handles GET /v1/admin/tenants/{tenantID}/quota.
func (a *API) GetQuota(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenantID")

	usage, err := a.cp.Admin.GetQuota(r.Context(), tenantID)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, usage)
}

// SetQuota handles PUT /v1/admin/tenants/{tenantID}/quota.
func (a *API) SetQuota(w http.ResponseWriter, r *http.Request) {
	tenantID := r.PathValue("tenantID")

	var quota admin.Quota

	if err := readJSON(r, &quota); err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	if err := a.cp.Admin.SetQuota(r.Context(), tenantID, quota); err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// QueryAuditLog handles GET /v1/admin/audit.
func (a *API) QueryAuditLog(w http.ResponseWriter, r *http.Request) {
	var q admin.AuditQuery

	if err := readJSON(r, &q); err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	result, err := a.cp.Admin.QueryAuditLog(r.Context(), q)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, result)
}
