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

	// ErrDatacenterUnavailable indicates the datacenter is not accepting new instances.
	ErrDatacenterUnavailable = errors.New("ctrlplane: datacenter unavailable")

	// ErrNotImplemented indicates a method is not yet implemented on
	// this backend. Used by store backends that satisfy an interface
	// for compilation but defer real persistence to a later phase.
	ErrNotImplemented = errors.New("ctrlplane: not implemented")

	// ErrInvalidSource indicates a deployment source is malformed — its
	// Type does not match a single populated payload, or required fields
	// for that source type are missing.
	ErrInvalidSource = errors.New("ctrlplane: invalid deployment source")

	// ErrUnsupportedSource indicates the selected provider cannot deploy
	// the requested source type (e.g. a Helm source on a provider without
	// the helm capability).
	ErrUnsupportedSource = errors.New("ctrlplane: provider does not support deployment source")
)
