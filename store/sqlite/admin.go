package sqlite

import (
	"context"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/admin"
)

func (s *Store) InsertTenant(ctx context.Context, tenant *admin.Tenant) error {
	model := toTenantModel(tenant)

	_, err := s.sdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: insert tenant failed: %w", err)
	}

	return nil
}

func (s *Store) GetTenant(ctx context.Context, tenantID string) (*admin.Tenant, error) {
	var model tenantModel

	err := s.sdb.NewSelect(&model).
		Where("id = ?", tenantID).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: tenant %s", ctrlplane.ErrNotFound, tenantID)
		}

		return nil, fmt.Errorf("sqlite: get tenant failed: %w", err)
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

	err := s.sdb.NewSelect(&model).
		Where("slug = ?", slug).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: slug %s", ctrlplane.ErrNotFound, slug)
		}

		return nil, fmt.Errorf("sqlite: get tenant by slug failed: %w", err)
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

	q := s.sdb.NewSelect(&models)

	if opts.Status != "" {
		q = q.Where("status = ?", opts.Status)
	}

	q = q.OrderExpr("created_at DESC")

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	q = q.Limit(limit)

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("sqlite: list tenants failed: %w", err)
	}

	// Count total.
	countQ := s.sdb.NewSelect((*tenantModel)(nil))

	if opts.Status != "" {
		countQ = countQ.Where("status = ?", opts.Status)
	}

	total, err := countQ.Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlite: count tenants failed: %w", err)
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

	res, err := s.sdb.NewUpdate(model).WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: update tenant failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: tenant %s", ctrlplane.ErrNotFound, tenant.ID)
	}

	return nil
}

func (s *Store) DeleteTenant(ctx context.Context, tenantID string) error {
	res, err := s.sdb.NewDelete((*tenantModel)(nil)).
		Where("id = ?", tenantID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: delete tenant failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: tenant %s", ctrlplane.ErrNotFound, tenantID)
	}

	return nil
}

func (s *Store) CountTenants(ctx context.Context) (int, error) {
	count, err := s.sdb.NewSelect((*tenantModel)(nil)).Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("sqlite: count tenants failed: %w", err)
	}

	return int(count), nil
}

func (s *Store) CountTenantsByStatus(ctx context.Context, status admin.TenantStatus) (int, error) {
	count, err := s.sdb.NewSelect((*tenantModel)(nil)).
		Where("status = ?", string(status)).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("sqlite: count tenants by status failed: %w", err)
	}

	return int(count), nil
}

func (s *Store) InsertAuditEntry(ctx context.Context, entry *admin.AuditEntry) error {
	model := toAuditEntryModel(entry)

	_, err := s.sdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: insert audit entry failed: %w", err)
	}

	return nil
}

func (s *Store) QueryAuditLog(ctx context.Context, opts admin.AuditQuery) (*admin.AuditResult, error) {
	var models []auditEntryModel

	q := s.sdb.NewSelect(&models)

	if opts.TenantID != "" {
		q = q.Where("tenant_id = ?", opts.TenantID)
	}

	if opts.ActorID != "" {
		q = q.Where("actor_id = ?", opts.ActorID)
	}

	if opts.Action != "" {
		q = q.Where("action = ?", opts.Action)
	}

	if opts.Resource != "" {
		q = q.Where("resource = ?", opts.Resource)
	}

	if !opts.Since.IsZero() {
		q = q.Where("created_at >= ?", opts.Since)
	}

	if !opts.Until.IsZero() {
		q = q.Where("created_at <= ?", opts.Until)
	}

	q = q.OrderExpr("created_at DESC")

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	q = q.Limit(limit)

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("sqlite: query audit log failed: %w", err)
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
