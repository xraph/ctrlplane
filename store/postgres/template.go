package postgres

import (
	"context"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/pagination"
	"github.com/xraph/ctrlplane/template"
)

// InsertTemplate persists a new workload template.
func (s *Store) InsertTemplate(ctx context.Context, t *template.Template) error {
	model := toTemplateModel(t)

	_, err := s.pg.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: insert template failed: %w", err)
	}

	return nil
}

// GetTemplate retrieves a workload template by ID within a tenant.
func (s *Store) GetTemplate(ctx context.Context, tenantID string, templateID id.ID) (*template.Template, error) {
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

// UpdateTemplate persists changes to an existing template.
func (s *Store) UpdateTemplate(ctx context.Context, t *template.Template) error {
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

// DeleteTemplate removes a template.
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

// ListTemplates returns a paginated list of templates for a tenant.
func (s *Store) ListTemplates(ctx context.Context, tenantID string, opts template.ListOptions) (*template.ListResult, error) {
	var models []templateModel

	position, err := pagination.Decode(opts.Cursor)
	if err != nil {
		return nil, err
	}

	q := s.pg.NewSelect(&models)
	argCount := 0
	q = q.Where(fmt.Sprintf("tenant_id = $%d", argCount+1), tenantID)
	argCount += 1

	total, err := q.Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: count ListTemplates: %w", err)
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
		return nil, fmt.Errorf("postgres: list ListTemplates: %w", err)
	}

	items := make([]*template.Template, 0, len(models))
	for i := range models {
		items = append(items, fromTemplateModel(&models[i]))
	}

	items, next := pagination.Trim(items, pageLimit, func(v *template.Template) ctrlplane.Entity { return v.Entity })

	return &template.ListResult{Items: items, Total: int(total), NextCursor: next}, nil
}
