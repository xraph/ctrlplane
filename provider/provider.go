package provider

import (
	"context"
	"io"

	"github.com/xraph/ctrlplane/id"
)

// Provider is the unified interface for infrastructure operations.
// Each cloud/orchestrator (K8s, Nomad, AWS ECS, Docker, etc.) implements this.
type Provider interface {
	// Info returns metadata about this provider.
	Info() ProviderInfo

	// Capabilities returns what this provider supports.
	Capabilities() []Capability

	// Provision creates infrastructure resources for an instance.
	Provision(ctx context.Context, req ProvisionRequest) (*ProvisionResult, error)

	// Deprovision tears down all resources for an instance.
	Deprovision(ctx context.Context, instanceID id.ID) error

	// Start starts a stopped instance.
	Start(ctx context.Context, instanceID id.ID) error

	// Stop gracefully stops a running instance.
	Stop(ctx context.Context, instanceID id.ID) error

	// Restart performs a stop+start cycle.
	Restart(ctx context.Context, instanceID id.ID) error

	// Status returns the current runtime status.
	Status(ctx context.Context, instanceID id.ID) (*InstanceStatus, error)

	// Deploy pushes a new release to the instance.
	Deploy(ctx context.Context, req DeployRequest) (*DeployResult, error)

	// Rollback reverts to a previous release.
	Rollback(ctx context.Context, instanceID id.ID, releaseID id.ID) error

	// Scale adjusts the instance's resource allocation.
	Scale(ctx context.Context, instanceID id.ID, spec ResourceSpec) error

	// Resources returns current resource utilization.
	Resources(ctx context.Context, instanceID id.ID) (*ResourceUsage, error)

	// Logs streams logs for the instance.
	Logs(ctx context.Context, instanceID id.ID, opts LogOptions) (io.ReadCloser, error)

	// Exec runs a command inside the instance.
	Exec(ctx context.Context, instanceID id.ID, cmd ExecRequest) (*ExecResult, error)
}
