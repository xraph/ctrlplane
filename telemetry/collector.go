package telemetry

import (
	"context"

	"github.com/xraph/ctrlplane/id"
)

// Collector gathers telemetry from a provider or external source.
// Implement for custom telemetry sources.
type Collector interface {
	// Name identifies this collector.
	Name() string

	// CollectMetrics gathers current metrics for an instance.
	CollectMetrics(ctx context.Context, instanceID id.ID) ([]Metric, error)

	// CollectResources gathers a resource snapshot for an instance.
	CollectResources(ctx context.Context, instanceID id.ID) (*ResourceSnapshot, error)
}
