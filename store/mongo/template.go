package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/id"
)

const colTemplates = "cp_templates"

// InsertTemplate persists a new deployment template.
func (s *Store) InsertTemplate(ctx context.Context, t *deploy.Template) error {
	model := toTemplateModel(t)

	_, err := s.mdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: insert template failed: %w", err)
	}

	return nil
}

// GetTemplate retrieves a deployment template by ID within a tenant.
func (s *Store) GetTemplate(ctx context.Context, tenantID string, templateID id.ID) (*deploy.Template, error) {
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

// UpdateTemplate persists changes to an existing deployment template.
func (s *Store) UpdateTemplate(ctx context.Context, t *deploy.Template) error {
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

// DeleteTemplate removes a deployment template.
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

// ListTemplates returns a paginated list of deployment templates for a tenant.
func (s *Store) ListTemplates(ctx context.Context, tenantID string, opts deploy.ListOptions) (*deploy.TemplateListResult, error) {
	var models []templateModel

	f := bson.M{"tenant_id": tenantID}

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	err := s.mdb.NewFind(&models).
		Filter(f).
		Sort(bson.D{{Key: "created_at", Value: -1}}).
		Limit(int64(limit)).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("mongo: list templates failed: %w", err)
	}

	// Count total.
	total, err := s.mdb.NewFind((*templateModel)(nil)).
		Filter(f).
		Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("mongo: count templates failed: %w", err)
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
