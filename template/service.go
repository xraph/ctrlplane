package template

import (
	"context"

	"github.com/xraph/ctrlplane/id"
)

// Service manages workload templates. It supports authoring templates
// directly, listing/reading/updating/deleting them, and forking a new
// template from an existing workload's spec.
type Service interface {
	// Create persists a new template authored from raw fields.
	Create(ctx context.Context, req CreateRequest) (*Template, error)

	// CreateFromWorkload forks a new template from an existing
	// workload's current spec. The workload's image, env, secrets,
	// config files, volumes, ports, health check, labels and
	// annotations are copied wholesale into the template.
	CreateFromWorkload(ctx context.Context, workloadID id.ID, req CreateFromWorkloadRequest) (*Template, error)

	// Get returns a specific template.
	Get(ctx context.Context, templateID id.ID) (*Template, error)

	// Update mutates an existing template; nil request fields are left untouched.
	Update(ctx context.Context, templateID id.ID, req UpdateRequest) (*Template, error)

	// Delete removes a template.
	Delete(ctx context.Context, templateID id.ID) error

	// List returns templates for the caller's tenant.
	List(ctx context.Context, opts ListOptions) (*ListResult, error)
}
