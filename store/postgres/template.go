package postgres

import (
	"context"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/id"
)

// InsertTemplate persists a new deployment template.
func (s *Store) InsertTemplate(ctx context.Context, t *deploy.Template) error {
	model := toTemplateModel(t)

	_, err := s.pg.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: insert template failed: %w", err)
	}

	return nil
}

// GetTemplate retrieves a deployment template by ID within a tenant.
func (s *Store) GetTemplate(ctx context.Context, tenantID string, templateID id.ID) (*deploy.Template, error) {
	var model templateModel

	err := s.pg.NewSelect(&model).
		Where("id = $1 AND tenant_id = $2", templateID.String(), tenantID).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: template %s", ctrlplane.ErrNotFound, templateID)
		}

		return nil, fmt.Errorf("postgres: get template failed: %w", err)
	}

	return fromTemplateModel(&model), nil
}

// UpdateTemplate persists changes to an existing deployment template.
func (s *Store) UpdateTemplate(ctx context.Context, t *deploy.Template) error {
	t.UpdatedAt = now()
	model := toTemplateModel(t)

	res, err := s.pg.NewUpdate(model).WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: update template failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: template %s", ctrlplane.ErrNotFound, t.ID)
	}

	return nil
}

// DeleteTemplate removes a deployment template.
func (s *Store) DeleteTemplate(ctx context.Context, tenantID string, templateID id.ID) error {
	res, err := s.pg.NewDelete((*templateModel)(nil)).
		Where("id = $1 AND tenant_id = $2", templateID.String(), tenantID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: delete template failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: template %s", ctrlplane.ErrNotFound, templateID)
	}

	return nil
}

// ListTemplates returns a paginated list of deployment templates for a tenant.
func (s *Store) ListTemplates(ctx context.Context, tenantID string, opts deploy.ListOptions) (*deploy.TemplateListResult, error) {
	var models []templateModel

	q := s.pg.NewSelect(&models).
		Where("tenant_id = $1", tenantID).
		OrderExpr("created_at DESC")

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	q = q.Limit(limit)

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("postgres: list templates failed: %w", err)
	}

	// Count total.
	total, err := s.pg.NewSelect((*templateModel)(nil)).
		Where("tenant_id = $1", tenantID).
		Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: count templates failed: %w", err)
	}

	items := make([]*deploy.Template, 0, len(models))
	for i := range models {
		items = append(items, fromTemplateModel(&models[i]))
	}

	return &deploy.TemplateListResult{
		Items: items,
		Total: int(total),
	}, nil
}
