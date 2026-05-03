package bootstrap

import (
	"context"

	"github.com/xraph/ctrlplane/id"
)

// Service drives bootstrap workloads from desired toward observed.
//
// All write paths run under the platform's authority (no tenant
// claims) — the service is invoked from the reconciler worker, never
// from a tenant-driven request. Read paths intended for the dashboard
// panel go through Get / ListByDatacenter and are gated upstream by
// the API handler on system:admin claims.
type Service interface {
	// Reconcile drives one datacenter's bootstrap state forward by
	// one step. Idempotent: safe to call repeatedly. The caller
	// supplies:
	//
	//   - dc: the datacenter projection used to address the
	//     provider and (via DatacenterInfo) feed registered hooks.
	//   - declared: the operator-declared spec list from the
	//     datacenter row. May be nil for datacenters that rely
	//     entirely on hooks.
	//
	// Reconcile reads the registered hook set, unions hook-
	// contributed specs with `declared`, dedupes by Spec.Name
	// (declared wins on conflict), then diffs against the current
	// row set and walks each delta toward the desired state.
	Reconcile(ctx context.Context, dc DatacenterInfo, declared []BootstrapServiceSpec) error

	// ListByDatacenter returns the bootstrap workloads attached to
	// a datacenter. Read-only.
	ListByDatacenter(ctx context.Context, datacenterID id.ID) ([]*BootstrapWorkload, error)

	// Get returns a single bootstrap workload by ID. Read-only.
	Get(ctx context.Context, bootstrapID id.ID) (*BootstrapWorkload, error)
}
