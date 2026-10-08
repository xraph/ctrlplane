package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/pagination"
	"github.com/xraph/ctrlplane/template"
)

const colTemplates = "cp_templates"

// InsertTemplate persists a new workload template.
func (s *Store) InsertTemplate(ctx context.Context, t *template.Template) error {
	model := toTemplateModel(t)

	_, err := s.mdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: insert template failed: %w", err)
	}

	return nil
}

// GetTemplate retrieves a workload template by ID within a tenant.
func (s *Store) GetTemplate(ctx context.Context, tenantID string, templateID id.ID) (*template.Template, error) {
	var model templateModel

	err := s.mdb.NewFind(&model).
		Filter(bson.M{"_id": templateID.String(), "tenant_id": tenantID}).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, fmt.Errorf("%w: template %s", ctrlplane.ErrNotFound, templateID)
		}

		return nil, fmt.Errorf("mongo: get template failed: %w", err)
	}

	return fromTemplateModel(&model), nil
}

// UpdateTemplate persists changes to an existing template.
func (s *Store) UpdateTemplate(ctx context.Context, t *template.Template) error {
	t.UpdatedAt = now()
	model := toTemplateModel(t)

	res, err := s.mdb.NewUpdate(model).
		Filter(bson.M{"_id": model.ID}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: update template failed: %w", err)
	}

	if res.MatchedCount() == 0 {
		return fmt.Errorf("%w: template %s", ctrlplane.ErrNotFound, t.ID)
	}

	return nil
}

// DeleteTemplate removes a template.
func (s *Store) DeleteTemplate(ctx context.Context, tenantID string, templateID id.ID) error {
	res, err := s.mdb.NewDelete((*templateModel)(nil)).
		Filter(bson.M{"_id": templateID.String(), "tenant_id": tenantID}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: delete template failed: %w", err)
	}

	if res.DeletedCount() == 0 {
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

	filter := bson.M{}
	filter["tenant_id"] = tenantID

	total, err := s.mdb.NewFind((*templateModel)(nil)).Filter(filter).Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("mongo: count ListTemplates: %w", err)
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
		return nil, fmt.Errorf("mongo: list ListTemplates: %w", err)
	}

	items := make([]*template.Template, 0, len(models))
	for i := range models {
		items = append(items, fromTemplateModel(&models[i]))
	}

	items, next := pagination.Trim(items, pageLimit, func(v *template.Template) ctrlplane.Entity { return v.Entity })

	return &template.ListResult{Items: items, Total: int(total), NextCursor: next}, nil
}
