package auth

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	ctrlplane "github.com/xraph/ctrlplane"
)

// RequireAuthorized checks the authenticated scope and the configured resource policy.
// Only tenant administration accepts a system identity outside the target tenant.
func RequireAuthorized(ctx context.Context, p Provider, action, resourceID, tenantID string) (*Claims, error) {
	c, err := RequireClaims(ctx)
	if err != nil || strings.TrimSpace(c.SubjectID) == "" {
		return nil, ErrUnauthorized
	}

	if p == nil || reflect.ValueOf(p).Kind() == reflect.Pointer && reflect.ValueOf(p).IsNil() {
		return nil, ErrUnauthorized
	}

	if tenantID == "" {
		tenantID = c.TenantID
	}

	if tenantID == "" || (c.TenantID != tenantID && (action != "tenants.delete" || !c.IsSystemAdmin())) {
		return nil, ctrlplane.ErrForbidden
	}

	if action == "" || resourceID == "" {
		return nil, ctrlplane.ErrForbidden
	}

	allowed, err := p.Authorize(ctx, AuthzRequest{SubjectID: c.SubjectID, TenantID: tenantID, Resource: "ctrlplane", Action: action, ResourceID: resourceID})
	if err != nil {
		return nil, fmt.Errorf("authorize %s: %w", action, err)
	}

	if !allowed {
		return nil, ctrlplane.ErrForbidden
	}

	return c, nil
}
