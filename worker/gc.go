package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/workload"
)

// workloadLabelKey is the instance-label key that records which
// workload owns a replica. Mirrored from workload.spawnReplica's
// stamp at workload/service_impl.go:738; kept as a string literal
// rather than an imported constant to avoid pulling the workload
// package's full surface area into the worker.
const workloadLabelKey = "ctrlplane.workload"

// gcReasonOrphanSwept tags audit events emitted by the GC for
// instances reaped because their parent workload no longer exists.
// Re-uses event.InstanceDeleted (no new event type) with the
// reason carried in the payload — operators querying the audit
// log can distinguish operator-driven deletes from cron-driven
// reaps via this field.
const gcReasonOrphanSwept = "orphan_swept"

// gcStoreCallTimeout caps each individual store roundtrip
// (ListTenants, instances.List, Delete) so a stalled driver session
// can't pin a connection from the shared pool past the next tick.
// 30 seconds is generous for cross-tenant scans on a healthy mongo
// and tight enough that one slow tenant doesn't blind-spot the rest.
const gcStoreCallTimeout = 30 * time.Second

// gcReasonTenantFailed tags an event published when a per-tenant
// sweep aborted half-way. Lets operators correlate GC error spikes
// with specific tenants (e.g. one tenant's storage backend down
// shouldn't blind-spot the rest).
const gcReasonTenantFailed = "gc.tenant_failed"

// GCConfig tunes the garbage-collector's per-tick behaviour.
//
// Defaults are conservative: 500 instances + 15 minutes of grace
// is enough to keep up with a busy tenant without causing the
// worker to run for more than a few seconds, and the grace period
// is wide enough that a workload row insert that happens shortly
// after its first replica is created never gets reaped pre-maturely.
type GCConfig struct {
	// MaxInstancesPerTick caps how many instances the orphan-instance
	// pass examines per tenant per tick. The remainder is picked up
	// by the next tick. Default 500.
	MaxInstancesPerTick int

	// InstanceGracePeriod is the minimum age an instance must reach
	// before it is eligible for reaping, even when its parent
	// workload appears to be missing. Catches mid-Provision races
	// where the workload row is being inserted right after the
	// instance. Default 15 minutes.
	InstanceGracePeriod time.Duration
}

// defaultGCConfig returns the fallback values applied when an
// operator passes a partial GCConfig to NewGarbageCollector.
func defaultGCConfig() GCConfig {
	return GCConfig{
		MaxInstancesPerTick: 500,
		InstanceGracePeriod: 15 * time.Minute,
	}
}

// GarbageCollector periodically removes orphaned resources from
// the store.
//
// Orphan classes handled today:
//
//   - Instances whose ctrlplane.workload label points at a Workload
//     that no longer exists. These are produced by:
//     • a workload.Service.Delete that crashed mid-cascade,
//     • workload rows force-removed via direct store access,
//     • backup-restore drift between the workloads and instances
//     tables.
//
// The GC is a backstop, not the primary cleanup path. The convergent
// instance.Service.Delete is the normal flow; the cron exists so
// operator-visible state catches up eventually when that flow has
// been short-circuited.
//
// Future passes (out of scope for the current implementation but
// flagged for follow-up): orphan releases / deployments tied to
// vanished instances, orphan health-check / domain / route rows,
// orphan certificates whose domain no longer exists. Each of those
// requires the matching child store to grow either a Delete method
// (deploy.Store has none today) or a tenant-global list, neither of
// which is in scope here.
type GarbageCollector struct {
	tenants   admin.Store
	instances instance.Service
	workloads workload.Store
	events    event.Bus
	interval  time.Duration
	cfg       GCConfig

	// clock lets tests inject a deterministic now() without polluting
	// the production constructor. Defaults to time.Now in NewGarbageCollector.
	clock func() time.Time
}

// NewGarbageCollector creates a GC worker.
//
// The worker calls instance.Service.Delete (not the underlying
// store) so the convergent provider Deprovision runs and the
// regular InstanceDeleted event fires for every reap — the same
// observability operators see for an interactive delete.
func NewGarbageCollector(
	tenants admin.Store,
	instances instance.Service,
	workloads workload.Store,
	events event.Bus,
	interval time.Duration,
	cfg GCConfig,
) *GarbageCollector {
	defaults := defaultGCConfig()

	if cfg.MaxInstancesPerTick <= 0 {
		cfg.MaxInstancesPerTick = defaults.MaxInstancesPerTick
	}

	if cfg.InstanceGracePeriod <= 0 {
		cfg.InstanceGracePeriod = defaults.InstanceGracePeriod
	}

	return &GarbageCollector{
		tenants:   tenants,
		instances: instances,
		workloads: workloads,
		events:    events,
		interval:  interval,
		cfg:       cfg,
		clock:     time.Now,
	}
}

// Name returns the worker name.
func (g *GarbageCollector) Name() string {
	return "gc"
}

// Interval returns how often the garbage collector should run.
func (g *GarbageCollector) Interval() time.Duration {
	return g.interval
}

