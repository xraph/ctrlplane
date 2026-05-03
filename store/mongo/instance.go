package mongo

import (
	"context"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
)

func (s *Store) Insert(ctx context.Context, inst *instance.Instance) error {
	model := toInstanceModel(inst)

	_, err := s.mdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: insert instance failed: %w", err)
	}

	return nil
}

func (s *Store) GetByID(ctx context.Context, tenantID string, instanceID id.ID) (*instance.Instance, error) {
	var model instanceModel

	// Empty tenantID is the cross-tenant convention: the caller
	// (typically a system admin via the dashboard) wants the
	// instance regardless of which tenant owns it. Service-level
	// gating ensures only privileged callers can pass empty tenant.
	filter := bson.M{"_id": instanceID.String()}
	if tenantID != "" {
		filter["tenant_id"] = tenantID
	}

	err := s.mdb.NewFind(&model).
		Filter(filter).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, fmt.Errorf("%w: instance %s", ctrlplane.ErrNotFound, instanceID)
		}

		return nil, fmt.Errorf("mongo: get instance failed: %w", err)
	}

	return fromInstanceModel(&model), nil
}

func (s *Store) GetBySlug(ctx context.Context, tenantID string, slug string) (*instance.Instance, error) {
	var model instanceModel

	err := s.mdb.NewFind(&model).
		Filter(bson.M{"tenant_id": tenantID, "slug": slug}).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, fmt.Errorf("%w: slug %s", ctrlplane.ErrNotFound, slug)
		}

		return nil, fmt.Errorf("mongo: get instance by slug failed: %w", err)
	}

	return fromInstanceModel(&model), nil
}

func (s *Store) List(ctx context.Context, tenantID string, opts instance.ListOptions) (*instance.ListResult, error) {
	var models []instanceModel

	// Empty tenantID = cross-tenant view (admin dashboard pattern).
	// Service-level gating decides whether the caller is allowed
	// to pass empty; here we just honour the contract.
	f := bson.M{}
	if tenantID != "" {
		f["tenant_id"] = tenantID
	}

	if opts.State != "" {
		f["state"] = opts.State
	}

	if opts.Provider != "" {
		f["provider_name"] = opts.Provider
	}

	// opts.Label is "key=value". The label key may contain dots
	// (e.g. "ctrlplane.workload"), which mongo's path syntax would
	// otherwise interpret as nested-field access. Use $expr +
	// $getField so the dotted key is treated as a literal map key
	// inside the labels sub-document.
	if opts.Label != "" {
		if key, val, ok := strings.Cut(opts.Label, "="); ok && key != "" {
			f["$expr"] = bson.M{
				"$eq": bson.A{
					bson.M{"$getField": bson.M{"field": key, "input": "$labels"}},
					val,
				},
			}
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
		return nil, fmt.Errorf("mongo: list instances failed: %w", err)
	}

	// Count total matching records.
	total, err := s.mdb.NewFind((*instanceModel)(nil)).
		Filter(f).
		Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("mongo: count instances failed: %w", err)
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

	res, err := s.mdb.NewUpdate(model).
		Filter(bson.M{"_id": model.ID}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: update instance failed: %w", err)
	}

	if res.MatchedCount() == 0 {
		return fmt.Errorf("%w: instance %s", ctrlplane.ErrNotFound, inst.ID)
	}

	return nil
}

func (s *Store) Delete(ctx context.Context, tenantID string, instanceID id.ID) error {
	res, err := s.mdb.NewDelete((*instanceModel)(nil)).
		Filter(bson.M{"_id": instanceID.String(), "tenant_id": tenantID}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: delete instance failed: %w", err)
	}

	if res.DeletedCount() == 0 {
		return fmt.Errorf("%w: instance %s", ctrlplane.ErrNotFound, instanceID)
	}

	return nil
}

func (s *Store) CountByTenant(ctx context.Context, tenantID string) (int, error) {
	count, err := s.mdb.NewFind((*instanceModel)(nil)).
		Filter(bson.M{"tenant_id": tenantID}).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("mongo: count instances failed: %w", err)
	}

	return int(count), nil
}
