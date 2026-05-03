package health

import (
	"context"
	"time"

	"github.com/xraph/ctrlplane/id"
)

// Service manages health checks and their results.
type Service interface {
	// Configure adds or updates a health check for an instance.
	Configure(ctx context.Context, req ConfigureRequest) (*HealthCheck, error)

	// Remove deletes a health check.
	Remove(ctx context.Context, checkID id.ID) error

	// GetHealth returns aggregate health for an instance.
	GetHealth(ctx context.Context, instanceID id.ID) (*InstanceHealth, error)

	// GetHistory returns check results over time.
	GetHistory(ctx context.Context, checkID id.ID, opts HistoryOptions) ([]HealthResult, error)

	// ListChecks returns all checks for an instance.
	ListChecks(ctx context.Context, instanceID id.ID) ([]HealthCheck, error)

	// RunCheck executes a one-off health check.
	RunCheck(ctx context.Context, checkID id.ID) (*HealthResult, error)

	// RegisterChecker adds a custom checker type.
	RegisterChecker(checker Checker)

	// Watch returns a channel that receives every HealthResult for
	// the given instance until ctx is cancelled. Subscribers are
	// fanned out from the worker's InsertResult call (and from
	// RunCheck) — a buffered channel (size 16) drops events when
	// the consumer can't keep up. The channel is closed when ctx
	// is cancelled.
	//
	// Provides push semantics over the existing poll-only Get/List
	// surface; the dashboard streaming endpoint is the primary
	// consumer. Cross-process consumers should still use the
	// event.Bus events (HealthCheckPassed/Failed) which remain
	// published unconditionally.
	Watch(ctx context.Context, instanceID id.ID) (<-chan *HealthResult, error)
}

// ConfigureRequest holds the parameters for creating or updating a health check.
//
// ServiceName targets a specific service inside a multi-service
// instance; empty means "the Main service" (the default for legacy
// single-service workloads).
type ConfigureRequest struct {
	InstanceID  id.ID         `json:"instance_id"            validate:"required"`
	ServiceName string        `json:"service_name,omitempty"`
	Name        string        `json:"name"                   validate:"required"`
	Type        CheckType     `json:"type"                   validate:"required"`
	Target      string        `json:"target"                 validate:"required"`
	Interval    time.Duration `default:"30s"                 json:"interval"`
	Timeout     time.Duration `default:"5s"                  json:"timeout"`
	Retries     int           `default:"3"                   json:"retries"`
}

// HistoryOptions configures health result history queries.
type HistoryOptions struct {
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
	Limit int       `json:"limit"`
}
