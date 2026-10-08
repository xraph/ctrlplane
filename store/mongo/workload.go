package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/pagination"
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

	position, err := pagination.Decode(opts.Cursor)
	if err != nil {
		return nil, err
	}

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

	total, err := s.mdb.NewFind((*workloadModel)(nil)).Filter(filter).Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("mongo: count ListWorkloads: %w", err)
	}

	if opts.Cursor != "" {
		filter["$or"] = bson.A{bson.M{"created_at": bson.M{"$lt": position.CreatedAt}}, bson.M{"created_at": position.CreatedAt, "_id": bson.M{"$lt": position.ID.String()}}}
	}

	q := s.mdb.NewFind(&models).Filter(filter).Sort(bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}})

	pageLimit := opts.Limit
	if pageLimit > 0 {
		q = q.Limit(int64(pageLimit + 1))
	}

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("mongo: list ListWorkloads: %w", err)
	}

	items := make([]*workload.Workload, 0, len(models))
	for i := range models {
		items = append(items, fromWorkloadModel(&models[i]))
	}

	items, next := pagination.Trim(items, pageLimit, func(v *workload.Workload) ctrlplane.Entity { return v.Entity })

	return &workload.ListResult{Items: items, Total: int(total), NextCursor: next}, nil
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
