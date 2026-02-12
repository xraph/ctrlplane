package admin

import (
	"context"
	"fmt"
	"strings"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/network"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/secrets"
)

// service implements the Service interface.
type service struct {
	store     Store
	instStore instance.Store
	netStore  network.Store
	secStore  secrets.Store
	providers *provider.Registry
	events    event.Bus
	auth      auth.Provider
}

// NewService creates a new admin service.
func NewService(
	store Store,
	instStore instance.Store,
	netStore network.Store,
	secStore secrets.Store,
	providers *provider.Registry,
	events event.Bus,
	auth auth.Provider,
) Service {
	return &service{
		store:     store,
		instStore: instStore,
		netStore:  netStore,
		secStore:  secStore,
		providers: providers,
		events:    events,
		auth:      auth,
	}
}

// CreateTenant creates a new tenant.
func (s *service) CreateTenant(ctx context.Context, req CreateTenantRequest) (*Tenant, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("create tenant: %w", err)
	}

	if !claims.IsSystemAdmin() {
		return nil, fmt.Errorf("create tenant: %w", ctrlplane.ErrForbidden)
	}

	plan := req.Plan
	if plan == "" {
		plan = "free"
	}

	quota := defaultQuota()

	if req.Quota != nil {
		quota = *req.Quota
	}

	tenant := &Tenant{
		Entity:     ctrlplane.NewEntity(id.PrefixTenant),
		ExternalID: req.ExternalID,
		Name:       req.Name,
		Slug:       slugify(req.Name),
		Status:     TenantActive,
		Plan:       plan,
		Quota:      quota,
		Metadata:   req.Metadata,
	}

	if err := s.store.InsertTenant(ctx, tenant); err != nil {
		return nil, fmt.Errorf("create tenant: insert: %w", err)
	}

	// Fire-and-forget event.
	_ = s.events.Publish(ctx, event.NewEvent(event.TenantCreated, tenant.ID.String()).
		WithActor(claims.SubjectID).
		WithPayload(map[string]any{
			"tenant_name": tenant.Name,
			"plan":        tenant.Plan,
		}))

	return tenant, nil
}

// GetTenant returns a tenant by ID.
func (s *service) GetTenant(ctx context.Context, tenantID string) (*Tenant, error) {
	_, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("get tenant: %w", err)
	}

	tenant, err := s.store.GetTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("get tenant: %w", err)
	}

	return tenant, nil
}

// ListTenants returns tenants with optional filtering.
func (s *service) ListTenants(ctx context.Context, opts ListTenantsOptions) (*TenantListResult, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}

	if !claims.IsSystemAdmin() {
		return nil, fmt.Errorf("list tenants: %w", ctrlplane.ErrForbidden)
	}

	result, err := s.store.ListTenants(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}

	return result, nil
}

// UpdateTenant modifies a tenant.
func (s *service) UpdateTenant(ctx context.Context, tenantID string, req UpdateTenantRequest) (*Tenant, error) {
	_, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("update tenant: %w", err)
	}

	tenant, err := s.store.GetTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("update tenant: get: %w", err)
	}

	if req.Name != nil {
		tenant.Name = *req.Name
		tenant.Slug = slugify(*req.Name)
	}

	if req.Plan != nil {
		tenant.Plan = *req.Plan
	}

	if req.Metadata != nil {
		tenant.Metadata = req.Metadata
	}

	tenant.UpdatedAt = time.Now().UTC()

	if err := s.store.UpdateTenant(ctx, tenant); err != nil {
		return nil, fmt.Errorf("update tenant: %w", err)
	}

	return tenant, nil
}

// SuspendTenant suspends a tenant with a reason.
func (s *service) SuspendTenant(ctx context.Context, tenantID string, reason string) error {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return fmt.Errorf("suspend tenant: %w", err)
	}

	if !claims.IsSystemAdmin() {
		return fmt.Errorf("suspend tenant: %w", ctrlplane.ErrForbidden)
	}

	tenant, err := s.store.GetTenant(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("suspend tenant: get: %w", err)
	}

	now := time.Now().UTC()
	tenant.Status = TenantSuspended
	tenant.SuspendedAt = &now
	tenant.UpdatedAt = now

	if err := s.store.UpdateTenant(ctx, tenant); err != nil {
		return fmt.Errorf("suspend tenant: update: %w", err)
	}

	// Fire-and-forget event.
	_ = s.events.Publish(ctx, event.NewEvent(event.TenantSuspended, tenantID).
		WithActor(claims.SubjectID).
		WithPayload(map[string]any{
			"reason": reason,
		}))

	return nil
}

