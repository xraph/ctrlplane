package memory

import (
	"context"
	"errors"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/bootstrap"
	"github.com/xraph/ctrlplane/id"
)

// InsertBootstrap persists a new bootstrap workload row.
//
// The in-memory store mirrors the persistent backends' shape: rows
// keyed by ID, with a (DatacenterID, Name) uniqueness check enforced
// at insert time so the reconciler's idempotency contract holds.
func (s *Store) InsertBootstrap(_ context.Context, bw *bootstrap.BootstrapWorkload) error {
	if bw == nil {
		return errors.New("memory: insert bootstrap: nil row")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.bootstraps[idStr(bw.ID)]; exists {
		return fmt.Errorf("memory: insert bootstrap %s: %w", bw.ID, ctrlplane.ErrAlreadyExists)
	}

	for _, existing := range s.bootstraps {
		if existing.DatacenterID == bw.DatacenterID && existing.Name == bw.Name {
			return fmt.Errorf("memory: insert bootstrap %s/%s: %w", bw.DatacenterID, bw.Name, ctrlplane.ErrAlreadyExists)
		}
	}

	clone := *bw
	if clone.CreatedAt.IsZero() {
		clone.CreatedAt = now()
	}

	clone.UpdatedAt = now()
	s.bootstraps[idStr(bw.ID)] = &clone

	return nil
}

// GetBootstrap returns a bootstrap workload by ID.
func (s *Store) GetBootstrap(_ context.Context, bootstrapID id.ID) (*bootstrap.BootstrapWorkload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	bw, ok := s.bootstraps[idStr(bootstrapID)]
	if !ok {
		return nil, fmt.Errorf("memory: get bootstrap %s: %w", bootstrapID, ctrlplane.ErrNotFound)
	}

	clone := *bw

	return &clone, nil
}

// GetBootstrapByName returns the row whose (DatacenterID, Name) pair
// matches. ErrNotFound when no row matches.
func (s *Store) GetBootstrapByName(_ context.Context, datacenterID id.ID, name string) (*bootstrap.BootstrapWorkload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, bw := range s.bootstraps {
		if bw.DatacenterID == datacenterID && bw.Name == name {
			clone := *bw

			return &clone, nil
		}
	}

	return nil, fmt.Errorf("memory: get bootstrap %s/%s: %w", datacenterID, name, ctrlplane.ErrNotFound)
}

// ListBootstraps returns every bootstrap workload attached to the
// given datacenter. Returns an empty slice (not nil) when no rows
// match — easier for callers to range over.
func (s *Store) ListBootstraps(_ context.Context, datacenterID id.ID) ([]*bootstrap.BootstrapWorkload, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*bootstrap.BootstrapWorkload, 0)

	for _, bw := range s.bootstraps {
		if bw.DatacenterID == datacenterID {
			clone := *bw
			out = append(out, &clone)
		}
	}

	return out, nil
}

// UpdateBootstrap persists changes to an existing row.
func (s *Store) UpdateBootstrap(_ context.Context, bw *bootstrap.BootstrapWorkload) error {
	if bw == nil {
		return errors.New("memory: update bootstrap: nil row")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.bootstraps[idStr(bw.ID)]; !exists {
		return fmt.Errorf("memory: update bootstrap %s: %w", bw.ID, ctrlplane.ErrNotFound)
	}

	clone := *bw
	clone.UpdatedAt = now()
	s.bootstraps[idStr(bw.ID)] = &clone

	return nil
}

// DeleteBootstrap removes a row. Idempotent: deleting a row that
// doesn't exist is treated as success (matches the convergent-delete
// shape used elsewhere — re-running the reconciler after a partial
// failure should not error).
func (s *Store) DeleteBootstrap(_ context.Context, bootstrapID id.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.bootstraps, idStr(bootstrapID))

	return nil
}
