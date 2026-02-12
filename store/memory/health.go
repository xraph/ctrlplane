package memory

import (
	"context"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/health"
	"github.com/xraph/ctrlplane/id"
)

func (s *Store) InsertCheck(_ context.Context, check *health.HealthCheck) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(check.ID)
	if _, exists := s.healthChecks[key]; exists {
		return fmt.Errorf("%w: health check %s", ctrlplane.ErrAlreadyExists, key)
	}

	clone := *check
	s.healthChecks[key] = &clone

	return nil
}

func (s *Store) GetCheck(_ context.Context, tenantID string, checkID id.ID) (*health.HealthCheck, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	check, ok := s.healthChecks[idStr(checkID)]
	if !ok || check.TenantID != tenantID {
		return nil, fmt.Errorf("%w: health check %s", ctrlplane.ErrNotFound, checkID)
	}

	clone := *check

	return &clone, nil
}

func (s *Store) ListChecks(_ context.Context, tenantID string, instanceID id.ID) ([]health.HealthCheck, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	instKey := idStr(instanceID)

	var result []health.HealthCheck

	for _, check := range s.healthChecks {
		if check.TenantID == tenantID && idStr(check.InstanceID) == instKey {
			result = append(result, *check)
		}
	}

	return result, nil
}

func (s *Store) UpdateCheck(_ context.Context, check *health.HealthCheck) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(check.ID)
	if _, ok := s.healthChecks[key]; !ok {
		return fmt.Errorf("%w: health check %s", ctrlplane.ErrNotFound, key)
	}

	check.UpdatedAt = now()
	clone := *check
	s.healthChecks[key] = &clone

	return nil
}

func (s *Store) DeleteCheck(_ context.Context, tenantID string, checkID id.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(checkID)

	check, ok := s.healthChecks[key]
	if !ok || check.TenantID != tenantID {
		return fmt.Errorf("%w: health check %s", ctrlplane.ErrNotFound, key)
	}

	delete(s.healthChecks, key)
	delete(s.healthResults, key)

	return nil
}

func (s *Store) InsertResult(_ context.Context, result *health.HealthResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(result.CheckID)
	s.healthResults[key] = append(s.healthResults[key], *result)

	return nil
}

func (s *Store) ListResults(_ context.Context, tenantID string, checkID id.ID, opts health.HistoryOptions) ([]health.HealthResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := idStr(checkID)

	results, ok := s.healthResults[key]

	if !ok {
		return nil, nil
	}

	var filtered []health.HealthResult

	for _, r := range results {
		if r.TenantID != tenantID {
			continue
		}

		if !opts.Since.IsZero() && r.CheckedAt.Before(opts.Since) {
			continue
		}

		if !opts.Until.IsZero() && r.CheckedAt.After(opts.Until) {
			continue
		}

		filtered = append(filtered, r)
	}

	if opts.Limit > 0 && len(filtered) > opts.Limit {
		filtered = filtered[len(filtered)-opts.Limit:]
	}

	return filtered, nil
}

func (s *Store) GetLatestResult(_ context.Context, tenantID string, checkID id.ID) (*health.HealthResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := idStr(checkID)

	results, ok := s.healthResults[key]
	if !ok || len(results) == 0 {
		return nil, fmt.Errorf("%w: no results for check %s", ctrlplane.ErrNotFound, key)
	}

	for i := len(results) - 1; i >= 0; i-- {
		if results[i].TenantID == tenantID {
			clone := results[i]

			return &clone, nil
		}
	}

	return nil, fmt.Errorf("%w: no results for check %s", ctrlplane.ErrNotFound, key)
}
