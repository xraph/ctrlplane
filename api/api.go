package api

import (
	"net/http"

	"github.com/xraph/forge"

	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/app"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/health"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/network"
	"github.com/xraph/ctrlplane/secrets"
	"github.com/xraph/ctrlplane/telemetry"
)

// API wires all Forge-style HTTP handlers together.
type API struct {
	cp     *app.CtrlPlane
	router forge.Router
}

// New creates an API from a CtrlPlane instance.
func New(cp *app.CtrlPlane, router forge.Router) *API {
	return &API{cp: cp, router: router}
}

// Handler returns the fully assembled http.Handler with all routes.
// It creates an internal Forge router, registers all routes with OpenAPI
// metadata, wraps with auth middleware, and returns the result.
func (a *API) Handler() http.Handler {
	if a.router == nil {
		a.router = forge.NewRouter()
	}

	a.RegisterRoutes(a.router)

	return a.authMiddleware(a.router.Handler())
}

// RegisterRoutes registers all API routes into the given Forge router
// with full OpenAPI metadata. Use this for Forge extension integration.
func (a *API) RegisterRoutes(router forge.Router) {
	protectRoutes := router.Group("")
	protectRoutes.Use(a.AuthForgeMiddleware())

	a.registerInstanceRoutes(protectRoutes)
	a.registerDeployRoutes(protectRoutes)
	a.registerHealthRoutes(protectRoutes)
	a.registerTelemetryRoutes(protectRoutes)
	a.registerNetworkRoutes(protectRoutes)
	a.registerSecretsRoutes(protectRoutes)
	a.registerAdminRoutes(protectRoutes)
}

// registerInstanceRoutes registers all instance management routes.
func (a *API) registerInstanceRoutes(router forge.Router) {
	g := router.Group("/v1", forge.WithGroupTags("instances"))

	_ = g.POST("/instances", a.createInstance,
		forge.WithSummary("Create instance"),
		forge.WithDescription("Provisions a new application instance."),
		forge.WithOperationID("createInstance"),
		forge.WithRequestSchema(CreateInstanceRequest{}),
		forge.WithCreatedResponse(instance.Instance{}),
		forge.WithErrorResponses(),
	)

	_ = g.GET("/instances", a.listInstances,
		forge.WithSummary("List instances"),
		forge.WithDescription("Returns a paginated list of instances."),
		forge.WithOperationID("listInstances"),
		forge.WithRequestSchema(ListInstancesRequest{}),
		forge.WithPaginatedResponse(instance.Instance{}, http.StatusOK),
		forge.WithErrorResponses(),
	)

	_ = g.GET("/instances/:instanceID", a.getInstance,
		forge.WithSummary("Get instance"),
		forge.WithDescription("Returns details of a specific instance."),
		forge.WithOperationID("getInstance"),
		forge.WithResponseSchema(http.StatusOK, "Instance details", instance.Instance{}),
		forge.WithErrorResponses(),
	)

	_ = g.PATCH("/instances/:instanceID", a.updateInstance,
		forge.WithSummary("Update instance"),
		forge.WithDescription("Updates mutable fields of an instance."),
		forge.WithOperationID("updateInstance"),
		forge.WithRequestSchema(UpdateInstanceRequest{}),
		forge.WithResponseSchema(http.StatusOK, "Updated instance", instance.Instance{}),
		forge.WithErrorResponses(),
	)

	_ = g.DELETE("/instances/:instanceID", a.deleteInstance,
		forge.WithSummary("Delete instance"),
		forge.WithDescription("Destroys an instance and its resources."),
		forge.WithOperationID("deleteInstance"),
		forge.WithNoContentResponse(),
		forge.WithErrorResponses(),
	)

	_ = g.POST("/instances/:instanceID/start", a.startInstance,
		forge.WithSummary("Start instance"),
		forge.WithDescription("Starts a stopped instance."),
		forge.WithOperationID("startInstance"),
		forge.WithNoContentResponse(),
		forge.WithErrorResponses(),
	)

	_ = g.POST("/instances/:instanceID/stop", a.stopInstance,
		forge.WithSummary("Stop instance"),
		forge.WithDescription("Stops a running instance."),
		forge.WithOperationID("stopInstance"),
		forge.WithNoContentResponse(),
		forge.WithErrorResponses(),
	)

	_ = g.POST("/instances/:instanceID/restart", a.restartInstance,
		forge.WithSummary("Restart instance"),
		forge.WithDescription("Restarts a running instance."),
		forge.WithOperationID("restartInstance"),
		forge.WithNoContentResponse(),
		forge.WithErrorResponses(),
	)

	_ = g.POST("/instances/:instanceID/scale", a.scaleInstance,
		forge.WithSummary("Scale instance"),
		forge.WithDescription("Adjusts CPU, memory, or replica count."),
		forge.WithOperationID("scaleInstance"),
		forge.WithRequestSchema(ScaleInstanceRequest{}),
		forge.WithNoContentResponse(),
		forge.WithErrorResponses(),
	)

	_ = g.POST("/instances/:instanceID/suspend", a.suspendInstance,
		forge.WithSummary("Suspend instance"),
		forge.WithDescription("Suspends an instance with a reason."),
		forge.WithOperationID("suspendInstance"),
		forge.WithRequestSchema(SuspendInstanceRequest{}),
		forge.WithNoContentResponse(),
		forge.WithErrorResponses(),
	)

	_ = g.POST("/instances/:instanceID/unsuspend", a.unsuspendInstance,
		forge.WithSummary("Unsuspend instance"),
		forge.WithDescription("Resumes a suspended instance."),
		forge.WithOperationID("unsuspendInstance"),
		forge.WithNoContentResponse(),
		forge.WithErrorResponses(),
	)
}

