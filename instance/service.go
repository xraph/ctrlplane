package instance

import (
	"context"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// Service manages instance lifecycle operations.
type Service interface {
	// Create provisions a new instance on the specified provider.
	Create(ctx context.Context, req CreateRequest) (*Instance, error)

	// Get returns an instance by ID, scoped to the tenant in context.
	Get(ctx context.Context, instanceID id.ID) (*Instance, error)

	// List returns instances for the current tenant with filtering.
	List(ctx context.Context, opts ListOptions) (*ListResult, error)

	// Update modifies instance configuration (env, labels, resources).
	Update(ctx context.Context, instanceID id.ID, req UpdateRequest) (*Instance, error)

	// Delete deprovisions and removes an instance.
	Delete(ctx context.Context, instanceID id.ID) error

	// Start starts a stopped instance.
	Start(ctx context.Context, instanceID id.ID) error

	// Stop gracefully stops an instance.
	Stop(ctx context.Context, instanceID id.ID) error

	// Restart performs a stop+start cycle.
	Restart(ctx context.Context, instanceID id.ID) error

	// Scale adjusts resource allocation.
	Scale(ctx context.Context, instanceID id.ID, req ScaleRequest) error

	// Suspend marks an instance as suspended (billing/abuse).
	Suspend(ctx context.Context, instanceID id.ID, reason string) error

	// Unsuspend restores a suspended instance.
	Unsuspend(ctx context.Context, instanceID id.ID) error
}

// CreateRequest holds the parameters for creating a new instance.
type CreateRequest struct {
	Name         string                `json:"name"                    validate:"required"`
	ProviderName string                `json:"provider_name,omitempty"`
	Region       string                `json:"region,omitempty"`
	Image        string                `json:"image"                   validate:"required"`
	Resources    provider.ResourceSpec `json:"resources"`
	Env          map[string]string     `json:"env,omitempty"`
	Ports        []provider.PortSpec   `json:"ports,omitempty"`
	Labels       map[string]string     `json:"labels,omitempty"`
}

// UpdateRequest holds the parameters for updating an instance.
type UpdateRequest struct {
	Name   *string           `json:"name,omitempty"`
	Env    map[string]string `json:"env,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

// ScaleRequest holds the parameters for scaling an instance.
type ScaleRequest struct {
	CPUMillis *int `json:"cpu_millis,omitempty"`
	MemoryMB  *int `json:"memory_mb,omitempty"`
	Replicas  *int `json:"replicas,omitempty"`
}

// ListOptions configures instance listing with optional filters and pagination.
type ListOptions struct {
	State    string `json:"state,omitempty"`
	Label    string `json:"label,omitempty"`
	Provider string `json:"provider,omitempty"`
	Cursor   string `json:"cursor,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// ListResult holds a page of instances with cursor-based pagination.
type ListResult struct {
	Items      []*Instance `json:"items"`
	NextCursor string      `json:"next_cursor,omitempty"`
	Total      int         `json:"total"`
}
