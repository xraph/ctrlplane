package api

import (
	"net/http"

	"github.com/xraph/ctrlplane/app"
)

// API wires all HTTP handlers together.
type API struct {
	cp *app.CtrlPlane
}

// New creates an API from a CtrlPlane instance.
func New(cp *app.CtrlPlane) *API {
	return &API{cp: cp}
}

// Handler returns the fully assembled http.Handler with all routes.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()

	// Instance routes.
	mux.HandleFunc("POST /v1/instances", a.CreateInstance)
	mux.HandleFunc("GET /v1/instances", a.ListInstances)
	mux.HandleFunc("GET /v1/instances/{instanceID}", a.GetInstance)
	mux.HandleFunc("PATCH /v1/instances/{instanceID}", a.UpdateInstance)
	mux.HandleFunc("DELETE /v1/instances/{instanceID}", a.DeleteInstance)
	mux.HandleFunc("POST /v1/instances/{instanceID}/start", a.StartInstance)
	mux.HandleFunc("POST /v1/instances/{instanceID}/stop", a.StopInstance)
	mux.HandleFunc("POST /v1/instances/{instanceID}/restart", a.RestartInstance)
	mux.HandleFunc("POST /v1/instances/{instanceID}/scale", a.ScaleInstance)
	mux.HandleFunc("POST /v1/instances/{instanceID}/suspend", a.SuspendInstance)
	mux.HandleFunc("POST /v1/instances/{instanceID}/unsuspend", a.UnsuspendInstance)

	// Deploy routes.
	mux.HandleFunc("POST /v1/instances/{instanceID}/deploy", a.Deploy)
	mux.HandleFunc("GET /v1/instances/{instanceID}/deployments", a.ListDeployments)
	mux.HandleFunc("GET /v1/deployments/{deploymentID}", a.GetDeployment)
	mux.HandleFunc("POST /v1/deployments/{deploymentID}/cancel", a.CancelDeployment)
	mux.HandleFunc("POST /v1/instances/{instanceID}/rollback", a.Rollback)
	mux.HandleFunc("GET /v1/instances/{instanceID}/releases", a.ListReleases)
	mux.HandleFunc("GET /v1/releases/{releaseID}", a.GetRelease)

	// Health routes.
	mux.HandleFunc("POST /v1/instances/{instanceID}/health/checks", a.ConfigureHealthCheck)
	mux.HandleFunc("GET /v1/instances/{instanceID}/health", a.GetInstanceHealth)
	mux.HandleFunc("GET /v1/instances/{instanceID}/health/checks", a.ListHealthChecks)
	mux.HandleFunc("POST /v1/health/checks/{checkID}/run", a.RunHealthCheck)
	mux.HandleFunc("DELETE /v1/health/checks/{checkID}", a.RemoveHealthCheck)

	// Telemetry routes.
	mux.HandleFunc("GET /v1/instances/{instanceID}/metrics", a.QueryMetrics)
	mux.HandleFunc("GET /v1/instances/{instanceID}/logs", a.QueryLogs)
	mux.HandleFunc("GET /v1/instances/{instanceID}/traces", a.QueryTraces)
	mux.HandleFunc("GET /v1/instances/{instanceID}/resources", a.GetResources)
	mux.HandleFunc("GET /v1/instances/{instanceID}/dashboard", a.GetDashboard)

	// Network routes.
	mux.HandleFunc("POST /v1/instances/{instanceID}/domains", a.AddDomain)
	mux.HandleFunc("GET /v1/instances/{instanceID}/domains", a.ListDomains)
	mux.HandleFunc("POST /v1/domains/{domainID}/verify", a.VerifyDomain)
	mux.HandleFunc("DELETE /v1/domains/{domainID}", a.RemoveDomain)
	mux.HandleFunc("POST /v1/instances/{instanceID}/routes", a.AddRoute)
	mux.HandleFunc("GET /v1/instances/{instanceID}/routes", a.ListRoutes)
	mux.HandleFunc("PATCH /v1/routes/{routeID}", a.UpdateRoute)
	mux.HandleFunc("DELETE /v1/routes/{routeID}", a.RemoveRoute)
	mux.HandleFunc("POST /v1/domains/{domainID}/cert", a.ProvisionCert)

	// Secrets routes.
	mux.HandleFunc("POST /v1/instances/{instanceID}/secrets", a.SetSecret)
	mux.HandleFunc("GET /v1/instances/{instanceID}/secrets", a.ListSecrets)
	mux.HandleFunc("DELETE /v1/instances/{instanceID}/secrets/{key}", a.DeleteSecret)

	// Admin routes.
	mux.HandleFunc("GET /v1/admin/stats", a.SystemStats)
	mux.HandleFunc("GET /v1/admin/providers", a.ListProviders)
	mux.HandleFunc("POST /v1/admin/tenants", a.CreateTenant)
	mux.HandleFunc("GET /v1/admin/tenants", a.ListTenants)
	mux.HandleFunc("GET /v1/admin/tenants/{tenantID}", a.GetTenant)
	mux.HandleFunc("PATCH /v1/admin/tenants/{tenantID}", a.UpdateTenant)
	mux.HandleFunc("POST /v1/admin/tenants/{tenantID}/suspend", a.SuspendTenant)
	mux.HandleFunc("POST /v1/admin/tenants/{tenantID}/unsuspend", a.UnsuspendTenant)
	mux.HandleFunc("DELETE /v1/admin/tenants/{tenantID}", a.DeleteTenant)
	mux.HandleFunc("GET /v1/admin/tenants/{tenantID}/quota", a.GetQuota)
	mux.HandleFunc("PUT /v1/admin/tenants/{tenantID}/quota", a.SetQuota)
	mux.HandleFunc("GET /v1/admin/audit", a.QueryAuditLog)

	// Wrap with auth middleware.
	return a.AuthMiddleware(mux)
}
