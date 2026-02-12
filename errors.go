package ctrlplane

import "errors"

// Sentinel errors returned by ctrlplane operations.
// Use errors.Is to check for these values.
var (
	// ErrNotFound indicates the requested resource does not exist.
	ErrNotFound = errors.New("ctrlplane: resource not found")

	// ErrAlreadyExists indicates a resource with the same identity already exists.
	ErrAlreadyExists = errors.New("ctrlplane: resource already exists")

	// ErrInvalidState indicates an invalid state transition was attempted.
	ErrInvalidState = errors.New("ctrlplane: invalid state transition")

	// ErrProviderNotFound indicates the named provider is not registered.
	ErrProviderNotFound = errors.New("ctrlplane: provider not registered")

	// ErrUnauthorized indicates the request lacks valid authentication credentials.
	ErrUnauthorized = errors.New("ctrlplane: unauthorized")

	// ErrForbidden indicates the authenticated identity lacks permission.
	ErrForbidden = errors.New("ctrlplane: forbidden")

	// ErrQuotaExceeded indicates the tenant has exceeded their resource quota.
	ErrQuotaExceeded = errors.New("ctrlplane: quota exceeded")

	// ErrProviderUnavail indicates the provider is temporarily unavailable.
	ErrProviderUnavail = errors.New("ctrlplane: provider unavailable")

	// ErrDeploymentFailed indicates a deployment operation failed.
	ErrDeploymentFailed = errors.New("ctrlplane: deployment failed")

	// ErrHealthCheckFailed indicates a health check did not pass.
	ErrHealthCheckFailed = errors.New("ctrlplane: health check failed")

	// ErrRollbackFailed indicates a rollback operation failed.
	ErrRollbackFailed = errors.New("ctrlplane: rollback failed")

	// ErrInvalidConfig indicates the provided configuration is invalid.
	ErrInvalidConfig = errors.New("ctrlplane: invalid configuration")
)
