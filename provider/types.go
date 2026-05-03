package provider

import (
	"time"

	"github.com/xraph/ctrlplane/id"
)

// Location holds geographic metadata for a provider's region. Same
// shape as datacenter.Location but defined here so the provider
// package doesn't depend on datacenter — keeps the import graph
// pointing one way.
type Location struct {
	Latitude  float64 `json:"latitude,omitempty"`
	Longitude float64 `json:"longitude,omitempty"`
	Country   string  `json:"country,omitempty"`
	City      string  `json:"city,omitempty"`
}

// ProviderInfo holds metadata about a provider. Location is optional —
// providers that don't know where they are (e.g. a generic SaaS API
// driver with no geographic identity) leave it nil.
type ProviderInfo struct {
	Name     string    `json:"name"`
	Version  string    `json:"version"`
	Region   string    `json:"region"`
	Location *Location `json:"location,omitempty"`
}

// ProvisionRequest describes what resources to create for an instance.
// One ProvisionRequest produces one unit of co-scheduling — a k8s Pod,
// a Nomad TaskGroup allocation, or a Docker Compose project — containing
// every entry in Services.
type ProvisionRequest struct {
	InstanceID id.ID             `json:"instance_id"`
	TenantID   string            `json:"tenant_id"`
	Name       string            `json:"name"`
	Kind       WorkloadKind      `json:"kind"` // deployment | stateful_set
	Services   []ServiceSpec     `json:"services"`
	Labels     map[string]string `json:"labels,omitempty"` // workload-level, applied to all services
}

// ProvisionResult holds the result of a provision operation.
type ProvisionResult struct {
	// ProviderRef is the workload-level handle (Pod name on k8s,
	// Job ID on Nomad, Compose project name on Docker).
	ProviderRef string `json:"provider_ref"`

	// ServiceRefs maps a service's Name to the provider-specific
	// per-container/task ref (container ID on Docker, container name
	// within the Pod on k8s, Task name on Nomad). Used for per-service
	// log/exec/restart operations.
	ServiceRefs map[string]string `json:"service_refs,omitempty"`

	Endpoints []Endpoint        `json:"endpoints"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// DeployRequest describes a deployment operation. Services lists only
// the services being changed in this rollout — services not listed are
// left at their previous version (the new Release inherits their
// snapshots from the prior Release).
type DeployRequest struct {
	InstanceID id.ID               `json:"instance_id"`
	ReleaseID  id.ID               `json:"release_id"`
	Services   []ServiceDeploySpec `json:"services"`
	Strategy   string              `json:"strategy"`
}

// DeployResult holds the result of a deploy operation.
type DeployResult struct {
	ProviderRef string `json:"provider_ref"`
	Status      string `json:"status"`
}

// InstanceStatus describes the current runtime state of an instance.
// State and Ready aggregate across all services (worst-of); per-service
// detail is in Services keyed by ServiceSpec.Name.
type InstanceStatus struct {
	State     InstanceState            `json:"state"`
	Ready     bool                     `json:"ready"`
	Restarts  int                      `json:"restarts"` // sum across services
	StartedAt *time.Time               `json:"started_at,omitempty"`
	Message   string                   `json:"message,omitempty"`
	Endpoints []Endpoint               `json:"endpoints"`
	Services  map[string]ServiceStatus `json:"services,omitempty"`
	Metadata  map[string]string        `json:"metadata,omitempty"`
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
