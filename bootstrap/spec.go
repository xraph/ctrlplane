package bootstrap

import (
	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// State is the lifecycle state of a single bootstrap workload row.
//
// The progression for a healthy install is:
//
//	pending → provisioning → running
//
// On retire (operator removes the spec or a hook stops contributing it):
//
//	running → retiring → retired (then row deleted)
//
// On transient provider failure:
//
//	provisioning → failed → provisioning (retry) → running
//
// The reconciler is the only writer of this field outside Insert.
type State string

const (
	// StatePending — row inserted, no provider call has run yet.
	StatePending State = "pending"

	// StateProvisioning — provider.Provision is in flight.
	StateProvisioning State = "provisioning"

	// StateRunning — provider acknowledged the workload is live;
	// ProviderRef and ServiceRefs populated.
	StateRunning State = "running"

	// StateFailed — last reconcile attempt errored; LastError + Attempts
	// populated. Next tick retries.
	StateFailed State = "failed"

	// StateRetiring — desired set no longer contains this spec;
	// Deprovision is in flight.
	StateRetiring State = "retiring"

	// StateRetired — Deprovision returned successfully. The reconciler
	// will delete the row immediately on the same tick.
	StateRetired State = "retired"
)

// BootstrapServiceSpec is the declarative shape carried on the
// datacenter row and returned by hooks. Re-uses the multi-service
// primitive from provider.ServiceSpec so a bootstrap workload can
// be a Main + Sidecars + Inits group exactly like a tenant workload.
//
// Name is unique per datacenter and is the identity key the reconciler
// uses to dedupe declarative + hook contributions and to match desired
// against current rows.
//
//nolint:revive // BootstrapServiceSpec name is intentional — distinguishes from provider.ServiceSpec.
type BootstrapServiceSpec struct {
	Name     string                 `db:"name"     json:"name"`
	Kind     provider.WorkloadKind  `db:"kind"     json:"kind,omitempty"`     // default deployment
	Replicas int                    `db:"replicas" json:"replicas,omitempty"` // default 1
	Services []provider.ServiceSpec `db:"services" json:"services"`
	Labels   map[string]string      `db:"labels"   json:"labels,omitempty"`
}

// DatacenterInfo is the slice of datacenter state the bootstrap
// package needs to decide what to install. Lives here (not in the
// datacenter package) so bootstrap stays a leaf package — the
// datacenter package imports bootstrap, never the other way round.
type DatacenterInfo struct {
	ID           id.ID
	ProviderName string
	Region       string
	Zone         string
	Labels       map[string]string
}

// BootstrapWorkload is one running bootstrap row. Detached from
// tenants — owned by a datacenter, managed by the platform,
// reconciled toward the union of declarative + hook-contributed
// specs.
type BootstrapWorkload struct {
	ctrlplane.Entity

	DatacenterID id.ID                  `db:"datacenter_id" json:"datacenter_id"`
	Name         string                 `db:"name"          json:"name"`
	Kind         provider.WorkloadKind  `db:"kind"          json:"kind"`
	Services     []provider.ServiceSpec `db:"services"      json:"services"`
	State        State                  `db:"state"         json:"state"`
	ProviderRef  string                 `db:"provider_ref"  json:"provider_ref,omitempty"`
	ServiceRefs  map[string]string      `db:"service_refs"  json:"service_refs,omitempty"`
	LastError    string                 `db:"last_error"    json:"last_error,omitempty"`
	Attempts     int                    `db:"attempts"      json:"attempts"`
	Labels       map[string]string      `db:"labels"        json:"labels,omitempty"`
}

// NewBootstrapWorkload mints a fresh bootstrap row with a TypeID
// and timestamps. State starts as Pending — the reconciler advances
// it on the next tick.
func NewBootstrapWorkload() *BootstrapWorkload {
	return &BootstrapWorkload{
		Entity: ctrlplane.NewEntity(id.PrefixBootstrap),
		State:  StatePending,
	}
}
