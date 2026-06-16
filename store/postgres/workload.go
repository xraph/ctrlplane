package postgres

import (
	"context"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/workload"
)

// InsertWorkload persists a Workload. The id is a TEXT pk assigned by the
// caller, so a plain insert is safe (no BIGSERIAL/autoincrement concern).
func (s *Store) InsertWorkload(ctx context.Context, w *workload.Workload) error {
	if _, err := s.pg.NewInsert(toWorkloadModel(w)).Exec(ctx); err != nil {
		return fmt.Errorf("postgres: insert workload: %w", err)
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

	q := s.pg.NewSelect(&models)

	argIdx := 0
	if tenantID != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("tenant_id = $%d", argIdx), tenantID)
	}
	if opts.State != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("state = $%d", argIdx), string(opts.State))
	}
	if opts.ProviderName != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("provider_name = $%d", argIdx), opts.ProviderName)
	}
	if opts.Region != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("region = $%d", argIdx), opts.Region)
	}

	q = q.OrderExpr("created_at DESC")
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("postgres: list workloads: %w", err)
	}

	items := make([]*workload.Workload, 0, len(models))
	for i := range models {
		items = append(items, fromWorkloadModel(&models[i]))
	}

	return &workload.ListResult{Items: items, Total: len(items)}, nil
}

// UpdateWorkload persists changes. Mirrors mongo: no not-found error when the
// row is absent (workload.Service handles existence checks).
func (s *Store) UpdateWorkload(ctx context.Context, w *workload.Workload) error {
	w.UpdatedAt = now()

	if _, err := s.pg.NewUpdate(toWorkloadModel(w)).WherePK().Exec(ctx); err != nil {
		return fmt.Errorf("postgres: update workload: %w", err)
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

	if _, err := q.Exec(ctx); err != nil {
		return fmt.Errorf("postgres: delete workload: %w", err)
	}

	return nil
}
