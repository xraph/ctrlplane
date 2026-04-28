package memory

import (
	"context"
	"fmt"
	"sort"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/datacenter"
	"github.com/xraph/ctrlplane/id"
)

// InsertDatacenter persists a new datacenter.
func (s *Store) InsertDatacenter(_ context.Context, dc *datacenter.Datacenter) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(dc.ID)
	if _, exists := s.datacenters[key]; exists {
		return fmt.Errorf("%w: datacenter %s", ctrlplane.ErrAlreadyExists, key)
	}

	for _, existing := range s.datacenters {
		if existing.TenantID == dc.TenantID && existing.Slug == dc.Slug {
			return fmt.Errorf("%w: slug %s", ctrlplane.ErrAlreadyExists, dc.Slug)
		}
	}

	clone := *dc
	s.datacenters[key] = &clone

	return nil
}

// GetDatacenterByID retrieves a datacenter by its ID. The result is
// returned when it belongs to tenantID OR when it is a platform-shared
// datacenter (TenantID == ""). The shared-tenant convention lets
// operator-managed regions (e.g. baked-in local-dev defaults, hosted
// multi-region SaaS) be visible to every customer without seeding a
// per-tenant copy.
func (s *Store) GetDatacenterByID(_ context.Context, tenantID string, datacenterID id.ID) (*datacenter.Datacenter, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dc, ok := s.datacenters[idStr(datacenterID)]
	if !ok || (dc.TenantID != tenantID && dc.TenantID != "") {
		return nil, fmt.Errorf("%w: datacenter %s", ctrlplane.ErrNotFound, datacenterID)
	}

	clone := *dc

	return &clone, nil
}

// GetDatacenterBySlug retrieves a datacenter by its slug, preferring
// the caller's tenant when both a tenant-scoped and a platform-shared
// DC share the same slug.
func (s *Store) GetDatacenterBySlug(_ context.Context, tenantID string, slug string) (*datacenter.Datacenter, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var shared *datacenter.Datacenter
	for _, dc := range s.datacenters {
		if dc.Slug != slug {
			continue
		}
		if dc.TenantID == tenantID {
			clone := *dc
			return &clone, nil
		}
		if dc.TenantID == "" && shared == nil {
			shared = dc
		}
	}
	if shared != nil {
		clone := *shared
		return &clone, nil
	}

	return nil, fmt.Errorf("%w: datacenter slug %s", ctrlplane.ErrNotFound, slug)
}

// ListDatacenters returns a filtered, paginated list of datacenters
// visible to tenantID. The result includes both tenant-owned DCs and
// platform-shared DCs (TenantID == "") so operator-managed regions
// surface in every tenant's catalog.
func (s *Store) ListDatacenters(_ context.Context, tenantID string, opts datacenter.ListOptions) (*datacenter.ListResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []*datacenter.Datacenter

	for _, dc := range s.datacenters {
		if dc.TenantID != tenantID && dc.TenantID != "" {
			continue
		}

		if opts.Status != "" && string(dc.Status) != opts.Status {
			continue
		}

		if opts.Provider != "" && dc.ProviderName != opts.Provider {
			continue
		}

		if opts.Region != "" && dc.Region != opts.Region {
			continue
		}

		if opts.Label != "" {
			if _, ok := dc.Labels[opts.Label]; !ok {
				continue
			}
		}

		clone := *dc
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

	return &datacenter.ListResult{
		Items: items,
		Total: total,
	}, nil
}

// UpdateDatacenter persists changes to an existing datacenter.
func (s *Store) UpdateDatacenter(_ context.Context, dc *datacenter.Datacenter) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(dc.ID)
	if _, ok := s.datacenters[key]; !ok {
		return fmt.Errorf("%w: datacenter %s", ctrlplane.ErrNotFound, key)
	}

	dc.UpdatedAt = now()
	clone := *dc
	s.datacenters[key] = &clone

	return nil
}

// DeleteDatacenter removes a datacenter from the store.
func (s *Store) DeleteDatacenter(_ context.Context, tenantID string, datacenterID id.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(datacenterID)

	dc, ok := s.datacenters[key]
	if !ok || dc.TenantID != tenantID {
		return fmt.Errorf("%w: datacenter %s", ctrlplane.ErrNotFound, key)
	}

	delete(s.datacenters, key)

	return nil
}

// CountDatacentersByTenant returns the total number of datacenters for a tenant.
func (s *Store) CountDatacentersByTenant(_ context.Context, tenantID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0

	for _, dc := range s.datacenters {
		if dc.TenantID == tenantID {
			count++
		}
	}

	return count, nil
}

// CountInstancesByDatacenter returns the number of instances linked to a datacenter.
func (s *Store) CountInstancesByDatacenter(_ context.Context, tenantID string, datacenterID id.ID) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dcKey := idStr(datacenterID)
	count := 0

	for _, inst := range s.instances {
		if inst.TenantID == tenantID && idStr(inst.DatacenterID) == dcKey {
			count++
		}
	}

	return count, nil
}