// registerDeployRoutes registers all deployment and release routes.
func (a *API) registerDeployRoutes(router forge.Router) {
	g := router.Group("/v1", forge.WithGroupTags("deployments"))

	_ = g.POST("/instances/:instanceID/deploy", a.deployInstance,
		forge.WithSummary("Deploy to instance"),
		forge.WithDescription("Creates a new deployment for an instance."),
		forge.WithOperationID("deployInstance"),
		forge.WithRequestSchema(DeployAPIRequest{}),
		forge.WithCreatedResponse(deploy.Deployment{}),
		forge.WithErrorResponses(),
	)

	_ = g.GET("/instances/:instanceID/deployments", a.listDeployments,
		forge.WithSummary("List deployments"),
		forge.WithDescription("Returns a paginated list of deployments for an instance."),
		forge.WithOperationID("listDeployments"),
		forge.WithRequestSchema(ListDeploymentsRequest{}),
		forge.WithPaginatedResponse(deploy.Deployment{}, http.StatusOK),
		forge.WithErrorResponses(),
	)

	_ = g.GET("/deployments/:deploymentID", a.getDeployment,
		forge.WithSummary("Get deployment"),
		forge.WithDescription("Returns details of a specific deployment."),
		forge.WithOperationID("getDeployment"),
		forge.WithResponseSchema(http.StatusOK, "Deployment details", deploy.Deployment{}),
		forge.WithErrorResponses(),
	)

	_ = g.POST("/deployments/:deploymentID/cancel", a.cancelDeployment,
		forge.WithSummary("Cancel deployment"),
		forge.WithDescription("Cancels an in-progress deployment."),
		forge.WithOperationID("cancelDeployment"),
		forge.WithNoContentResponse(),
		forge.WithErrorResponses(),
	)

	_ = g.POST("/instances/:instanceID/rollback", a.rollback,
		forge.WithSummary("Rollback instance"),
		forge.WithDescription("Rolls back an instance to a previous release."),
		forge.WithOperationID("rollbackInstance"),
		forge.WithRequestSchema(RollbackRequest{}),
		forge.WithCreatedResponse(deploy.Deployment{}),
		forge.WithErrorResponses(),
	)

	_ = g.GET("/instances/:instanceID/releases", a.listReleases,
		forge.WithSummary("List releases"),
		forge.WithDescription("Returns a paginated list of releases for an instance."),
		forge.WithOperationID("listReleases"),
		forge.WithRequestSchema(ListReleasesRequest{}),
		forge.WithPaginatedResponse(deploy.Release{}, http.StatusOK),
		forge.WithErrorResponses(),
	)

	_ = g.GET("/releases/:releaseID", a.getRelease,
		forge.WithSummary("Get release"),
		forge.WithDescription("Returns details of a specific release."),
		forge.WithOperationID("getRelease"),
		forge.WithResponseSchema(http.StatusOK, "Release details", deploy.Release{}),
		forge.WithErrorResponses(),
	)
}

