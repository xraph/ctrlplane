package mongo

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/id"
)

// InsertDeployment persists a new deployment.
func (s *Store) InsertDeployment(ctx context.Context, d *deploy.Deployment) error {
	m := toDeploymentModel(d)

	_, err := s.col(colDeployments).InsertOne(ctx, m)
	if err != nil {
		if isDuplicateKeyError(err) {
			return fmt.Errorf("mongo: insert deployment: %w: %s", ctrlplane.ErrAlreadyExists, m.ID)
		}

		return fmt.Errorf("mongo: insert deployment: %w", err)
	}

	return nil
}

// GetDeployment retrieves a deployment by ID within a tenant.
func (s *Store) GetDeployment(ctx context.Context, tenantID string, deployID id.ID) (*deploy.Deployment, error) {
	filter := bson.D{
		{Key: "_id", Value: idStr(deployID)},
		{Key: "tenant_id", Value: tenantID},
	}

	var m deploymentModel

	err := s.col(colDeployments).FindOne(ctx, filter).Decode(&m)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("mongo: get deployment: %w: %s", ctrlplane.ErrNotFound, deployID)
		}

		return nil, fmt.Errorf("mongo: get deployment: %w", err)
	}

	return fromDeploymentModel(&m), nil
}

// UpdateDeployment persists changes to an existing deployment.
func (s *Store) UpdateDeployment(ctx context.Context, d *deploy.Deployment) error {
	d.UpdatedAt = now()
	m := toDeploymentModel(d)

	result, err := s.col(colDeployments).ReplaceOne(
		ctx,
		bson.D{{Key: "_id", Value: m.ID}},
		m,
	)
	if err != nil {
		return fmt.Errorf("mongo: update deployment: %w", err)
	}

	if result.MatchedCount == 0 {
		return fmt.Errorf("mongo: update deployment: %w: %s", ctrlplane.ErrNotFound, m.ID)
	}

	return nil
}

// ListDeployments returns a filtered, paginated list of deployments for an instance.
func (s *Store) ListDeployments(ctx context.Context, tenantID string, instanceID id.ID, opts deploy.ListOptions) (*deploy.DeployListResult, error) {
	filter := bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "instance_id", Value: idStr(instanceID)},
	}

	total, err := s.col(colDeployments).CountDocuments(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("mongo: list deployments count: %w", err)
	}

	findOpts := options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}})

	if opts.Limit > 0 {
		findOpts.SetLimit(int64(opts.Limit))
	}

	cursor, err := s.col(colDeployments).Find(ctx, filter, findOpts)
	if err != nil {
		return nil, fmt.Errorf("mongo: list deployments: %w", err)
	}
	defer cursor.Close(ctx)

	items := make([]*deploy.Deployment, 0)

	for cursor.Next(ctx) {
		var m deploymentModel

		if err := cursor.Decode(&m); err != nil {
			return nil, fmt.Errorf("mongo: list deployments decode: %w", err)
		}

		items = append(items, fromDeploymentModel(&m))
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("mongo: list deployments cursor: %w", err)
	}

	return &deploy.DeployListResult{
		Items: items,
		Total: int(total),
	}, nil
}

// InsertRelease persists a new release.
func (s *Store) InsertRelease(ctx context.Context, r *deploy.Release) error {
	m := toReleaseModel(r)

	_, err := s.col(colReleases).InsertOne(ctx, m)
	if err != nil {
		if isDuplicateKeyError(err) {
			return fmt.Errorf("mongo: insert release: %w: %s", ctrlplane.ErrAlreadyExists, m.ID)
		}

		return fmt.Errorf("mongo: insert release: %w", err)
	}

	return nil
}

// GetRelease retrieves a release by ID within a tenant.
func (s *Store) GetRelease(ctx context.Context, tenantID string, releaseID id.ID) (*deploy.Release, error) {
	filter := bson.D{
		{Key: "_id", Value: idStr(releaseID)},
		{Key: "tenant_id", Value: tenantID},
	}

	var m releaseModel

	err := s.col(colReleases).FindOne(ctx, filter).Decode(&m)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("mongo: get release: %w: %s", ctrlplane.ErrNotFound, releaseID)
		}

		return nil, fmt.Errorf("mongo: get release: %w", err)
	}

	return fromReleaseModel(&m), nil
}

// ListReleases returns a filtered, paginated list of releases for an instance.
func (s *Store) ListReleases(ctx context.Context, tenantID string, instanceID id.ID, opts deploy.ListOptions) (*deploy.ReleaseListResult, error) {
	filter := bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "instance_id", Value: idStr(instanceID)},
	}

	total, err := s.col(colReleases).CountDocuments(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("mongo: list releases count: %w", err)
	}

	findOpts := options.Find().
		SetSort(bson.D{{Key: "version", Value: -1}})

	if opts.Limit > 0 {
		findOpts.SetLimit(int64(opts.Limit))
	}

	cursor, err := s.col(colReleases).Find(ctx, filter, findOpts)
	if err != nil {
		return nil, fmt.Errorf("mongo: list releases: %w", err)
	}
	defer cursor.Close(ctx)

	items := make([]*deploy.Release, 0)

	for cursor.Next(ctx) {
		var m releaseModel

		if err := cursor.Decode(&m); err != nil {
			return nil, fmt.Errorf("mongo: list releases decode: %w", err)
		}

		items = append(items, fromReleaseModel(&m))
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("mongo: list releases cursor: %w", err)
	}

	return &deploy.ReleaseListResult{
		Items: items,
		Total: int(total),
	}, nil
}

// NextReleaseVersion returns the next auto-incrementing version number for an instance.
func (s *Store) NextReleaseVersion(ctx context.Context, tenantID string, instanceID id.ID) (int, error) {
	filter := bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "instance_id", Value: idStr(instanceID)},
	}

	findOpts := options.FindOne().
		SetSort(bson.D{{Key: "version", Value: -1}}).
		SetProjection(bson.D{{Key: "version", Value: 1}})

	var result struct {
		Version int `bson:"version"`
	}

	err := s.col(colReleases).FindOne(ctx, filter, findOpts).Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return 1, nil
		}

		return 0, fmt.Errorf("mongo: next release version: %w", err)
	}

	return result.Version + 1, nil
}
