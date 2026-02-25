package sqlite

import (
	"context"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
)

func (s *Store) Insert(ctx context.Context, inst *instance.Instance) error {
	model := toInstanceModel(inst)

	_, err := s.sdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: insert instance failed: %w", err)
	}

	return nil
}

func (s *Store) GetByID(ctx context.Context, tenantID string, instanceID id.ID) (*instance.Instance, error) {
	var model instanceModel

	err := s.sdb.NewSelect(&model).
		Where("id = ? AND tenant_id = ?", instanceID.String(), tenantID).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: instance %s", ctrlplane.ErrNotFound, instanceID)
		}

		return nil, fmt.Errorf("sqlite: get instance failed: %w", err)
	}

	return fromInstanceModel(&model), nil
}

func (s *Store) GetBySlug(ctx context.Context, tenantID string, slug string) (*instance.Instance, error) {
	var model instanceModel

	err := s.sdb.NewSelect(&model).
		Where("tenant_id = ? AND slug = ?", tenantID, slug).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: slug %s", ctrlplane.ErrNotFound, slug)
		}

		return nil, fmt.Errorf("sqlite: get instance by slug failed: %w", err)
	}

	return fromInstanceModel(&model), nil
}

func (s *Store) List(ctx context.Context, tenantID string, opts instance.ListOptions) (*instance.ListResult, error) {
	var models []instanceModel

	q := s.sdb.NewSelect(&models).Where("tenant_id = ?", tenantID)

	if opts.State != "" {
		q = q.Where("state = ?", opts.State)
	}

	if opts.Provider != "" {
		q = q.Where("provider_name = ?", opts.Provider)
	}

	q = q.OrderExpr("created_at DESC")

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	q = q.Limit(limit)

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("sqlite: list instances failed: %w", err)
	}

	// Count total matching records.
	countQ := s.sdb.NewSelect((*instanceModel)(nil)).Where("tenant_id = ?", tenantID)

	if opts.State != "" {
		countQ = countQ.Where("state = ?", opts.State)
	}

	if opts.Provider != "" {
		countQ = countQ.Where("provider_name = ?", opts.Provider)
	}

	total, err := countQ.Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlite: count instances failed: %w", err)
	}

	items := make([]*instance.Instance, 0, len(models))
	for i := range models {
		items = append(items, fromInstanceModel(&models[i]))
	}

	return &instance.ListResult{
		Items: items,
		Total: int(total),
	}, nil
}

func (s *Store) Update(ctx context.Context, inst *instance.Instance) error {
	inst.UpdatedAt = now()
	model := toInstanceModel(inst)

	res, err := s.sdb.NewUpdate(model).WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: update instance failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: instance %s", ctrlplane.ErrNotFound, inst.ID)
	}

	return nil
}

func (s *Store) Delete(ctx context.Context, tenantID string, instanceID id.ID) error {
	res, err := s.sdb.NewDelete((*instanceModel)(nil)).
		Where("id = ? AND tenant_id = ?", instanceID.String(), tenantID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: delete instance failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: instance %s", ctrlplane.ErrNotFound, instanceID)
	}

	return nil
}

func (s *Store) CountByTenant(ctx context.Context, tenantID string) (int, error) {
	count, err := s.sdb.NewSelect((*instanceModel)(nil)).
		Where("tenant_id = ?", tenantID).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("sqlite: count instances failed: %w", err)
	}

	return int(count), nil
}
