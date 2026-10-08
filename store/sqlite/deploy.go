package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/pagination"
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

	position, err := pagination.Decode(opts.Cursor)
	if err != nil {
		return nil, err
	}

	q := s.sdb.NewSelect(&models)
	q = q.Where("tenant_id = ?", tenantID)
	q = q.Where("instance_id = ?", instanceID.String())

	total, err := q.Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlite: count ListDeployments: %w", err)
	}

	if opts.Cursor != "" {
		q = q.Where("(created_at < ? OR (created_at = ? AND id < ?))", position.CreatedAt, position.CreatedAt, position.ID.String())
	}

	q = q.OrderExpr("created_at DESC, id DESC")

	pageLimit := opts.Limit
	if pageLimit <= 0 {
		pageLimit = 100
	}

	if pageLimit > 0 {
		q = q.Limit(pageLimit + 1)
	}

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("sqlite: list ListDeployments: %w", err)
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

	position, err := pagination.Decode(opts.Cursor)
	if err != nil {
		return nil, err
	}

	q := s.sdb.NewSelect(&models)
	q = q.Where("tenant_id = ?", tenantID)
	q = q.Where("instance_id = ?", instanceID.String())

	total, err := q.Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlite: count ListReleases: %w", err)
	}

	if opts.Cursor != "" {
		q = q.Where("(created_at < ? OR (created_at = ? AND id < ?))", position.CreatedAt, position.CreatedAt, position.ID.String())
	}

	q = q.OrderExpr("created_at DESC, id DESC")

	pageLimit := opts.Limit
	if pageLimit <= 0 {
		pageLimit = 100
	}

	if pageLimit > 0 {
		q = q.Limit(pageLimit + 1)
	}

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("sqlite: list ListReleases: %w", err)
	}

	items := make([]*deploy.Release, 0, len(models))
	for i := range models {
		items = append(items, fromReleaseModel(&models[i]))
	}

	items, next := pagination.Trim(items, pageLimit, func(v *deploy.Release) ctrlplane.Entity { return v.Entity })

	return &deploy.ReleaseListResult{Items: items, Total: int(total), NextCursor: next}, nil
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
