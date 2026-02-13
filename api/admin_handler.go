package api

import (
	"net/http"

	"github.com/xraph/forge"

	"github.com/xraph/ctrlplane/admin"
)

// systemStats handles GET /v1/admin/stats.
func (a *API) systemStats(ctx forge.Context, _ *EmptyRequest) (*admin.SystemStats, error) {
	stats, err := a.cp.Admin.SystemStats(ctx.Context())
	if err != nil {
		return nil, mapError(err)
	}

	return stats, nil
}

// listProviders handles GET /v1/admin/providers.
func (a *API) listProviders(ctx forge.Context, _ *EmptyRequest) ([]admin.ProviderStatus, error) {
	providers, err := a.cp.Admin.ListProviders(ctx.Context())
	if err != nil {
		return nil, mapError(err)
	}

	return providers, nil
}

// createTenant handles POST /v1/admin/tenants.
func (a *API) createTenant(ctx forge.Context, req *CreateTenantAPIRequest) (*admin.Tenant, error) {
	tenant, err := a.cp.Admin.CreateTenant(ctx.Context(), req.CreateTenantRequest)
	if err != nil {
		return nil, mapError(err)
	}

	_ = ctx.JSON(http.StatusCreated, tenant)

	//nolint:nilnil // response already written via ctx.JSON/ctx.NoContent.
	return nil, nil
}

// listTenants handles GET /v1/admin/tenants.
func (a *API) listTenants(ctx forge.Context, req *ListTenantsAPIRequest) (*admin.TenantListResult, error) {
	limit := req.Limit
	if limit == 0 {
		limit = 50
	}

	opts := admin.ListTenantsOptions{
		Status: req.Status,
		Cursor: req.Cursor,
		Limit:  limit,
	}

	result, err := a.cp.Admin.ListTenants(ctx.Context(), opts)
	if err != nil {
		return nil, mapError(err)
	}

	return result, nil
}

// getTenant handles GET /v1/admin/tenants/:tenantID.
func (a *API) getTenant(ctx forge.Context, req *TenantPathRequest) (*admin.Tenant, error) {
	tenant, err := a.cp.Admin.GetTenant(ctx.Context(), req.TenantID)
	if err != nil {
		return nil, mapError(err)
	}

	return tenant, nil
}

// updateTenant handles PATCH /v1/admin/tenants/:tenantID.
func (a *API) updateTenant(ctx forge.Context, req *UpdateTenantAPIRequest) (*admin.Tenant, error) {
	tenant, err := a.cp.Admin.UpdateTenant(ctx.Context(), req.TenantID, req.UpdateTenantRequest)
	if err != nil {
		return nil, mapError(err)
	}

	return tenant, nil
}

// suspendTenant handles POST /v1/admin/tenants/:tenantID/suspend.
func (a *API) suspendTenant(ctx forge.Context, req *SuspendTenantAPIRequest) (*admin.Tenant, error) {
	if err := a.cp.Admin.SuspendTenant(ctx.Context(), req.TenantID, req.Reason); err != nil {
		return nil, mapError(err)
	}

	_ = ctx.NoContent(http.StatusNoContent)

	//nolint:nilnil // response already written via ctx.JSON/ctx.NoContent.
	return nil, nil
}

// unsuspendTenant handles POST /v1/admin/tenants/:tenantID/unsuspend.
func (a *API) unsuspendTenant(ctx forge.Context, req *TenantPathRequest) (*admin.Tenant, error) {
	if err := a.cp.Admin.UnsuspendTenant(ctx.Context(), req.TenantID); err != nil {
		return nil, mapError(err)
	}

	_ = ctx.NoContent(http.StatusNoContent)

	//nolint:nilnil // response already written via ctx.JSON/ctx.NoContent.
	return nil, nil
}

// deleteTenant handles DELETE /v1/admin/tenants/:tenantID.
func (a *API) deleteTenant(ctx forge.Context, req *TenantPathRequest) (*admin.Tenant, error) {
	if err := a.cp.Admin.DeleteTenant(ctx.Context(), req.TenantID); err != nil {
		return nil, mapError(err)
	}

	_ = ctx.NoContent(http.StatusNoContent)

	//nolint:nilnil // response already written via ctx.JSON/ctx.NoContent.
	return nil, nil
}

// getQuota handles GET /v1/admin/tenants/:tenantID/quota.
func (a *API) getQuota(ctx forge.Context, req *TenantPathRequest) (*admin.QuotaUsage, error) {
	usage, err := a.cp.Admin.GetQuota(ctx.Context(), req.TenantID)
	if err != nil {
		return nil, mapError(err)
	}

	return usage, nil
}

// setQuota handles PUT /v1/admin/tenants/:tenantID/quota.
func (a *API) setQuota(ctx forge.Context, req *SetQuotaAPIRequest) (*admin.Tenant, error) {
	if err := a.cp.Admin.SetQuota(ctx.Context(), req.TenantID, req.Quota); err != nil {
		return nil, mapError(err)
	}

	_ = ctx.NoContent(http.StatusNoContent)

	//nolint:nilnil // response already written via ctx.JSON/ctx.NoContent.
	return nil, nil
}

// queryAuditLog handles POST /v1/admin/audit.
func (a *API) queryAuditLog(ctx forge.Context, req *QueryAuditLogAPIRequest) (*admin.AuditResult, error) {
	result, err := a.cp.Admin.QueryAuditLog(ctx.Context(), req.AuditQuery)
	if err != nil {
		return nil, mapError(err)
	}

	return result, nil
}
