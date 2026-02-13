package bun

import (
	"context"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/provider"
)

func (s *Store) Insert(ctx context.Context, inst *instance.Instance) error {
	model := toInstanceModel(inst)

	_, err := s.db.NewInsert().Model(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("bun: insert instance failed: %w", err)
	}

	return nil
}

func (s *Store) GetByID(ctx context.Context, tenantID string, instanceID id.ID) (*instance.Instance, error) {
	var model instanceModel

	err := s.db.NewSelect().
		Model(&model).
		Where("id = ? AND tenant_id = ?", instanceID.String(), tenantID).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: instance %s", ctrlplane.ErrNotFound, instanceID)
	}

	inst := &instance.Instance{
		Entity: ctrlplane.Entity{
			ID:        id.MustParse(model.ID),
			CreatedAt: model.CreatedAt,
			UpdatedAt: model.UpdatedAt,
		},
		TenantID:     model.TenantID,
		Slug:         model.Slug,
		Name:         model.Name,
		State:        provider.InstanceState(model.State),
		ProviderName: model.ProviderName,
		ProviderRef:  model.ProviderRef,
		Region:       model.Region,
		Image:        model.Image,
	}

	return inst, nil
}

func (s *Store) GetBySlug(ctx context.Context, tenantID string, slug string) (*instance.Instance, error) {
	var model instanceModel

	err := s.db.NewSelect().
		Model(&model).
		Where("tenant_id = ? AND slug = ?", tenantID, slug).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: slug %s", ctrlplane.ErrNotFound, slug)
	}

	inst := &instance.Instance{
		Entity: ctrlplane.Entity{
			ID:        id.MustParse(model.ID),
			CreatedAt: model.CreatedAt,
			UpdatedAt: model.UpdatedAt,
		},
		TenantID:     model.TenantID,
		Slug:         model.Slug,
		Name:         model.Name,
		State:        provider.InstanceState(model.State),
		ProviderName: model.ProviderName,
		ProviderRef:  model.ProviderRef,
		Region:       model.Region,
		Image:        model.Image,
	}

	return inst, nil
}

func (s *Store) List(ctx context.Context, tenantID string, opts instance.ListOptions) (*instance.ListResult, error) {
	var models []instanceModel

	query := s.db.NewSelect().Model(&models).Where("tenant_id = ?", tenantID)

	if opts.State != "" {
		query = query.Where("state = ?", opts.State)
	}

	if opts.Provider != "" {
		query = query.Where("provider_name = ?", opts.Provider)
	}

	query = query.Order("created_at DESC")

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	query = query.Limit(limit)

	count, err := query.ScanAndCount(ctx)
	if err != nil {
		return nil, fmt.Errorf("bun: list instances failed: %w", err)
	}

	items := make([]*instance.Instance, 0, len(models))

	for _, model := range models {
		inst := &instance.Instance{
			Entity: ctrlplane.Entity{
				ID:        id.MustParse(model.ID),
				CreatedAt: model.CreatedAt,
				UpdatedAt: model.UpdatedAt,
			},
			TenantID:     model.TenantID,
			Slug:         model.Slug,
			Name:         model.Name,
			State:        provider.InstanceState(model.State),
			ProviderName: model.ProviderName,
			ProviderRef:  model.ProviderRef,
			Region:       model.Region,
			Image:        model.Image,
		}
		items = append(items, inst)
	}

	return &instance.ListResult{
		Items: items,
		Total: count,
	}, nil
}

func (s *Store) Update(ctx context.Context, inst *instance.Instance) error {
	inst.UpdatedAt = now()
	model := toInstanceModel(inst)

	result, err := s.db.NewUpdate().
		Model(model).
		WherePK().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("bun: update instance failed: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("bun: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: instance %s", ctrlplane.ErrNotFound, inst.ID)
	}

	return nil
}

func (s *Store) Delete(ctx context.Context, tenantID string, instanceID id.ID) error {
	result, err := s.db.NewDelete().
		Model((*instanceModel)(nil)).
		Where("id = ? AND tenant_id = ?", instanceID.String(), tenantID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("bun: delete instance failed: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("bun: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: instance %s", ctrlplane.ErrNotFound, instanceID)
	}

	return nil
}

func (s *Store) CountByTenant(ctx context.Context, tenantID string) (int, error) {
	count, err := s.db.NewSelect().
		Model((*instanceModel)(nil)).
		Where("tenant_id = ?", tenantID).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("bun: count instances failed: %w", err)
	}

	return count, nil
}
