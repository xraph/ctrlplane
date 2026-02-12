package worker

import (
	"context"
	"time"

	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/provider"
)

// Reconciler compares desired state in the database with actual provider state
// and corrects any drift.
type Reconciler struct {
	instances instance.Store
	providers *provider.Registry
	events    event.Bus
	interval  time.Duration
}

// NewReconciler creates a new reconciler worker.
func NewReconciler(instances instance.Store, providers *provider.Registry, events event.Bus, interval time.Duration) *Reconciler {
	return &Reconciler{
		instances: instances,
		providers: providers,
		events:    events,
		interval:  interval,
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
// TODO: implement reconciliation logic. For each instance in the store, compare
// the desired state (DB) with the actual state reported by the provider. If drift
// is detected, issue corrective actions through the provider and publish events
// for any state changes.
func (r *Reconciler) Run(_ context.Context) error {
	// TODO: implement
	return nil
}
