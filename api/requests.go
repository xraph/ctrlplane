package api

import (
	"time"

	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/provider"
)

// ---------------------------------------------------------------------------
// Instance requests
// ---------------------------------------------------------------------------

// CreateInstanceRequest binds the body for POST /v1/instances.
type CreateInstanceRequest struct {
	instance.CreateRequest
}

// ListInstancesRequest binds query parameters for GET /v1/instances.
type ListInstancesRequest struct {
	State    string `description:"Filter by instance state" query:"state"`
	Label    string `description:"Filter by label"          query:"label"`
	Provider string `description:"Filter by provider"       query:"provider"`
	Cursor   string `description:"Pagination cursor"        query:"cursor"`
	Limit    int    `description:"Page size (default 20)"   query:"limit"`
}

// GetInstanceRequest binds the path for GET /v1/instances/:instanceID.
type GetInstanceRequest struct {
	InstanceID id.ID `description:"Instance identifier" path:"instanceID"`
}

// UpdateInstanceRequest binds path + body for PATCH /v1/instances/:instanceID.
type UpdateInstanceRequest struct {
	instance.UpdateRequest

	InstanceID id.ID `description:"Instance identifier" path:"instanceID"`
}

// DeleteInstanceRequest binds the path for DELETE /v1/instances/:instanceID.
type DeleteInstanceRequest struct {
	InstanceID id.ID `description:"Instance identifier" path:"instanceID"`
}

// InstanceActionRequest binds the path for action endpoints (start, stop, restart, unsuspend).
type InstanceActionRequest struct {
	InstanceID id.ID `description:"Instance identifier" path:"instanceID"`
}

// ScaleInstanceRequest binds path + body for POST /v1/instances/:instanceID/scale.
type ScaleInstanceRequest struct {
	instance.ScaleRequest

	InstanceID id.ID `description:"Instance identifier" path:"instanceID"`
}

// SuspendInstanceRequest binds path + body for POST /v1/instances/:instanceID/suspend.
type SuspendInstanceRequest struct {
	InstanceID id.ID  `description:"Instance identifier" path:"instanceID"`
	Reason     string `description:"Suspension reason"   json:"reason"`
}

// ---------------------------------------------------------------------------
// Deploy requests
// ---------------------------------------------------------------------------

// DeployAPIRequest binds path + body for POST /v1/instances/:instanceID/deploy.
// Body fields are replicated because deploy.DeployRequest has a conflicting
// InstanceID json tag.
type DeployAPIRequest struct {
	InstanceID id.ID             `description:"Instance identifier"   path:"instanceID"`
	Image      string            `description:"Container image"       json:"image"`
	Env        map[string]string `description:"Environment overrides" json:"env,omitempty"`
	Strategy   string            `description:"Deploy strategy"       json:"strategy,omitempty"`
	Notes      string            `description:"Deploy notes"          json:"notes,omitempty"`
	CommitSHA  string            `description:"Git commit SHA"        json:"commit_sha,omitempty"`
}

// ListDeploymentsRequest binds path + query for GET /v1/instances/:instanceID/deployments.
type ListDeploymentsRequest struct {
	InstanceID id.ID  `description:"Instance identifier"    path:"instanceID"`
	Cursor     string `description:"Pagination cursor"      query:"cursor"`
	Limit      int    `description:"Page size (default 20)" query:"limit"`
}

// GetDeploymentRequest binds the path for GET /v1/deployments/:deploymentID.
type GetDeploymentRequest struct {
	DeploymentID id.ID `description:"Deployment identifier" path:"deploymentID"`
}

// CancelDeploymentRequest binds the path for POST /v1/deployments/:deploymentID/cancel.
type CancelDeploymentRequest struct {
	DeploymentID id.ID `description:"Deployment identifier" path:"deploymentID"`
}

// RollbackRequest binds path + body for POST /v1/instances/:instanceID/rollback.
type RollbackRequest struct {
	InstanceID id.ID `description:"Instance identifier"    path:"instanceID"`
	ReleaseID  id.ID `description:"Release to rollback to" json:"release_id"`
}

