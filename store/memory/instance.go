package memory

import (
	"context"
	"fmt"
	"sort"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
)

func (s *Store) Insert(_ context.Context, inst *instance.Instance) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(inst.ID)
	if _, exists := s.instances[key]; exists {
		return fmt.Errorf("%w: instance %s", ctrlplane.ErrAlreadyExists, key)
	}

	for _, existing := range s.instances {
		if existing.TenantID == inst.TenantID && existing.Slug == inst.Slug {
			return fmt.Errorf("%w: slug %s", ctrlplane.ErrAlreadyExists, inst.Slug)
		}
	}

	clone := *inst
	s.instances[key] = &clone

	return nil
}

func (s *Store) GetByID(_ context.Context, tenantID string, instanceID id.ID) (*instance.Instance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	inst, ok := s.instances[idStr(instanceID)]
	if !ok || inst.TenantID != tenantID {
		return nil, fmt.Errorf("%w: instance %s", ctrlplane.ErrNotFound, instanceID)
	}

	clone := *inst

	return &clone, nil
}

func (s *Store) GetBySlug(_ context.Context, tenantID string, slug string) (*instance.Instance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, inst := range s.instances {
		if inst.TenantID == tenantID && inst.Slug == slug {
			clone := *inst

			return &clone, nil
		}
	}

	return nil, fmt.Errorf("%w: slug %s", ctrlplane.ErrNotFound, slug)
}

func (s *Store) List(_ context.Context, tenantID string, opts instance.ListOptions) (*instance.ListResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []*instance.Instance

	for _, inst := range s.instances {
		if inst.TenantID != tenantID {
			continue
		}

		if opts.State != "" && string(inst.State) != opts.State {
			continue
		}

		if opts.Provider != "" && inst.ProviderName != opts.Provider {
			continue
		}

		clone := *inst
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

	return &instance.ListResult{
		Items: items,
		Total: total,
	}, nil
}

func (s *Store) Update(_ context.Context, inst *instance.Instance) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(inst.ID)
	if _, ok := s.instances[key]; !ok {
		return fmt.Errorf("%w: instance %s", ctrlplane.ErrNotFound, key)
	}

	inst.UpdatedAt = now()
	clone := *inst
	s.instances[key] = &clone

	return nil
}

func (s *Store) Delete(_ context.Context, tenantID string, instanceID id.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(instanceID)

	inst, ok := s.instances[key]
	if !ok || inst.TenantID != tenantID {
		return fmt.Errorf("%w: instance %s", ctrlplane.ErrNotFound, key)
	}

	delete(s.instances, key)

	return nil
}

func (s *Store) CountByTenant(_ context.Context, tenantID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0

	for _, inst := range s.instances {
		if inst.TenantID == tenantID {
			count++
		}
	}

	return count, nil
}
