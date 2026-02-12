package worker

import (
	"context"
	"time"

	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/telemetry"
)

// TelemetryCollector periodically gathers metrics and resource usage from providers.
type TelemetryCollector struct {
	telemetry telemetry.Service
	providers *provider.Registry
	interval  time.Duration
}

// NewTelemetryCollector creates a new telemetry collector worker.
func NewTelemetryCollector(telemetry telemetry.Service, providers *provider.Registry, interval time.Duration) *TelemetryCollector {
	return &TelemetryCollector{
		telemetry: telemetry,
		providers: providers,
		interval:  interval,
	}
}

// Name returns the worker name.
func (t *TelemetryCollector) Name() string {
	return "telemetry_collector"
}

// Interval returns how often the telemetry collector should run.
func (t *TelemetryCollector) Interval() time.Duration {
	return t.interval
}

// Run executes one telemetry collection cycle.
// TODO: implement telemetry collection. Query each registered provider for
// current resource usage and metrics, then push the data through the telemetry
// service for storage and aggregation.
func (t *TelemetryCollector) Run(_ context.Context) error {
	// TODO: implement
	return nil
}
