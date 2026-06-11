package instance

import (
	"context"
	"io"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/vars"
)

// Service manages instance lifecycle operations.
type Service interface {
	// Create provisions a new instance on the specified provider.
	Create(ctx context.Context, req CreateRequest) (*Instance, error)

	// Get returns an instance by ID, scoped to the tenant in context.
	Get(ctx context.Context, instanceID id.ID) (*Instance, error)

	// GetBySlug returns an instance by slug within the caller's tenant.
	// Returns ctrlplane.ErrNotFound when no row matches. Used by
	// callers (notably workload.spawnReplica) that need to check
	// whether the unique (tenant, slug) namespace is already
	// occupied before attempting an insert that would otherwise
	// trip the database unique index.
	GetBySlug(ctx context.Context, slug string) (*Instance, error)

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

	// Logs returns a stream of log events from the instance's
	// container/pod via the underlying provider. Caller closes the
	// returned ReadCloser to stop. opts.Follow=true keeps the
	// stream open as new lines arrive.
	Logs(ctx context.Context, instanceID id.ID, opts LogsOptions) (io.ReadCloser, error)

	// Resources returns a one-shot resource-usage sample for the
	// instance via the underlying provider (e.g., docker stats).
	// Used by the metrics package's poller. Returns a zero-valued
	// usage (no error) when the underlying container has gone away
	// — the poller treats that as a missing sample, not a failure.
	Resources(ctx context.Context, instanceID id.ID) (*provider.ResourceUsage, error)
}

// LogsOptions mirrors provider.LogOptions on the public Service
// surface so callers don't need to import the provider package.
//
// ServiceName picks one service inside the instance — empty defaults
// to the Main service. Per-service log streaming lets a caller tail a
// sidecar without merging it into the main container's stream.
type LogsOptions struct {
	ServiceName string    `json:"service_name,omitempty"`
	Follow      bool      `json:"follow"`
	Since       time.Time `json:"since,omitzero"`
	Tail        int       `json:"tail,omitempty"`
}

// DatacenterResolver resolves a datacenter's provider name without introducing
// a circular import between the instance and datacenter packages.
type DatacenterResolver interface {
	// ResolveProvider returns the provider name for a given datacenter.
	ResolveProvider(ctx context.Context, datacenterID id.ID) (string, error)
}

// CreateRequest holds the parameters for creating a new instance.
//
// A non-services deployment is described via Source (helm | manifests |
// argocd) plus optional Variables/VariableValues resolved at provision
// time. For backward compatibility, callers may instead populate Services
// alone — Create projects them onto a services Source.
type CreateRequest struct {
	Name         string                 `json:"name"                    validate:"required"`
	DatacenterID id.ID                  `json:"datacenter_id,omitzero"`
	ProviderName string                 `json:"provider_name,omitempty"`
	Region       string                 `json:"region,omitempty"`
	Kind         provider.WorkloadKind  `json:"kind,omitempty"`
	Services     []provider.ServiceSpec `json:"services,omitempty"`
	Labels       map[string]string      `json:"labels,omitempty"`

	// Source describes a non-services deployment. When empty, Services is
	// projected onto a services Source.
	Source provider.DeploymentSource `json:"source,omitzero"`

	// Variables and VariableValues are resolved against derived instance
	// context and injected into the rendered Source.
	Variables      []vars.Definition `json:"variables,omitempty"`
	VariableValues map[string]any    `json:"variable_values,omitempty"`
}

// UpdateRequest holds the parameters for updating an instance.
type UpdateRequest struct {
	Name     *string                `json:"name,omitempty"`
	Services []provider.ServiceSpec `json:"services,omitempty"`
	Labels   map[string]string      `json:"labels,omitempty"`
}

// ScaleRequest holds the parameters for scaling an instance.
type ScaleRequest struct {
	CPUMillis *int `json:"cpu_millis,omitempty"`
	MemoryMB  *int `json:"memory_mb,omitempty"`
	Replicas  *int `json:"replicas,omitempty"`
}

// ListOptions configures instance listing with optional filters and pagination.
type ListOptions struct {
	State      string `json:"state,omitempty"`
	Label      string `json:"label,omitempty"`
	Provider   string `json:"provider,omitempty"`
	Datacenter string `json:"datacenter,omitempty"`
	Cursor     string `json:"cursor,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

// ListResult holds a page of instances with cursor-based pagination.
type ListResult struct {
	Items      []*Instance `json:"items"`
	NextCursor string      `json:"next_cursor,omitempty"`
	Total      int         `json:"total"`
}
