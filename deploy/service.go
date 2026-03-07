package deploy

import (
	"context"

	"github.com/xraph/ctrlplane/id"
)

// Service manages deployments and releases for instances.
type Service interface {
	// Deploy creates a new release and deploys it to the instance.
	Deploy(ctx context.Context, req DeployRequest) (*Deployment, error)

	// Rollback reverts to a specific release.
	Rollback(ctx context.Context, instanceID id.ID, releaseID id.ID) (*Deployment, error)

	// Cancel aborts an in-progress deployment.
	Cancel(ctx context.Context, deploymentID id.ID) error

	// GetDeployment returns a specific deployment.
	GetDeployment(ctx context.Context, deploymentID id.ID) (*Deployment, error)

	// ListDeployments lists deployments for an instance.
	ListDeployments(ctx context.Context, instanceID id.ID, opts ListOptions) (*DeployListResult, error)

	// GetRelease returns a specific release.
	GetRelease(ctx context.Context, releaseID id.ID) (*Release, error)

	// ListReleases lists releases for an instance.
	ListReleases(ctx context.Context, instanceID id.ID, opts ListOptions) (*ReleaseListResult, error)

	// CreateTemplate creates a new reusable deployment template.
	CreateTemplate(ctx context.Context, req CreateTemplateRequest) (*Template, error)

	// GetTemplate returns a specific deployment template.
	GetTemplate(ctx context.Context, templateID id.ID) (*Template, error)

	// UpdateTemplate updates an existing deployment template.
	UpdateTemplate(ctx context.Context, templateID id.ID, req UpdateTemplateRequest) (*Template, error)

	// DeleteTemplate removes a deployment template.
	DeleteTemplate(ctx context.Context, templateID id.ID) error

	// ListTemplates lists deployment templates for the current tenant.
	ListTemplates(ctx context.Context, opts ListOptions) (*TemplateListResult, error)
}

// DeployRequest holds the parameters for initiating a deployment.
type DeployRequest struct {
	InstanceID  id.ID             `json:"instance_id"            validate:"required"`
	Image       string            `json:"image"                  validate:"required"`
	Env         map[string]string `json:"env,omitempty"`
	Strategy    string            `json:"strategy,omitempty"`
	Notes       string            `json:"notes,omitempty"`
	CommitSHA   string            `json:"commit_sha,omitempty"`
	ConfigFiles []ConfigFile      `json:"config_files,omitempty"` // Vault-powered config files from template.
	Secrets     []SecretRef       `json:"secrets,omitempty"`      // Secret references from template.
}

// ListOptions configures deployment or release listing with pagination.
type ListOptions struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// DeployListResult holds a page of deployments with cursor-based pagination.
type DeployListResult struct {
	Items      []*Deployment `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
	Total      int           `json:"total"`
}

// ReleaseListResult holds a page of releases with cursor-based pagination.
type ReleaseListResult struct {
	Items      []*Release `json:"items"`
	NextCursor string     `json:"next_cursor,omitempty"`
	Total      int        `json:"total"`
}
