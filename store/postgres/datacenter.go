package postgres

import (
	"context"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/datacenter"
	"github.com/xraph/ctrlplane/id"
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
// it belongs to tenantID OR when it's platform-shared (TenantID = '').
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
// tenant-owned and platform-shared (TenantID = '').
func (s *Store) ListDatacenters(ctx context.Context, tenantID string, opts datacenter.ListOptions) (*datacenter.ListResult, error) {
	var models []datacenterModel

	q := s.pg.NewSelect(&models).Where("tenant_id = $1 OR tenant_id = ''", tenantID)

	if opts.Status != "" {
		q = q.Where("status = $1", opts.Status)
	}

	if opts.Provider != "" {
		q = q.Where("provider_name = $1", opts.Provider)
	}

	if opts.Region != "" {
		q = q.Where("region = $1", opts.Region)
	}

	q = q.OrderExpr("created_at DESC")

	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("postgres: list datacenters: %w", err)
	}

	items := make([]*datacenter.Datacenter, 0, len(models))
	for _, m := range models {
		items = append(items, fromDatacenterModel(&m))
	}

	return &datacenter.ListResult{
		Items: items,
		Total: len(items),
	}, nil
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
