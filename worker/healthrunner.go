package worker

import (
	"context"
	"time"

	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/health"
)

// HealthRunner periodically executes health checks for all instances.
type HealthRunner struct {
	health   health.Service
	events   event.Bus
	interval time.Duration
}

// NewHealthRunner creates a new health runner worker.
func NewHealthRunner(health health.Service, events event.Bus, interval time.Duration) *HealthRunner {
	return &HealthRunner{
		health:   health,
		events:   events,
		interval: interval,
	}
}

// Name returns the worker name.
func (h *HealthRunner) Name() string {
	return "health_runner"
}

// Interval returns how often the health runner should run.
func (h *HealthRunner) Interval() time.Duration {
	return h.interval
}

// Run executes one health check cycle.
// TODO: implement health check execution. Iterate over all configured health
// checks, run each via the health service, and publish events for status changes.
func (h *HealthRunner) Run(_ context.Context) error {
	// TODO: implement
	return nil
}
