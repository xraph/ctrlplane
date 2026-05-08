package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/bootstrap"
	"github.com/xraph/ctrlplane/datacenter"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/provider"
)

// Reconciler is the platform's per-tick driver for state that lives
// outside any single user request:
//
//   - Datacenter bootstrap services — declarative + hook-contributed
//     shared infrastructure that auto-deploys on every datacenter.
//   - (Future) instance drift correction — detect rows where the
//     persisted state disagrees with the provider and pull them
//     back into agreement. Stub today; the bootstrap path is the
//     first concrete consumer.
//
// The reconciler runs under synthesized system claims (no tenant
// scoping) so it can call services that gate on auth.RequireClaims.
type Reconciler struct {
	instances   instance.Store
	datacenters datacenter.Store
	bootstraps  bootstrap.Service
	providers   *provider.Registry
	events      event.Bus
	interval    time.Duration
}

// NewReconciler wires the reconciler. The instances store stays in
// the signature for the future drift-correction pass; the bootstrap
// service is the only consumer today.
func NewReconciler(
	instances instance.Store,
	datacenters datacenter.Store,
	bootstraps bootstrap.Service,
	providers *provider.Registry,
	events event.Bus,
	interval time.Duration,
) *Reconciler {
	return &Reconciler{
		instances:   instances,
		datacenters: datacenters,
		bootstraps:  bootstraps,
		providers:   providers,
		events:      events,
		interval:    interval,
	}
}

// Name returns the worker name.
func (r *Reconciler) Name() string {
	return "reconciler"
}

// Interval returns how often the reconciler should run.
func (r *Reconciler) Interval() time.Duration {
	return r.interval
}

// Run executes one reconciliation cycle.
//
// Walks every datacenter and asks the bootstrap service to drive its
// state forward. Per-datacenter errors are isolated — a broken
// datacenter does not blind-spot the others.
//
// The list call uses an empty tenantID, which the datacenter store's
// hybrid-visibility query treats as cross-tenant. Bootstrap workloads
// are platform-owned, not tenant-scoped, so the reconciler operates
// on the union of all datacenters across tenants on every tick.
func (r *Reconciler) Run(ctx context.Context) error {
	if r.bootstraps == nil {
		// Bootstrap not wired (legacy app construction path or
		// tests that don't need it) — quietly no-op until it is.
		return nil
	}

	listCtx, listCancel := context.WithTimeout(ctx, gcStoreCallTimeout)
	dcs, err := r.datacenters.ListDatacenters(listCtx, "", datacenter.ListOptions{Limit: 1000})
	listCancel()
	if err != nil {
		return fmt.Errorf("reconciler: list datacenters: %w", err)
	}

	for _, dc := range dcs.Items {
		if dc == nil {
			continue
		}

		// System claims so any inner service that gates on
		// auth.RequireClaims can run; tenant ID intentionally
		// blank because bootstrap workloads aren't tenant-scoped.
		dcCtx := auth.WithClaims(ctx, &auth.Claims{
			SubjectID: systemSubject,
			Roles:     []string{"system:admin"},
		})

		info := bootstrap.DatacenterInfo{
			ID:           dc.ID,
			ProviderName: dc.ProviderName,
			Region:       dc.Region,
			Zone:         dc.Zone,
			Labels:       dc.Labels,
		}

		// Bound the Reconcile call too — it can fan out into provider
		// and instance.Service work that touches the same shared
		// driver pool the dispatch / GC loops use.
		recCtx, recCancel := context.WithTimeout(dcCtx, gcStoreCallTimeout)
		err := r.bootstraps.Reconcile(recCtx, info, dc.BootstrapServices)
		recCancel()
		if err != nil {
			// Isolate per-datacenter failures so the rest of the
			// tick still runs. The bootstrap service itself emits
			// a kind=bootstrap event with the error payload — no
			// need to double-publish here.
			continue
		}
	}

	return nil
}
