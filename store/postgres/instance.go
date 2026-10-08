package postgres

import (
	"context"
	"fmt"
	"strings"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/internal/pagination"
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

	position, err := pagination.Decode(opts.Cursor)
	if err != nil {
		return nil, err
	}

	q := s.pg.NewSelect(&models)
	argCount := 0
	q = q.Where(fmt.Sprintf("tenant_id = $%d", argCount+1), tenantID)

	argCount += 1
	if opts.State != "" {
		q = q.Where(fmt.Sprintf("state = $%d", argCount+1), opts.State)
		argCount += 1
	}

	if opts.Provider != "" {
		q = q.Where(fmt.Sprintf("provider_name = $%d", argCount+1), opts.Provider)
		argCount += 1
	}

	if opts.Datacenter != "" {
		q = q.Where(fmt.Sprintf("datacenter_id = $%d", argCount+1), opts.Datacenter)
		argCount += 1
	}

	if opts.Label != "" {
		if key, val, ok := strings.Cut(opts.Label, "="); ok && key != "" {
			q = q.Where(fmt.Sprintf("labels::jsonb ->> $%d = $%d", argCount+1, argCount+2), key, val)
			argCount += 2
		}
	}

	total, err := q.Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: count List: %w", err)
	}

	if opts.Cursor != "" {
		q = q.Where(fmt.Sprintf("(created_at < $%d OR (created_at = $%d AND id < $%d))", argCount+1, argCount+2, argCount+3), position.CreatedAt, position.CreatedAt, position.ID.String())
	}

	q = q.OrderExpr("created_at DESC, id DESC")

	pageLimit := opts.Limit
	if pageLimit <= 0 {
		pageLimit = 100
	}

	if pageLimit > 0 {
		q = q.Limit(pageLimit + 1)
	}

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("postgres: list List: %w", err)
	}

	items := make([]*instance.Instance, 0, len(models))
	for i := range models {
		items = append(items, fromInstanceModel(&models[i]))
	}

	items, next := pagination.Trim(items, pageLimit, func(v *instance.Instance) ctrlplane.Entity { return v.Entity })

	return &instance.ListResult{Items: items, Total: int(total), NextCursor: next}, nil
}

func (s *Store) Update(ctx context.Context, inst *instance.Instance) error {
	inst.UpdatedAt = now()
	model := toInstanceModel(inst)

	res, err := s.pg.NewUpdate(model).Where("id = ? AND tenant_id = ?", inst.ID.String(), inst.TenantID).Exec(ctx)
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
