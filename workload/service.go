package workload

import (
	"context"
	"time"

	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/network"
)

// Service is the public interface for managing Workloads. The
// service orchestrates per-replica Instance lifecycle through
// instance.Service — callers should not call instance.Service.Create
// directly except for one-off debugging needs.
type Service interface {
	// Create allocates a Workload and provisions Replicas Instances
	// in one shot. Returns the Workload; List the Workload's
	// instances separately if you need them.
	Create(ctx context.Context, req CreateRequest) (*Workload, error)

	Get(ctx context.Context, workloadID id.ID) (*Workload, error)
	GetBySlug(ctx context.Context, slug string) (*Workload, error)
	List(ctx context.Context, opts ListOptions) (*ListResult, error)

	// Update mutates the Workload spec (image, env, resources,
	// labels). For changes that need a deploy (image swap),
	// callers should use Deploy instead — Update sets the spec but
	// doesn't push it to running replicas.
	Update(ctx context.Context, workloadID id.ID, req UpdateRequest) (*Workload, error)

	// Scale adjusts the replica count. Adds new Instances when
	// growing, deprovisions trailing Instances when shrinking. Does
	// not touch existing replicas' images or env.
	Scale(ctx context.Context, workloadID id.ID, replicas int) (*Workload, error)

	// Pause scales to zero while retaining the spec. Resume scales
	// back to the previously-set replica count.
	Pause(ctx context.Context, workloadID id.ID) error
	Resume(ctx context.Context, workloadID id.ID) error

	// Restart performs an in-place restart of every replica. Each
	// replica's container is stopped and started again by the
	// underlying provider (docker ContainerRestart, k8s Pod
	// restart, etc.) — no deprovision, no replica-count change,
	// no new container IDs. The Workload state stays Active
	// throughout.
	Restart(ctx context.Context, workloadID id.ID) error

	// Deploy creates a new Release from the Workload's current spec
	// (or from req overrides) and rolls it out to all replicas via
	// the chosen strategy. Returns the Deployment record so callers
	// can poll its state.
	Deploy(ctx context.Context, workloadID id.ID, req DeployRequest) (*deploy.Deployment, error)

	// Delete tears down the Workload and all its replicas.
	Delete(ctx context.Context, workloadID id.ID) error

	// ListInstances returns every Instance owned by the Workload,
	// ordered by ReplicaIndex.
	ListInstances(ctx context.Context, workloadID id.ID) ([]*instance.Instance, error)

	// ListDeployments returns every Deployment whose target instance
	// is one of the workload's replicas. Each rollout is represented
	// once (deployments are inherently per-replica today; if a
	// future Deploy strategy emits a single workload-level record,
	// this aggregator collapses to a passthrough).
	ListDeployments(ctx context.Context, workloadID id.ID, opts deploy.ListOptions) (*deploy.DeployListResult, error)

	// ListReleases returns the union of releases across the
	// workload's replicas, deduplicated by Release.ID. Sorted by
	// CreatedAt descending so the most recent release is first.
	ListReleases(ctx context.Context, workloadID id.ID, opts deploy.ListOptions) (*deploy.ReleaseListResult, error)

	// ListDomains returns every Domain currently bound to any of
	// the workload's replicas, deduplicated by Domain.ID.
	ListDomains(ctx context.Context, workloadID id.ID) ([]network.Domain, error)

	// ListRoutes returns every Route currently bound to any of the
	// workload's replicas, deduplicated by Route.ID.
	ListRoutes(ctx context.Context, workloadID id.ID) ([]network.Route, error)

	// WatchHealth returns a channel that fans in HealthResults from
	// every replica in the workload. Each event carries the source
	// instance ID + replica index. The fan-in re-lists replicas
	// every 30s so a Scale that adds new replicas mid-stream picks
	// them up automatically. Channel is closed when ctx is cancelled.
	WatchHealth(ctx context.Context, workloadID id.ID) (<-chan *HealthEvent, error)

	// StreamLogs returns a channel of log events fanned in across
	// every replica's stdout/stderr. Each event carries the source
	// instance ID + replica index alongside the original log line.
	// Closed when ctx is cancelled or every replica's underlying
	// log stream has terminated.
	StreamLogs(ctx context.Context, workloadID id.ID, opts LogsOptions) (<-chan *LogEvent, error)

	// GetHealth returns the worst-of-replicas health status. Useful
	// for the workspace dashboard badge — aggregates per-replica
	// InstanceHealth and takes the most severe state. Replica counts
	// (healthy/degraded/unhealthy/unknown) are returned alongside so
	// the badge can show "2/3 healthy" detail.
	GetHealth(ctx context.Context, workloadID id.ID) (*WorkloadHealth, error)

	// RangeMetrics returns aggregated resource metrics summed across
	// the workload's replicas at each bucket. CPU/Memory/Network are
	// summed; LatencyP95 and RequestsPerSec are averaged (a per-
	// replica P95 isn't meaningfully summable). Empty when no
	// replicas have any samples in the window.
	RangeMetrics(ctx context.Context, workloadID id.ID, q MetricsRange) (MetricsSeries, error)

	// WatchMetrics emits one MetricsEvent per replica per sample.
	// Aggregating into a workload total is the consumer's job — the
	// dashboard wants per-replica spark lines next to the aggregate.
	WatchMetrics(ctx context.Context, workloadID id.ID) (<-chan *MetricsEvent, error)
}

