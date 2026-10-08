package sqlite

import (
	"context"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/pagination"
	"github.com/xraph/ctrlplane/workload"
)

// InsertWorkload persists a workload with a unique tenant and slug.
func (s *Store) InsertWorkload(ctx context.Context, w *workload.Workload) error {
	result, err := s.sdb.NewInsert(toWorkloadModel(w)).OnConflict("DO NOTHING").Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: insert workload: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: insert workload count: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("workload %s: %w", w.ID, ctrlplane.ErrAlreadyExists)
	}

	return nil
}

// GetWorkloadByID retrieves a workload within its tenant. Empty tenant is reserved for authorized administrators.
func (s *Store) GetWorkloadByID(ctx context.Context, tenantID string, target id.ID) (*workload.Workload, error) {
	var model workloadModel

	q := s.sdb.NewSelect(&model).Where("id = ?", target.String())
	if tenantID != "" {
		q = q.Where("tenant_id = ?", tenantID)
	}

	if err := q.Scan(ctx); err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("workload %s: %w", target, ctrlplane.ErrNotFound)
		}

		return nil, fmt.Errorf("sqlite: get workload: %w", err)
	}

	return fromWorkloadModel(&model), nil
}

// GetWorkloadBySlug retrieves a workload by its tenant-local slug.
func (s *Store) GetWorkloadBySlug(ctx context.Context, tenantID, slug string) (*workload.Workload, error) {
	var model workloadModel

	q := s.sdb.NewSelect(&model).Where("slug = ?", slug)
	if tenantID != "" {
		q = q.Where("tenant_id = ?", tenantID)
	}

	if err := q.Scan(ctx); err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("workload slug %s: %w", slug, ctrlplane.ErrNotFound)
		}

		return nil, fmt.Errorf("sqlite: get workload by slug: %w", err)
	}

	return fromWorkloadModel(&model), nil
}

// ListWorkloads returns matching workloads in stable descending creation order.
func (s *Store) ListWorkloads(ctx context.Context, tenantID string, opts workload.ListOptions) (*workload.ListResult, error) {
	position, err := pagination.Decode(opts.Cursor)
	if err != nil {
		return nil, err
	}

	var models []workloadModel

	q := s.sdb.NewSelect(&models)
	if tenantID != "" {
		q = q.Where("tenant_id = ?", tenantID)
	}

	if opts.State != "" {
		q = q.Where("state = ?", opts.State)
	}

	if opts.ProviderName != "" {
		q = q.Where("provider_name = ?", opts.ProviderName)
	}

	if opts.Region != "" {
		q = q.Where("region = ?", opts.Region)
	}

	total, err := q.Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlite: count workloads: %w", err)
	}

	if opts.Cursor != "" {
		q = q.Where("(created_at < ? OR (created_at = ? AND id < ?))", position.CreatedAt, position.CreatedAt, position.ID.String())
	}

	q = q.OrderExpr("created_at DESC, id DESC")
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit + 1)
	}

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("sqlite: list workloads: %w", err)
	}

	items := make([]*workload.Workload, 0, len(models))
	for i := range models {
		items = append(items, fromWorkloadModel(&models[i]))
	}

	items, next := pagination.Trim(items, opts.Limit, func(w *workload.Workload) ctrlplane.Entity { return w.Entity })

	return &workload.ListResult{Items: items, Total: int(total), NextCursor: next}, nil
}

// UpdateWorkload persists changes only within the workload's owning tenant.
func (s *Store) UpdateWorkload(ctx context.Context, w *workload.Workload) error {
	w.UpdatedAt = now()

	result, err := s.sdb.NewUpdate(toWorkloadModel(w)).Where("id = ? AND tenant_id = ?", w.ID.String(), w.TenantID).Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: update workload: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: update workload count: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("workload %s: %w", w.ID, ctrlplane.ErrNotFound)
	}

	return nil
}

// DeleteWorkload deletes the owning tenant's workload; the service handles replica teardown.
func (s *Store) DeleteWorkload(ctx context.Context, tenantID string, target id.ID) error {
	q := s.sdb.NewDelete((*workloadModel)(nil)).Where("id = ?", target.String())
	if tenantID != "" {
		q = q.Where("tenant_id = ?", tenantID)
	}

	result, err := q.Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: delete workload: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: delete workload count: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("workload %s: %w", target, ctrlplane.ErrNotFound)
	}

	return nil
}
