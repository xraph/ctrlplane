package postgres

import (
	"context"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/bootstrap"
	"github.com/xraph/ctrlplane/id"
)

// InsertBootstrap persists a new bootstrap workload row.
//
// The (datacenter_id, name) uniqueness contract is enforced by the
// `idx_cp_bootstrap_workloads_dc_name` unique index from migration
// 20240101000021. Duplicates surface as a postgres unique-violation
// which the bun layer maps to a generic error — the reconciler
// already handles "row exists" by reading + updating instead, so
// the rare collision path is benign.
func (s *Store) InsertBootstrap(ctx context.Context, bw *bootstrap.BootstrapWorkload) error {
	model := toBootstrapModel(bw)

	_, err := s.pg.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: insert bootstrap: %w", err)
	}

	return nil
}

// GetBootstrap retrieves a bootstrap workload by ID.
func (s *Store) GetBootstrap(ctx context.Context, bootstrapID id.ID) (*bootstrap.BootstrapWorkload, error) {
	var model bootstrapModel

	err := s.pg.NewSelect(&model).
		Where("id = $1", bootstrapID.String()).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: bootstrap %s", ctrlplane.ErrNotFound, bootstrapID)
		}

		return nil, fmt.Errorf("postgres: get bootstrap: %w", err)
	}

	return fromBootstrapModel(&model), nil
}

// GetBootstrapByName retrieves the row whose (DatacenterID, Name)
// pair matches. ErrNotFound when no row matches.
func (s *Store) GetBootstrapByName(ctx context.Context, datacenterID id.ID, name string) (*bootstrap.BootstrapWorkload, error) {
	var model bootstrapModel

	err := s.pg.NewSelect(&model).
		Where("datacenter_id = $1 AND name = $2", datacenterID.String(), name).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: bootstrap %s/%s", ctrlplane.ErrNotFound, datacenterID, name)
		}

		return nil, fmt.Errorf("postgres: get bootstrap by name: %w", err)
	}

	return fromBootstrapModel(&model), nil
}

// ListBootstraps returns every bootstrap workload attached to the
// given datacenter. Ordered by created_at so the reconciler's
// per-tick iteration is stable across runs.
func (s *Store) ListBootstraps(ctx context.Context, datacenterID id.ID) ([]*bootstrap.BootstrapWorkload, error) {
	var models []bootstrapModel

	err := s.pg.NewSelect(&models).
		Where("datacenter_id = $1", datacenterID.String()).
		OrderExpr("created_at ASC").
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: list bootstraps: %w", err)
	}

	out := make([]*bootstrap.BootstrapWorkload, 0, len(models))
	for i := range models {
		out = append(out, fromBootstrapModel(&models[i]))
	}

	return out, nil
}

// UpdateBootstrap persists changes to an existing row. UpdatedAt is
// stamped on every save so dashboard sort-by-recently-touched is
// meaningful.
func (s *Store) UpdateBootstrap(ctx context.Context, bw *bootstrap.BootstrapWorkload) error {
	bw.UpdatedAt = now()
	model := toBootstrapModel(bw)

	_, err := s.pg.NewUpdate(model).WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: update bootstrap: %w", err)
	}

	return nil
}

// DeleteBootstrap removes a row. Idempotent: a delete against a
// non-existent ID returns nil, matching the in-memory store's
// reconciler-friendly contract.
func (s *Store) DeleteBootstrap(ctx context.Context, bootstrapID id.ID) error {
	_, err := s.pg.NewDelete(&bootstrapModel{}).
		Where("id = $1", bootstrapID.String()).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: delete bootstrap: %w", err)
	}

	return nil
}
