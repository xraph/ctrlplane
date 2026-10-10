package managed

import (
	"context"
	"errors"
	"testing"

	"github.com/xraph/ctrlplane/id"
)

type guardAuthority struct {
	protection Protection
	err        error
	rows       []Summary
}

func (a *guardAuthority) InspectManagedProtection(context.Context, Key) (Protection, error) {
	return a.protection, a.err
}
func (a *guardAuthority) InspectManagedOwnerProtection(ctx context.Context, key OwnerKey) (Protection, error) {
	return a.protection, a.err
}
func (a *guardAuthority) HasManagedObligations(context.Context, string) (bool, error) {
	return false, a.err
}
func (a *guardAuthority) ListManagedTargets(ctx context.Context, request PageRequest) (Page, error) {
	return Page{Items: a.rows}, a.err
}

type protectionOnly struct{ ProtectionStore }

func TestGuardRefusesUnavailableAndRetainedAuthority(t *testing.T) {
	key := Key{TenantID: "tenant", InstanceID: id.New(id.PrefixInstance)}

	owner := OwnerKey{TenantID: "tenant", OwnerID: id.New(id.PrefixWorkload)}
	for _, tc := range []struct {
		name      string
		authority any
		want      error
	}{
		{"unsupported", struct{}{}, ErrUnsupported},
		{"typed nil", (*guardAuthority)(nil), ErrUnsupported},
		{"read error", &guardAuthority{err: errors.New("schema missing")}, ErrUnavailable},
		{"tombstone", &guardAuthority{protection: Protection{Protected: true, Phase: Deleted, Revision: 2}}, ErrBlocked},
		{"inconsistent absence", &guardAuthority{protection: Protection{Revision: 1}}, ErrAuthority},
		{"absence", &guardAuthority{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGuard(tc.authority)
			for _, err := range []error{g.CheckInstance(t.Context(), key, "instances.delete"), g.CheckOwner(t.Context(), owner, "workloads.delete")} {
				if !errors.Is(err, tc.want) {
					t.Fatalf("got %v want %v", err, tc.want)
				}

				if err != nil {
					var r *RefusalError
					if !errors.As(err, &r) || r.Operation != "retire" {
						t.Fatalf("missing typed retire refusal: %v", err)
					}
				}
			}
		})
	}
}

func TestGuardTenantRetainsDeletedIdentity(t *testing.T) {
	a := &guardAuthority{rows: []Summary{{Key: Key{TenantID: "tenant", InstanceID: id.New(id.PrefixInstance)}, Phase: Deleted}}}
	if err := NewGuard(a).CheckTenant(t.Context(), "tenant"); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}

	if err := NewGuard(protectionOnly{a}).CheckTenant(t.Context(), "tenant"); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestGuardAuthorityIdentity(t *testing.T) {
	a := &guardAuthority{}

	g := NewGuard(a)
	for _, candidate := range []any{nil, (*guardAuthority)(nil), &guardAuthority{}, []string{"indeterminate"}, guardAuthority{}} {
		if g.UsesAuthority(candidate) {
			t.Fatalf("accepted indeterminate authority: %T", candidate)
		}
	}

	if !g.UsesAuthority(a) {
		t.Fatal("same persistence object rejected")
	}
}
