package admin

import (
	"context"
	"time"
)

// Service is the admin management interface for system and tenant operations.
type Service interface {
	// CreateTenant creates a new tenant.
	CreateTenant(ctx context.Context, req CreateTenantRequest) (*Tenant, error)

	// GetTenant returns a tenant by ID.
	GetTenant(ctx context.Context, tenantID string) (*Tenant, error)

	// ListTenants returns tenants with optional filtering.
	ListTenants(ctx context.Context, opts ListTenantsOptions) (*TenantListResult, error)

	// UpdateTenant modifies a tenant.
	UpdateTenant(ctx context.Context, tenantID string, req UpdateTenantRequest) (*Tenant, error)

	// SuspendTenant suspends a tenant with a reason.
	SuspendTenant(ctx context.Context, tenantID string, reason string) error

	// UnsuspendTenant restores a suspended tenant.
	UnsuspendTenant(ctx context.Context, tenantID string) error

	// DeleteTenant removes a tenant.
	DeleteTenant(ctx context.Context, tenantID string) error

	// GetQuota returns quota usage for a tenant.
	GetQuota(ctx context.Context, tenantID string) (*QuotaUsage, error)

	// SetQuota updates quota limits for a tenant.
	SetQuota(ctx context.Context, tenantID string, quota Quota) error

	// SystemStats returns system-wide statistics.
	SystemStats(ctx context.Context) (*SystemStats, error)

	// ListProviders returns status of all registered providers.
	ListProviders(ctx context.Context) ([]ProviderStatus, error)

	// QueryAuditLog queries the audit log.
	QueryAuditLog(ctx context.Context, opts AuditQuery) (*AuditResult, error)
}

// CreateTenantRequest holds the parameters for creating a tenant.
type CreateTenantRequest struct {
	Name       string            `json:"name"                  validate:"required"`
	Plan       string            `default:"free"               json:"plan"`
	ExternalID string            `json:"external_id,omitempty"`
	Quota      *Quota            `json:"quota,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// UpdateTenantRequest holds the parameters for updating a tenant.
type UpdateTenantRequest struct {
	Name     *string           `json:"name,omitempty"`
	Plan     *string           `json:"plan,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// ListTenantsOptions configures tenant listing.
type ListTenantsOptions struct {
	Status string `json:"status,omitempty"`
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// TenantListResult holds a page of tenants.
type TenantListResult struct {
	Items      []*Tenant `json:"items"`
	NextCursor string    `json:"next_cursor,omitempty"`
	Total      int       `json:"total"`
}

// SystemStats holds system-wide statistics.
type SystemStats struct {
	TotalTenants     int `json:"total_tenants"`
	ActiveTenants    int `json:"active_tenants"`
	TotalInstances   int `json:"total_instances"`
	RunningInstances int `json:"running_instances"`
	TotalProviders   int `json:"total_providers"`
	HealthyProviders int `json:"healthy_providers"`
}

// ProviderStatus describes the operational status of a provider.
type ProviderStatus struct {
	Name         string   `json:"name"`
	Region       string   `json:"region"`
	Healthy      bool     `json:"healthy"`
	Instances    int      `json:"instances"`
	Capabilities []string `json:"capabilities"`
}

// AuditQuery configures an audit log query.
type AuditQuery struct {
	TenantID string    `json:"tenant_id,omitempty"`
	ActorID  string    `json:"actor_id,omitempty"`
	Resource string    `json:"resource,omitempty"`
	Action   string    `json:"action,omitempty"`
	Since    time.Time `json:"since"`
	Until    time.Time `json:"until"`
	Limit    int       `json:"limit"`
}

// AuditResult holds a page of audit entries.
type AuditResult struct {
	Items      []AuditEntry `json:"items"`
	NextCursor string       `json:"next_cursor,omitempty"`
	Total      int          `json:"total"`
}
