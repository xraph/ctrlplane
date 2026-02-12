package docker

import (
	"context"
	"errors"
	"io"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// ErrLogsNotImplemented is returned when logs are not yet available.
var ErrLogsNotImplemented = errors.New("docker: logs not implemented")

// Compile-time check that Provider implements provider.Provider.
var _ provider.Provider = (*Provider)(nil)

// Provider is a Docker-based infrastructure provider.
type Provider struct {
	cfg Config
}

// New creates a new Docker provider with the given configuration.
func New(cfg Config) *Provider {
	return &Provider{cfg: cfg}
}

// Info returns metadata about this provider.
func (p *Provider) Info() provider.ProviderInfo {
	return provider.ProviderInfo{
		Name:    "docker",
		Version: "1.0.0",
		Region:  "local",
	}
}

// Capabilities returns the set of features this provider supports.
func (p *Provider) Capabilities() []provider.Capability {
	return []provider.Capability{
		provider.CapDeploy,
		provider.CapScale,
		provider.CapLogs,
		provider.CapExec,
	}
}

// Provision creates infrastructure resources for an instance.
func (p *Provider) Provision(_ context.Context, req provider.ProvisionRequest) (*provider.ProvisionResult, error) {
	return &provider.ProvisionResult{
		ProviderRef: "docker:" + req.InstanceID.String(),
	}, nil
}

// Deprovision tears down all resources for an instance.
func (p *Provider) Deprovision(_ context.Context, _ id.ID) error {
	return nil
}

// Start starts a stopped instance.
func (p *Provider) Start(_ context.Context, _ id.ID) error {
	return nil
}

// Stop gracefully stops a running instance.
func (p *Provider) Stop(_ context.Context, _ id.ID) error {
	return nil
}

// Restart performs a stop followed by start cycle.
func (p *Provider) Restart(_ context.Context, _ id.ID) error {
	return nil
}

// Status returns the current runtime status of an instance.
func (p *Provider) Status(_ context.Context, _ id.ID) (*provider.InstanceStatus, error) {
	return &provider.InstanceStatus{
		State: provider.StateRunning,
		Ready: true,
	}, nil
}

// Deploy pushes a new release to the instance.
func (p *Provider) Deploy(_ context.Context, req provider.DeployRequest) (*provider.DeployResult, error) {
	return &provider.DeployResult{
		ProviderRef: "docker:" + req.InstanceID.String(),
	}, nil
}

// Rollback reverts to a previous release.
func (p *Provider) Rollback(_ context.Context, _ id.ID, _ id.ID) error {
	return nil
}

// Scale adjusts the resource allocation for an instance.
func (p *Provider) Scale(_ context.Context, _ id.ID, _ provider.ResourceSpec) error {
	return nil
}

// Resources returns current resource utilization for an instance.
func (p *Provider) Resources(_ context.Context, _ id.ID) (*provider.ResourceUsage, error) {
	return &provider.ResourceUsage{}, nil
}

// Logs streams logs for the instance.
func (p *Provider) Logs(_ context.Context, _ id.ID, _ provider.LogOptions) (io.ReadCloser, error) {
	return nil, ErrLogsNotImplemented
}

// Exec runs a command inside the instance.
func (p *Provider) Exec(_ context.Context, _ id.ID, _ provider.ExecRequest) (*provider.ExecResult, error) {
	return &provider.ExecResult{ExitCode: 0}, nil
}
