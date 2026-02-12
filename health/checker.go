package health

import "context"

// Checker executes a specific type of health check.
// Register custom checkers to extend health checking beyond the built-in types.
type Checker interface {
	// Type returns the check type this checker handles.
	Type() CheckType

	// Check executes the health check and returns the result.
	Check(ctx context.Context, check *HealthCheck) (*HealthResult, error)
}