// Run executes one garbage-collection cycle.
//
// One pass per tenant: list instances → for each, if it carries
// the ctrlplane.workload label and the named workload is missing
// AND the instance is older than the grace window, call Delete.
// A per-tenant error is logged via a gc.tenant_failed event and
// the sweep continues to the next tenant — one broken tenant
// must not blind-spot the rest.
func (g *GarbageCollector) Run(ctx context.Context) error {
	listCtx, listCancel := context.WithTimeout(ctx, gcStoreCallTimeout)
	tenants, err := g.tenants.ListTenants(listCtx, admin.ListTenantsOptions{Limit: 1000})
	listCancel()
	if err != nil {
		return fmt.Errorf("gc: list tenants: %w", err)
	}

	var (
		swept   int
		errored int
	)

	for _, tenant := range tenants.Items {
		if tenant == nil {
			continue
		}

		n, sweepErr := g.sweepTenant(ctx, tenant.ID.String())
		if sweepErr != nil {
			errored++

			_ = g.events.Publish(ctx, event.NewEvent(event.InstanceFailed, tenant.ID.String()).
				WithActor(systemSubject).
				WithPayload(map[string]any{
					"reason": gcReasonTenantFailed,
					"error":  sweepErr.Error(),
				}))

			continue
		}

		swept += n
	}

	_ = swept   // metrics-friendly local; consumed by future telemetry hook
	_ = errored // ditto

	return nil
}

// sweepTenant runs the orphan-instance pass for a single tenant.
// Returns the count of reaped instances. Errors from individual
// Delete calls are best-effort: they're logged via per-instance
// events (via the convergent service) and don't abort the sweep —
// the next tick retries since instance.Service.Delete is convergent.
//
// Only the *enumeration* call (List) is treated as fatal for the
// tenant: if we can't even list a tenant's instances, we have no
// way to know what to reap, so we surface the error.
func (g *GarbageCollector) sweepTenant(ctx context.Context, tenantID string) (int, error) {
	tCtx := withSystemClaims(ctx, tenantID)

	listCtx, listCancel := context.WithTimeout(tCtx, gcStoreCallTimeout)
	res, err := g.instances.List(listCtx, instance.ListOptions{Limit: g.cfg.MaxInstancesPerTick})
	listCancel()
	if err != nil {
		return 0, fmt.Errorf("list instances: %w", err)
	}

	var swept int

	now := g.clock()

	for _, inst := range res.Items {
		if inst == nil {
			continue
		}

		if !g.isOrphan(tCtx, inst, now) {
			continue
		}

		delCtx, delCancel := context.WithTimeout(tCtx, gcStoreCallTimeout)
		err := g.instances.Delete(delCtx, inst.ID)
		delCancel()
		if err != nil {
			// Don't abort the tenant sweep on a single Delete
			// failure — convergent Delete will retry next tick.
			// We still emit a tagged event so operators see the
			// failure in the audit log.
			_ = g.events.Publish(tCtx, event.NewEvent(event.InstanceFailed, tenantID).
				WithInstance(inst.ID).
				WithActor(systemSubject).
				WithPayload(map[string]any{
					"reason": gcReasonOrphanSwept,
					"error":  err.Error(),
				}))

			continue
		}

		// Reap succeeded — publish a tagged event so the audit
		// trail can distinguish system-driven from user-driven
		// deletes. The convergent service has already published
		// its plain InstanceDeleted; this is the metadata-bearing
		// twin.
		_ = g.events.Publish(tCtx, event.NewEvent(event.InstanceDeleted, tenantID).
			WithInstance(inst.ID).
			WithActor(systemSubject).
			WithPayload(map[string]any{
				"reason":      gcReasonOrphanSwept,
				"workload_id": inst.Labels[workloadLabelKey],
			}))

		swept++
	}

	return swept, nil
}

// isOrphan returns true when an instance is eligible for reaping.
//
// Eligibility rules — all must hold:
//
//  1. The instance carries a non-empty ctrlplane.workload label.
//     Without it we can't identify the parent and the row is
//     considered out of scope (manual / non-workload instance).
//
//  2. The instance is at least InstanceGracePeriod old. Younger rows
//     get a pass: the workload row may still be in flight after
//     the spawnReplica call — reaping pre-maturely would race the
//     normal happy path.
//
//  3. workload.Store.GetWorkloadByID returns ErrNotFound for the
//     parent. Any other error (transient store failure) is treated
//     as "parent might exist" — we err on the side of *not* reaping.
//     The next tick will check again.
func (g *GarbageCollector) isOrphan(ctx context.Context, inst *instance.Instance, now time.Time) bool {
	parent := inst.Labels[workloadLabelKey]
	if parent == "" {
		return false
	}

	if now.Sub(inst.CreatedAt) < g.cfg.InstanceGracePeriod {
		return false
	}

	parentID, err := id.ParseWithPrefix(parent, id.PrefixWorkload)
	if err != nil {
		// Malformed label value — treat as un-reapable. A future
		// pass can flag this as a data-quality issue, but reaping
		// rows we can't even resolve to a workload would be too
		// aggressive.
		return false
	}

	_, err = g.workloads.GetWorkloadByID(ctx, inst.TenantID, parentID)
	if err == nil {
		return false
	}

	// Only reap on a definitive "not found". Other errors (network
	// blip, store down) keep the row alive for the next tick.
	return errors.Is(err, ctrlplane.ErrNotFound)
}