// LogsOptions mirrors instance.LogsOptions on the workload service.
// Defined here to avoid the workload package importing the
// instance LogsOptions struct directly (cycle prevention).
type LogsOptions struct {
	Follow bool      `json:"follow"`
	Since  time.Time `json:"since,omitzero"`
	Tail   int       `json:"tail,omitempty"`
}

// HealthEvent is one HealthResult tagged with the replica metadata
// the consumer needs to render per-replica state in a UI.
type HealthEvent struct {
	WorkloadID   id.ID `json:"workload_id"`
	InstanceID   id.ID `json:"instance_id"`
	ReplicaIndex int   `json:"replica_index"`
	Result       any   `json:"result"` // *health.HealthResult — kept opaque to avoid an import cycle
}

// LogEvent wraps one line of log output with the replica metadata.
// The Line field is the raw demuxed JSON object emitted by the
// docker provider (or whatever the underlying provider produces),
// kept as bytes so the consumer can stream it through without an
// extra unmarshal.
type LogEvent struct {
	WorkloadID   id.ID  `json:"workload_id"`
	InstanceID   id.ID  `json:"instance_id"`
	ReplicaIndex int    `json:"replica_index"`
	Line         []byte `json:"line"`
}

// WorkloadHealth is the aggregate health view across a workload's
// replicas. Status is the worst-of-replicas — a single unhealthy
// replica is enough to flip the workload to "unhealthy". When no
// replicas have any health checks configured the whole workload
// reports "unknown".
type WorkloadHealth struct {
	WorkloadID    id.ID  `json:"workload_id"`
	Status        string `json:"status"` // healthy / degraded / unhealthy / unknown — uses health.Status string values
	ReplicaCount  int    `json:"replica_count"`
	HealthyCount  int    `json:"healthy_count"`
	DegradedCount int    `json:"degraded_count"`
	UnhealthyCnt  int    `json:"unhealthy_count"`
	UnknownCount  int    `json:"unknown_count"`
}

// MetricsEvent is one metric sample tagged with replica metadata.
// Sample is kept opaque (any) so this package doesn't import the
// metrics package directly — the upstream metrics.Sample type is
// what gets attached at runtime.
type MetricsEvent struct {
	WorkloadID   id.ID `json:"workload_id"`
	InstanceID   id.ID `json:"instance_id"`
	ReplicaIndex int   `json:"replica_index"`
	Sample       any   `json:"sample"` // metrics.Sample
}

// MetricsRange mirrors metrics.RangeQuery without the import.
type MetricsRange struct {
	Since      time.Time     `json:"since"`
	Until      time.Time     `json:"until"`
	Resolution time.Duration `json:"resolution,omitempty"`
}

// MetricsSeries is the aggregated workload-level series. Each
// AggregatedSample sums replica CPU/Memory/Network and averages
// latency-style fields across replicas that had a sample in the
// bucket.
type MetricsSeries []AggregatedSample

// AggregatedSample is a per-bucket roll-up across replicas. It is
// shaped like a single metrics.Sample so dashboards can render it
// the same way as per-instance series.
type AggregatedSample struct {
	At                    time.Time `json:"at"`
	ReplicaCount          int       `json:"replica_count"`
	CPUPercent            float64   `json:"cpu_percent"`
	MemoryUsedMB          int       `json:"memory_used_mb"`
	MemoryLimitMB         int       `json:"memory_limit_mb"`
	NetworkInBytesPerSec  float64   `json:"network_in_bytes_per_sec"`
	NetworkOutBytesPerSec float64   `json:"network_out_bytes_per_sec"`
	RequestsPerSec        float64   `json:"requests_per_sec,omitempty"`
	LatencyP95Ms          float64   `json:"latency_p95_ms,omitempty"`
}
