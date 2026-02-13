package bun

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

	_, err := s.db.NewInsert().Model(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("bun: insert deployment failed: %w", err)
	}

	return nil
}

func (s *Store) GetDeployment(ctx context.Context, tenantID string, deployID id.ID) (*deploy.Deployment, error) {
	var model deploymentModel

	err := s.db.NewSelect().
		Model(&model).
		Where("id = ? AND tenant_id = ?", deployID.String(), tenantID).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: deployment %s", ctrlplane.ErrNotFound, deployID)
	}

	d := &deploy.Deployment{
		Entity: ctrlplane.Entity{
			ID:        id.MustParse(model.ID),
			CreatedAt: model.CreatedAt,
			UpdatedAt: model.UpdatedAt,
		},
		TenantID:    model.TenantID,
		InstanceID:  id.MustParse(model.InstanceID),
		ReleaseID:   id.MustParse(model.ReleaseID),
		State:       deploy.DeployState(model.State),
		Strategy:    model.Strategy,
		Image:       model.Image,
		ProviderRef: model.ProviderRef,
		Error:       model.Error,
		Initiator:   model.Initiator,
		StartedAt:   model.StartedAt,
		FinishedAt:  model.FinishedAt,
	}

	return d, nil
}

func (s *Store) UpdateDeployment(ctx context.Context, d *deploy.Deployment) error {
	d.UpdatedAt = now()
	model := toDeploymentModel(d)

	result, err := s.db.NewUpdate().
		Model(model).
		WherePK().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("bun: update deployment failed: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("bun: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: deployment %s", ctrlplane.ErrNotFound, d.ID)
	}

	return nil
}

func (s *Store) ListDeployments(ctx context.Context, tenantID string, instanceID id.ID, opts deploy.ListOptions) (*deploy.DeployListResult, error) {
	var models []deploymentModel

	query := s.db.NewSelect().
		Model(&models).
		Where("tenant_id = ? AND instance_id = ?", tenantID, instanceID.String())

	query = query.Order("created_at DESC")

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	query = query.Limit(limit)

	count, err := query.ScanAndCount(ctx)
	if err != nil {
		return nil, fmt.Errorf("bun: list deployments failed: %w", err)
	}

	items := make([]*deploy.Deployment, 0, len(models))

	for _, model := range models {
		d := &deploy.Deployment{
			Entity: ctrlplane.Entity{
				ID:        id.MustParse(model.ID),
				CreatedAt: model.CreatedAt,
				UpdatedAt: model.UpdatedAt,
			},
			TenantID:    model.TenantID,
			InstanceID:  id.MustParse(model.InstanceID),
			ReleaseID:   id.MustParse(model.ReleaseID),
			State:       deploy.DeployState(model.State),
			Strategy:    model.Strategy,
			Image:       model.Image,
			ProviderRef: model.ProviderRef,
			Error:       model.Error,
			Initiator:   model.Initiator,
			StartedAt:   model.StartedAt,
			FinishedAt:  model.FinishedAt,
		}
		items = append(items, d)
	}

	return &deploy.DeployListResult{
		Items: items,
		Total: count,
	}, nil
}

func (s *Store) InsertRelease(ctx context.Context, r *deploy.Release) error {
	model := toReleaseModel(r)

	_, err := s.db.NewInsert().Model(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("bun: insert release failed: %w", err)
	}

	return nil
}

func (s *Store) GetRelease(ctx context.Context, tenantID string, releaseID id.ID) (*deploy.Release, error) {
	var model releaseModel

	err := s.db.NewSelect().
		Model(&model).
		Where("id = ? AND tenant_id = ?", releaseID.String(), tenantID).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: release %s", ctrlplane.ErrNotFound, releaseID)
	}

	r := &deploy.Release{
		Entity: ctrlplane.Entity{
			ID:        id.MustParse(model.ID),
			CreatedAt: model.CreatedAt,
		},
		TenantID:   model.TenantID,
		InstanceID: id.MustParse(model.InstanceID),
		Version:    model.Version,
		Image:      model.Image,
		Notes:      model.Notes,
		CommitSHA:  model.CommitSHA,
		Active:     model.Active,
	}

	return r, nil
}

func (s *Store) ListReleases(ctx context.Context, tenantID string, instanceID id.ID, opts deploy.ListOptions) (*deploy.ReleaseListResult, error) {
	var models []releaseModel

	query := s.db.NewSelect().
		Model(&models).
		Where("tenant_id = ? AND instance_id = ?", tenantID, instanceID.String()).
		Order("version DESC")

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	query = query.Limit(limit)

	count, err := query.ScanAndCount(ctx)
	if err != nil {
		return nil, fmt.Errorf("bun: list releases failed: %w", err)
	}

	items := make([]*deploy.Release, 0, len(models))

	for _, model := range models {
		r := &deploy.Release{
			Entity: ctrlplane.Entity{
				ID:        id.MustParse(model.ID),
				CreatedAt: model.CreatedAt,
			},
			TenantID:   model.TenantID,
			InstanceID: id.MustParse(model.InstanceID),
			Version:    model.Version,
			Image:      model.Image,
			Notes:      model.Notes,
			CommitSHA:  model.CommitSHA,
			Active:     model.Active,
		}
		items = append(items, r)
	}

	return &deploy.ReleaseListResult{
		Items: items,
		Total: count,
	}, nil
}

func (s *Store) NextReleaseVersion(ctx context.Context, tenantID string, instanceID id.ID) (int, error) {
	var maxVersion int

	err := s.db.NewSelect().
		Model((*releaseModel)(nil)).
		Column("version").
		Where("tenant_id = ? AND instance_id = ?", tenantID, instanceID.String()).
		Order("version DESC").
		Limit(1).
		Scan(ctx, &maxVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return 1, nil
	}

	if err != nil {
		return 0, fmt.Errorf("bun: next release version failed: %w", err)
	}

	return maxVersion + 1, nil
}
