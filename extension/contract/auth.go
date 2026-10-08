package contract

import (
	"context"
	"fmt"
	"strings"

	dash "github.com/xraph/forge/extensions/dashboard/contract"

	"github.com/xraph/ctrlplane/app"
	"github.com/xraph/ctrlplane/auth"
)

func principalContext(ctx context.Context, p dash.Principal) (context.Context, error) {
	if p.User == nil || !p.User.Authenticated() {
		return nil, dash.ErrUnauthenticated
	}

	tenant := ""

	if value, present := p.Claims["tenant_id"]; present {
		var ok bool

		tenant, ok = value.(string)
		if !ok || strings.TrimSpace(tenant) == "" {
			return nil, &dash.Error{Code: dash.CodePermissionDenied, Message: "A valid tenant_id claim is required."}
		}
	} else if !p.User.HasRole("system:admin") {
		return nil, &dash.Error{Code: dash.CodePermissionDenied, Message: "A tenant_id claim is required."}
	}

	return auth.WithClaims(ctx, &auth.Claims{SubjectID: p.User.Subject, TenantID: tenant, Email: p.User.Email, Name: p.User.DisplayName, Roles: append([]string(nil), p.User.Roles...)}), nil
}

type authorizer struct{ cp *app.CtrlPlane }

func (a authorizer) Authorize(ctx context.Context, p dash.Principal, action dash.Action) (dash.Decision, error) {
	scoped, err := principalContext(ctx, p)
	if err != nil {
		return dash.Decision{Reason: err.Error()}, err
	}

	claims := auth.ClaimsFrom(scoped)

	admin := claims.IsSystemAdmin()
	if systemIntent(action.Intent) && !admin {
		return dash.Decision{Reason: "This operation requires system:admin."}, nil
	}

	scope := "ctrlplane:read"
	if action.Kind == dash.KindCommand {
		scope = "ctrlplane:write"
	}

	if !admin && !p.User.HasScope(scope) {
		return dash.Decision{Reason: "Missing " + scope + " scope."}, nil
	}

	allowed, err := a.cp.Auth().Authorize(scoped, auth.AuthzRequest{TenantID: claims.TenantID, SubjectID: claims.SubjectID, Resource: "ctrlplane", Action: action.Intent})
	if err != nil {
		return dash.Decision{}, fmt.Errorf("authorize ctrlplane intent %s: %w", action.Intent, err)
	}

	return dash.Decision{Allow: allowed, Reason: "Ctrlplane authorization policy"}, nil
}

func systemIntent(name string) bool {
	switch name {
	case "datacenters.create", "datacenters.update", "datacenters.delete", "datacenters.status":
		return true
	}

	for _, prefix := range []string{"system.", "tenants.", "providers.", "workers.", "events.", "bootstrap.", "config."} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}

	return false
}
