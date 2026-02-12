package auth

import "context"

// NoopProvider allows all operations. Use for development and testing only.
type NoopProvider struct {
	DefaultTenantID string
	DefaultClaims   *Claims
}

// Authenticate returns default claims for any token.
func (n *NoopProvider) Authenticate(_ context.Context, _ string) (*Claims, error) {
	if n.DefaultClaims != nil {
		return n.DefaultClaims, nil
	}

	return &Claims{
		SubjectID: "dev-user",
		TenantID:  n.DefaultTenantID,
		Roles:     []string{"system:admin"},
	}, nil
}

// Authorize allows all operations.
func (n *NoopProvider) Authorize(_ context.Context, _ AuthzRequest) (bool, error) {
	return true, nil
}

// GetTenantID returns the default tenant ID.
func (n *NoopProvider) GetTenantID(_ context.Context) string {
	return n.DefaultTenantID
}
