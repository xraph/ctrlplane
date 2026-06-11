package instance

import (
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// Instance is one replica of a Workload — a single co-scheduling unit
// (k8s Pod, Nomad allocation, Docker Compose project) running every
// service in the workload's Services slice.
type Instance struct {
	ctrlplane.Entity

	TenantID     string `db:"tenant_id"     json:"tenant_id"`
	Name         string `db:"name"          json:"name"`
	Slug         string `db:"slug"          json:"slug"`
	DatacenterID id.ID  `db:"datacenter_id" json:"datacenter_id,omitzero"`
	ProviderName string `db:"provider_name" json:"provider_name"`

	// ProviderRef is the workload-level handle (Pod name on k8s,
	// Compose project name on Docker, Allocation ID on Nomad).
	ProviderRef string `db:"provider_ref" json:"provider_ref"`

	// ServiceRefs maps a service's Name to its provider-specific
	// per-container ref (container ID on Docker, container name within
	// the Pod on k8s, Task name on Nomad). Used for per-service
	// log/exec/restart operations.
	ServiceRefs map[string]string `db:"service_refs" json:"service_refs,omitempty"`

	Region string                 `db:"region" json:"region"`
	State  provider.InstanceState `db:"state"  json:"state"`

	// Kind is inherited from the Workload at provision time and locked
	// for the instance's lifetime. Mirrors Workload.Kind so per-replica
	// teardown doesn't have to look the workload up.
	Kind provider.WorkloadKind `db:"kind" json:"kind"`

	// Services is a snapshot of the spec the replica was provisioned
	// with. Mutating Workload.Services does not retroactively change
	// running replicas — the next Deploy bumps each replica's snapshot
	// to the new Release.
	Services []provider.ServiceSpec `db:"services" json:"services"`

	// Source records what was deployed (services | helm | manifests |
	// argocd) so teardown and status route to the right provider engine.
	// Empty on legacy instances, which are treated as services.
	Source provider.DeploymentSource `db:"source" json:"source,omitzero"`

	// Endpoints is the union of every service's accessible endpoints,
	// each tagged with ServiceName.
	Endpoints []provider.Endpoint `db:"endpoints" json:"endpoints,omitempty"`

	// Labels is workload-level metadata (mirrors Workload.Labels) plus
	// the per-replica `ctrlplane.replica_index=<N>` label.
	Labels map[string]string `db:"labels" json:"labels,omitempty"`

	CurrentRelease id.ID      `db:"current_release" json:"current_release,omitzero"`
	SuspendedAt    *time.Time `db:"suspended_at"    json:"suspended_at,omitempty"`
}

// MainService returns the Main service from the instance's snapshot.
// Convenience for callers that want "the instance's primary image" —
// e.g. dashboard summaries.
func (i *Instance) MainService() *provider.ServiceSpec {
	if i == nil {
		return nil
	}

	for j := range i.Services {
		if i.Services[j].Role == provider.RoleMain || i.Services[j].Role == "" {
			return &i.Services[j]
		}
	}

	return nil
}
