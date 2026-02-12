package admin

import "context"

// Store is the persistence interface for admin entities.
type Store interface {
	// InsertTenant persists a new tenant.
	InsertTenant(ctx context.Context, tenant *Tenant) error

	// GetTenant retrieves a tenant by ID.
	GetTenant(ctx context.Context, tenantID string) (*Tenant, error)

	// GetTenantBySlug retrieves a tenant by slug.
	GetTenantBySlug(ctx context.Context, slug string) (*Tenant, error)

	// ListTenants returns tenants with optional filtering.
	ListTenants(ctx context.Context, opts ListTenantsOptions) (*TenantListResult, error)

	// UpdateTenant persists changes to a tenant.
	UpdateTenant(ctx context.Context, tenant *Tenant) error

	// DeleteTenant removes a tenant.
	DeleteTenant(ctx context.Context, tenantID string) error

	// CountTenants returns the total number of tenants.
	CountTenants(ctx context.Context) (int, error)

	// CountTenantsByStatus returns the number of tenants in a given status.
	CountTenantsByStatus(ctx context.Context, status TenantStatus) (int, error)

	// InsertAuditEntry persists an audit log entry.
	InsertAuditEntry(ctx context.Context, entry *AuditEntry) error

	// QueryAuditLog returns audit entries matching the query.
	QueryAuditLog(ctx context.Context, opts AuditQuery) (*AuditResult, error)
}