// ListReleasesRequest binds path + query for GET /v1/instances/:instanceID/releases.
type ListReleasesRequest struct {
	InstanceID id.ID  `description:"Instance identifier"    path:"instanceID"`
	Cursor     string `description:"Pagination cursor"      query:"cursor"`
	Limit      int    `description:"Page size (default 20)" query:"limit"`
}

// GetReleaseRequest binds the path for GET /v1/releases/:releaseID.
type GetReleaseRequest struct {
	ReleaseID id.ID `description:"Release identifier" path:"releaseID"`
}

// ---------------------------------------------------------------------------
// Health requests
// ---------------------------------------------------------------------------

// ConfigureHealthCheckAPIRequest binds path + body for POST /v1/instances/:instanceID/health/checks.
// Body fields are replicated because health.ConfigureRequest has a conflicting
// InstanceID json tag.
type ConfigureHealthCheckAPIRequest struct {
	InstanceID id.ID         `description:"Instance identifier"                   path:"instanceID"`
	Name       string        `description:"Health check name"                     json:"name"`
	Type       string        `description:"Check type (http, tcp, grpc, command)" json:"type"`
	Target     string        `description:"Check target (URL, host:port, etc.)"   json:"target"`
	Interval   time.Duration `description:"Check interval"                        json:"interval"`
	Timeout    time.Duration `description:"Check timeout"                         json:"timeout"`
	Retries    int           `description:"Number of retries"                     json:"retries"`
}

// GetInstanceHealthRequest binds the path for GET /v1/instances/:instanceID/health.
type GetInstanceHealthRequest struct {
	InstanceID id.ID `description:"Instance identifier" path:"instanceID"`
}

// ListHealthChecksRequest binds the path for GET /v1/instances/:instanceID/health/checks.
type ListHealthChecksRequest struct {
	InstanceID id.ID `description:"Instance identifier" path:"instanceID"`
}

// RunHealthCheckRequest binds the path for POST /v1/health/checks/:checkID/run.
type RunHealthCheckRequest struct {
	CheckID id.ID `description:"Health check identifier" path:"checkID"`
}

// RemoveHealthCheckRequest binds the path for DELETE /v1/health/checks/:checkID.
type RemoveHealthCheckRequest struct {
	CheckID id.ID `description:"Health check identifier" path:"checkID"`
}

// ---------------------------------------------------------------------------
// Telemetry requests
// ---------------------------------------------------------------------------

// InstanceTelemetryRequest binds the path for GET telemetry endpoints.
type InstanceTelemetryRequest struct {
	InstanceID id.ID `description:"Instance identifier" path:"instanceID"`
}

// ---------------------------------------------------------------------------
// Network requests
// ---------------------------------------------------------------------------

// AddDomainAPIRequest binds path + body for POST /v1/instances/:instanceID/domains.
// Body fields are replicated because network.AddDomainRequest has a conflicting
// InstanceID json tag.
type AddDomainAPIRequest struct {
	InstanceID id.ID  `description:"Instance identifier"         path:"instanceID"`
	Hostname   string `description:"Fully-qualified domain name" json:"hostname"`
	TLSEnabled bool   `description:"Enable TLS for this domain"  json:"tls_enabled"`
}

// ListDomainsRequest binds the path for GET /v1/instances/:instanceID/domains.
type ListDomainsRequest struct {
	InstanceID id.ID `description:"Instance identifier" path:"instanceID"`
}

// VerifyDomainRequest binds the path for POST /v1/domains/:domainID/verify.
type VerifyDomainRequest struct {
	DomainID id.ID `description:"Domain identifier" path:"domainID"`
}

// RemoveDomainRequest binds the path for DELETE /v1/domains/:domainID.
type RemoveDomainRequest struct {
	DomainID id.ID `description:"Domain identifier" path:"domainID"`
}

// AddRouteAPIRequest binds path + body for POST /v1/instances/:instanceID/routes.
// Body fields are replicated because network.AddRouteRequest has a conflicting
// InstanceID json tag.
type AddRouteAPIRequest struct {
	InstanceID id.ID  `description:"Instance identifier"    path:"instanceID"`
	Path       string `description:"Route path pattern"     json:"path"`
	Port       int    `description:"Target port"            json:"port"`
	Protocol   string `description:"Protocol (http, grpc)"  json:"protocol"`
	Weight     int    `description:"Traffic weight (0-100)" json:"weight"`
}