// registerHealthRoutes registers all health check routes.
func (a *API) registerHealthRoutes(router forge.Router) {
	g := router.Group("/v1", forge.WithGroupTags("health"))

	_ = g.POST("/instances/:instanceID/health/checks", a.configureHealthCheck,
		forge.WithSummary("Configure health check"),
		forge.WithDescription("Adds or updates a health check for an instance."),
		forge.WithOperationID("configureHealthCheck"),
		forge.WithRequestSchema(ConfigureHealthCheckAPIRequest{}),
		forge.WithCreatedResponse(health.HealthCheck{}),
		forge.WithErrorResponses(),
	)

	_ = g.GET("/instances/:instanceID/health", a.getInstanceHealth,
		forge.WithSummary("Get instance health"),
		forge.WithDescription("Returns the aggregate health status of an instance."),
		forge.WithOperationID("getInstanceHealth"),
		forge.WithResponseSchema(http.StatusOK, "Instance health", health.InstanceHealth{}),
		forge.WithErrorResponses(),
	)

	_ = g.GET("/instances/:instanceID/health/checks", a.listHealthChecks,
		forge.WithSummary("List health checks"),
		forge.WithDescription("Returns all health checks configured for an instance."),
		forge.WithOperationID("listHealthChecks"),
		forge.WithResponseSchema(http.StatusOK, "Health checks", []health.HealthCheck{}),
		forge.WithErrorResponses(),
	)

	_ = g.POST("/health/checks/:checkID/run", a.runHealthCheck,
		forge.WithSummary("Run health check"),
		forge.WithDescription("Runs a specific health check immediately."),
		forge.WithOperationID("runHealthCheck"),
		forge.WithResponseSchema(http.StatusOK, "Health check result", health.HealthResult{}),
		forge.WithErrorResponses(),
	)

	_ = g.DELETE("/health/checks/:checkID", a.removeHealthCheck,
		forge.WithSummary("Remove health check"),
		forge.WithDescription("Removes a health check configuration."),
		forge.WithOperationID("removeHealthCheck"),
		forge.WithNoContentResponse(),
		forge.WithErrorResponses(),
	)
}

// registerTelemetryRoutes registers all telemetry and observability routes.
func (a *API) registerTelemetryRoutes(router forge.Router) {
	g := router.Group("/v1", forge.WithGroupTags("telemetry"))

	_ = g.GET("/instances/:instanceID/metrics", a.queryMetrics,
		forge.WithSummary("Query metrics"),
		forge.WithDescription("Returns metric data points for an instance."),
		forge.WithOperationID("queryMetrics"),
		forge.WithResponseSchema(http.StatusOK, "Metric points", []telemetry.Metric{}),
		forge.WithErrorResponses(),
	)

	_ = g.GET("/instances/:instanceID/logs", a.queryLogs,
		forge.WithSummary("Query logs"),
		forge.WithDescription("Returns log entries for an instance."),
		forge.WithOperationID("queryLogs"),
		forge.WithResponseSchema(http.StatusOK, "Log entries", []telemetry.LogEntry{}),
		forge.WithErrorResponses(),
	)

	_ = g.GET("/instances/:instanceID/traces", a.queryTraces,
		forge.WithSummary("Query traces"),
		forge.WithDescription("Returns trace spans for an instance."),
		forge.WithOperationID("queryTraces"),
		forge.WithResponseSchema(http.StatusOK, "Trace spans", []telemetry.Trace{}),
		forge.WithErrorResponses(),
	)

	_ = g.GET("/instances/:instanceID/resources", a.getResources,
		forge.WithSummary("Get resources"),
		forge.WithDescription("Returns the current resource snapshot for an instance."),
		forge.WithOperationID("getResources"),
		forge.WithResponseSchema(http.StatusOK, "Resource snapshot", telemetry.ResourceSnapshot{}),
		forge.WithErrorResponses(),
	)

	_ = g.GET("/instances/:instanceID/dashboard", a.getDashboard,
		forge.WithSummary("Get dashboard"),
		forge.WithDescription("Returns the telemetry dashboard for an instance."),
		forge.WithOperationID("getDashboard"),
		forge.WithResponseSchema(http.StatusOK, "Dashboard data", telemetry.DashboardData{}),
		forge.WithErrorResponses(),
	)
}

