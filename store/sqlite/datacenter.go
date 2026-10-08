package sqlite

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/datacenter"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/pagination"
)

// InsertDatacenter persists a new datacenter.
func (s *Store) InsertDatacenter(ctx context.Context, dc *datacenter.Datacenter) error {
	model := toDatacenterModel(dc)

	_, err := s.sdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: insert datacenter: %w", err)
	}

	return nil
}

// GetDatacenterByID retrieves a datacenter by ID. Returns the DC when
// it belongs to tenantID OR when it's platform-shared (TenantID = "").
func (s *Store) GetDatacenterByID(ctx context.Context, tenantID string, datacenterID id.ID) (*datacenter.Datacenter, error) {
	var model datacenterModel

	err := s.sdb.NewSelect(&model).
		Where("id = ? AND (tenant_id = ? OR tenant_id = '')", datacenterID.String(), tenantID).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: datacenter %s", ctrlplane.ErrNotFound, datacenterID)
		}

		return nil, fmt.Errorf("sqlite: get datacenter: %w", err)
	}

	return fromDatacenterModel(&model), nil
}

// GetDatacenterBySlug retrieves a datacenter by slug. Tenant-scoped
// hits take precedence over platform-shared ones with the same slug.
func (s *Store) GetDatacenterBySlug(ctx context.Context, tenantID string, slug string) (*datacenter.Datacenter, error) {
	var models []datacenterModel

	err := s.sdb.NewSelect(&models).
		Where("slug = ? AND (tenant_id = ? OR tenant_id = '')", slug, tenantID).
		Limit(2).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlite: get datacenter by slug: %w", err)
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
// tenant-owned and platform-shared (TenantID = "").
func (s *Store) ListDatacenters(ctx context.Context, tenantID string, opts datacenter.ListOptions) (*datacenter.ListResult, error) {
	var models []datacenterModel

	position, err := pagination.Decode(opts.Cursor)
	if err != nil {
		return nil, err
	}

	q := s.sdb.NewSelect(&models)

	q = q.Where("(tenant_id = ? OR tenant_id = '')", tenantID)
	if opts.Status != "" {
		q = q.Where("status = ?", opts.Status)
	}

	if opts.Provider != "" {
		q = q.Where("provider_name = ?", opts.Provider)
	}

	if opts.Region != "" {
		q = q.Where("region = ?", opts.Region)
	}

	if opts.Label != "" {
		if key, val, ok := strings.Cut(opts.Label, "="); ok && key != "" {
			q = q.Where("json_extract(labels, ?) = ?", "$."+strconv.Quote(key), val)
		}
	}

	total, err := q.Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlite: count ListDatacenters: %w", err)
	}

	if opts.Cursor != "" {
		q = q.Where("(created_at < ? OR (created_at = ? AND id < ?))", position.CreatedAt, position.CreatedAt, position.ID.String())
	}

	q = q.OrderExpr("created_at DESC, id DESC")

	pageLimit := opts.Limit
	if pageLimit > 0 {
		q = q.Limit(pageLimit + 1)
	}

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("sqlite: list ListDatacenters: %w", err)
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

	_, err := s.sdb.NewUpdate(model).WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: update datacenter: %w", err)
	}

	return nil
}

// DeleteDatacenter removes a datacenter from the store.
func (s *Store) DeleteDatacenter(ctx context.Context, tenantID string, datacenterID id.ID) error {
	_, err := s.sdb.NewDelete(&datacenterModel{}).
		Where("id = ? AND tenant_id = ?", datacenterID.String(), tenantID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: delete datacenter: %w", err)
	}

	return nil
}

// CountDatacentersByTenant returns the total number of datacenters for a tenant.
func (s *Store) CountDatacentersByTenant(ctx context.Context, tenantID string) (int, error) {
	count, err := s.sdb.NewSelect(&datacenterModel{}).
		Where("tenant_id = ?", tenantID).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("sqlite: count datacenters: %w", err)
	}

	return int(count), nil
}

// CountInstancesByDatacenter returns the number of instances linked to a datacenter.
func (s *Store) CountInstancesByDatacenter(ctx context.Context, tenantID string, datacenterID id.ID) (int, error) {
	count, err := s.sdb.NewSelect(&instanceModel{}).
		Where("tenant_id = ? AND datacenter_id = ?", tenantID, datacenterID.String()).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("sqlite: count instances by datacenter: %w", err)
	}

	return int(count), nil
}
