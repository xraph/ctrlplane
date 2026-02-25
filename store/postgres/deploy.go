package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/id"
)

func (s *Store) InsertDeployment(ctx context.Context, d *deploy.Deployment) error {
	model := toDeploymentModel(d)

	_, err := s.pg.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: insert deployment failed: %w", err)
	}

	return nil
}

func (s *Store) GetDeployment(ctx context.Context, tenantID string, deployID id.ID) (*deploy.Deployment, error) {
	var model deploymentModel

	err := s.pg.NewSelect(&model).
		Where("id = $1 AND tenant_id = $2", deployID.String(), tenantID).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: deployment %s", ctrlplane.ErrNotFound, deployID)
		}

		return nil, fmt.Errorf("postgres: get deployment failed: %w", err)
	}

	return fromDeploymentModel(&model), nil
}

func (s *Store) UpdateDeployment(ctx context.Context, d *deploy.Deployment) error {
	d.UpdatedAt = now()
	model := toDeploymentModel(d)

	res, err := s.pg.NewUpdate(model).WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: update deployment failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: deployment %s", ctrlplane.ErrNotFound, d.ID)
	}

	return nil
}

func (s *Store) ListDeployments(ctx context.Context, tenantID string, instanceID id.ID, opts deploy.ListOptions) (*deploy.DeployListResult, error) {
	var models []deploymentModel

	q := s.pg.NewSelect(&models).
		Where("tenant_id = $1 AND instance_id = $2", tenantID, instanceID.String()).
		OrderExpr("created_at DESC")

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	q = q.Limit(limit)

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("postgres: list deployments failed: %w", err)
	}

	// Count total.
	total, err := s.pg.NewSelect((*deploymentModel)(nil)).
		Where("tenant_id = $1 AND instance_id = $2", tenantID, instanceID.String()).
		Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: count deployments failed: %w", err)
	}

	items := make([]*deploy.Deployment, 0, len(models))
	for i := range models {
		items = append(items, fromDeploymentModel(&models[i]))
	}

	return &deploy.DeployListResult{
		Items: items,
		Total: int(total),
	}, nil
}

func (s *Store) InsertRelease(ctx context.Context, r *deploy.Release) error {
	model := toReleaseModel(r)

	_, err := s.pg.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: insert release failed: %w", err)
	}

	return nil
}

func (s *Store) GetRelease(ctx context.Context, tenantID string, releaseID id.ID) (*deploy.Release, error) {
	var model releaseModel

	err := s.pg.NewSelect(&model).
		Where("id = $1 AND tenant_id = $2", releaseID.String(), tenantID).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: release %s", ctrlplane.ErrNotFound, releaseID)
		}

		return nil, fmt.Errorf("postgres: get release failed: %w", err)
	}

	return fromReleaseModel(&model), nil
}

func (s *Store) ListReleases(ctx context.Context, tenantID string, instanceID id.ID, opts deploy.ListOptions) (*deploy.ReleaseListResult, error) {
	var models []releaseModel

	q := s.pg.NewSelect(&models).
		Where("tenant_id = $1 AND instance_id = $2", tenantID, instanceID.String()).
		OrderExpr("version DESC")

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	q = q.Limit(limit)

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("postgres: list releases failed: %w", err)
	}

	// Count total.
	total, err := s.pg.NewSelect((*releaseModel)(nil)).
		Where("tenant_id = $1 AND instance_id = $2", tenantID, instanceID.String()).
		Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: count releases failed: %w", err)
	}

	items := make([]*deploy.Release, 0, len(models))
	for i := range models {
		items = append(items, fromReleaseModel(&models[i]))
	}

	return &deploy.ReleaseListResult{
		Items: items,
		Total: int(total),
	}, nil
}

func (s *Store) NextReleaseVersion(ctx context.Context, tenantID string, instanceID id.ID) (int, error) {
	var maxVersion int

	err := s.pg.NewSelect((*releaseModel)(nil)).
		Column("version").
		Where("tenant_id = $1 AND instance_id = $2", tenantID, instanceID.String()).
		OrderExpr("version DESC").
		Limit(1).
		Scan(ctx, &maxVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return 1, nil
	}

	if err != nil {
		return 0, fmt.Errorf("postgres: next release version failed: %w", err)
	}

	return maxVersion + 1, nil
}