// registerNetworkRoutes registers all network, domain, and route management routes.
func (a *API) registerNetworkRoutes(router forge.Router) {
	g := router.Group("/v1", forge.WithGroupTags("network"))

	_ = g.POST("/instances/:instanceID/domains", a.addDomain,
		forge.WithSummary("Add domain"),
		forge.WithDescription("Registers a custom domain for an instance."),
		forge.WithOperationID("addDomain"),
		forge.WithRequestSchema(AddDomainAPIRequest{}),
		forge.WithCreatedResponse(network.Domain{}),
		forge.WithErrorResponses(),
	)

	_ = g.GET("/instances/:instanceID/domains", a.listDomains,
		forge.WithSummary("List domains"),
		forge.WithDescription("Returns all domains for an instance."),
		forge.WithOperationID("listDomains"),
		forge.WithResponseSchema(http.StatusOK, "Domain list", []network.Domain{}),
		forge.WithErrorResponses(),
	)

	_ = g.POST("/domains/:domainID/verify", a.verifyDomain,
		forge.WithSummary("Verify domain"),
		forge.WithDescription("Confirms DNS ownership of a domain."),
		forge.WithOperationID("verifyDomain"),
		forge.WithResponseSchema(http.StatusOK, "Verified domain", network.Domain{}),
		forge.WithErrorResponses(),
	)

	_ = g.DELETE("/domains/:domainID", a.removeDomain,
		forge.WithSummary("Remove domain"),
		forge.WithDescription("Removes a custom domain."),
		forge.WithOperationID("removeDomain"),
		forge.WithNoContentResponse(),
		forge.WithErrorResponses(),
	)

	_ = g.POST("/instances/:instanceID/routes", a.addRoute,
		forge.WithSummary("Add route"),
		forge.WithDescription("Creates a traffic route to an instance."),
		forge.WithOperationID("addRoute"),
		forge.WithRequestSchema(AddRouteAPIRequest{}),
		forge.WithCreatedResponse(network.Route{}),
		forge.WithErrorResponses(),
	)

	_ = g.GET("/instances/:instanceID/routes", a.listRoutes,
		forge.WithSummary("List routes"),
		forge.WithDescription("Returns all routes for an instance."),
		forge.WithOperationID("listRoutes"),
		forge.WithResponseSchema(http.StatusOK, "Route list", []network.Route{}),
		forge.WithErrorResponses(),
	)

	_ = g.PATCH("/routes/:routeID", a.updateRoute,
		forge.WithSummary("Update route"),
		forge.WithDescription("Modifies an existing route."),
		forge.WithOperationID("updateRoute"),
		forge.WithRequestSchema(UpdateRouteAPIRequest{}),
		forge.WithResponseSchema(http.StatusOK, "Updated route", network.Route{}),
		forge.WithErrorResponses(),
	)

	_ = g.DELETE("/routes/:routeID", a.removeRoute,
		forge.WithSummary("Remove route"),
		forge.WithDescription("Removes a traffic route."),
		forge.WithOperationID("removeRoute"),
		forge.WithNoContentResponse(),
		forge.WithErrorResponses(),
	)

	_ = g.POST("/domains/:domainID/cert", a.provisionCert,
		forge.WithSummary("Provision certificate"),
		forge.WithDescription("Obtains or renews a TLS certificate for a domain."),
		forge.WithOperationID("provisionCert"),
		forge.WithCreatedResponse(network.Certificate{}),
		forge.WithErrorResponses(),
	)
}

// registerSecretsRoutes registers all secret management routes.
func (a *API) registerSecretsRoutes(router forge.Router) {
	g := router.Group("/v1", forge.WithGroupTags("secrets"))

	_ = g.POST("/instances/:instanceID/secrets", a.setSecret,
		forge.WithSummary("Set secret"),
		forge.WithDescription("Creates or updates a secret for an instance."),
		forge.WithOperationID("setSecret"),
		forge.WithRequestSchema(SetSecretAPIRequest{}),
		forge.WithCreatedResponse(secrets.Secret{}),
		forge.WithErrorResponses(),
	)

	_ = g.GET("/instances/:instanceID/secrets", a.listSecrets,
		forge.WithSummary("List secrets"),
		forge.WithDescription("Returns all secrets for an instance (metadata only)."),
		forge.WithOperationID("listSecrets"),
		forge.WithResponseSchema(http.StatusOK, "Secret list", []secrets.Secret{}),
		forge.WithErrorResponses(),
	)

	_ = g.DELETE("/instances/:instanceID/secrets/:key", a.deleteSecret,
		forge.WithSummary("Delete secret"),
		forge.WithDescription("Removes a secret from an instance."),
		forge.WithOperationID("deleteSecret"),
		forge.WithNoContentResponse(),
		forge.WithErrorResponses(),
	)
}

