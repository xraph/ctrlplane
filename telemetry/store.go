package telemetry

import (
	"context"

	"github.com/xraph/ctrlplane/id"
)

// Store is the persistence interface for telemetry data.
type Store interface {
	// InsertMetrics persists metric data points.
	InsertMetrics(ctx context.Context, metrics []Metric) error

	// QueryMetrics returns metrics matching the query parameters.
	QueryMetrics(ctx context.Context, q MetricQuery) ([]Metric, error)

	// InsertLogs persists log entries.
	InsertLogs(ctx context.Context, logs []LogEntry) error

	// QueryLogs returns log entries matching the query parameters.
	QueryLogs(ctx context.Context, q LogQuery) ([]LogEntry, error)

	// InsertTraces persists trace spans.
	InsertTraces(ctx context.Context, traces []Trace) error

	// QueryTraces returns traces matching the query parameters.
	QueryTraces(ctx context.Context, q TraceQuery) ([]Trace, error)

	// InsertResourceSnapshot persists a resource snapshot.
	InsertResourceSnapshot(ctx context.Context, snap *ResourceSnapshot) error

	// GetLatestResourceSnapshot returns the most recent snapshot for an instance.
	GetLatestResourceSnapshot(ctx context.Context, tenantID string, instanceID id.ID) (*ResourceSnapshot, error)

	// ListResourceSnapshots returns snapshots for an instance within a time range.
	ListResourceSnapshots(ctx context.Context, tenantID string, instanceID id.ID, opts TimeRange) ([]ResourceSnapshot, error)
}
