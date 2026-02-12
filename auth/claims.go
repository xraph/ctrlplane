package auth

import "slices"

// Claims represents the authenticated identity.
type Claims struct {
	SubjectID string            `json:"sub"`
	TenantID  string            `json:"tenant_id,omitempty"`
	Email     string            `json:"email,omitempty"`
	Name      string            `json:"name,omitempty"`
	Roles     []string          `json:"roles,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// IsSystemAdmin returns true if claims contain the system admin role.
func (c *Claims) IsSystemAdmin() bool {
	return slices.Contains(c.Roles, "system:admin")
}

// HasRole checks for a specific role.
func (c *Claims) HasRole(role string) bool {
	return slices.Contains(c.Roles, role)
}
