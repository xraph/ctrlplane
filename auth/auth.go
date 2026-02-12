package auth

import (
	"context"
	"errors"
)

// ErrUnauthorized indicates the request lacks valid authentication credentials.
var ErrUnauthorized = errors.New("ctrlplane: unauthorized")

// Provider abstracts authentication and authorization.
// Implement this interface to plug in any auth backend.
type Provider interface {
	// Authenticate validates credentials or token and returns Claims.
	// Typically called by middleware from an HTTP request.
	Authenticate(ctx context.Context, token string) (*Claims, error)

	// Authorize checks whether the identity in ctx has the given
	// permission on the specified resource.
	Authorize(ctx context.Context, req AuthzRequest) (bool, error)

	// GetTenantID extracts the tenant/org ID from context.
	// Returns empty string if not in a tenant context.
	GetTenantID(ctx context.Context) string
}

// AuthzRequest describes an authorization check.
type AuthzRequest struct {
	TenantID   string `json:"tenant_id"`
	SubjectID  string `json:"subject_id"`
	Resource   string `json:"resource"`
	Action     string `json:"action"`
	ResourceID string `json:"resource_id,omitempty"`
}
