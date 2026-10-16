package obs

import (
	"strings"
	"sync"
	"time"

	gometrics "github.com/xraph/go-utils/metrics"
)

// Metric names. Forge exports a name that isn't an OTel semantic convention
// under its namespace, so ctrlplane_worker_run_duration_seconds scrapes as
// forge_ctrlplane_worker_run_duration_seconds. Forge adds no unit suffix to
// such names, which is why _seconds and _total are spelled out here.
const (
	MetricWorkerRunDuration    = "ctrlplane_worker_run_duration_seconds"
	MetricWorkerRunsSkipped    = "ctrlplane_worker_runs_skipped_total"
	MetricSchedulerLeader      = "ctrlplane_scheduler_leader"
	MetricLeaderChecks         = "ctrlplane_leader_checks_total"
	MetricDatacenterReconciles = "ctrlplane_datacenter_reconciles_total"
	MetricBootstrapActions     = "ctrlplane_bootstrap_actions_total"
	MetricGCTenantSweeps       = "ctrlplane_gc_tenant_sweeps_total"
	MetricGCInstancesReaped    = "ctrlplane_gc_instances_reaped_total"
	MetricProviderCallDuration = "ctrlplane_provider_call_duration_seconds"
	MetricResolverDuration     = "ctrlplane_provider_credentials_duration_seconds"
	MetricReadinessWait        = "ctrlplane_readiness_wait_duration_seconds"
	MetricDeploymentDuration   = "ctrlplane_deployment_duration_seconds"
	MetricProviderHealthChecks = "ctrlplane_provider_health_check_duration_seconds"
	MetricResourceSamples      = "ctrlplane_resource_samples_total"
	MetricResourcePollers      = "ctrlplane_resource_pollers"
	MetricHealthCheckDuration  = "ctrlplane_health_check_duration_seconds"
	MetricLifecycleEvents      = "ctrlplane_lifecycle_events_total"
	MetricPluginHookErrors     = "ctrlplane_plugin_hook_errors_total"
)

// Label keys. Every series of a family carries the same keys.
const (
	LabelWorker    = "worker"
	LabelResult    = "result"
	LabelAction    = "action"
	LabelProvider  = "provider"
	LabelOperation = "operation"
	LabelStrategy  = "strategy"
	LabelCheckType = "check_type"
	LabelStatus    = "status"
	LabelEvent     = "event"
	LabelHook      = "hook"
)

// Bucket sets, in seconds.
var (
	// workerBuckets covers a worker pass from a no-op tick to a long sweep.
	workerBuckets = []float64{0.005, 0.025, 0.1, 0.5, 1, 5, 15, 30, 60, 120, 300}

	// callBuckets covers a provider call from a cached status read to a
	// slow provision or Helm install.
	callBuckets = []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300}

	// credentialBuckets covers a resolver from a cache hit to a slow vault.
	credentialBuckets = []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}

	// rolloutBuckets covers a readiness wait or a whole deployment, which
	// can take until the five minute readiness timeout and beyond.
	rolloutBuckets = []float64{0.1, 0.5, 1, 5, 10, 30, 60, 120, 300, 600, 1200, 1800}

	// probeBuckets covers a health probe, bounded by its timeout.
	probeBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
)

// Metrics records Ctrl Plane's metric families on a go-utils sink. Build it
// with NewMetrics; a nil *Metrics is valid and records nothing, so every
// subsystem can hold one whether metrics are wired or not.
type Metrics struct {
	workerRunDuration    *vec[gometrics.Histogram]
	workerRunsSkipped    *vec[gometrics.Counter]
	schedulerLeader      gometrics.Gauge
	leaderChecks         *vec[gometrics.Counter]
	datacenterReconciles *vec[gometrics.Counter]
	bootstrapActions     *vec[gometrics.Counter]
	gcTenantSweeps       *vec[gometrics.Counter]
	gcInstancesReaped    *vec[gometrics.Counter]
	providerCallDuration *vec[gometrics.Histogram]
	credentialsDuration  *vec[gometrics.Histogram]
	readinessWait        *vec[gometrics.Histogram]
	deploymentDuration   *vec[gometrics.Histogram]
	providerHealthChecks *vec[gometrics.Histogram]
	resourceSamples      *vec[gometrics.Counter]
	resourcePollers      gometrics.Gauge
	healthCheckDuration  *vec[gometrics.Histogram]
	lifecycleEvents      *vec[gometrics.Counter]
	pluginHookErrors     *vec[gometrics.Counter]
}

