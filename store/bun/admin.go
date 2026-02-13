package bun

import (
	"context"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/admin"
)

func (s *Store) InsertTenant(ctx context.Context, tenant *admin.Tenant) error {
	model := toTenantModel(tenant)

	_, err := s.db.NewInsert().Model(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("bun: insert tenant failed: %w", err)
	}

	return nil
}

func (s *Store) GetTenant(ctx context.Context, tenantID string) (*admin.Tenant, error) {
	var model tenantModel

	err := s.db.NewSelect().
		Model(&model).
		Where("id = ?", tenantID).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: tenant %s", ctrlplane.ErrNotFound, tenantID)
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

	err := s.db.NewSelect().
		Model(&model).
		Where("slug = ?", slug).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: slug %s", ctrlplane.ErrNotFound, slug)
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

	query := s.db.NewSelect().Model(&models)

	if opts.Status != "" {
		query = query.Where("status = ?", opts.Status)
	}

	query = query.Order("created_at DESC")

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	query = query.Limit(limit)

	count, err := query.ScanAndCount(ctx)
	if err != nil {
		return nil, fmt.Errorf("bun: list tenants failed: %w", err)
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
		Total: count,
	}, nil
}

func (s *Store) UpdateTenant(ctx context.Context, tenant *admin.Tenant) error {
	tenant.UpdatedAt = now()
	model := toTenantModel(tenant)

	result, err := s.db.NewUpdate().
		Model(model).
		WherePK().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("bun: update tenant failed: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("bun: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: tenant %s", ctrlplane.ErrNotFound, tenant.ID)
	}

	return nil
}

func (s *Store) DeleteTenant(ctx context.Context, tenantID string) error {
	result, err := s.db.NewDelete().
		Model((*tenantModel)(nil)).
		Where("id = ?", tenantID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("bun: delete tenant failed: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("bun: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: tenant %s", ctrlplane.ErrNotFound, tenantID)
	}

	return nil
}

func (s *Store) CountTenants(ctx context.Context) (int, error) {
	count, err := s.db.NewSelect().
		Model((*tenantModel)(nil)).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("bun: count tenants failed: %w", err)
	}

	return count, nil
}

func (s *Store) CountTenantsByStatus(ctx context.Context, status admin.TenantStatus) (int, error) {
	count, err := s.db.NewSelect().
		Model((*tenantModel)(nil)).
		Where("status = ?", string(status)).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("bun: count tenants by status failed: %w", err)
	}

	return count, nil
}

func (s *Store) InsertAuditEntry(ctx context.Context, entry *admin.AuditEntry) error {
	model := toAuditEntryModel(entry)

	_, err := s.db.NewInsert().Model(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("bun: insert audit entry failed: %w", err)
	}

	return nil
}

func (s *Store) QueryAuditLog(ctx context.Context, opts admin.AuditQuery) (*admin.AuditResult, error) {
	var models []auditEntryModel

	query := s.db.NewSelect().Model(&models)

	if opts.TenantID != "" {
		query = query.Where("tenant_id = ?", opts.TenantID)
	}

	if opts.ActorID != "" {
		query = query.Where("actor_id = ?", opts.ActorID)
	}

	if opts.Action != "" {
		query = query.Where("action = ?", opts.Action)
	}

	if opts.Resource != "" {
		query = query.Where("resource = ?", opts.Resource)
	}

	if !opts.Since.IsZero() {
		query = query.Where("created_at >= ?", opts.Since)
	}

	if !opts.Until.IsZero() {
		query = query.Where("created_at <= ?", opts.Until)
	}

	query = query.Order("created_at DESC")

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	query = query.Limit(limit)

	count, err := query.ScanAndCount(ctx)
	if err != nil {
		return nil, fmt.Errorf("bun: query audit log failed: %w", err)
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
		Total: count,
	}, nil
}
