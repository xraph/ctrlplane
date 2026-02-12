package memory

import (
	"context"
	"fmt"
	"sort"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/admin"
)

func (s *Store) InsertTenant(_ context.Context, tenant *admin.Tenant) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(tenant.ID)
	if _, exists := s.tenants[key]; exists {
		return fmt.Errorf("%w: tenant %s", ctrlplane.ErrAlreadyExists, key)
	}

	for _, existing := range s.tenants {
		if existing.Slug == tenant.Slug {
			return fmt.Errorf("%w: slug %s", ctrlplane.ErrAlreadyExists, tenant.Slug)
		}
	}

	clone := *tenant
	s.tenants[key] = &clone

	return nil
}

func (s *Store) GetTenant(_ context.Context, tenantID string) (*admin.Tenant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, t := range s.tenants {
		if idStr(t.ID) == tenantID || t.ExternalID == tenantID {
			clone := *t

			return &clone, nil
		}
	}

	return nil, fmt.Errorf("%w: tenant %s", ctrlplane.ErrNotFound, tenantID)
}

func (s *Store) GetTenantBySlug(_ context.Context, slug string) (*admin.Tenant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, t := range s.tenants {
		if t.Slug == slug {
			clone := *t

			return &clone, nil
		}
	}

	return nil, fmt.Errorf("%w: slug %s", ctrlplane.ErrNotFound, slug)
}

func (s *Store) ListTenants(_ context.Context, opts admin.ListTenantsOptions) (*admin.TenantListResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []*admin.Tenant

	for _, t := range s.tenants {
		if opts.Status != "" && string(t.Status) != opts.Status {
			continue
		}

		clone := *t
		items = append(items, &clone)
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})

	total := len(items)

	limit := opts.Limit
	if limit <= 0 || limit > total {
		limit = total
	}

	items = items[:limit]

	return &admin.TenantListResult{
		Items: items,
		Total: total,
	}, nil
}

func (s *Store) UpdateTenant(_ context.Context, tenant *admin.Tenant) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(tenant.ID)
	if _, ok := s.tenants[key]; !ok {
		return fmt.Errorf("%w: tenant %s", ctrlplane.ErrNotFound, key)
	}

	tenant.UpdatedAt = now()
	clone := *tenant
	s.tenants[key] = &clone

	return nil
}

func (s *Store) DeleteTenant(_ context.Context, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for key, t := range s.tenants {
		if idStr(t.ID) == tenantID || t.ExternalID == tenantID {
			delete(s.tenants, key)

			return nil
		}
	}

	return fmt.Errorf("%w: tenant %s", ctrlplane.ErrNotFound, tenantID)
}

func (s *Store) CountTenants(_ context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.tenants), nil
}

func (s *Store) CountTenantsByStatus(_ context.Context, status admin.TenantStatus) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0

	for _, t := range s.tenants {
		if t.Status == status {
			count++
		}
	}

	return count, nil
}

func (s *Store) InsertAuditEntry(_ context.Context, entry *admin.AuditEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.auditEntries = append(s.auditEntries, *entry)

	return nil
}

func (s *Store) QueryAuditLog(_ context.Context, opts admin.AuditQuery) (*admin.AuditResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []admin.AuditEntry

	for _, e := range s.auditEntries {
		if opts.TenantID != "" && e.TenantID != opts.TenantID {
			continue
		}

		if opts.ActorID != "" && e.ActorID != opts.ActorID {
			continue
		}

		if opts.Resource != "" && e.Resource != opts.Resource {
			continue
		}

		if opts.Action != "" && e.Action != opts.Action {
			continue
		}

		if !opts.Since.IsZero() && e.CreatedAt.Before(opts.Since) {
			continue
		}

		if !opts.Until.IsZero() && e.CreatedAt.After(opts.Until) {
			continue
		}

		items = append(items, e)
	}

	total := len(items)

	limit := opts.Limit
	if limit <= 0 || limit > total {
		limit = total
	}

	items = items[:limit]

	return &admin.AuditResult{
		Items: items,
		Total: total,
	}, nil
}
