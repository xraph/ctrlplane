package provider

// ServiceRole categorises a service inside a Workload by lifecycle.
//
//   - RoleMain: long-lived process; the workload's primary container.
//     Every workload must have exactly one Main service. Workload health
//     defaults to the Main service's readiness.
//   - RoleSidecar: long-lived process that runs alongside Main inside the
//     same co-scheduling unit (k8s Pod, Nomad TaskGroup, Docker Compose
//     project). Examples: log-forwarding agent, envoy proxy, metrics
//     shipper. Started after Main in dependency order.
//   - RoleInit: run-once before Main and Sidecars start. The Main/Sidecars
//     do not start until every Init has exited successfully. An Init
//     failure marks the Instance as Failed.
type ServiceRole string

const (
	RoleMain    ServiceRole = "main"
	RoleSidecar ServiceRole = "sidecar"
	RoleInit    ServiceRole = "init"
)

// WorkloadKind picks the runtime topology a workload deploys as.
//
//   - KindDeployment: stateless replicas. On Kubernetes this becomes a
//     Deployment; on Nomad a Job/TaskGroup; on Docker N independent
//     Compose projects. Volumes are shared/ephemeral by default.
//   - KindStatefulSet: replicas with stable per-replica identity and
//     storage. On Kubernetes this becomes a StatefulSet + headless Service
//     with volumeClaimTemplates; on Docker per-replica named volumes give
//     equivalent semantics.
type WorkloadKind string

const (
	KindDeployment  WorkloadKind = "deployment"
	KindStatefulSet WorkloadKind = "stateful_set"
)

// ServiceSpec is the per-service slice of a Workload's spec. A Workload
// is `Services []ServiceSpec` plus workload-level fields (Replicas, Kind,
// Labels). Each entry produces one container/task within the replica's
// co-scheduling unit.
//
// SecretRef and ConfigFile are spec-level descriptors of vault-backed
// data the provider mounts at provision time; they live in this package
// (rather than template/) so this struct can embed them without a cycle.
type ServiceSpec struct {
	// Name is the service's identifier within the workload. Must be
	// unique among the workload's services and DNS-safe (lowercase
	// alphanumerics + hyphens) so providers can use it as a network
	// alias for service-to-service discovery.
	Name string `json:"name" validate:"required"`

	// Image is the container image reference, e.g. "nginx:1.25".
	Image string `json:"image" validate:"required"`

	// Role determines lifecycle (Main / Sidecar / Init). Defaults to
	// Main when empty; validation enforces exactly one Main per workload.
	Role ServiceRole `json:"role,omitempty"`

	// DependsOn lists service names that must reach Running before this
	// service starts. Used for ordering Sidecars relative to Main and
	// for chaining Inits. A cycle is a validation error.
	DependsOn []string `json:"depends_on,omitempty"`

	// Resources / Env / Ports / Volumes / HealthCheck are per-service.
	// Two services in the same workload can request different CPU/memory
	// or expose different ports.
	Resources   ResourceSpec      `json:"resources"`
	Env         map[string]string `json:"env,omitempty"`
	Ports       []PortSpec        `json:"ports,omitempty"`
	Volumes     []VolumeSpec      `json:"volumes,omitempty"`
	HealthCheck *HealthCheckSpec  `json:"health_check,omitempty"`

	// Secrets references env-injected secrets resolved against the
	// vault at deploy time. Per-service so a sidecar can have its own
	// credentials without leaking them to the main container.
	Secrets []SecretRef `json:"secrets,omitempty"`

	// ConfigFiles describes vault-backed files mounted into this
	// service's filesystem at deploy time.
	ConfigFiles []ConfigFile `json:"config_files,omitempty"`

	// Annotations is per-service free-form metadata (e.g. k8s
	// container annotations). Workload-level metadata lives on the
	// Workload's Labels map.
	Annotations map[string]string `json:"annotations,omitempty"`

	// Command and Args optionally override the container image's
	// ENTRYPOINT and CMD respectively. Empty leaves the image
	// defaults in place.
	Command []string `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
}

// ServiceStatus is the per-service runtime state reported back from the
// provider. InstanceStatus.Services map keys these by ServiceSpec.Name.
type ServiceStatus struct {
	State       InstanceState `json:"state"`
	Ready       bool          `json:"ready"`
	Restarts    int           `json:"restarts"`
	ProviderRef string        `json:"provider_ref,omitempty"`
	Message     string        `json:"message,omitempty"`
}

// ServiceDeploySpec is a per-service slice of a Deploy operation. A
// Deploy carries Services []ServiceDeploySpec — partial deploys leave
// services not listed here untouched.
type ServiceDeploySpec struct {
	Name        string            `json:"name"  validate:"required"`
	Image       string            `json:"image" validate:"required"`
	Env         map[string]string `json:"env,omitempty"`
	HealthCheck *HealthCheckSpec  `json:"health_check,omitempty"`
}

// ServiceSnapshot is the per-service slice of a Release. Releases are
// always self-contained — partial deploys produce a new Release whose
// non-targeted services are inherited from the prior Release.
type ServiceSnapshot struct {
	Name  string            `json:"name"`
	Image string            `json:"image"`
	Env   map[string]string `json:"env,omitempty"`
}
