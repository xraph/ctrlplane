package deploy

import (
	"context"

	"github.com/xraph/ctrlplane/id"
)

// Store is the persistence interface for deployments and releases.
type Store interface {
	// InsertDeployment persists a new deployment.
	InsertDeployment(ctx context.Context, d *Deployment) error

	// GetDeployment retrieves a deployment by ID within a tenant.
	GetDeployment(ctx context.Context, tenantID string, deployID id.ID) (*Deployment, error)

	// UpdateDeployment persists changes to an existing deployment.
	UpdateDeployment(ctx context.Context, d *Deployment) error

	// ListDeployments returns a filtered, paginated list of deployments for an instance.
	ListDeployments(ctx context.Context, tenantID string, instanceID id.ID, opts ListOptions) (*DeployListResult, error)

	// InsertRelease persists a new release.
	InsertRelease(ctx context.Context, r *Release) error

	// GetRelease retrieves a release by ID within a tenant.
	GetRelease(ctx context.Context, tenantID string, releaseID id.ID) (*Release, error)

	// ListReleases returns a filtered, paginated list of releases for an instance.
	ListReleases(ctx context.Context, tenantID string, instanceID id.ID, opts ListOptions) (*ReleaseListResult, error)

	// NextReleaseVersion returns the next auto-incrementing version number for an instance.
	NextReleaseVersion(ctx context.Context, tenantID string, instanceID id.ID) (int, error)

	// InsertTemplate persists a new deployment template.
	InsertTemplate(ctx context.Context, t *Template) error

	// GetTemplate retrieves a deployment template by ID within a tenant.
	GetTemplate(ctx context.Context, tenantID string, templateID id.ID) (*Template, error)

	// UpdateTemplate persists changes to an existing deployment template.
	UpdateTemplate(ctx context.Context, t *Template) error

	// DeleteTemplate removes a deployment template.
	DeleteTemplate(ctx context.Context, tenantID string, templateID id.ID) error

	// ListTemplates returns a paginated list of deployment templates for a tenant.
	ListTemplates(ctx context.Context, tenantID string, opts ListOptions) (*TemplateListResult, error)
}
