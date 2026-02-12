package memory

import (
	"context"
	"fmt"
	"sort"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/id"
)

func (s *Store) InsertDeployment(_ context.Context, d *deploy.Deployment) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(d.ID)
	if _, exists := s.deployments[key]; exists {
		return fmt.Errorf("%w: deployment %s", ctrlplane.ErrAlreadyExists, key)
	}

	clone := *d
	s.deployments[key] = &clone

	return nil
}

func (s *Store) GetDeployment(_ context.Context, tenantID string, deployID id.ID) (*deploy.Deployment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	d, ok := s.deployments[idStr(deployID)]
	if !ok || d.TenantID != tenantID {
		return nil, fmt.Errorf("%w: deployment %s", ctrlplane.ErrNotFound, deployID)
	}

	clone := *d

	return &clone, nil
}

func (s *Store) UpdateDeployment(_ context.Context, d *deploy.Deployment) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(d.ID)
	if _, ok := s.deployments[key]; !ok {
		return fmt.Errorf("%w: deployment %s", ctrlplane.ErrNotFound, key)
	}

	d.UpdatedAt = now()
	clone := *d
	s.deployments[key] = &clone

	return nil
}

func (s *Store) ListDeployments(_ context.Context, tenantID string, instanceID id.ID, opts deploy.ListOptions) (*deploy.DeployListResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	instKey := idStr(instanceID)

	var items []*deploy.Deployment

	for _, d := range s.deployments {
		if d.TenantID != tenantID || idStr(d.InstanceID) != instKey {
			continue
		}

		clone := *d
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

	return &deploy.DeployListResult{
		Items: items,
		Total: total,
	}, nil
}

func (s *Store) InsertRelease(_ context.Context, r *deploy.Release) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(r.ID)
	if _, exists := s.releases[key]; exists {
		return fmt.Errorf("%w: release %s", ctrlplane.ErrAlreadyExists, key)
	}

	clone := *r
	s.releases[key] = &clone

	return nil
}

func (s *Store) GetRelease(_ context.Context, tenantID string, releaseID id.ID) (*deploy.Release, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	r, ok := s.releases[idStr(releaseID)]
	if !ok || r.TenantID != tenantID {
		return nil, fmt.Errorf("%w: release %s", ctrlplane.ErrNotFound, releaseID)
	}

	clone := *r

	return &clone, nil
}

func (s *Store) ListReleases(_ context.Context, tenantID string, instanceID id.ID, opts deploy.ListOptions) (*deploy.ReleaseListResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	instKey := idStr(instanceID)

	var items []*deploy.Release

	for _, r := range s.releases {
		if r.TenantID != tenantID || idStr(r.InstanceID) != instKey {
			continue
		}

		clone := *r
		items = append(items, &clone)
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Version > items[j].Version
	})

	total := len(items)

	limit := opts.Limit
	if limit <= 0 || limit > total {
		limit = total
	}

	items = items[:limit]

	return &deploy.ReleaseListResult{
		Items: items,
		Total: total,
	}, nil
}

func (s *Store) NextReleaseVersion(_ context.Context, tenantID string, instanceID id.ID) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	instKey := idStr(instanceID)
	maxVersion := 0

	for _, r := range s.releases {
		if r.TenantID == tenantID && idStr(r.InstanceID) == instKey && r.Version > maxVersion {
			maxVersion = r.Version
		}
	}

	return maxVersion + 1, nil
}
