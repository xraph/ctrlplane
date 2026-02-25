package sqlite

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

	_, err := s.sdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: insert deployment failed: %w", err)
	}

	return nil
}

func (s *Store) GetDeployment(ctx context.Context, tenantID string, deployID id.ID) (*deploy.Deployment, error) {
	var model deploymentModel

	err := s.sdb.NewSelect(&model).
		Where("id = ? AND tenant_id = ?", deployID.String(), tenantID).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: deployment %s", ctrlplane.ErrNotFound, deployID)
		}

		return nil, fmt.Errorf("sqlite: get deployment failed: %w", err)
	}

	return fromDeploymentModel(&model), nil
}

func (s *Store) UpdateDeployment(ctx context.Context, d *deploy.Deployment) error {
	d.UpdatedAt = now()
	model := toDeploymentModel(d)

	res, err := s.sdb.NewUpdate(model).WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: update deployment failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: deployment %s", ctrlplane.ErrNotFound, d.ID)
	}

	return nil
}

func (s *Store) ListDeployments(ctx context.Context, tenantID string, instanceID id.ID, opts deploy.ListOptions) (*deploy.DeployListResult, error) {
	var models []deploymentModel

	q := s.sdb.NewSelect(&models).
		Where("tenant_id = ? AND instance_id = ?", tenantID, instanceID.String()).
		OrderExpr("created_at DESC")

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	q = q.Limit(limit)

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("sqlite: list deployments failed: %w", err)
	}

	// Count total.
	total, err := s.sdb.NewSelect((*deploymentModel)(nil)).
		Where("tenant_id = ? AND instance_id = ?", tenantID, instanceID.String()).
		Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlite: count deployments failed: %w", err)
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

	_, err := s.sdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: insert release failed: %w", err)
	}

	return nil
}

func (s *Store) GetRelease(ctx context.Context, tenantID string, releaseID id.ID) (*deploy.Release, error) {
	var model releaseModel

	err := s.sdb.NewSelect(&model).
		Where("id = ? AND tenant_id = ?", releaseID.String(), tenantID).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: release %s", ctrlplane.ErrNotFound, releaseID)
		}

		return nil, fmt.Errorf("sqlite: get release failed: %w", err)
	}

	return fromReleaseModel(&model), nil
}

func (s *Store) ListReleases(ctx context.Context, tenantID string, instanceID id.ID, opts deploy.ListOptions) (*deploy.ReleaseListResult, error) {
	var models []releaseModel

	q := s.sdb.NewSelect(&models).
		Where("tenant_id = ? AND instance_id = ?", tenantID, instanceID.String()).
		OrderExpr("version DESC")

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	q = q.Limit(limit)

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("sqlite: list releases failed: %w", err)
	}

	// Count total.
	total, err := s.sdb.NewSelect((*releaseModel)(nil)).
		Where("tenant_id = ? AND instance_id = ?", tenantID, instanceID.String()).
		Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlite: count releases failed: %w", err)
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

	err := s.sdb.NewSelect((*releaseModel)(nil)).
		Column("version").
		Where("tenant_id = ? AND instance_id = ?", tenantID, instanceID.String()).
		OrderExpr("version DESC").
		Limit(1).
		Scan(ctx, &maxVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return 1, nil
	}

	if err != nil {
		return 0, fmt.Errorf("sqlite: next release version failed: %w", err)
	}

	return maxVersion + 1, nil
}
