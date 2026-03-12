package datacenter

import (
	"context"

	"github.com/xraph/ctrlplane/id"
)

// Store is the persistence interface for datacenters.
type Store interface {
	// InsertDatacenter persists a new datacenter.
	InsertDatacenter(ctx context.Context, dc *Datacenter) error

	// GetDatacenterByID retrieves a datacenter by its ID within a tenant.
	GetDatacenterByID(ctx context.Context, tenantID string, datacenterID id.ID) (*Datacenter, error)

	// GetDatacenterBySlug retrieves a datacenter by its slug within a tenant.
	GetDatacenterBySlug(ctx context.Context, tenantID string, slug string) (*Datacenter, error)

	// ListDatacenters returns a filtered, paginated list of datacenters for a tenant.
	ListDatacenters(ctx context.Context, tenantID string, opts ListOptions) (*ListResult, error)

	// UpdateDatacenter persists changes to an existing datacenter.
	UpdateDatacenter(ctx context.Context, dc *Datacenter) error

	// DeleteDatacenter removes a datacenter from the store.
	DeleteDatacenter(ctx context.Context, tenantID string, datacenterID id.ID) error

	// CountDatacentersByTenant returns the total number of datacenters for a tenant.
	CountDatacentersByTenant(ctx context.Context, tenantID string) (int, error)

	// CountInstancesByDatacenter returns the number of instances linked to a datacenter.
	CountInstancesByDatacenter(ctx context.Context, tenantID string, datacenterID id.ID) (int, error)
}
