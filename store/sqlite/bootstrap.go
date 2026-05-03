package sqlite

import (
	"context"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/bootstrap"
	"github.com/xraph/ctrlplane/id"
)

// InsertBootstrap persists a new bootstrap workload row. Mirrors the
// postgres impl shape — see store/postgres/bootstrap.go for the
// idempotency contract.
func (s *Store) InsertBootstrap(ctx context.Context, bw *bootstrap.BootstrapWorkload) error {
	model := toBootstrapModel(bw)

	_, err := s.sdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: insert bootstrap: %w", err)
	}

	return nil
}

// GetBootstrap retrieves a bootstrap workload by ID.
func (s *Store) GetBootstrap(ctx context.Context, bootstrapID id.ID) (*bootstrap.BootstrapWorkload, error) {
	var model bootstrapModel

	err := s.sdb.NewSelect(&model).
		Where("id = ?", bootstrapID.String()).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: bootstrap %s", ctrlplane.ErrNotFound, bootstrapID)
		}

		return nil, fmt.Errorf("sqlite: get bootstrap: %w", err)
	}

	return fromBootstrapModel(&model), nil
}

// GetBootstrapByName retrieves the row whose (DatacenterID, Name)
// pair matches.
func (s *Store) GetBootstrapByName(ctx context.Context, datacenterID id.ID, name string) (*bootstrap.BootstrapWorkload, error) {
	var model bootstrapModel

	err := s.sdb.NewSelect(&model).
		Where("datacenter_id = ? AND name = ?", datacenterID.String(), name).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: bootstrap %s/%s", ctrlplane.ErrNotFound, datacenterID, name)
		}

		return nil, fmt.Errorf("sqlite: get bootstrap by name: %w", err)
	}

	return fromBootstrapModel(&model), nil
}

// ListBootstraps returns every bootstrap workload attached to the
// given datacenter, ordered by created_at for stable iteration.
func (s *Store) ListBootstraps(ctx context.Context, datacenterID id.ID) ([]*bootstrap.BootstrapWorkload, error) {
	var models []bootstrapModel

	err := s.sdb.NewSelect(&models).
		Where("datacenter_id = ?", datacenterID.String()).
		OrderExpr("created_at ASC").
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list bootstraps: %w", err)
	}

	out := make([]*bootstrap.BootstrapWorkload, 0, len(models))
	for i := range models {
		out = append(out, fromBootstrapModel(&models[i]))
	}

	return out, nil
}

// UpdateBootstrap persists changes to an existing row.
func (s *Store) UpdateBootstrap(ctx context.Context, bw *bootstrap.BootstrapWorkload) error {
	bw.UpdatedAt = now()
	model := toBootstrapModel(bw)

	_, err := s.sdb.NewUpdate(model).WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: update bootstrap: %w", err)
	}

	return nil
}

// DeleteBootstrap removes a row. Idempotent.
func (s *Store) DeleteBootstrap(ctx context.Context, bootstrapID id.ID) error {
	_, err := s.sdb.NewDelete(&bootstrapModel{}).
		Where("id = ?", bootstrapID.String()).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: delete bootstrap: %w", err)
	}

	return nil
}
