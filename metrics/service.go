package metrics

import (
	"context"
	"time"

	"github.com/xraph/ctrlplane/id"
)

// Service is the public read/subscribe surface for instance metrics.
// Writes happen through the poller, which the service owns and runs
// internally — callers never push samples directly.
type Service interface {
	// Range returns downsampled samples for the given instance and
	// window. Returns nil with no error when no samples are stored
	// (instance never tracked, or tracking just started).
	Range(ctx context.Context, instanceID id.ID, q RangeQuery) (Series, error)

	// Watch returns a channel that receives every newly-pushed
	// sample for instanceID until ctx cancels. Buffered; drops on
	// slow consumers.
	Watch(ctx context.Context, instanceID id.ID) (<-chan Sample, error)

	// Latest returns the most recent sample, ok=false when none.
	Latest(instanceID id.ID) (Sample, bool)

	// Track / Untrack are lifecycle hooks. Track starts the poller
	// for the given instance; Untrack stops it and discards stored
	// samples. Idempotent.
	Track(instanceID id.ID)
	Untrack(instanceID id.ID)
}

// Sampler is what the poller uses to take a one-shot reading. The
// concrete impl wraps instance.Service so we can resolve the
// provider per-instance without metrics depending on the full
// instance package.
type Sampler interface {
	Sample(ctx context.Context, instanceID id.ID) (*Sample, error)
}

// Config tunes the service.
type Config struct {
	// PollInterval is how often the poller takes a sample per
	// tracked instance. Default 10s.
	PollInterval time.Duration

	// RetentionCapacity is the per-instance ring-buffer capacity.
	// At 10s interval, 60480 samples = 7d. Default 60480.
	RetentionCapacity int

	// WatchBuffer is the per-subscriber channel buffer. Default 64.
	WatchBuffer int
}

// DefaultConfig returns sensible defaults for dev + small prod.
func DefaultConfig() Config {
	return Config{
		PollInterval:      10 * time.Second,
		RetentionCapacity: 60480, // 7d at 10s
		WatchBuffer:       64,
	}
}
