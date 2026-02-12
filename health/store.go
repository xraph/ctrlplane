package health

import (
	"context"

	"github.com/xraph/ctrlplane/id"
)

// Store is the persistence interface for health checks and results.
type Store interface {
	// InsertCheck persists a new health check configuration.
	InsertCheck(ctx context.Context, check *HealthCheck) error

	// GetCheck retrieves a health check by ID.
	GetCheck(ctx context.Context, tenantID string, checkID id.ID) (*HealthCheck, error)

	// ListChecks returns all health checks for an instance.
	ListChecks(ctx context.Context, tenantID string, instanceID id.ID) ([]HealthCheck, error)

	// UpdateCheck persists changes to a health check.
	UpdateCheck(ctx context.Context, check *HealthCheck) error

	// DeleteCheck removes a health check.
	DeleteCheck(ctx context.Context, tenantID string, checkID id.ID) error

	// InsertResult persists a health check result.
	InsertResult(ctx context.Context, result *HealthResult) error

	// ListResults returns health results for a check within a time range.
	ListResults(ctx context.Context, tenantID string, checkID id.ID, opts HistoryOptions) ([]HealthResult, error)

	// GetLatestResult returns the most recent result for a check.
	GetLatestResult(ctx context.Context, tenantID string, checkID id.ID) (*HealthResult, error)
}
