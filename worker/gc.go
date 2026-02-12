package worker

import (
	"context"
	"time"

	"github.com/xraph/ctrlplane/store"
)

// GarbageCollector periodically removes stale data from the store.
type GarbageCollector struct {
	store    store.Store
	interval time.Duration
}

// NewGarbageCollector creates a new garbage collector worker.
func NewGarbageCollector(store store.Store, interval time.Duration) *GarbageCollector {
	return &GarbageCollector{
		store:    store,
		interval: interval,
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

// Run executes one garbage collection cycle.
// TODO: implement garbage collection. Remove expired deployments, orphaned
// resources, stale telemetry data, and revoked certificates that exceed their
// retention period.
func (g *GarbageCollector) Run(_ context.Context) error {
	// TODO: implement
	return nil
}
