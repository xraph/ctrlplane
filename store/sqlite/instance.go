package sqlite

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/internal/pagination"
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

	position, err := pagination.Decode(opts.Cursor)
	if err != nil {
		return nil, err
	}

	q := s.sdb.NewSelect(&models)

	q = q.Where("tenant_id = ?", tenantID)
	if opts.State != "" {
		q = q.Where("state = ?", opts.State)
	}

	if opts.Provider != "" {
		q = q.Where("provider_name = ?", opts.Provider)
	}

	if opts.Datacenter != "" {
		q = q.Where("datacenter_id = ?", opts.Datacenter)
	}

	if opts.Label != "" {
		if key, val, ok := strings.Cut(opts.Label, "="); ok && key != "" {
			q = q.Where("json_extract(labels, ?) = ?", "$."+strconv.Quote(key), val)
		}
	}

	total, err := q.Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlite: count List: %w", err)
	}

	if opts.Cursor != "" {
		q = q.Where("(created_at < ? OR (created_at = ? AND id < ?))", position.CreatedAt, position.CreatedAt, position.ID.String())
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
		return nil, fmt.Errorf("sqlite: list List: %w", err)
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

	res, err := s.sdb.NewUpdate(model).Where("id = ? AND tenant_id = ?", inst.ID.String(), inst.TenantID).Exec(ctx)
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
