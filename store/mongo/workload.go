package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/workload"
)

const colWorkloads = "cp_workloads"

// InsertWorkload persists a Workload.
func (s *Store) InsertWorkload(ctx context.Context, w *workload.Workload) error {
	model := toWorkloadModel(w)

	if _, err := s.mdb.NewInsert(model).Exec(ctx); err != nil {
		return fmt.Errorf("mongo: insert workload: %w", err)
	}

	return nil
}

// GetWorkloadByID returns a workload by ID. Empty tenantID is the
// cross-tenant convention used by admin views.
func (s *Store) GetWorkloadByID(ctx context.Context, tenantID string, workloadID id.ID) (*workload.Workload, error) {
	var model workloadModel

	filter := bson.M{"_id": workloadID.String()}
	if tenantID != "" {
		filter["tenant_id"] = tenantID
	}

	err := s.mdb.NewFind(&model).Filter(filter).Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, fmt.Errorf("%w: workload %s", ctrlplane.ErrNotFound, workloadID)
		}

		return nil, fmt.Errorf("mongo: get workload: %w", err)
	}

	return fromWorkloadModel(&model), nil
}

// GetWorkloadBySlug returns a workload by URL-safe slug within the
// tenant.
func (s *Store) GetWorkloadBySlug(ctx context.Context, tenantID, slug string) (*workload.Workload, error) {
	var model workloadModel

	filter := bson.M{"slug": slug}
	if tenantID != "" {
		filter["tenant_id"] = tenantID
	}

	err := s.mdb.NewFind(&model).Filter(filter).Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, fmt.Errorf("%w: workload slug %s", ctrlplane.ErrNotFound, slug)
		}

		return nil, fmt.Errorf("mongo: get workload by slug: %w", err)
	}

	return fromWorkloadModel(&model), nil
}

// ListWorkloads returns workloads matching the filter. Empty
// tenantID = cross-tenant view.
func (s *Store) ListWorkloads(ctx context.Context, tenantID string, opts workload.ListOptions) (*workload.ListResult, error) {
	var models []workloadModel

	filter := bson.M{}
	if tenantID != "" {
		filter["tenant_id"] = tenantID
	}

	if opts.State != "" {
		filter["state"] = string(opts.State)
	}

	if opts.ProviderName != "" {
		filter["provider_name"] = opts.ProviderName
	}

	if opts.Region != "" {
		filter["region"] = opts.Region
	}

	q := s.mdb.NewFind(&models).
		Filter(filter).
		Sort(bson.D{{Key: "created_at", Value: -1}})

	if opts.Limit > 0 {
		q = q.Limit(int64(opts.Limit))
	}

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("mongo: list workloads: %w", err)
	}

	items := make([]*workload.Workload, 0, len(models))
	for i := range models {
		items = append(items, fromWorkloadModel(&models[i]))
	}

	return &workload.ListResult{Items: items, Total: len(items)}, nil
}

// UpdateWorkload persists changes.
func (s *Store) UpdateWorkload(ctx context.Context, w *workload.Workload) error {
	w.UpdatedAt = now()
	model := toWorkloadModel(w)

	if _, err := s.mdb.NewUpdate(model).
		Filter(bson.M{"_id": model.ID}).
		Exec(ctx); err != nil {
		return fmt.Errorf("mongo: update workload: %w", err)
	}

	return nil
}

// DeleteWorkload removes a workload row. Replica Instances are not
// touched here — workload.Service.Delete handles cascade by calling
// instance.Service.Delete first.
func (s *Store) DeleteWorkload(ctx context.Context, tenantID string, workloadID id.ID) error {
	filter := bson.M{"_id": workloadID.String()}
	if tenantID != "" {
		filter["tenant_id"] = tenantID
	}

	if _, err := s.mdb.NewDelete((*workloadModel)(nil)).
		Filter(filter).
		Exec(ctx); err != nil {
		return fmt.Errorf("mongo: delete workload: %w", err)
	}

	return nil
}
