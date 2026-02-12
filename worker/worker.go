package worker

import (
	"context"
	"time"
)

// Worker is a background task that runs periodically.
type Worker interface {
	// Name identifies the worker.
	Name() string

	// Interval returns how often the worker should run.
	// Return 0 for event-driven workers that do not run periodically.
	Interval() time.Duration

	// Run executes one cycle of the worker.
	Run(ctx context.Context) error
}
