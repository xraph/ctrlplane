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
}

// ConfigureRequest holds the parameters for creating or updating a health check.
type ConfigureRequest struct {
	InstanceID id.ID         `json:"instance_id" validate:"required"`
	Name       string        `json:"name"        validate:"required"`
	Type       CheckType     `json:"type"        validate:"required"`
	Target     string        `json:"target"      validate:"required"`
	Interval   time.Duration `default:"30s"      json:"interval"`
	Timeout    time.Duration `default:"5s"       json:"timeout"`
	Retries    int           `default:"3"        json:"retries"`
}

// HistoryOptions configures health result history queries.
type HistoryOptions struct {
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
	Limit int       `json:"limit"`
}
