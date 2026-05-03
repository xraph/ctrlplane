package deploy

import (
	"context"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// Service manages deployments and releases for instances.
type Service interface {
	// Deploy creates a new release and deploys it to the instance.
	Deploy(ctx context.Context, req DeployRequest) (*Deployment, error)

	// RecordInitial persists the v1 Release + a synthetic
	// already-succeeded Deployment for a freshly-provisioned
	// instance whose container/pod is already running.
	//
	// This closes the gap where workload.Create + spawnReplica
	// produced a running instance without ever recording a Release
	// — which left:
	//   - the dashboard's Deployments list empty after Create,
	//   - rollback with no v1 target,
	//   - partial deploys silently dropping un-listed services
	//     because there was no prior Release to inherit from.
	//
	// Idempotent: when a Release already exists for the instance,
	// RecordInitial returns the existing first Release without
	// inserting a duplicate. Safe to call from spawnReplica's
	// adoption path (where the instance row may have been created
	// by a prior workload create).
	RecordInitial(ctx context.Context, instanceID id.ID) (*Release, error)

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
}

// DeployRequest holds the parameters for initiating a deployment.
// Services lists only the services being changed; services not listed
// inherit their snapshot from the prior Release.
type DeployRequest struct {
	InstanceID id.ID                        `json:"instance_id"          validate:"required"`
	Services   []provider.ServiceDeploySpec `json:"services"             validate:"required,min=1"`
	Strategy   string                       `json:"strategy,omitempty"`
	Notes      string                       `json:"notes,omitempty"`
	CommitSHA  string                       `json:"commit_sha,omitempty"`
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
