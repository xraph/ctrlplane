package mongo

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/admin"
)

// InsertTenant persists a new tenant.
func (s *Store) InsertTenant(ctx context.Context, tenant *admin.Tenant) error {
	m := toTenantModel(tenant)

	_, err := s.col(colTenants).InsertOne(ctx, m)
	if err != nil {
		if isDuplicateKeyError(err) {
			return fmt.Errorf("mongo: insert tenant: %w: %s", ctrlplane.ErrAlreadyExists, m.ID)
		}

		return fmt.Errorf("mongo: insert tenant: %w", err)
	}

	return nil
}

// GetTenant retrieves a tenant by ID.
func (s *Store) GetTenant(ctx context.Context, tenantID string) (*admin.Tenant, error) {
	filter := bson.D{{Key: "_id", Value: tenantID}}

	var m tenantModel

	err := s.col(colTenants).FindOne(ctx, filter).Decode(&m)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("mongo: get tenant: %w: %s", ctrlplane.ErrNotFound, tenantID)
		}

		return nil, fmt.Errorf("mongo: get tenant: %w", err)
	}

	return fromTenantModel(&m), nil
}

// GetTenantBySlug retrieves a tenant by slug.
func (s *Store) GetTenantBySlug(ctx context.Context, slug string) (*admin.Tenant, error) {
	filter := bson.D{{Key: "slug", Value: slug}}

	var m tenantModel

	err := s.col(colTenants).FindOne(ctx, filter).Decode(&m)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("mongo: get tenant by slug: %w: %s", ctrlplane.ErrNotFound, slug)
		}

		return nil, fmt.Errorf("mongo: get tenant by slug: %w", err)
	}

	return fromTenantModel(&m), nil
}

// ListTenants returns tenants with optional filtering.
func (s *Store) ListTenants(ctx context.Context, opts admin.ListTenantsOptions) (*admin.TenantListResult, error) {
	filter := bson.D{}

	if opts.Status != "" {
		filter = append(filter, bson.E{Key: "status", Value: opts.Status})
	}

	total, err := s.col(colTenants).CountDocuments(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("mongo: list tenants count: %w", err)
	}

	findOpts := options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}})

	if opts.Limit > 0 {
		findOpts.SetLimit(int64(opts.Limit))
	}

	cursor, err := s.col(colTenants).Find(ctx, filter, findOpts)
	if err != nil {
		return nil, fmt.Errorf("mongo: list tenants: %w", err)
	}
	defer cursor.Close(ctx)

	items := make([]*admin.Tenant, 0)

	for cursor.Next(ctx) {
		var m tenantModel

		if err := cursor.Decode(&m); err != nil {
			return nil, fmt.Errorf("mongo: list tenants decode: %w", err)
		}

		items = append(items, fromTenantModel(&m))
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("mongo: list tenants cursor: %w", err)
	}

	return &admin.TenantListResult{
		Items: items,
		Total: int(total),
	}, nil
}

// UpdateTenant persists changes to a tenant.
func (s *Store) UpdateTenant(ctx context.Context, tenant *admin.Tenant) error {
	tenant.UpdatedAt = now()
	m := toTenantModel(tenant)

	result, err := s.col(colTenants).ReplaceOne(
		ctx,
		bson.D{{Key: "_id", Value: m.ID}},
		m,
	)
	if err != nil {
		return fmt.Errorf("mongo: update tenant: %w", err)
	}

	if result.MatchedCount == 0 {
		return fmt.Errorf("mongo: update tenant: %w: %s", ctrlplane.ErrNotFound, m.ID)
	}

	return nil
}

// DeleteTenant removes a tenant.
func (s *Store) DeleteTenant(ctx context.Context, tenantID string) error {
	result, err := s.col(colTenants).DeleteOne(ctx, bson.D{{Key: "_id", Value: tenantID}})
	if err != nil {
		return fmt.Errorf("mongo: delete tenant: %w", err)
	}

	if result.DeletedCount == 0 {
		return fmt.Errorf("mongo: delete tenant: %w: %s", ctrlplane.ErrNotFound, tenantID)
	}

	return nil
}

// CountTenants returns the total number of tenants.
func (s *Store) CountTenants(ctx context.Context) (int, error) {
	count, err := s.col(colTenants).CountDocuments(ctx, bson.D{})
	if err != nil {
		return 0, fmt.Errorf("mongo: count tenants: %w", err)
	}

	return int(count), nil
}

// CountTenantsByStatus returns the number of tenants in a given status.
func (s *Store) CountTenantsByStatus(ctx context.Context, status admin.TenantStatus) (int, error) {
	filter := bson.D{{Key: "status", Value: string(status)}}

	count, err := s.col(colTenants).CountDocuments(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("mongo: count tenants by status: %w", err)
	}

	return int(count), nil
}

// InsertAuditEntry persists an audit log entry.
func (s *Store) InsertAuditEntry(ctx context.Context, entry *admin.AuditEntry) error {
	m := toAuditEntryModel(entry)

	_, err := s.col(colAuditEntries).InsertOne(ctx, m)
	if err != nil {
		return fmt.Errorf("mongo: insert audit entry: %w", err)
	}

	return nil
}

// QueryAuditLog returns audit entries matching the query.
func (s *Store) QueryAuditLog(ctx context.Context, opts admin.AuditQuery) (*admin.AuditResult, error) {
	filter := bson.D{}

	if opts.TenantID != "" {
		filter = append(filter, bson.E{Key: "tenant_id", Value: opts.TenantID})
	}

	if opts.ActorID != "" {
		filter = append(filter, bson.E{Key: "actor_id", Value: opts.ActorID})
	}

	if opts.Resource != "" {
		filter = append(filter, bson.E{Key: "resource", Value: opts.Resource})
	}

	if opts.Action != "" {
		filter = append(filter, bson.E{Key: "action", Value: opts.Action})
	}

	if !opts.Since.IsZero() {
		filter = append(filter, bson.E{Key: "created_at", Value: bson.D{{Key: "$gte", Value: opts.Since}}})
	}

	if !opts.Until.IsZero() {
		filter = append(filter, bson.E{Key: "created_at", Value: bson.D{{Key: "$lte", Value: opts.Until}}})
	}

	total, err := s.col(colAuditEntries).CountDocuments(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("mongo: query audit log count: %w", err)
	}

	findOpts := options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}})

	if opts.Limit > 0 {
		findOpts.SetLimit(int64(opts.Limit))
	}

	cursor, err := s.col(colAuditEntries).Find(ctx, filter, findOpts)
	if err != nil {
		return nil, fmt.Errorf("mongo: query audit log: %w", err)
	}
	defer cursor.Close(ctx)

	items := make([]admin.AuditEntry, 0)

	for cursor.Next(ctx) {
		var m auditEntryModel

		if err := cursor.Decode(&m); err != nil {
			return nil, fmt.Errorf("mongo: query audit log decode: %w", err)
		}

		items = append(items, fromAuditEntryModel(&m))
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("mongo: query audit log cursor: %w", err)
	}

	return &admin.AuditResult{
		Items: items,
		Total: int(total),
	}, nil
}
