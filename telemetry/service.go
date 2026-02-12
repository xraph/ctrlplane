package telemetry

import (
	"context"
	"time"

	"github.com/xraph/ctrlplane/id"
)

// Service manages telemetry collection, storage, and querying.
type Service interface {
	// PushMetrics ingests metric data points.
	PushMetrics(ctx context.Context, metrics []Metric) error

	// QueryMetrics returns metrics matching the query.
	QueryMetrics(ctx context.Context, q MetricQuery) ([]Metric, error)

	// PushLogs ingests log entries.
	PushLogs(ctx context.Context, logs []LogEntry) error

	// QueryLogs returns log entries matching the query.
	QueryLogs(ctx context.Context, q LogQuery) ([]LogEntry, error)

	// PushTraces ingests trace spans.
	PushTraces(ctx context.Context, traces []Trace) error

	// QueryTraces returns traces matching the query.
	QueryTraces(ctx context.Context, q TraceQuery) ([]Trace, error)

	// GetCurrentResources returns the latest resource snapshot for an instance.
	GetCurrentResources(ctx context.Context, instanceID id.ID) (*ResourceSnapshot, error)

	// GetResourceHistory returns resource snapshots over a time range.
	GetResourceHistory(ctx context.Context, instanceID id.ID, opts TimeRange) ([]ResourceSnapshot, error)

	// GetDashboard returns a pre-aggregated view of instance telemetry.
	GetDashboard(ctx context.Context, instanceID id.ID) (*DashboardData, error)

	// RegisterCollector adds a custom telemetry collector.
	RegisterCollector(collector Collector)
}

// MetricQuery configures a metrics query.
type MetricQuery struct {
	InstanceID id.ID         `json:"instance_id"`
	Name       string        `json:"name,omitempty"`
	Since      time.Time     `json:"since"`
	Until      time.Time     `json:"until"`
	Step       time.Duration `json:"step,omitempty"`
	Limit      int           `json:"limit,omitempty"`
}

// LogQuery configures a log query.
type LogQuery struct {
	InstanceID id.ID     `json:"instance_id"`
	Level      string    `json:"level,omitempty"`
	Search     string    `json:"search,omitempty"`
	Since      time.Time `json:"since"`
	Until      time.Time `json:"until"`
	Limit      int       `json:"limit,omitempty"`
}

// TraceQuery configures a trace query.
type TraceQuery struct {
	InstanceID id.ID     `json:"instance_id"`
	TraceID    string    `json:"trace_id,omitempty"`
	Operation  string    `json:"operation,omitempty"`
	Since      time.Time `json:"since"`
	Until      time.Time `json:"until"`
	Limit      int       `json:"limit,omitempty"`
}

// TimeRange specifies a time window for queries.
type TimeRange struct {
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
}

// DashboardData is a pre-aggregated view of instance telemetry.
type DashboardData struct {
	InstanceID    id.ID             `json:"instance_id"`
	Resources     *ResourceSnapshot `json:"resources"`
	HealthStatus  string            `json:"health_status"`
	UptimePercent float64           `json:"uptime_percent"`
	RequestRate   float64           `json:"request_rate_per_sec"`
	ErrorRate     float64           `json:"error_rate_per_sec"`
	AvgLatency    time.Duration     `json:"avg_latency"`
	P99Latency    time.Duration     `json:"p99_latency"`
	RecentDeploys int               `json:"recent_deploys_24h"`
	ActiveAlerts  int               `json:"active_alerts"`
}