// registerAdminRoutes registers all admin, tenant, and audit routes.
func (a *API) registerAdminRoutes(router forge.Router) {
	g := router.Group("/v1/admin", forge.WithGroupTags("admin"))

	_ = g.GET("/stats", a.systemStats,
		forge.WithSummary("System stats"),
		forge.WithDescription("Returns aggregate system statistics."),
		forge.WithOperationID("systemStats"),
		forge.WithResponseSchema(http.StatusOK, "System statistics", admin.SystemStats{}),
		forge.WithErrorResponses(),
	)

	_ = g.GET("/providers", a.listProviders,
		forge.WithSummary("List providers"),
		forge.WithDescription("Returns all registered infrastructure providers."),
		forge.WithOperationID("listProviders"),
		forge.WithResponseSchema(http.StatusOK, "Provider list", []admin.ProviderStatus{}),
		forge.WithErrorResponses(),
	)

	_ = g.POST("/tenants", a.createTenant,
		forge.WithSummary("Create tenant"),
		forge.WithDescription("Creates a new tenant."),
		forge.WithOperationID("createTenant"),
		forge.WithRequestSchema(CreateTenantAPIRequest{}),
		forge.WithCreatedResponse(admin.Tenant{}),
		forge.WithErrorResponses(),
	)

	_ = g.GET("/tenants", a.listTenants,
		forge.WithSummary("List tenants"),
		forge.WithDescription("Returns a paginated list of tenants."),
		forge.WithOperationID("listTenants"),
		forge.WithRequestSchema(ListTenantsAPIRequest{}),
		forge.WithPaginatedResponse(admin.Tenant{}, http.StatusOK),
		forge.WithErrorResponses(),
	)

	_ = g.GET("/tenants/:tenantID", a.getTenant,
		forge.WithSummary("Get tenant"),
		forge.WithDescription("Returns details of a specific tenant."),
		forge.WithOperationID("getTenant"),
		forge.WithResponseSchema(http.StatusOK, "Tenant details", admin.Tenant{}),
		forge.WithErrorResponses(),
	)

	_ = g.PATCH("/tenants/:tenantID", a.updateTenant,
		forge.WithSummary("Update tenant"),
		forge.WithDescription("Updates mutable fields of a tenant."),
		forge.WithOperationID("updateTenant"),
		forge.WithRequestSchema(UpdateTenantAPIRequest{}),
		forge.WithResponseSchema(http.StatusOK, "Updated tenant", admin.Tenant{}),
		forge.WithErrorResponses(),
	)

	_ = g.POST("/tenants/:tenantID/suspend", a.suspendTenant,
		forge.WithSummary("Suspend tenant"),
		forge.WithDescription("Suspends a tenant with a reason."),
		forge.WithOperationID("suspendTenant"),
		forge.WithRequestSchema(SuspendTenantAPIRequest{}),
		forge.WithNoContentResponse(),
		forge.WithErrorResponses(),
	)

	_ = g.POST("/tenants/:tenantID/unsuspend", a.unsuspendTenant,
		forge.WithSummary("Unsuspend tenant"),
		forge.WithDescription("Resumes a suspended tenant."),
		forge.WithOperationID("unsuspendTenant"),
		forge.WithNoContentResponse(),
		forge.WithErrorResponses(),
	)

	_ = g.DELETE("/tenants/:tenantID", a.deleteTenant,
		forge.WithSummary("Delete tenant"),
		forge.WithDescription("Permanently deletes a tenant."),
		forge.WithOperationID("deleteTenant"),
		forge.WithNoContentResponse(),
		forge.WithErrorResponses(),
	)

	_ = g.GET("/tenants/:tenantID/quota", a.getQuota,
		forge.WithSummary("Get quota"),
		forge.WithDescription("Returns the quota and usage for a tenant."),
		forge.WithOperationID("getQuota"),
		forge.WithResponseSchema(http.StatusOK, "Quota usage", admin.QuotaUsage{}),
		forge.WithErrorResponses(),
	)

	_ = g.PUT("/tenants/:tenantID/quota", a.setQuota,
		forge.WithSummary("Set quota"),
		forge.WithDescription("Sets the resource quota for a tenant."),
		forge.WithOperationID("setQuota"),
		forge.WithRequestSchema(SetQuotaAPIRequest{}),
		forge.WithNoContentResponse(),
		forge.WithErrorResponses(),
	)

	_ = g.POST("/audit", a.queryAuditLog,
		forge.WithSummary("Query audit log"),
		forge.WithDescription("Searches the audit log with filters."),
		forge.WithOperationID("queryAuditLog"),
		forge.WithRequestSchema(QueryAuditLogAPIRequest{}),
		forge.WithResponseSchema(http.StatusOK, "Audit log results", admin.AuditResult{}),
		forge.WithErrorResponses(),
	)
}
