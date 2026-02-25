package postgres

import (
	"context"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
)

func (s *Store) Insert(ctx context.Context, inst *instance.Instance) error {
	model := toInstanceModel(inst)

	_, err := s.pg.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: insert instance failed: %w", err)
	}

	return nil
}

func (s *Store) GetByID(ctx context.Context, tenantID string, instanceID id.ID) (*instance.Instance, error) {
	var model instanceModel

	err := s.pg.NewSelect(&model).
		Where("id = $1 AND tenant_id = $2", instanceID.String(), tenantID).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: instance %s", ctrlplane.ErrNotFound, instanceID)
		}

		return nil, fmt.Errorf("postgres: get instance failed: %w", err)
	}

	return fromInstanceModel(&model), nil
}

func (s *Store) GetBySlug(ctx context.Context, tenantID string, slug string) (*instance.Instance, error) {
	var model instanceModel

	err := s.pg.NewSelect(&model).
		Where("tenant_id = $1 AND slug = $2", tenantID, slug).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: slug %s", ctrlplane.ErrNotFound, slug)
		}

		return nil, fmt.Errorf("postgres: get instance by slug failed: %w", err)
	}

	return fromInstanceModel(&model), nil
}

func (s *Store) List(ctx context.Context, tenantID string, opts instance.ListOptions) (*instance.ListResult, error) {
	var models []instanceModel

	q := s.pg.NewSelect(&models).Where("tenant_id = $1", tenantID)

	argIdx := 1
	if opts.State != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("state = $%d", argIdx), opts.State)
	}

	if opts.Provider != "" {
		argIdx++
		q = q.Where(fmt.Sprintf("provider_name = $%d", argIdx), opts.Provider)
	}

	q = q.OrderExpr("created_at DESC")

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	q = q.Limit(limit)

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("postgres: list instances failed: %w", err)
	}

	// Count total matching records.
	countQ := s.pg.NewSelect((*instanceModel)(nil)).Where("tenant_id = $1", tenantID)

	cArgIdx := 1
	if opts.State != "" {
		cArgIdx++
		countQ = countQ.Where(fmt.Sprintf("state = $%d", cArgIdx), opts.State)
	}

	if opts.Provider != "" {
		cArgIdx++
		countQ = countQ.Where(fmt.Sprintf("provider_name = $%d", cArgIdx), opts.Provider)
	}

	total, err := countQ.Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: count instances failed: %w", err)
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

	res, err := s.pg.NewUpdate(model).WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: update instance failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: instance %s", ctrlplane.ErrNotFound, inst.ID)
	}

	return nil
}

func (s *Store) Delete(ctx context.Context, tenantID string, instanceID id.ID) error {
	res, err := s.pg.NewDelete((*instanceModel)(nil)).
		Where("id = $1 AND tenant_id = $2", instanceID.String(), tenantID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: delete instance failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: instance %s", ctrlplane.ErrNotFound, instanceID)
	}

	return nil
}

func (s *Store) CountByTenant(ctx context.Context, tenantID string) (int, error) {
	count, err := s.pg.NewSelect((*instanceModel)(nil)).
		Where("tenant_id = $1", tenantID).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgres: count instances failed: %w", err)
	}

	return int(count), nil
}
