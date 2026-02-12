package health

import (
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
)

// Status represents the health status of a check or instance.
type Status string

const (
	// StatusHealthy indicates all checks pass.
	StatusHealthy Status = "healthy"

	// StatusDegraded indicates some checks are failing.
	StatusDegraded Status = "degraded"

	// StatusUnhealthy indicates the instance is unhealthy.
	StatusUnhealthy Status = "unhealthy"

	// StatusUnknown indicates health status cannot be determined.
	StatusUnknown Status = "unknown"
)

// CheckType identifies the kind of health check.
type CheckType string

const (
	// CheckHTTP performs an HTTP GET/HEAD check.
	CheckHTTP CheckType = "http"

	// CheckTCP performs a TCP dial check.
	CheckTCP CheckType = "tcp"

	// CheckGRPC uses the gRPC health check protocol.
	CheckGRPC CheckType = "grpc"

	// CheckCommand executes a command and checks the exit code.
	CheckCommand CheckType = "command"

	// CheckCustom is a user-defined check type.
	CheckCustom CheckType = "custom"
)

// HealthCheck is a configured check for an instance.
type HealthCheck struct {
	ctrlplane.Entity

	TenantID   string        `db:"tenant_id"   json:"tenant_id"`
	InstanceID id.ID         `db:"instance_id" json:"instance_id"`
	Name       string        `db:"name"        json:"name"`
	Type       CheckType     `db:"type"        json:"type"`
	Target     string        `db:"target"      json:"target"`
	Interval   time.Duration `db:"interval"    json:"interval"`
	Timeout    time.Duration `db:"timeout"     json:"timeout"`
	Retries    int           `db:"retries"     json:"retries"`
	Enabled    bool          `db:"enabled"     json:"enabled"`
}

// HealthResult is the outcome of a single check execution.
type HealthResult struct {
	ctrlplane.Entity

	CheckID    id.ID         `db:"check_id"    json:"check_id"`
	InstanceID id.ID         `db:"instance_id" json:"instance_id"`
	TenantID   string        `db:"tenant_id"   json:"tenant_id"`
	Status     Status        `db:"status"      json:"status"`
	Latency    time.Duration `db:"latency"     json:"latency"`
	Message    string        `db:"message"     json:"message,omitempty"`
	StatusCode int           `db:"status_code" json:"status_code,omitempty"`
	CheckedAt  time.Time     `db:"checked_at"  json:"checked_at"`
}

// InstanceHealth is the aggregate health for an instance.
type InstanceHealth struct {
	InstanceID  id.ID          `json:"instance_id"`
	Status      Status         `json:"status"`
	Checks      []CheckSummary `json:"checks"`
	LastChecked time.Time      `json:"last_checked"`
	Uptime      float64        `json:"uptime_percent"`
	ConsecFails int            `json:"consecutive_failures"`
}

// CheckSummary provides a brief overview of a single check's current state.
type CheckSummary struct {
	CheckID    id.ID         `json:"check_id"`
	Name       string        `json:"name"`
	Status     Status        `json:"status"`
	Latency    time.Duration `json:"latency"`
	LastResult *HealthResult `json:"last_result,omitempty"`
}