// UnsuspendTenant restores a suspended tenant.
func (s *service) UnsuspendTenant(ctx context.Context, tenantID string) error {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return fmt.Errorf("unsuspend tenant: %w", err)
	}

	if !claims.IsSystemAdmin() {
		return fmt.Errorf("unsuspend tenant: %w", ctrlplane.ErrForbidden)
	}

	tenant, err := s.store.GetTenant(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("unsuspend tenant: get: %w", err)
	}

	tenant.Status = TenantActive
	tenant.SuspendedAt = nil
	tenant.UpdatedAt = time.Now().UTC()

	if err := s.store.UpdateTenant(ctx, tenant); err != nil {
		return fmt.Errorf("unsuspend tenant: update: %w", err)
	}

	return nil
}

// DeleteTenant removes a tenant.
func (s *service) DeleteTenant(ctx context.Context, tenantID string) error {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return fmt.Errorf("delete tenant: %w", err)
	}

	if !claims.IsSystemAdmin() {
		return fmt.Errorf("delete tenant: %w", ctrlplane.ErrForbidden)
	}

	if err := s.store.DeleteTenant(ctx, tenantID); err != nil {
		return fmt.Errorf("delete tenant: %w", err)
	}

	// Fire-and-forget event.
	_ = s.events.Publish(ctx, event.NewEvent(event.TenantDeleted, tenantID).
		WithActor(claims.SubjectID))

	return nil
}

// GetQuota returns quota usage for a tenant.
func (s *service) GetQuota(ctx context.Context, tenantID string) (*QuotaUsage, error) {
	_, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("get quota: %w", err)
	}

	tenant, err := s.store.GetTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("get quota: get tenant: %w", err)
	}

	instanceCount, err := s.instStore.CountByTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("get quota: count instances: %w", err)
	}

	domainCount, err := s.netStore.CountDomainsByTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("get quota: count domains: %w", err)
	}

	secretCount, err := s.secStore.CountSecretsByTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("get quota: count secrets: %w", err)
	}

	return &QuotaUsage{
		Tenant: tenant,
		Quota:  tenant.Quota,
		Used: QuotaSnapshot{
			Instances: instanceCount,
			Domains:   domainCount,
			Secrets:   secretCount,
		},
	}, nil
}

// SetQuota updates quota limits for a tenant.
func (s *service) SetQuota(ctx context.Context, tenantID string, quota Quota) error {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return fmt.Errorf("set quota: %w", err)
	}

	if !claims.IsSystemAdmin() {
		return fmt.Errorf("set quota: %w", ctrlplane.ErrForbidden)
	}

	tenant, err := s.store.GetTenant(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("set quota: get tenant: %w", err)
	}

	tenant.Quota = quota
	tenant.UpdatedAt = time.Now().UTC()

	if err := s.store.UpdateTenant(ctx, tenant); err != nil {
		return fmt.Errorf("set quota: update: %w", err)
	}

	return nil
}

// SystemStats returns system-wide statistics.
func (s *service) SystemStats(ctx context.Context) (*SystemStats, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("system stats: %w", err)
	}

	if !claims.IsSystemAdmin() {
		return nil, fmt.Errorf("system stats: %w", ctrlplane.ErrForbidden)
	}

	totalTenants, err := s.store.CountTenants(ctx)
	if err != nil {
		return nil, fmt.Errorf("system stats: count tenants: %w", err)
	}

	activeTenants, err := s.store.CountTenantsByStatus(ctx, TenantActive)
	if err != nil {
		return nil, fmt.Errorf("system stats: count active tenants: %w", err)
	}

	providerNames := s.providers.List()

	return &SystemStats{
		TotalTenants:   totalTenants,
		ActiveTenants:  activeTenants,
		TotalProviders: len(providerNames),
	}, nil
}

// ListProviders returns status of all registered providers.
func (s *service) ListProviders(ctx context.Context) ([]ProviderStatus, error) {
	_, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}

	all := s.providers.All()
	statuses := make([]ProviderStatus, 0, len(all))

	for _, p := range all {
		info := p.Info()
		caps := p.Capabilities()

		capStrings := make([]string, 0, len(caps))

		for _, c := range caps {
			capStrings = append(capStrings, string(c))
		}

		statuses = append(statuses, ProviderStatus{
			Name:         info.Name,
			Region:       info.Region,
			Healthy:      true,
			Capabilities: capStrings,
		})
	}

	return statuses, nil
}

// QueryAuditLog queries the audit log.
func (s *service) QueryAuditLog(ctx context.Context, opts AuditQuery) (*AuditResult, error) {
	_, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("query audit log: %w", err)
	}

	result, err := s.store.QueryAuditLog(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("query audit log: %w", err)
	}

	return result, nil
}

// slugify converts a name to a URL-friendly slug.
func slugify(name string) string {
	return strings.ReplaceAll(strings.ToLower(name), " ", "-")
}

// defaultQuota returns the default quota for new tenants.
func defaultQuota() Quota {
	return Quota{
		MaxInstances: 5,
		MaxCPUMillis: 4000,
		MaxMemoryMB:  8192,
		MaxDiskMB:    20480,
		MaxDomains:   10,
		MaxSecrets:   50,
	}
}
