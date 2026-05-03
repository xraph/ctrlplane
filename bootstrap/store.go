package bootstrap

import (
	"context"

	"github.com/xraph/ctrlplane/id"
)

// Store is the persistence interface for BootstrapWorkload entities.
// Unlike the rest of the codebase's stores, methods here do NOT take
// a tenantID — bootstrap workloads are platform-owned and addressable
// only by their own ID or by the parent datacenter.
type Store interface {
	// InsertBootstrap persists a new bootstrap workload row.
	InsertBootstrap(ctx context.Context, bw *BootstrapWorkload) error

	// GetBootstrap returns a bootstrap workload by ID.
	GetBootstrap(ctx context.Context, bootstrapID id.ID) (*BootstrapWorkload, error)

	// GetBootstrapByName returns the row whose (DatacenterID, Name)
	// pair matches. Used by the reconciler to match desired specs
	// against current rows.
	GetBootstrapByName(ctx context.Context, datacenterID id.ID, name string) (*BootstrapWorkload, error)

	// ListBootstraps returns every bootstrap workload attached to
	// the given datacenter. Read path for the reconciler and the
	// dashboard panel.
	ListBootstraps(ctx context.Context, datacenterID id.ID) ([]*BootstrapWorkload, error)

	// UpdateBootstrap persists changes to an existing row.
	UpdateBootstrap(ctx context.Context, bw *BootstrapWorkload) error

	// DeleteBootstrap removes a row. Called by the reconciler after
	// Deprovision returns successfully on a retiring workload.
	DeleteBootstrap(ctx context.Context, bootstrapID id.ID) error
}
