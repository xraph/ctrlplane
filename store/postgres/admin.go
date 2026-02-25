package postgres

import (
	"context"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/admin"
)

func (s *Store) InsertTenant(ctx context.Context, tenant *admin.Tenant) error {
	model := toTenantModel(tenant)

	_, err := s.pg.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: insert tenant failed: %w", err)
	}

	return nil
}

func (s *Store) GetTenant(ctx context.Context, tenantID string) (*admin.Tenant, error) {
	var model tenantModel

	err := s.pg.NewSelect(&model).
		Where("id = $1", tenantID).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: tenant %s", ctrlplane.ErrNotFound, tenantID)
		}

		return nil, fmt.Errorf("postgres: get tenant failed: %w", err)
	}

	tenant := &admin.Tenant{
		Entity: ctrlplane.Entity{
			ID:        model.ID,
			CreatedAt: model.CreatedAt,
			UpdatedAt: model.UpdatedAt,
		},
		ExternalID: model.ExternalID,
		Slug:       model.Slug,
		Name:       model.Name,
		Status:     admin.TenantStatus(model.Status),
	}

	return tenant, nil
}

func (s *Store) GetTenantBySlug(ctx context.Context, slug string) (*admin.Tenant, error) {
	var model tenantModel

	err := s.pg.NewSelect(&model).
		Where("slug = $1", slug).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: slug %s", ctrlplane.ErrNotFound, slug)
		}

		return nil, fmt.Errorf("postgres: get tenant by slug failed: %w", err)
	}

	tenant := &admin.Tenant{
		Entity: ctrlplane.Entity{
			ID:        model.ID,
			CreatedAt: model.CreatedAt,
			UpdatedAt: model.UpdatedAt,
		},
		ExternalID: model.ExternalID,
		Slug:       model.Slug,
		Name:       model.Name,
		Status:     admin.TenantStatus(model.Status),
	}

	return tenant, nil
}

func (s *Store) ListTenants(ctx context.Context, opts admin.ListTenantsOptions) (*admin.TenantListResult, error) {
	var models []tenantModel

	q := s.pg.NewSelect(&models)

	argIdx := 0
	if opts.Status != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("status = $%d", argIdx), opts.Status)
	}

	q = q.OrderExpr("created_at DESC")

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	q = q.Limit(limit)

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("postgres: list tenants failed: %w", err)
	}

	// Count total.
	countQ := s.pg.NewSelect((*tenantModel)(nil))

	cArgIdx := 0
	if opts.Status != "" {
		cArgIdx++
		countQ = countQ.Where(fmt.Sprintf("status = $%d", cArgIdx), opts.Status)
	}

	total, err := countQ.Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: count tenants failed: %w", err)
	}

	items := make([]*admin.Tenant, 0, len(models))
	for _, model := range models {
		tenant := &admin.Tenant{
			Entity: ctrlplane.Entity{
				ID:        model.ID,
				CreatedAt: model.CreatedAt,
				UpdatedAt: model.UpdatedAt,
			},
			ExternalID: model.ExternalID,
			Slug:       model.Slug,
			Name:       model.Name,
			Status:     admin.TenantStatus(model.Status),
		}
		items = append(items, tenant)
	}

	return &admin.TenantListResult{
		Items: items,
		Total: int(total),
	}, nil
}

func (s *Store) UpdateTenant(ctx context.Context, tenant *admin.Tenant) error {
	tenant.UpdatedAt = now()
	model := toTenantModel(tenant)

	res, err := s.pg.NewUpdate(model).WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: update tenant failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: tenant %s", ctrlplane.ErrNotFound, tenant.ID)
	}

	return nil
}

func (s *Store) DeleteTenant(ctx context.Context, tenantID string) error {
	res, err := s.pg.NewDelete((*tenantModel)(nil)).
		Where("id = $1", tenantID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: delete tenant failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: tenant %s", ctrlplane.ErrNotFound, tenantID)
	}

	return nil
}

func (s *Store) CountTenants(ctx context.Context) (int, error) {
	count, err := s.pg.NewSelect((*tenantModel)(nil)).Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgres: count tenants failed: %w", err)
	}

	return int(count), nil
}

func (s *Store) CountTenantsByStatus(ctx context.Context, status admin.TenantStatus) (int, error) {
	count, err := s.pg.NewSelect((*tenantModel)(nil)).
		Where("status = $1", string(status)).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgres: count tenants by status failed: %w", err)
	}

	return int(count), nil
}

func (s *Store) InsertAuditEntry(ctx context.Context, entry *admin.AuditEntry) error {
	model := toAuditEntryModel(entry)

	_, err := s.pg.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: insert audit entry failed: %w", err)
	}

	return nil
}

func (s *Store) QueryAuditLog(ctx context.Context, opts admin.AuditQuery) (*admin.AuditResult, error) {
	var models []auditEntryModel

	q := s.pg.NewSelect(&models)

	argIdx := 0
	if opts.TenantID != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("tenant_id = $%d", argIdx), opts.TenantID)
	}

	if opts.ActorID != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("actor_id = $%d", argIdx), opts.ActorID)
	}

	if opts.Action != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("action = $%d", argIdx), opts.Action)
	}

	if opts.Resource != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("resource = $%d", argIdx), opts.Resource)
	}

	if !opts.Since.IsZero() {
		argIdx++
		q = q.Where(fmt.Sprintf("created_at >= $%d", argIdx), opts.Since)
	}

	if !opts.Until.IsZero() {
		argIdx++
		q = q.Where(fmt.Sprintf("created_at <= $%d", argIdx), opts.Until)
	}

	q = q.OrderExpr("created_at DESC")

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	q = q.Limit(limit)

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("postgres: query audit log failed: %w", err)
	}

	items := make([]admin.AuditEntry, 0, len(models))
	for _, model := range models {
		entry := admin.AuditEntry{
			Entity: ctrlplane.Entity{
				CreatedAt: model.CreatedAt,
			},
			TenantID:   model.TenantID,
			ActorID:    model.ActorID,
			Action:     model.Action,
			Resource:   model.Resource,
			ResourceID: model.ResourceID,
		}
		items = append(items, entry)
	}

	return &admin.AuditResult{
		Items: items,
		Total: len(items),
	}, nil
}
