package postgres

import (
	"context"
	"fmt"
	"strings"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/datacenter"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/pagination"
)

// InsertDatacenter persists a new datacenter.
func (s *Store) InsertDatacenter(ctx context.Context, dc *datacenter.Datacenter) error {
	model := toDatacenterModel(dc)

	_, err := s.pg.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: insert datacenter: %w", err)
	}

	return nil
}

// GetDatacenterByID retrieves a datacenter by ID. Returns the DC when
// it belongs to tenantID OR when it's platform-shared (TenantID = ”).
func (s *Store) GetDatacenterByID(ctx context.Context, tenantID string, datacenterID id.ID) (*datacenter.Datacenter, error) {
	var model datacenterModel

	err := s.pg.NewSelect(&model).
		Where("id = $1 AND (tenant_id = $2 OR tenant_id = '')", datacenterID.String(), tenantID).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: datacenter %s", ctrlplane.ErrNotFound, datacenterID)
		}

		return nil, fmt.Errorf("postgres: get datacenter: %w", err)
	}

	return fromDatacenterModel(&model), nil
}

// GetDatacenterBySlug retrieves a datacenter by slug. Tenant-scoped
// hits take precedence over platform-shared ones with the same slug.
func (s *Store) GetDatacenterBySlug(ctx context.Context, tenantID string, slug string) (*datacenter.Datacenter, error) {
	var models []datacenterModel

	err := s.pg.NewSelect(&models).
		Where("slug = $1 AND (tenant_id = $2 OR tenant_id = '')", slug, tenantID).
		Limit(2).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: get datacenter by slug: %w", err)
	}

	if len(models) == 0 {
		return nil, fmt.Errorf("%w: datacenter slug %s", ctrlplane.ErrNotFound, slug)
	}

	pick := &models[0]
	for i := range models {
		if models[i].TenantID == tenantID {
			pick = &models[i]

			break
		}
	}

	return fromDatacenterModel(pick), nil
}

// ListDatacenters returns datacenters visible to tenantID — both
// tenant-owned and platform-shared (TenantID = ”).
func (s *Store) ListDatacenters(ctx context.Context, tenantID string, opts datacenter.ListOptions) (*datacenter.ListResult, error) {
	var models []datacenterModel

	position, err := pagination.Decode(opts.Cursor)
	if err != nil {
		return nil, err
	}

	q := s.pg.NewSelect(&models)
	argCount := 0
	q = q.Where(fmt.Sprintf("(tenant_id = $%d OR tenant_id = '')", argCount+1), tenantID)

	argCount += 1
	if opts.Status != "" {
		q = q.Where(fmt.Sprintf("status = $%d", argCount+1), opts.Status)
		argCount += 1
	}

	if opts.Provider != "" {
		q = q.Where(fmt.Sprintf("provider_name = $%d", argCount+1), opts.Provider)
		argCount += 1
	}

	if opts.Region != "" {
		q = q.Where(fmt.Sprintf("region = $%d", argCount+1), opts.Region)
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
		return nil, fmt.Errorf("postgres: count ListDatacenters: %w", err)
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
		return nil, fmt.Errorf("postgres: list ListDatacenters: %w", err)
	}

	items := make([]*datacenter.Datacenter, 0, len(models))
	for i := range models {
		items = append(items, fromDatacenterModel(&models[i]))
	}

	items, next := pagination.Trim(items, pageLimit, func(v *datacenter.Datacenter) ctrlplane.Entity { return v.Entity })

	return &datacenter.ListResult{Items: items, Total: int(total), NextCursor: next}, nil
}

// UpdateDatacenter persists changes to an existing datacenter.
func (s *Store) UpdateDatacenter(ctx context.Context, dc *datacenter.Datacenter) error {
	dc.UpdatedAt = now()
	model := toDatacenterModel(dc)

	_, err := s.pg.NewUpdate(model).WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: update datacenter: %w", err)
	}

	return nil
}

// DeleteDatacenter removes a datacenter from the store.
func (s *Store) DeleteDatacenter(ctx context.Context, tenantID string, datacenterID id.ID) error {
	_, err := s.pg.NewDelete(&datacenterModel{}).
		Where("id = $1 AND tenant_id = $2", datacenterID.String(), tenantID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: delete datacenter: %w", err)
	}

	return nil
}

// CountDatacentersByTenant returns the total number of datacenters for a tenant.
func (s *Store) CountDatacentersByTenant(ctx context.Context, tenantID string) (int, error) {
	count, err := s.pg.NewSelect(&datacenterModel{}).
		Where("tenant_id = $1", tenantID).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgres: count datacenters: %w", err)
	}

	return int(count), nil
}

// CountInstancesByDatacenter returns the number of instances linked to a datacenter.
func (s *Store) CountInstancesByDatacenter(ctx context.Context, tenantID string, datacenterID id.ID) (int, error) {
	count, err := s.pg.NewSelect(&instanceModel{}).
		Where("tenant_id = $1 AND datacenter_id = $2", tenantID, datacenterID.String()).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgres: count instances by datacenter: %w", err)
	}

	return int(count), nil
}
