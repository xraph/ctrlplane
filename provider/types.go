package provider

import (
	"time"

	"github.com/xraph/ctrlplane/id"
)

// ProviderInfo holds metadata about a provider.
type ProviderInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Region  string `json:"region"`
}

// ProvisionRequest describes what resources to create for an instance.
type ProvisionRequest struct {
	InstanceID  id.ID             `json:"instance_id"`
	TenantID    string            `json:"tenant_id"`
	Name        string            `json:"name"`
	Image       string            `json:"image"`
	Resources   ResourceSpec      `json:"resources"`
	Env         map[string]string `json:"env,omitempty"`
	Ports       []PortSpec        `json:"ports,omitempty"`
	Volumes     []VolumeSpec      `json:"volumes,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// ProvisionResult holds the result of a provision operation.
type ProvisionResult struct {
	ProviderRef string            `json:"provider_ref"`
	Endpoints   []Endpoint        `json:"endpoints"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// DeployRequest describes a deployment operation.
type DeployRequest struct {
	InstanceID  id.ID             `json:"instance_id"`
	ReleaseID   id.ID             `json:"release_id"`
	Image       string            `json:"image"`
	Env         map[string]string `json:"env,omitempty"`
	Strategy    string            `json:"strategy"`
	HealthCheck *HealthCheckSpec  `json:"health_check,omitempty"`
}

// DeployResult holds the result of a deploy operation.
type DeployResult struct {
	ProviderRef string `json:"provider_ref"`
	Status      string `json:"status"`
}

// InstanceStatus describes the current runtime state of an instance.
type InstanceStatus struct {
	State     InstanceState     `json:"state"`
	Ready     bool              `json:"ready"`
	Restarts  int               `json:"restarts"`
	StartedAt *time.Time        `json:"started_at,omitempty"`
	Message   string            `json:"message,omitempty"`
	Endpoints []Endpoint        `json:"endpoints"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// InstanceState represents the lifecycle state of an instance.
type InstanceState string

const (
	// StateProvisioning indicates the instance is being provisioned.
	StateProvisioning InstanceState = "provisioning"

	// StateStarting indicates the instance is starting up.
	StateStarting InstanceState = "starting"

	// StateRunning indicates the instance is running and healthy.
	StateRunning InstanceState = "running"

	// StateStopping indicates the instance is shutting down.
	StateStopping InstanceState = "stopping"

	// StateStopped indicates the instance is stopped.
	StateStopped InstanceState = "stopped"

	// StateFailed indicates the instance is in a failed state.
	StateFailed InstanceState = "failed"

	// StateDestroying indicates the instance is being torn down.
	StateDestroying InstanceState = "destroying"

	// StateDestroyed indicates the instance has been fully removed.
	StateDestroyed InstanceState = "destroyed"
)
