package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/datacenter"
	"github.com/xraph/ctrlplane/id"
)

const colDatacenters = "cp_datacenters"

// InsertDatacenter persists a new datacenter.
func (s *Store) InsertDatacenter(ctx context.Context, dc *datacenter.Datacenter) error {
	model := toDatacenterModel(dc)

	_, err := s.mdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: insert datacenter: %w", err)
	}

	return nil
}

// GetDatacenterByID retrieves a datacenter by ID within a tenant.
func (s *Store) GetDatacenterByID(ctx context.Context, tenantID string, datacenterID id.ID) (*datacenter.Datacenter, error) {
	var model datacenterModel

	err := s.mdb.NewFind(&model).
		Filter(bson.M{"_id": datacenterID.String(), "tenant_id": tenantID}).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, fmt.Errorf("%w: datacenter %s", ctrlplane.ErrNotFound, datacenterID)
		}

		return nil, fmt.Errorf("mongo: get datacenter: %w", err)
	}

	return fromDatacenterModel(&model), nil
}

// GetDatacenterBySlug retrieves a datacenter by slug within a tenant.
func (s *Store) GetDatacenterBySlug(ctx context.Context, tenantID string, slug string) (*datacenter.Datacenter, error) {
	var model datacenterModel

	err := s.mdb.NewFind(&model).
		Filter(bson.M{"tenant_id": tenantID, "slug": slug}).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, fmt.Errorf("%w: datacenter slug %s", ctrlplane.ErrNotFound, slug)
		}

		return nil, fmt.Errorf("mongo: get datacenter by slug: %w", err)
	}

	return fromDatacenterModel(&model), nil
}

// ListDatacenters returns a filtered, paginated list of datacenters for a tenant.
func (s *Store) ListDatacenters(ctx context.Context, tenantID string, opts datacenter.ListOptions) (*datacenter.ListResult, error) {
	var models []datacenterModel

	filter := bson.M{"tenant_id": tenantID}

	if opts.Status != "" {
		filter["status"] = opts.Status
	}

	if opts.Provider != "" {
		filter["provider_name"] = opts.Provider
	}

	if opts.Region != "" {
		filter["region"] = opts.Region
	}

	q := s.mdb.NewFind(&models).
		Filter(filter).
		Sort(bson.D{{Key: "created_at", Value: -1}})

	if opts.Limit > 0 {
		q = q.Limit(int64(opts.Limit))
	}

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("mongo: list datacenters: %w", err)
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

	_, err := s.mdb.NewUpdate(model).
		Filter(bson.M{"_id": model.ID}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: update datacenter: %w", err)
	}

	return nil
}

// DeleteDatacenter removes a datacenter from the store.
func (s *Store) DeleteDatacenter(ctx context.Context, tenantID string, datacenterID id.ID) error {
	_, err := s.mdb.NewDelete(&datacenterModel{}).
		Filter(bson.M{"_id": datacenterID.String(), "tenant_id": tenantID}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: delete datacenter: %w", err)
	}

	return nil
}

// CountDatacentersByTenant returns the total number of datacenters for a tenant.
func (s *Store) CountDatacentersByTenant(ctx context.Context, tenantID string) (int, error) {
	count, err := s.mdb.Collection(colDatacenters).CountDocuments(ctx, bson.M{"tenant_id": tenantID})
	if err != nil {
		return 0, fmt.Errorf("mongo: count datacenters: %w", err)
	}

	return int(count), nil
}

// CountInstancesByDatacenter returns the number of instances linked to a datacenter.
func (s *Store) CountInstancesByDatacenter(ctx context.Context, tenantID string, datacenterID id.ID) (int, error) {
	count, err := s.mdb.Collection(colInstances).CountDocuments(ctx, bson.M{
		"tenant_id":     tenantID,
		"datacenter_id": datacenterID.String(),
	})
	if err != nil {
		return 0, fmt.Errorf("mongo: count instances by datacenter: %w", err)
	}

	return int(count), nil
}