// NewMetrics declares Ctrl Plane's metric families on m. Labelled series are
// created on first use. A nil m returns nil, which records nothing.
func NewMetrics(m gometrics.MetricFactory) *Metrics {
	if m == nil {
		return nil
	}

	return &Metrics{
		workerRunDuration: histogramVec(m, MetricWorkerRunDuration, workerBuckets,
			"Time one background worker pass took.", LabelWorker, LabelResult),
		workerRunsSkipped: counterVec(m, MetricWorkerRunsSkipped,
			"Worker ticks skipped because this replica was not the leader.", LabelWorker),
		schedulerLeader: m.Gauge(MetricSchedulerLeader,
			gometrics.WithDescription("1 while this replica runs the background workers, 0 while another replica leads.")),
		leaderChecks: counterVec(m, MetricLeaderChecks,
			"Leader election checks by answer.", LabelResult),
		datacenterReconciles: counterVec(m, MetricDatacenterReconciles,
			"Per datacenter bootstrap reconciles.", LabelResult),
		bootstrapActions: counterVec(m, MetricBootstrapActions,
			"Bootstrap workloads provisioned or retired.", LabelAction, LabelResult),
		gcTenantSweeps: counterVec(m, MetricGCTenantSweeps,
			"Per tenant orphan sweeps by the garbage collector.", LabelResult),
		gcInstancesReaped: counterVec(m, MetricGCInstancesReaped,
			"Orphaned instances the garbage collector tried to delete.", LabelResult),
		providerCallDuration: histogramVec(m, MetricProviderCallDuration, callBuckets,
			"Time a provider operation took.", LabelProvider, LabelOperation, LabelResult),
		credentialsDuration: histogramVec(m, MetricResolverDuration, credentialBuckets,
			"Time the credentials resolver took before a provider call.", LabelResult),
		readinessWait: histogramVec(m, MetricReadinessWait, rolloutBuckets,
			"Time spent waiting for an asynchronous provider to report an instance ready.", LabelResult),
		deploymentDuration: histogramVec(m, MetricDeploymentDuration, rolloutBuckets,
			"Time a deployment or rollback took, from strategy start to readiness.", LabelStrategy, LabelAction, LabelResult),
		providerHealthChecks: histogramVec(m, MetricProviderHealthChecks, probeBuckets,
			"Time a provider health check took.", LabelProvider, LabelResult),
		resourceSamples: counterVec(m, MetricResourceSamples,
			"Instance resource samples the metrics poller took.", LabelResult),
		resourcePollers: m.Gauge(MetricResourcePollers,
			gometrics.WithDescription("Instances the metrics poller is tracking.")),
		healthCheckDuration: histogramVec(m, MetricHealthCheckDuration, probeBuckets,
			"Time an instance health check took.", LabelCheckType, LabelStatus),
		lifecycleEvents: counterVec(m, MetricLifecycleEvents,
			"Lifecycle events delivered to plugins.", LabelEvent),
		pluginHookErrors: counterVec(m, MetricPluginHookErrors,
			"Plugin lifecycle hooks that returned an error.", LabelHook),
	}
}

// WorkerRun records one worker pass. result is ok or error.
func (m *Metrics) WorkerRun(worker string, took time.Duration, err error) {
	if m == nil {
		return
	}

	m.workerRunDuration.with(worker, Result(err)).Observe(took.Seconds())
}

// WorkerSkipped records a tick a worker skipped because this replica did
// not lead.
func (m *Metrics) WorkerSkipped(worker string) {
	if m == nil {
		return
	}

	m.workerRunsSkipped.with(worker).Inc()
}

// Values of the leader check result label.
const (
	LeaderLeader   = "leader"
	LeaderFollower = "follower"
)

// LeaderCheck records one leader election answer: leader, follower, or
// error when the elector could not tell. The scheduler leader gauge
// follows the answer, with an error counting as not leading.
func (m *Metrics) LeaderCheck(leader bool, err error) {
	if m == nil {
		return
	}

	result := LeaderFollower

	switch {
	case err != nil:
		result = ResultError
	case leader:
		result = LeaderLeader
	}

	m.leaderChecks.with(result).Inc()

	if leader && err == nil {
		m.schedulerLeader.Set(1)
	} else {
		m.schedulerLeader.Set(0)
	}
}

// SetLeader sets the scheduler leader gauge without counting a check. The
// scheduler uses it when no elector is configured, and when it resigns.
func (m *Metrics) SetLeader(leader bool) {
	if m == nil {
		return
	}

	if leader {
		m.schedulerLeader.Set(1)
	} else {
		m.schedulerLeader.Set(0)
	}
}

// DatacenterReconciled records one datacenter's bootstrap reconcile.
func (m *Metrics) DatacenterReconciled(err error) {
	if m == nil {
		return
	}

	m.datacenterReconciles.with(Result(err)).Inc()
}

// Values of the bootstrap action label.
const (
	ActionProvision = "provision"
	ActionRetire    = "retire"
)

// BootstrapAction records a bootstrap workload provision or retirement.
func (m *Metrics) BootstrapAction(action string, err error) {
	if m == nil {
		return
	}

	m.bootstrapActions.with(action, Result(err)).Inc()
}