// ListRoutesRequest binds the path for GET /v1/instances/:instanceID/routes.
type ListRoutesRequest struct {
	InstanceID id.ID `description:"Instance identifier" path:"instanceID"`
}

// UpdateRouteAPIRequest binds path + body for PATCH /v1/routes/:routeID.
type UpdateRouteAPIRequest struct {
	RouteID     id.ID   `description:"Route identifier"   path:"routeID"`
	Path        *string `description:"Route path pattern" json:"path,omitempty"`
	Weight      *int    `description:"Traffic weight"     json:"weight,omitempty"`
	StripPrefix *bool   `description:"Strip path prefix"  json:"strip_prefix,omitempty"`
}

// RemoveRouteRequest binds the path for DELETE /v1/routes/:routeID.
type RemoveRouteRequest struct {
	RouteID id.ID `description:"Route identifier" path:"routeID"`
}

// ProvisionCertRequest binds the path for POST /v1/domains/:domainID/cert.
type ProvisionCertRequest struct {
	DomainID id.ID `description:"Domain identifier" path:"domainID"`
}

// ---------------------------------------------------------------------------
// Secrets requests
// ---------------------------------------------------------------------------

// SetSecretAPIRequest binds path + body for POST /v1/instances/:instanceID/secrets.
// Body fields are replicated because secrets.SetRequest has a conflicting
// InstanceID json tag.
type SetSecretAPIRequest struct {
	InstanceID id.ID  `description:"Instance identifier"          path:"instanceID"`
	Key        string `description:"Secret key"                   json:"key"`
	Value      string `description:"Secret value"                 json:"value"`
	Type       string `description:"Secret type (env, file, tls)" json:"type"`
}

// ListSecretsRequest binds the path for GET /v1/instances/:instanceID/secrets.
type ListSecretsRequest struct {
	InstanceID id.ID `description:"Instance identifier" path:"instanceID"`
}

// DeleteSecretRequest binds the path for DELETE /v1/instances/:instanceID/secrets/:key.
type DeleteSecretRequest struct {
	InstanceID id.ID  `description:"Instance identifier" path:"instanceID"`
	Key        string `description:"Secret key"          path:"key"`
}

// ---------------------------------------------------------------------------
// Admin requests
// ---------------------------------------------------------------------------

// EmptyRequest is used for endpoints with no parameters.
type EmptyRequest struct{}

// CreateTenantAPIRequest binds the body for POST /v1/admin/tenants.
type CreateTenantAPIRequest struct {
	admin.CreateTenantRequest
}

// ListTenantsAPIRequest binds query parameters for GET /v1/admin/tenants.
type ListTenantsAPIRequest struct {
	Status string `description:"Filter by tenant status" query:"status"`
	Cursor string `description:"Pagination cursor"       query:"cursor"`
	Limit  int    `description:"Page size (default 50)"  query:"limit"`
}

// TenantPathRequest binds the path for tenant endpoints.
type TenantPathRequest struct {
	TenantID string `description:"Tenant identifier" path:"tenantID"`
}

// UpdateTenantAPIRequest binds path + body for PATCH /v1/admin/tenants/:tenantID.
type UpdateTenantAPIRequest struct {
	admin.UpdateTenantRequest

	TenantID string `description:"Tenant identifier" path:"tenantID"`
}

// SuspendTenantAPIRequest binds path + body for POST /v1/admin/tenants/:tenantID/suspend.
type SuspendTenantAPIRequest struct {
	TenantID string `description:"Tenant identifier" path:"tenantID"`
	Reason   string `description:"Suspension reason" json:"reason"`
}

// SetQuotaAPIRequest binds path + body for PUT /v1/admin/tenants/:tenantID/quota.
type SetQuotaAPIRequest struct {
	admin.Quota

	TenantID string `description:"Tenant identifier" path:"tenantID"`
}

// QueryAuditLogAPIRequest binds the body for POST /v1/admin/audit.
type QueryAuditLogAPIRequest struct {
	admin.AuditQuery
}

// ---------------------------------------------------------------------------
// Compile-time assertions to keep provider import used.
// ---------------------------------------------------------------------------

var _ = provider.ResourceSpec{}
