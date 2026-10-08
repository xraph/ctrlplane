package postgres

import (
	"context"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/pagination"
	"github.com/xraph/ctrlplane/workload"
)

// InsertWorkload persists a Workload. The id is a TEXT pk assigned by the
// caller, so a plain insert is safe (no BIGSERIAL/autoincrement concern).
func (s *Store) InsertWorkload(ctx context.Context, w *workload.Workload) error {
	result, err := s.pg.NewInsert(toWorkloadModel(w)).OnConflict("DO NOTHING").Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: insert workload: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres: insert workload count: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("workload %s: %w", w.ID, ctrlplane.ErrAlreadyExists)
	}

	return nil
}

// GetWorkloadByID returns a workload by ID. Empty tenantID is the cross-tenant
// convention used by admin views.
func (s *Store) GetWorkloadByID(ctx context.Context, tenantID string, workloadID id.ID) (*workload.Workload, error) {
	var model workloadModel

	q := s.pg.NewSelect(&model).Where("id = $1", workloadID.String())
	if tenantID != "" {
		q = q.Where("tenant_id = $2", tenantID)
	}

	if err := q.Scan(ctx); err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: workload %s", ctrlplane.ErrNotFound, workloadID)
		}

		return nil, fmt.Errorf("postgres: get workload: %w", err)
	}

	return fromWorkloadModel(&model), nil
}

// GetWorkloadBySlug returns a workload by URL-safe slug within the tenant.
func (s *Store) GetWorkloadBySlug(ctx context.Context, tenantID, slug string) (*workload.Workload, error) {
	var model workloadModel

	q := s.pg.NewSelect(&model).Where("slug = $1", slug)
	if tenantID != "" {
		q = q.Where("tenant_id = $2", tenantID)
	}

	if err := q.Scan(ctx); err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: workload slug %s", ctrlplane.ErrNotFound, slug)
		}

		return nil, fmt.Errorf("postgres: get workload by slug: %w", err)
	}

	return fromWorkloadModel(&model), nil
}

// ListWorkloads returns workloads matching the filter. Empty tenantID = a
// cross-tenant view. Each conditional clause continues the positional
// placeholder index ($1, $2, …) so the args line up — restarting at $1 per
// clause is the placeholder-reuse bug.
func (s *Store) ListWorkloads(ctx context.Context, tenantID string, opts workload.ListOptions) (*workload.ListResult, error) {
	var models []workloadModel

	position, err := pagination.Decode(opts.Cursor)
	if err != nil {
		return nil, err
	}

	q := s.pg.NewSelect(&models)

	argCount := 0
	if tenantID != "" {
		q = q.Where(fmt.Sprintf("tenant_id = $%d", argCount+1), tenantID)
		argCount += 1
	}

	if opts.State != "" {
		q = q.Where(fmt.Sprintf("state = $%d", argCount+1), string(opts.State))
		argCount += 1
	}

	if opts.ProviderName != "" {
		q = q.Where(fmt.Sprintf("provider_name = $%d", argCount+1), opts.ProviderName)
		argCount += 1
	}

	if opts.Region != "" {
		q = q.Where(fmt.Sprintf("region = $%d", argCount+1), opts.Region)
		argCount += 1
	}

	total, err := q.Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: count ListWorkloads: %w", err)
	}

	if opts.Cursor != "" {
		q = q.Where(fmt.Sprintf("(created_at < $%d OR (created_at = $%d AND id < $%d))", argCount+1, argCount+2, argCount+3), position.CreatedAt, position.CreatedAt, position.ID.String())
	}

	q = q.OrderExpr("created_at DESC, id DESC")

	pageLimit := opts.Limit
	if pageLimit > 0 {
		q = q.Limit(pageLimit + 1)
	}

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("postgres: list ListWorkloads: %w", err)
	}

	items := make([]*workload.Workload, 0, len(models))
	for i := range models {
		items = append(items, fromWorkloadModel(&models[i]))
	}

	items, next := pagination.Trim(items, pageLimit, func(v *workload.Workload) ctrlplane.Entity { return v.Entity })

	return &workload.ListResult{Items: items, Total: int(total), NextCursor: next}, nil
}

// UpdateWorkload persists changes only within the workload's owning tenant.
func (s *Store) UpdateWorkload(ctx context.Context, w *workload.Workload) error {
	w.UpdatedAt = now()

	result, err := s.pg.NewUpdate(toWorkloadModel(w)).Where("id = ? AND tenant_id = ?", w.ID.String(), w.TenantID).Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: update workload: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres: update workload count: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("workload %s: %w", w.ID, ctrlplane.ErrNotFound)
	}

	return nil
}

// DeleteWorkload removes a workload row. Replica Instances are not touched here
// — workload.Service.Delete cascades by deleting instances first.
func (s *Store) DeleteWorkload(ctx context.Context, tenantID string, workloadID id.ID) error {
	q := s.pg.NewDelete((*workloadModel)(nil)).Where("id = $1", workloadID.String())
	if tenantID != "" {
		q = q.Where("tenant_id = $2", tenantID)
	}

	result, err := q.Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: delete workload: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres: delete workload count: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("workload %s: %w", workloadID, ctrlplane.ErrNotFound)
	}

	return nil
}
