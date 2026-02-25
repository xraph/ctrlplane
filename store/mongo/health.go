package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/health"
	"github.com/xraph/ctrlplane/id"
)

func (s *Store) InsertCheck(ctx context.Context, check *health.HealthCheck) error {
	model := toHealthCheckModel(check)

	_, err := s.mdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: insert health check failed: %w", err)
	}

	return nil
}

func (s *Store) GetCheck(ctx context.Context, tenantID string, checkID id.ID) (*health.HealthCheck, error) {
	var model healthCheckModel

	err := s.mdb.NewFind(&model).
		Filter(bson.M{"_id": checkID.String(), "tenant_id": tenantID}).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, fmt.Errorf("%w: health check %s", ctrlplane.ErrNotFound, checkID)
		}

		return nil, fmt.Errorf("mongo: get health check failed: %w", err)
	}

	return fromHealthCheckModel(&model), nil
}

func (s *Store) ListChecks(ctx context.Context, tenantID string, instanceID id.ID) ([]health.HealthCheck, error) {
	var models []healthCheckModel

	err := s.mdb.NewFind(&models).
		Filter(bson.M{"tenant_id": tenantID, "instance_id": instanceID.String()}).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("mongo: list health checks failed: %w", err)
	}

	items := make([]health.HealthCheck, 0, len(models))
	for i := range models {
		items = append(items, *fromHealthCheckModel(&models[i]))
	}

	return items, nil
}

func (s *Store) UpdateCheck(ctx context.Context, check *health.HealthCheck) error {
	check.UpdatedAt = now()
	model := toHealthCheckModel(check)

	res, err := s.mdb.NewUpdate(model).
		Filter(bson.M{"_id": model.ID}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: update health check failed: %w", err)
	}

	if res.MatchedCount() == 0 {
		return fmt.Errorf("%w: health check %s", ctrlplane.ErrNotFound, check.ID)
	}

	return nil
}

func (s *Store) DeleteCheck(ctx context.Context, tenantID string, checkID id.ID) error {
	res, err := s.mdb.NewDelete((*healthCheckModel)(nil)).
		Filter(bson.M{"_id": checkID.String(), "tenant_id": tenantID}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: delete health check failed: %w", err)
	}

	if res.DeletedCount() == 0 {
		return fmt.Errorf("%w: health check %s", ctrlplane.ErrNotFound, checkID)
	}

	return nil
}

func (s *Store) InsertResult(ctx context.Context, result *health.HealthResult) error {
	model := toHealthResultModel(result)

	_, err := s.mdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: insert health result failed: %w", err)
	}

	return nil
}

func (s *Store) ListResults(ctx context.Context, tenantID string, checkID id.ID, opts health.HistoryOptions) ([]health.HealthResult, error) {
	var models []healthResultModel

	f := bson.M{"check_id": checkID.String()}

	if !opts.Since.IsZero() {
		f["checked_at"] = bson.M{"$gte": opts.Since}
	}

	if !opts.Until.IsZero() {
		if existing, ok := f["checked_at"]; ok {
			existing.(bson.M)["$lte"] = opts.Until
		} else {
			f["checked_at"] = bson.M{"$lte": opts.Until}
		}
	}

	q := s.mdb.NewFind(&models).
		Filter(f).
		Sort(bson.D{{Key: "checked_at", Value: -1}})

	if opts.Limit > 0 {
		q = q.Limit(int64(opts.Limit))
	}

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("mongo: list health results failed: %w", err)
	}

	items := make([]health.HealthResult, 0, len(models))
	for i := range models {
		items = append(items, fromHealthResultModel(&models[i]))
	}

	return items, nil
}

func (s *Store) GetLatestResult(ctx context.Context, tenantID string, checkID id.ID) (*health.HealthResult, error) {
	var model healthResultModel

	err := s.mdb.NewFind(&model).
		Filter(bson.M{"check_id": checkID.String()}).
		Sort(bson.D{{Key: "checked_at", Value: -1}}).
		Limit(1).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, fmt.Errorf("%w: no results for check %s", ctrlplane.ErrNotFound, checkID)
		}

		return nil, fmt.Errorf("mongo: get latest result failed: %w", err)
	}

	result := fromHealthResultModel(&model)

	return &result, nil
}