// GCTenantSwept records one tenant's orphan sweep.
func (m *Metrics) GCTenantSwept(err error) {
	if m == nil {
		return
	}

	m.gcTenantSweeps.with(Result(err)).Inc()
}

// GCInstanceReaped records one orphan delete attempt.
func (m *Metrics) GCInstanceReaped(err error) {
	if m == nil {
		return
	}

	m.gcInstancesReaped.with(Result(err)).Inc()
}

// CredentialsResolved records one credentials resolver call.
func (m *Metrics) CredentialsResolved(took time.Duration, err error) {
	if m == nil {
		return
	}

	m.credentialsDuration.with(Result(err)).Observe(took.Seconds())
}

// ReadinessWaited records one readiness wait and how it ended: ready,
// failed, timeout or canceled (provider.ReadinessOutcome).
func (m *Metrics) ReadinessWaited(outcome string, took time.Duration) {
	if m == nil {
		return
	}

	m.readinessWait.with(outcome).Observe(took.Seconds())
}

// Values of the deployment action label.
const (
	ActionDeploy   = "deploy"
	ActionRollback = "rollback"
)

// DeploymentFinished records one deployment or rollback.
func (m *Metrics) DeploymentFinished(strategy, action string, took time.Duration, err error) {
	if m == nil {
		return
	}

	m.deploymentDuration.with(strategy, action, Result(err)).Observe(took.Seconds())
}

// Values of the provider health check result label.
const (
	HealthHealthy   = "healthy"
	HealthUnhealthy = "unhealthy"
)

// ProviderHealthChecked records one provider health check. result is
// healthy, unhealthy, or error when the check itself failed.
func (m *Metrics) ProviderHealthChecked(providerType, result string, took time.Duration) {
	if m == nil {
		return
	}

	m.providerHealthChecks.with(providerType, result).Observe(took.Seconds())
}

// SampleEmpty is the resource sample result label value for a provider
// that had nothing to report.
const SampleEmpty = "empty"

// ResourceSampled records one metrics poller sample: ok, empty when the
// provider had nothing to report, or error.
func (m *Metrics) ResourceSampled(result string) {
	if m == nil {
		return
	}

	m.resourceSamples.with(result).Inc()
}

// SetResourcePollers sets how many instances the metrics poller tracks.
func (m *Metrics) SetResourcePollers(n int) {
	if m == nil {
		return
	}

	m.resourcePollers.Set(float64(n))
}

// HealthChecked records one instance health check. status is the result's
// health status, or error when the checker failed to run.
func (m *Metrics) HealthChecked(checkType, status string, took time.Duration) {
	if m == nil {
		return
	}

	m.healthCheckDuration.with(checkType, status).Observe(took.Seconds())
}

// LifecycleEvent counts one lifecycle event delivered to plugins.
func (m *Metrics) LifecycleEvent(eventType string) {
	if m == nil {
		return
	}

	m.lifecycleEvents.with(eventType).Inc()
}

// PluginHookFailed counts one plugin hook that returned an error.
func (m *Metrics) PluginHookFailed(hook string) {
	if m == nil {
		return
	}

	m.pluginHookErrors.with(hook).Inc()
}

// vec is a metric family with declared label names. Children are created
// on first use and cached, so the hot path takes no registry lock.
type vec[T any] struct {
	labels []string
	build  func(labels map[string]string) T
	cache  sync.Map // joined label values -> T
}

func (v *vec[T]) with(values ...string) T {
	key := strings.Join(values, "\xff")

	if child, ok := v.cache.Load(key); ok {
		return child.(T) //nolint:forcetypeassert // the cache only holds T
	}

	labels := make(map[string]string, len(v.labels))
	for i, name := range v.labels {
		labels[name] = values[i]
	}

	child, _ := v.cache.LoadOrStore(key, v.build(labels))

	return child.(T) //nolint:forcetypeassert // the cache only holds T
}

func counterVec(m gometrics.MetricFactory, name, help string, labels ...string) *vec[gometrics.Counter] {
	return &vec[gometrics.Counter]{
		labels: labels,
		build: func(l map[string]string) gometrics.Counter {
			return m.Counter(name, gometrics.WithDescription(help), gometrics.WithLabels(l))
		},
	}
}

func histogramVec(m gometrics.MetricFactory, name string, buckets []float64, help string, labels ...string) *vec[gometrics.Histogram] {
	return &vec[gometrics.Histogram]{
		labels: labels,
		build: func(l map[string]string) gometrics.Histogram {
			return m.Histogram(name,
				gometrics.WithDescription(help),
				gometrics.WithUnit("s"),
				gometrics.WithBuckets(buckets...),
				gometrics.WithLabels(l))
		},
	}
}
