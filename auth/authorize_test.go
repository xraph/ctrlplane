package auth

import (
	"context"
	"errors"
	"reflect"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
)

type recordingPolicy struct {
	requests []AuthzRequest
	allow    bool
	err      error
}

func (p *recordingPolicy) Authenticate(context.Context, string) (*Claims, error) {
	return nil, ErrUnauthorized
}
func (p *recordingPolicy) GetTenantID(context.Context) string { return "untrusted-provider-tenant" }
func (p *recordingPolicy) Authorize(_ context.Context, r AuthzRequest) (bool, error) {
	p.requests = append(p.requests, r)

	return p.allow, p.err
}

func TestRequireAuthorized(t *testing.T) {
	policyErr := errors.New("policy unavailable")
	for _, tc := range []struct {
		name   string
		claims *Claims
		p      *recordingPolicy
		tenant string
		want   error
		called bool
	}{
		{"absent", nil, &recordingPolicy{allow: true}, "tenant", ErrUnauthorized, false},
		{"empty subject", &Claims{TenantID: "tenant"}, &recordingPolicy{allow: true}, "tenant", ErrUnauthorized, false},
		{"missing tenant", &Claims{SubjectID: "user", Roles: []string{"system:admin"}}, &recordingPolicy{allow: true}, "tenant", ctrlplane.ErrForbidden, false},
		{"foreign tenant", &Claims{SubjectID: "user", TenantID: "foreign", Roles: []string{"system:admin"}}, &recordingPolicy{allow: true}, "tenant", ctrlplane.ErrForbidden, false},
		{"missing provider", &Claims{SubjectID: "user", TenantID: "tenant"}, nil, "tenant", ErrUnauthorized, false},
		{"role does not bypass denial", &Claims{SubjectID: "user", TenantID: "tenant", Roles: []string{"system:admin"}}, &recordingPolicy{}, "tenant", ctrlplane.ErrForbidden, true},
		{"policy error", &Claims{SubjectID: "user", TenantID: "tenant"}, &recordingPolicy{err: policyErr}, "tenant", policyErr, true},
		{"authorized", &Claims{SubjectID: "user", TenantID: "tenant"}, &recordingPolicy{allow: true}, "tenant", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := WithClaims(t.Context(), tc.claims)

			_, err := RequireAuthorized(ctx, tc.p, "instances.stop", "inst-resource", tc.tenant)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}

			if tc.p != nil && (len(tc.p.requests) > 0) != tc.called {
				t.Fatalf("calls: %v", tc.p.requests)
			}

			if tc.called && !reflect.DeepEqual(tc.p.requests[0], AuthzRequest{TenantID: "tenant", SubjectID: "user", Resource: "ctrlplane", Action: "instances.stop", ResourceID: "inst-resource"}) {
				t.Fatalf("wrong policy request: %+v", tc.p.requests[0])
			}
		})
	}
}
