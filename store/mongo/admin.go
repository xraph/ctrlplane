package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/admin"
)

func (s *Store) InsertTenant(ctx context.Context, tenant *admin.Tenant) error {
	model := toTenantModel(tenant)

	_, err := s.mdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: insert tenant failed: %w", err)
	}

	return nil
}

func (s *Store) GetTenant(ctx context.Context, tenantID string) (*admin.Tenant, error) {
	var model tenantModel

	err := s.mdb.NewFind(&model).
		Filter(bson.M{"_id": tenantID}).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, fmt.Errorf("%w: tenant %s", ctrlplane.ErrNotFound, tenantID)
		}

		return nil, fmt.Errorf("mongo: get tenant failed: %w", err)
	}

	return fromTenantModel(&model), nil
}

func (s *Store) GetTenantBySlug(ctx context.Context, slug string) (*admin.Tenant, error) {
	var model tenantModel

	err := s.mdb.NewFind(&model).
		Filter(bson.M{"slug": slug}).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, fmt.Errorf("%w: slug %s", ctrlplane.ErrNotFound, slug)
		}

		return nil, fmt.Errorf("mongo: get tenant by slug failed: %w", err)
	}

	return fromTenantModel(&model), nil
}

func (s *Store) ListTenants(ctx context.Context, opts admin.ListTenantsOptions) (*admin.TenantListResult, error) {
	var models []tenantModel

	f := bson.M{}
	if opts.Status != "" {
		f["status"] = opts.Status
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	err := s.mdb.NewFind(&models).
		Filter(f).
		Sort(bson.D{{Key: "created_at", Value: -1}}).
		Limit(int64(limit)).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("mongo: list tenants failed: %w", err)
	}

	// Count total.
	total, err := s.mdb.NewFind((*tenantModel)(nil)).
		Filter(f).
		Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("mongo: count tenants failed: %w", err)
	}

	items := make([]*admin.Tenant, 0, len(models))
	for i := range models {
		items = append(items, fromTenantModel(&models[i]))
	}

	return &admin.TenantListResult{
		Items: items,
		Total: int(total),
	}, nil
}

func (s *Store) UpdateTenant(ctx context.Context, tenant *admin.Tenant) error {
	tenant.UpdatedAt = now()
	model := toTenantModel(tenant)

	res, err := s.mdb.NewUpdate(model).
		Filter(bson.M{"_id": model.ID}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: update tenant failed: %w", err)
	}

	if res.MatchedCount() == 0 {
		return fmt.Errorf("%w: tenant %s", ctrlplane.ErrNotFound, tenant.ID)
	}

	return nil
}

func (s *Store) DeleteTenant(ctx context.Context, tenantID string) error {
	res, err := s.mdb.NewDelete((*tenantModel)(nil)).
		Filter(bson.M{"_id": tenantID}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: delete tenant failed: %w", err)
	}

	if res.DeletedCount() == 0 {
		return fmt.Errorf("%w: tenant %s", ctrlplane.ErrNotFound, tenantID)
	}

	return nil
}

func (s *Store) CountTenants(ctx context.Context) (int, error) {
	count, err := s.mdb.NewFind((*tenantModel)(nil)).
		Filter(bson.M{}).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("mongo: count tenants failed: %w", err)
	}

	return int(count), nil
}

func (s *Store) CountTenantsByStatus(ctx context.Context, status admin.TenantStatus) (int, error) {
	count, err := s.mdb.NewFind((*tenantModel)(nil)).
		Filter(bson.M{"status": string(status)}).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("mongo: count tenants by status failed: %w", err)
	}

	return int(count), nil
}

func (s *Store) InsertAuditEntry(ctx context.Context, entry *admin.AuditEntry) error {
	model := toAuditEntryModel(entry)

	_, err := s.mdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: insert audit entry failed: %w", err)
	}

	return nil
}

func (s *Store) QueryAuditLog(ctx context.Context, opts admin.AuditQuery) (*admin.AuditResult, error) {
	var models []auditEntryModel

	f := bson.M{}
	if opts.TenantID != "" {
		f["tenant_id"] = opts.TenantID
	}

	if opts.ActorID != "" {
		f["actor_id"] = opts.ActorID
	}

	if opts.Action != "" {
		f["action"] = opts.Action
	}

	if opts.Resource != "" {
		f["resource"] = opts.Resource
	}

	if !opts.Since.IsZero() {
		f["created_at"] = bson.M{"$gte": opts.Since}
	}

	if !opts.Until.IsZero() {
		if existing, ok := f["created_at"]; ok {
			existing.(bson.M)["$lte"] = opts.Until
		} else {
			f["created_at"] = bson.M{"$lte": opts.Until}
		}
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	err := s.mdb.NewFind(&models).
		Filter(f).
		Sort(bson.D{{Key: "created_at", Value: -1}}).
		Limit(int64(limit)).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("mongo: query audit log failed: %w", err)
	}

	items := make([]admin.AuditEntry, 0, len(models))
	for i := range models {
		items = append(items, fromAuditEntryModel(&models[i]))
	}

	return &admin.AuditResult{
		Items: items,
		Total: len(items),
	}, nil
}
