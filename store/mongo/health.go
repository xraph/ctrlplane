package mongo

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/health"
	"github.com/xraph/ctrlplane/id"
)

// InsertCheck persists a new health check configuration.
func (s *Store) InsertCheck(ctx context.Context, check *health.HealthCheck) error {
	m := toHealthCheckModel(check)

	_, err := s.col(colHealthChecks).InsertOne(ctx, m)
	if err != nil {
		if isDuplicateKeyError(err) {
			return fmt.Errorf("mongo: insert health check: %w: %s", ctrlplane.ErrAlreadyExists, m.ID)
		}

		return fmt.Errorf("mongo: insert health check: %w", err)
	}

	return nil
}

// GetCheck retrieves a health check by ID.
func (s *Store) GetCheck(ctx context.Context, tenantID string, checkID id.ID) (*health.HealthCheck, error) {
	filter := bson.D{
		{Key: "_id", Value: idStr(checkID)},
		{Key: "tenant_id", Value: tenantID},
	}

	var m healthCheckModel

	err := s.col(colHealthChecks).FindOne(ctx, filter).Decode(&m)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("mongo: get health check: %w: %s", ctrlplane.ErrNotFound, checkID)
		}

		return nil, fmt.Errorf("mongo: get health check: %w", err)
	}

	return fromHealthCheckModel(&m), nil
}

// ListChecks returns all health checks for an instance.
func (s *Store) ListChecks(ctx context.Context, tenantID string, instanceID id.ID) ([]health.HealthCheck, error) {
	filter := bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "instance_id", Value: idStr(instanceID)},
	}

	cursor, err := s.col(colHealthChecks).Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("mongo: list health checks: %w", err)
	}
	defer cursor.Close(ctx)

	items := make([]health.HealthCheck, 0)

	for cursor.Next(ctx) {
		var m healthCheckModel

		if err := cursor.Decode(&m); err != nil {
			return nil, fmt.Errorf("mongo: list health checks decode: %w", err)
		}

		items = append(items, *fromHealthCheckModel(&m))
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("mongo: list health checks cursor: %w", err)
	}

	return items, nil
}

// UpdateCheck persists changes to a health check.
func (s *Store) UpdateCheck(ctx context.Context, check *health.HealthCheck) error {
	check.UpdatedAt = now()
	m := toHealthCheckModel(check)

	result, err := s.col(colHealthChecks).ReplaceOne(
		ctx,
		bson.D{{Key: "_id", Value: m.ID}},
		m,
	)
	if err != nil {
		return fmt.Errorf("mongo: update health check: %w", err)
	}

	if result.MatchedCount == 0 {
		return fmt.Errorf("mongo: update health check: %w: %s", ctrlplane.ErrNotFound, m.ID)
	}

	return nil
}

// DeleteCheck removes a health check.
func (s *Store) DeleteCheck(ctx context.Context, tenantID string, checkID id.ID) error {
	filter := bson.D{
		{Key: "_id", Value: idStr(checkID)},
		{Key: "tenant_id", Value: tenantID},
	}

	result, err := s.col(colHealthChecks).DeleteOne(ctx, filter)
	if err != nil {
		return fmt.Errorf("mongo: delete health check: %w", err)
	}

	if result.DeletedCount == 0 {
		return fmt.Errorf("mongo: delete health check: %w: %s", ctrlplane.ErrNotFound, checkID)
	}

	return nil
}

// InsertResult persists a health check result.
func (s *Store) InsertResult(ctx context.Context, result *health.HealthResult) error {
	m := toHealthResultModel(result)

	_, err := s.col(colHealthResults).InsertOne(ctx, m)
	if err != nil {
		return fmt.Errorf("mongo: insert health result: %w", err)
	}

	return nil
}

// ListResults returns health results for a check within a time range.
func (s *Store) ListResults(ctx context.Context, tenantID string, checkID id.ID, opts health.HistoryOptions) ([]health.HealthResult, error) {
	filter := bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "check_id", Value: idStr(checkID)},
	}

	if !opts.Since.IsZero() {
		filter = append(filter, bson.E{Key: "checked_at", Value: bson.D{{Key: "$gte", Value: opts.Since}}})
	}

	if !opts.Until.IsZero() {
		filter = append(filter, bson.E{Key: "checked_at", Value: bson.D{{Key: "$lte", Value: opts.Until}}})
	}

	findOpts := options.Find().
		SetSort(bson.D{{Key: "checked_at", Value: -1}})

	if opts.Limit > 0 {
		findOpts.SetLimit(int64(opts.Limit))
	}

	cursor, err := s.col(colHealthResults).Find(ctx, filter, findOpts)
	if err != nil {
		return nil, fmt.Errorf("mongo: list health results: %w", err)
	}
	defer cursor.Close(ctx)

	items := make([]health.HealthResult, 0)

	for cursor.Next(ctx) {
		var m healthResultModel

		if err := cursor.Decode(&m); err != nil {
			return nil, fmt.Errorf("mongo: list health results decode: %w", err)
		}

		items = append(items, *fromHealthResultModel(&m))
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("mongo: list health results cursor: %w", err)
	}

	return items, nil
}

// GetLatestResult returns the most recent result for a check.
func (s *Store) GetLatestResult(ctx context.Context, tenantID string, checkID id.ID) (*health.HealthResult, error) {
	filter := bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "check_id", Value: idStr(checkID)},
	}

	findOpts := options.FindOne().
		SetSort(bson.D{{Key: "checked_at", Value: -1}})

	var m healthResultModel

	err := s.col(colHealthResults).FindOne(ctx, filter, findOpts).Decode(&m)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("mongo: get latest health result: %w: %s", ctrlplane.ErrNotFound, checkID)
		}

		return nil, fmt.Errorf("mongo: get latest health result: %w", err)
	}

	return fromHealthResultModel(&m), nil
}
