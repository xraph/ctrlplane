package workload

import (
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// Workload is the top-level deployable unit. It owns N Instance
// replicas (the actual running co-scheduling units — k8s Pods, Nomad
// allocations, Docker Compose projects). Workload itself never runs
// anything; provider interactions happen via Instance lifecycle.
//
// Multi-service: Services is the per-service spec slice. Every replica
// runs every service co-scheduled together. Workload-level fields
// (ReplicaCount, Kind, Labels, State) apply across all services.
type Workload struct {
	ctrlplane.Entity

	TenantID     string `db:"tenant_id"     json:"tenant_id"`
	Name         string `db:"name"          json:"name"`
	Slug         string `db:"slug"          json:"slug"`
	DatacenterID id.ID  `db:"datacenter_id" json:"datacenter_id,omitzero"`
	ProviderName string `db:"provider_name" json:"provider_name"`
	Region       string `db:"region"        json:"region"`

	// Kind selects the runtime topology — KindDeployment for stateless
	// replicas (default), KindStatefulSet for stable per-replica
	// identity + storage. Cannot change after creation; mutating this
	// field through Update is rejected.
	Kind provider.WorkloadKind `db:"kind" json:"kind"`

	// Services is the per-service spec applied to every replica. One
	// Workload spawns N replicas; each replica spawns every service in
	// this slice as a co-scheduled container/task.
	Services []provider.ServiceSpec `db:"services" json:"services"`

	// Labels is workload-level metadata applied to every container in
	// every replica. ctrlplane reserves keys prefixed `ctrlplane.` for
	// internal discovery (e.g. `ctrlplane.workload=<id>`).
	Labels map[string]string `db:"labels" json:"labels,omitempty"`

	// CurrentReleaseID points at the Release whose snapshot is in effect.
	// Bumped by Deploy. Empty until the first Deploy succeeds.
	CurrentReleaseID id.ID `db:"current_release_id" json:"current_release_id,omitzero"`

	// ReplicaCount is the desired number of running replicas.
	ReplicaCount int `db:"replica_count" json:"replica_count"`

	// State is the lifecycle of the Workload as an aggregate.
	State State `db:"state" json:"state"`

	PausedAt *time.Time `db:"paused_at" json:"paused_at,omitempty"`

	// PreviousReplicas remembers the desired replica count just
	// before the most recent Pause. Resume reads this so a 3-replica
	// workload paused-and-resumed comes back as 3 replicas, not 1.
	PreviousReplicas int `db:"previous_replicas" json:"previous_replicas,omitempty"`

	// TemplateID records the template (if any) the Workload was forked
	// from. Empty for workloads created from raw fields.
	TemplateID id.ID `db:"template_id" json:"template_id,omitzero"`
}

// State is the lifecycle of a Workload as an aggregate.
type State string

const (
	// StateProvisioning — initial replicas being created.
	StateProvisioning State = "provisioning"

	// StateActive — desired replica count met and all replicas Running.
	StateActive State = "active"

	// StateScaling — a Scale call is mid-flight.
	StateScaling State = "scaling"

	// StateDeploying — a Deploy is mid-rollout.
	StateDeploying State = "deploying"

	// StatePaused — explicitly scaled to zero. Spec retained.
	StatePaused State = "paused"

	// StateFailed — non-recoverable error. Manual intervention required.
	StateFailed State = "failed"

	StateDestroying State = "destroying"
	StateDestroyed  State = "destroyed"
)

// NewWorkload allocates a Workload with a fresh ID and timestamps.
// Defaults to StateProvisioning + KindDeployment so callers don't
// have to remember to set them.
func NewWorkload() *Workload {
	return &Workload{
		Entity: ctrlplane.NewEntity(id.PrefixWorkload),
		State:  StateProvisioning,
		Kind:   provider.KindDeployment,
	}
}

// MainService returns the workload's primary (RoleMain) service, or
// nil when no Main is configured. Used by callers that need a single
// representative service — health, default network endpoint,
// "the workload's image" displays.
func (w *Workload) MainService() *provider.ServiceSpec {
	if w == nil {
		return nil
	}

	for i := range w.Services {
		if w.Services[i].Role == provider.RoleMain || w.Services[i].Role == "" {
			return &w.Services[i]
		}
	}

	return nil
}
