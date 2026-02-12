package instance

import (
	"context"

	"github.com/xraph/ctrlplane/id"
)

// Store is the persistence interface for instances.
type Store interface {
	// Insert persists a new instance.
	Insert(ctx context.Context, inst *Instance) error

	// GetByID retrieves an instance by its ID within a tenant.
	GetByID(ctx context.Context, tenantID string, instanceID id.ID) (*Instance, error)

	// GetBySlug retrieves an instance by its slug within a tenant.
	GetBySlug(ctx context.Context, tenantID string, slug string) (*Instance, error)

	// List returns a filtered, paginated list of instances for a tenant.
	List(ctx context.Context, tenantID string, opts ListOptions) (*ListResult, error)

	// Update persists changes to an existing instance.
	Update(ctx context.Context, inst *Instance) error

	// Delete removes an instance from the store.
	Delete(ctx context.Context, tenantID string, instanceID id.ID) error

	// CountByTenant returns the total number of instances for a tenant.
	CountByTenant(ctx context.Context, tenantID string) (int, error)
}
