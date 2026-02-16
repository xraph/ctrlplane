package mongo

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
)

// Insert persists a new instance.
func (s *Store) Insert(ctx context.Context, inst *instance.Instance) error {
	m := toInstanceModel(inst)

	_, err := s.col(colInstances).InsertOne(ctx, m)
	if err != nil {
		if isDuplicateKeyError(err) {
			return fmt.Errorf("mongo: insert instance: %w: %s", ctrlplane.ErrAlreadyExists, m.ID)
		}

		return fmt.Errorf("mongo: insert instance: %w", err)
	}

	return nil
}

// GetByID retrieves an instance by its ID within a tenant.
func (s *Store) GetByID(ctx context.Context, tenantID string, instanceID id.ID) (*instance.Instance, error) {
	filter := bson.D{
		{Key: "_id", Value: idStr(instanceID)},
		{Key: "tenant_id", Value: tenantID},
	}

	var m instanceModel

	err := s.col(colInstances).FindOne(ctx, filter).Decode(&m)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("mongo: get instance: %w: %s", ctrlplane.ErrNotFound, instanceID)
		}

		return nil, fmt.Errorf("mongo: get instance: %w", err)
	}

	return fromInstanceModel(&m), nil
}

// GetBySlug retrieves an instance by its slug within a tenant.
func (s *Store) GetBySlug(ctx context.Context, tenantID string, slug string) (*instance.Instance, error) {
	filter := bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "slug", Value: slug},
	}

	var m instanceModel

	err := s.col(colInstances).FindOne(ctx, filter).Decode(&m)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("mongo: get instance by slug: %w: %s", ctrlplane.ErrNotFound, slug)
		}

		return nil, fmt.Errorf("mongo: get instance by slug: %w", err)
	}

	return fromInstanceModel(&m), nil
}

// List returns a filtered, paginated list of instances for a tenant.
func (s *Store) List(ctx context.Context, tenantID string, opts instance.ListOptions) (*instance.ListResult, error) {
	filter := bson.D{{Key: "tenant_id", Value: tenantID}}

	if opts.State != "" {
		filter = append(filter, bson.E{Key: "state", Value: opts.State})
	}

	if opts.Provider != "" {
		filter = append(filter, bson.E{Key: "provider_name", Value: opts.Provider})
	}

	total, err := s.col(colInstances).CountDocuments(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("mongo: list instances count: %w", err)
	}

	findOpts := options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}})

	if opts.Limit > 0 {
		findOpts.SetLimit(int64(opts.Limit))
	}

	cursor, err := s.col(colInstances).Find(ctx, filter, findOpts)
	if err != nil {
		return nil, fmt.Errorf("mongo: list instances: %w", err)
	}
	defer cursor.Close(ctx)

	items := make([]*instance.Instance, 0)

	for cursor.Next(ctx) {
		var m instanceModel

		if err := cursor.Decode(&m); err != nil {
			return nil, fmt.Errorf("mongo: list instances decode: %w", err)
		}

		items = append(items, fromInstanceModel(&m))
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("mongo: list instances cursor: %w", err)
	}

	return &instance.ListResult{
		Items: items,
		Total: int(total),
	}, nil
}

// Update persists changes to an existing instance.
func (s *Store) Update(ctx context.Context, inst *instance.Instance) error {
	inst.UpdatedAt = now()
	m := toInstanceModel(inst)

	result, err := s.col(colInstances).ReplaceOne(
		ctx,
		bson.D{{Key: "_id", Value: m.ID}},
		m,
	)
	if err != nil {
		return fmt.Errorf("mongo: update instance: %w", err)
	}

	if result.MatchedCount == 0 {
		return fmt.Errorf("mongo: update instance: %w: %s", ctrlplane.ErrNotFound, m.ID)
	}

	return nil
}

// Delete removes an instance from the store.
func (s *Store) Delete(ctx context.Context, tenantID string, instanceID id.ID) error {
	filter := bson.D{
		{Key: "_id", Value: idStr(instanceID)},
		{Key: "tenant_id", Value: tenantID},
	}

	result, err := s.col(colInstances).DeleteOne(ctx, filter)
	if err != nil {
		return fmt.Errorf("mongo: delete instance: %w", err)
	}

	if result.DeletedCount == 0 {
		return fmt.Errorf("mongo: delete instance: %w: %s", ctrlplane.ErrNotFound, instanceID)
	}

	return nil
}

// CountByTenant returns the total number of instances for a tenant.
func (s *Store) CountByTenant(ctx context.Context, tenantID string) (int, error) {
	filter := bson.D{{Key: "tenant_id", Value: tenantID}}

	count, err := s.col(colInstances).CountDocuments(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("mongo: count instances: %w", err)
	}

	return int(count), nil
}
