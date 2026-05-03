package workload

import (
	"context"

	"github.com/xraph/ctrlplane/id"
)

// Store is the persistence interface for Workload entities. Method
// names are suffixed with `Workload` so a single concrete store
// type can implement Workload + Instance + Deploy + Network etc.
// without method-name collisions on `Insert`/`Get`/`List`/`Update`.
// (Instance.Store has dibs on the bare names.)
type Store interface {
	InsertWorkload(ctx context.Context, w *Workload) error
	GetWorkloadByID(ctx context.Context, tenantID string, workloadID id.ID) (*Workload, error)
	GetWorkloadBySlug(ctx context.Context, tenantID, slug string) (*Workload, error)

	// ListWorkloads returns workloads visible to tenantID. Empty
	// tenantID is the cross-tenant convention used by admin views
	// (matches the instance + deploy stores).
	ListWorkloads(ctx context.Context, tenantID string, opts ListOptions) (*ListResult, error)

	UpdateWorkload(ctx context.Context, w *Workload) error
	DeleteWorkload(ctx context.Context, tenantID string, workloadID id.ID) error
}
