package template

import (
	"context"

	"github.com/xraph/ctrlplane/id"
)

// Store is the persistence interface for workload templates. Method
// names are entity-suffixed so a single concrete store type can
// implement Template + Workload + Instance + Deploy + Network etc.
// without method-name collisions on Insert/Get/List/Update/Delete.
type Store interface {
	InsertTemplate(ctx context.Context, t *Template) error
	GetTemplate(ctx context.Context, tenantID string, templateID id.ID) (*Template, error)
	UpdateTemplate(ctx context.Context, t *Template) error
	DeleteTemplate(ctx context.Context, tenantID string, templateID id.ID) error
	ListTemplates(ctx context.Context, tenantID string, opts ListOptions) (*ListResult, error)
}
