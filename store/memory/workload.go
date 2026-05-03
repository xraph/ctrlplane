package memory

import (
	"context"
	"fmt"
	"sort"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/workload"
)

// InsertWorkload persists a Workload. Enforces (tenant_id, slug)
// uniqueness to match the convention used by datacenter/instance
// stores — callers can safely retry Create after a network error
// knowing the second attempt fails fast on slug conflict.
func (s *Store) InsertWorkload(_ context.Context, w *workload.Workload) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(w.ID)
	if _, exists := s.workloads[key]; exists {
		return fmt.Errorf("%w: workload %s", ctrlplane.ErrAlreadyExists, key)
	}

	for _, existing := range s.workloads {
		if existing.TenantID == w.TenantID && existing.Slug == w.Slug {
			return fmt.Errorf("%w: workload slug %s in tenant %q",
				ctrlplane.ErrAlreadyExists, w.Slug, w.TenantID)
		}
	}

	clone := *w
	s.workloads[key] = &clone

	return nil
}

// GetByID returns the workload with the given ID when it belongs
// to the supplied tenant. Empty tenantID is the cross-tenant
// admin convention (matches instance/deploy stores).
func (s *Store) GetWorkloadByID(_ context.Context, tenantID string, workloadID id.ID) (*workload.Workload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	w, ok := s.workloads[idStr(workloadID)]
	if !ok {
		return nil, fmt.Errorf("%w: workload %s", ctrlplane.ErrNotFound, workloadID)
	}

	if tenantID != "" && w.TenantID != tenantID {
		return nil, fmt.Errorf("%w: workload %s", ctrlplane.ErrNotFound, workloadID)
	}

	clone := *w

	return &clone, nil
}

func (s *Store) GetWorkloadBySlug(_ context.Context, tenantID, slug string) (*workload.Workload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, w := range s.workloads {
		if (tenantID == "" || w.TenantID == tenantID) && w.Slug == slug {
			clone := *w

			return &clone, nil
		}
	}

	return nil, fmt.Errorf("%w: workload slug %s", ctrlplane.ErrNotFound, slug)
}

func (s *Store) ListWorkloads(_ context.Context, tenantID string, opts workload.ListOptions) (*workload.ListResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []*workload.Workload

	for _, w := range s.workloads {
		if tenantID != "" && w.TenantID != tenantID {
			continue
		}

		if opts.State != "" && w.State != opts.State {
			continue
		}

		if opts.ProviderName != "" && w.ProviderName != opts.ProviderName {
			continue
		}

		if opts.Region != "" && w.Region != opts.Region {
			continue
		}

		clone := *w
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

	return &workload.ListResult{Items: items, Total: total}, nil
}

func (s *Store) UpdateWorkload(_ context.Context, w *workload.Workload) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(w.ID)
	if _, ok := s.workloads[key]; !ok {
		return fmt.Errorf("%w: workload %s", ctrlplane.ErrNotFound, key)
	}

	w.UpdatedAt = now()
	clone := *w
	s.workloads[key] = &clone

	return nil
}

func (s *Store) DeleteWorkload(_ context.Context, tenantID string, workloadID id.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(workloadID)

	w, ok := s.workloads[key]
	if !ok {
		return fmt.Errorf("%w: workload %s", ctrlplane.ErrNotFound, workloadID)
	}

	if tenantID != "" && w.TenantID != tenantID {
		return fmt.Errorf("%w: workload %s", ctrlplane.ErrNotFound, workloadID)
	}

	delete(s.workloads, key)

	return nil
}
