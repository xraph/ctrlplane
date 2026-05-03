package datacenter

import (
	"context"

	"github.com/xraph/ctrlplane/bootstrap"
	"github.com/xraph/ctrlplane/id"
)

// Service manages datacenter lifecycle operations.
type Service interface {
	// Create registers a new datacenter backed by a named provider.
	Create(ctx context.Context, req CreateRequest) (*Datacenter, error)

	// Get returns a datacenter by ID, scoped to the tenant in context.
	Get(ctx context.Context, datacenterID id.ID) (*Datacenter, error)

	// GetBySlug returns a datacenter by slug, scoped to the tenant in context.
	GetBySlug(ctx context.Context, slug string) (*Datacenter, error)

	// List returns datacenters for the current tenant with filtering.
	List(ctx context.Context, opts ListOptions) (*ListResult, error)

	// Update modifies a datacenter's configuration.
	Update(ctx context.Context, datacenterID id.ID, req UpdateRequest) (*Datacenter, error)

	// Delete removes a datacenter. Fails if instances still reference it.
	Delete(ctx context.Context, datacenterID id.ID) error

	// SetStatus transitions a datacenter to a new operational status.
	SetStatus(ctx context.Context, datacenterID id.ID, status Status) error

	// ResolveProvider returns the provider name for a given datacenter.
	ResolveProvider(ctx context.Context, datacenterID id.ID) (string, error)
}

// CreateRequest holds the parameters for creating a datacenter.
type CreateRequest struct {
	Name              string                           `json:"name"                         validate:"required"`
	ProviderName      string                           `json:"provider_name"                validate:"required"`
	Region            string                           `json:"region"                       validate:"required"`
	Zone              string                           `json:"zone,omitempty"`
	Location          *Location                        `json:"location,omitempty"`
	Capacity          *Capacity                        `json:"capacity,omitempty"`
	Labels            map[string]string                `json:"labels,omitempty"`
	Metadata          map[string]string                `json:"metadata,omitempty"`
	BootstrapServices []bootstrap.BootstrapServiceSpec `json:"bootstrap_services,omitempty"`
}

// UpdateRequest holds the parameters for updating a datacenter.
//
// BootstrapServices uses a pointer-to-slice to distinguish "not
// supplied" (nil — leave existing list untouched) from "explicit
// empty" (non-nil zero-length slice — clear all bootstrap services).
// The reconciler diffs against whatever ends up persisted on the
// row.
type UpdateRequest struct {
	Name              *string                           `json:"name,omitempty"`
	Zone              *string                           `json:"zone,omitempty"`
	Location          *Location                         `json:"location,omitempty"`
	Capacity          *Capacity                         `json:"capacity,omitempty"`
	Labels            map[string]string                 `json:"labels,omitempty"`
	Metadata          map[string]string                 `json:"metadata,omitempty"`
	BootstrapServices *[]bootstrap.BootstrapServiceSpec `json:"bootstrap_services,omitempty"`
}

// ListOptions configures datacenter listing with optional filters and pagination.
type ListOptions struct {
	Status   string `json:"status,omitempty"`
	Provider string `json:"provider,omitempty"`
	Region   string `json:"region,omitempty"`
	Label    string `json:"label,omitempty"`
	Cursor   string `json:"cursor,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// ListResult holds a page of datacenters with cursor-based pagination.
type ListResult struct {
	Items      []*Datacenter `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
	Total      int           `json:"total"`
}
