package telemetry

import (
	"time"

	"github.com/xraph/ctrlplane/id"
)

// MetricType classifies the metric.
type MetricType string

const (
	// MetricGauge is a point-in-time value.
	MetricGauge MetricType = "gauge"

	// MetricCounter is a monotonically increasing value.
	MetricCounter MetricType = "counter"

	// MetricHist is a histogram distribution.
	MetricHist MetricType = "histogram"
)

// Metric is a single metric data point.
type Metric struct {
	InstanceID id.ID             `db:"instance_id" json:"instance_id"`
	TenantID   string            `db:"tenant_id"   json:"tenant_id"`
	Name       string            `db:"name"        json:"name"`
	Type       MetricType        `db:"type"        json:"type"`
	Value      float64           `db:"value"       json:"value"`
	Labels     map[string]string `db:"labels"      json:"labels,omitempty"`
	Timestamp  time.Time         `db:"timestamp"   json:"timestamp"`
}

// LogEntry is a structured log line from an instance.
type LogEntry struct {
	InstanceID id.ID          `db:"instance_id" json:"instance_id"`
	TenantID   string         `db:"tenant_id"   json:"tenant_id"`
	Level      string         `db:"level"       json:"level"`
	Message    string         `db:"message"     json:"message"`
	Fields     map[string]any `db:"fields"      json:"fields,omitempty"`
	Source     string         `db:"source"      json:"source"`
	Timestamp  time.Time      `db:"timestamp"   json:"timestamp"`
}

// Trace represents a distributed trace span.
type Trace struct {
	InstanceID id.ID             `db:"instance_id" json:"instance_id"`
	TenantID   string            `db:"tenant_id"   json:"tenant_id"`
	TraceID    string            `db:"trace_id"    json:"trace_id"`
	SpanID     string            `db:"span_id"     json:"span_id"`
	ParentID   string            `db:"parent_id"   json:"parent_id,omitempty"`
	Operation  string            `db:"operation"   json:"operation"`
	Duration   time.Duration     `db:"duration"    json:"duration"`
	Status     string            `db:"status"      json:"status"`
	Attributes map[string]string `db:"attributes"  json:"attributes,omitempty"`
	Timestamp  time.Time         `db:"timestamp"   json:"timestamp"`
}

// ResourceSnapshot captures point-in-time resource usage for an instance.
type ResourceSnapshot struct {
	InstanceID    id.ID     `db:"instance_id"     json:"instance_id"`
	TenantID      string    `db:"tenant_id"       json:"tenant_id"`
	CPUPercent    float64   `db:"cpu_percent"     json:"cpu_percent"`
	MemoryUsedMB  int       `db:"memory_used_mb"  json:"memory_used_mb"`
	MemoryLimitMB int       `db:"memory_limit_mb" json:"memory_limit_mb"`
	DiskUsedMB    int       `db:"disk_used_mb"    json:"disk_used_mb"`
	NetworkInMB   float64   `db:"network_in_mb"   json:"network_in_mb"`
	NetworkOutMB  float64   `db:"network_out_mb"  json:"network_out_mb"`
	Timestamp     time.Time `db:"timestamp"       json:"timestamp"`
}
