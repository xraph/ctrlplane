package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/bootstrap"
	"github.com/xraph/ctrlplane/id"
)

const colBootstraps = "cp_bootstrap_workloads"

// InsertBootstrap persists a new bootstrap workload row.
func (s *Store) InsertBootstrap(ctx context.Context, bw *bootstrap.BootstrapWorkload) error {
	model := toBootstrapModel(bw)

	_, err := s.mdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: insert bootstrap: %w", err)
	}

	return nil
}

// GetBootstrap retrieves a bootstrap workload by ID.
func (s *Store) GetBootstrap(ctx context.Context, bootstrapID id.ID) (*bootstrap.BootstrapWorkload, error) {
	var model bootstrapModel

	err := s.mdb.NewFind(&model).
		Filter(bson.M{"_id": bootstrapID.String()}).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, fmt.Errorf("%w: bootstrap %s", ctrlplane.ErrNotFound, bootstrapID)
		}

		return nil, fmt.Errorf("mongo: get bootstrap: %w", err)
	}

	return fromBootstrapModel(&model), nil
}

// GetBootstrapByName retrieves the row whose (DatacenterID, Name)
// pair matches.
func (s *Store) GetBootstrapByName(ctx context.Context, datacenterID id.ID, name string) (*bootstrap.BootstrapWorkload, error) {
	var model bootstrapModel

	err := s.mdb.NewFind(&model).
		Filter(bson.M{"datacenter_id": datacenterID.String(), "name": name}).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, fmt.Errorf("%w: bootstrap %s/%s", ctrlplane.ErrNotFound, datacenterID, name)
		}

		return nil, fmt.Errorf("mongo: get bootstrap by name: %w", err)
	}

	return fromBootstrapModel(&model), nil
}

// ListBootstraps returns every bootstrap workload attached to the
// given datacenter, ordered by created_at for stable iteration.
func (s *Store) ListBootstraps(ctx context.Context, datacenterID id.ID) ([]*bootstrap.BootstrapWorkload, error) {
	var models []bootstrapModel

	err := s.mdb.NewFind(&models).
		Filter(bson.M{"datacenter_id": datacenterID.String()}).
		Sort(bson.D{{Key: "created_at", Value: 1}}).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("mongo: list bootstraps: %w", err)
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

	_, err := s.mdb.NewUpdate(model).
		Filter(bson.M{"_id": model.ID}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: update bootstrap: %w", err)
	}

	return nil
}

// DeleteBootstrap removes a row.
func (s *Store) DeleteBootstrap(ctx context.Context, bootstrapID id.ID) error {
	_, err := s.mdb.NewDelete(&bootstrapModel{}).
		Filter(bson.M{"_id": bootstrapID.String()}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: delete bootstrap: %w", err)
	}

	return nil
}
