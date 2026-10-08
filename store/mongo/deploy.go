package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/pagination"
)

func (s *Store) InsertDeployment(ctx context.Context, d *deploy.Deployment) error {
	model := toDeploymentModel(d)

	_, err := s.mdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: insert deployment failed: %w", err)
	}

	return nil
}

func (s *Store) GetDeployment(ctx context.Context, tenantID string, deployID id.ID) (*deploy.Deployment, error) {
	var model deploymentModel

	err := s.mdb.NewFind(&model).
		Filter(bson.M{"_id": deployID.String(), "tenant_id": tenantID}).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, fmt.Errorf("%w: deployment %s", ctrlplane.ErrNotFound, deployID)
		}

		return nil, fmt.Errorf("mongo: get deployment failed: %w", err)
	}

	return fromDeploymentModel(&model), nil
}

func (s *Store) UpdateDeployment(ctx context.Context, d *deploy.Deployment) error {
	d.UpdatedAt = now()
	model := toDeploymentModel(d)

	res, err := s.mdb.NewUpdate(model).
		Filter(bson.M{"_id": model.ID}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: update deployment failed: %w", err)
	}

	if res.MatchedCount() == 0 {
		return fmt.Errorf("%w: deployment %s", ctrlplane.ErrNotFound, d.ID)
	}

	return nil
}

func (s *Store) ListDeployments(ctx context.Context, tenantID string, instanceID id.ID, opts deploy.ListOptions) (*deploy.DeployListResult, error) {
	var models []deploymentModel

	position, err := pagination.Decode(opts.Cursor)
	if err != nil {
		return nil, err
	}

	filter := bson.M{}
	filter["tenant_id"] = tenantID
	filter["instance_id"] = instanceID.String()

	total, err := s.mdb.NewFind((*deploymentModel)(nil)).Filter(filter).Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("mongo: count ListDeployments: %w", err)
	}

	if opts.Cursor != "" {
		filter["$or"] = bson.A{bson.M{"created_at": bson.M{"$lt": position.CreatedAt}}, bson.M{"created_at": position.CreatedAt, "_id": bson.M{"$lt": position.ID.String()}}}
	}

	q := s.mdb.NewFind(&models).Filter(filter).Sort(bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}})

	pageLimit := opts.Limit
	if pageLimit <= 0 {
		pageLimit = 100
	}

	if pageLimit > 0 {
		q = q.Limit(int64(pageLimit + 1))
	}

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("mongo: list ListDeployments: %w", err)
	}

	items := make([]*deploy.Deployment, 0, len(models))
	for i := range models {
		items = append(items, fromDeploymentModel(&models[i]))
	}

	items, next := pagination.Trim(items, pageLimit, func(v *deploy.Deployment) ctrlplane.Entity { return v.Entity })

	return &deploy.DeployListResult{Items: items, Total: int(total), NextCursor: next}, nil
}

func (s *Store) InsertRelease(ctx context.Context, r *deploy.Release) error {
	model := toReleaseModel(r)

	_, err := s.mdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: insert release failed: %w", err)
	}

	return nil
}

func (s *Store) GetRelease(ctx context.Context, tenantID string, releaseID id.ID) (*deploy.Release, error) {
	var model releaseModel

	err := s.mdb.NewFind(&model).
		Filter(bson.M{"_id": releaseID.String(), "tenant_id": tenantID}).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, fmt.Errorf("%w: release %s", ctrlplane.ErrNotFound, releaseID)
		}

		return nil, fmt.Errorf("mongo: get release failed: %w", err)
	}

	return fromReleaseModel(&model), nil
}

func (s *Store) ListReleases(ctx context.Context, tenantID string, instanceID id.ID, opts deploy.ListOptions) (*deploy.ReleaseListResult, error) {
	var models []releaseModel

	position, err := pagination.Decode(opts.Cursor)
	if err != nil {
		return nil, err
	}

	filter := bson.M{}
	filter["tenant_id"] = tenantID
	filter["instance_id"] = instanceID.String()

	total, err := s.mdb.NewFind((*releaseModel)(nil)).Filter(filter).Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("mongo: count ListReleases: %w", err)
	}

	if opts.Cursor != "" {
		filter["$or"] = bson.A{bson.M{"created_at": bson.M{"$lt": position.CreatedAt}}, bson.M{"created_at": position.CreatedAt, "_id": bson.M{"$lt": position.ID.String()}}}
	}

	q := s.mdb.NewFind(&models).Filter(filter).Sort(bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}})

	pageLimit := opts.Limit
	if pageLimit <= 0 {
		pageLimit = 100
	}

	if pageLimit > 0 {
		q = q.Limit(int64(pageLimit + 1))
	}

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("mongo: list ListReleases: %w", err)
	}

	items := make([]*deploy.Release, 0, len(models))
	for i := range models {
		items = append(items, fromReleaseModel(&models[i]))
	}

	items, next := pagination.Trim(items, pageLimit, func(v *deploy.Release) ctrlplane.Entity { return v.Entity })

	return &deploy.ReleaseListResult{Items: items, Total: int(total), NextCursor: next}, nil
}

func (s *Store) NextReleaseVersion(ctx context.Context, tenantID string, instanceID id.ID) (int, error) {
	var model releaseModel

	err := s.mdb.NewFind(&model).
		Filter(bson.M{"tenant_id": tenantID, "instance_id": instanceID.String()}).
		Sort(bson.D{{Key: "version", Value: -1}}).
		Limit(1).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return 1, nil
		}

		return 0, fmt.Errorf("mongo: next release version failed: %w", err)
	}

	return model.Version + 1, nil
}
