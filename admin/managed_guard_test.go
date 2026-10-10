package admin_test

import (
	"context"
	"errors"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/store/memory"
)

type tenantPolicy struct {
	auth.NoopProvider

	deny    bool
	err     error
	request auth.AuthzRequest
}

func (p *tenantPolicy) Authorize(_ context.Context, r auth.AuthzRequest) (bool, error) {
	p.request = r

	return !p.deny, p.err
}

type tenantProtectionOnly struct {
	instance.Store
	managed.ProtectionStore
}

func TestTenantDeletionFailsClosed(t *testing.T) {
	for _, mode := range []string{"nil authority", "missing enumeration", "denied", "policy error", "missing subject", "not admin"} {
		t.Run(mode, func(t *testing.T) {
			s := memory.New()
			p := &tenantPolicy{}

			tenant := &admin.Tenant{Entity: ctrlplane.NewEntity(id.PrefixTenant), Name: "retained"}
			if err := s.InsertTenant(t.Context(), tenant); err != nil {
				t.Fatal(err)
			}

			claims := &auth.Claims{SubjectID: "operator", TenantID: tenant.ID.String(), Roles: []string{"system:admin"}}

			var authority instance.Store = s

			switch mode {
			case "nil authority":
				authority = nil
			case "missing enumeration":
				authority = tenantProtectionOnly{s, s}
			case "denied":
				p.deny = true
			case "policy error":
				p.err = errors.New("policy failed")
			case "missing subject":
				claims.SubjectID = ""
			case "not admin":
				claims.Roles = nil
			}

			svc := admin.NewService(s, authority, s, s, provider.NewRegistry(), event.NewInMemoryBus(), p)
			if err := svc.DeleteTenant(auth.WithClaims(t.Context(), claims), tenant.ID.String()); err == nil {
				t.Fatal("deletion allowed")
			}

			if _, err := s.GetTenant(t.Context(), tenant.ID.String()); err != nil {
				t.Fatalf("tenant deleted: %v", err)
			}

			if p.request.Action != "" && (p.request.Action != "tenants.delete" || p.request.ResourceID != tenant.ID.String() || p.request.TenantID != tenant.ID.String()) {
				t.Fatalf("wrong target: %+v", p.request)
			}
		})
	}
}
