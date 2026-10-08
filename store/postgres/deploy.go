package postgres

import (
	"context"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/pagination"
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

	position, err := pagination.Decode(opts.Cursor)
	if err != nil {
		return nil, err
	}

	q := s.pg.NewSelect(&models)
	argCount := 0
	q = q.Where(fmt.Sprintf("tenant_id = $%d", argCount+1), tenantID)
	argCount += 1
	q = q.Where(fmt.Sprintf("instance_id = $%d", argCount+1), instanceID.String())
	argCount += 1

	total, err := q.Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: count ListDeployments: %w", err)
	}

	if opts.Cursor != "" {
		q = q.Where(fmt.Sprintf("(created_at < $%d OR (created_at = $%d AND id < $%d))", argCount+1, argCount+2, argCount+3), position.CreatedAt, position.CreatedAt, position.ID.String())
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
		return nil, fmt.Errorf("postgres: list ListDeployments: %w", err)
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

	position, err := pagination.Decode(opts.Cursor)
	if err != nil {
		return nil, err
	}

	q := s.pg.NewSelect(&models)
	argCount := 0
	q = q.Where(fmt.Sprintf("tenant_id = $%d", argCount+1), tenantID)
	argCount += 1
	q = q.Where(fmt.Sprintf("instance_id = $%d", argCount+1), instanceID.String())
	argCount += 1

	total, err := q.Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: count ListReleases: %w", err)
	}

	if opts.Cursor != "" {
		q = q.Where(fmt.Sprintf("(created_at < $%d OR (created_at = $%d AND id < $%d))", argCount+1, argCount+2, argCount+3), position.CreatedAt, position.CreatedAt, position.ID.String())
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
		return nil, fmt.Errorf("postgres: list ListReleases: %w", err)
	}

	items := make([]*deploy.Release, 0, len(models))
	for i := range models {
		items = append(items, fromReleaseModel(&models[i]))
	}

	items, next := pagination.Trim(items, pageLimit, func(v *deploy.Release) ctrlplane.Entity { return v.Entity })

	return &deploy.ReleaseListResult{Items: items, Total: int(total), NextCursor: next}, nil
}

func (s *Store) NextReleaseVersion(ctx context.Context, tenantID string, instanceID id.ID) (int, error) {
	// Scalar aggregate: grove's SelectQuery.Scan only fills struct dests
	// (passing &int errors "dest must be a pointer to a struct" and leaks the
	// connection). Use a raw single-row query and COALESCE(MAX,0) so the
	// no-rows case naturally yields 0 → first version 1, with no sentinel
	// handling and no leaked connection.
	var maxVersion int

	err := s.pg.QueryRow(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM cp_releases WHERE tenant_id = $1 AND instance_id = $2`,
		tenantID, instanceID.String(),
	).Scan(&maxVersion)
	if err != nil {
		return 0, fmt.Errorf("postgres: next release version failed: %w", err)
	}

	return maxVersion + 1, nil
}
