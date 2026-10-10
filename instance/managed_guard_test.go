package instance_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/store/memory"
)

type guardPolicy struct {
	auth.NoopProvider

	denied   bool
	err      error
	requests []auth.AuthzRequest
}

func (p *guardPolicy) Authorize(_ context.Context, r auth.AuthzRequest) (bool, error) {
	p.requests = append(p.requests, r)

	return !p.denied, p.err
}

type withoutProtection struct{ instance.Store }
type failedProtection struct{ *memory.Store }

func (s failedProtection) InspectManagedProtection(context.Context, managed.Key) (managed.Protection, error) {
	return managed.Protection{}, errors.New("schema missing")
}

func TestInstanceGuardNoMutation(t *testing.T) {
	for _, mode := range []string{"missing claims", "empty subject", "missing provider", "deny", "policy error", "wrong tenant", "unsupported", "read failure"} {
		t.Run(mode, func(t *testing.T) {
			s := memory.New()
			p := &guardPolicy{}
			ctx := auth.WithClaims(t.Context(), &auth.Claims{SubjectID: "operator", TenantID: "tenant", Roles: []string{"system:admin"}})

			inst := &instance.Instance{Entity: ctrlplane.NewEntity(id.PrefixInstance), TenantID: "tenant", State: provider.StateDestroyed, Name: "before", Labels: map[string]string{"before": "yes"}}
			if err := s.Insert(ctx, inst); err != nil {
				t.Fatal(err)
			}

			var (
				authority  instance.Store = s
				authorizer auth.Provider  = p
			)

			switch mode {
			case "missing claims":
				ctx = t.Context()
			case "empty subject":
				ctx = auth.WithClaims(ctx, &auth.Claims{TenantID: "tenant"})
			case "missing provider":
				authorizer = nil
			case "deny":
				p.denied = true
			case "policy error":
				p.err = errors.New("policy failed")
			case "wrong tenant":
				ctx = auth.WithClaims(ctx, &auth.Claims{SubjectID: "operator", TenantID: "foreign"})
			case "unsupported":
				authority = withoutProtection{s}
			case "read failure":
				authority = failedProtection{s}
			}

			svc := instance.NewService(authority, provider.NewRegistry(), event.NewInMemoryBus(), authorizer, nil)
			name := "after"

			calls := []func() error{func() error {
				_, err := svc.Update(ctx, inst.ID, instance.UpdateRequest{Name: &name})

				return err
			}, func() error { return svc.Delete(ctx, inst.ID) }, func() error { return svc.Start(ctx, inst.ID) }, func() error { return svc.Stop(ctx, inst.ID) }, func() error { return svc.Restart(ctx, inst.ID) }, func() error { return svc.Scale(ctx, inst.ID, instance.ScaleRequest{}) }, func() error { return svc.Suspend(ctx, inst.ID, "test") }, func() error { return svc.Unsuspend(ctx, inst.ID) }}
			for _, call := range calls {
				if err := call(); err == nil {
					t.Fatal("mutation allowed")
				}
			}

			got, err := s.GetByID(t.Context(), "tenant", inst.ID)
			if err != nil || !reflect.DeepEqual(got, inst) {
				t.Fatalf("changed instance: %+v %v", got, err)
			}
		})
	}
}

type creationScopePolicy struct {
	auth.NoopProvider

	allowTenant bool
	requests    []auth.AuthzRequest
}

func (p *creationScopePolicy) Authorize(_ context.Context, request auth.AuthzRequest) (bool, error) {
	p.requests = append(p.requests, request)
	if request.Action == "instances.create" {
		return (request.ResourceID == request.TenantID) == p.allowTenant, nil
	}

	return true, nil
}

type creationProvider struct {
	provider.Provider

	calls int
}

func (p *creationProvider) Info() provider.ProviderInfo {
	return provider.ProviderInfo{Name: "fixture"}
}
func (p *creationProvider) Provision(context.Context, provider.ProvisionRequest) (*provider.ProvisionResult, error) {
	p.calls++

	return &provider.ProvisionResult{ProviderRef: "provisioned"}, nil
}

func TestInstanceCreateAuthorizesTenantBeforeAllocation(t *testing.T) {
	for _, allow := range []bool{false, true} {
		t.Run(map[bool]string{false: "deny tenant allow future ID", true: "allow tenant deny future ID"}[allow], func(t *testing.T) {
			s := memory.New()
			policy := &creationScopePolicy{allowTenant: allow}
			runtime := &creationProvider{}
			registry := provider.NewRegistry()
			registry.Register("fixture", runtime)
			svc := instance.NewService(s, registry, event.NewInMemoryBus(), policy, nil)
			ctx := auth.WithClaims(t.Context(), &auth.Claims{SubjectID: "operator", TenantID: "tenant"})

			inst, err := svc.Create(ctx, instance.CreateRequest{Name: "worker", ProviderName: "fixture", Services: []provider.ServiceSpec{{Name: "worker", Image: "image", Role: provider.RoleMain}}})
			if allow {
				if err != nil || inst.ID.Prefix() != id.PrefixInstance || runtime.calls != 1 {
					t.Fatalf("create: %+v %v calls=%d", inst, err, runtime.calls)
				}
			} else if !errors.Is(err, ctrlplane.ErrForbidden) || inst != nil || runtime.calls != 0 {
				t.Fatalf("refusal: %+v %v calls=%d", inst, err, runtime.calls)
			}

			want := []auth.AuthzRequest{{SubjectID: "operator", TenantID: "tenant", Resource: "ctrlplane", Action: "instances.create", ResourceID: "tenant"}}
			if !reflect.DeepEqual(policy.requests, want) {
				t.Fatalf("creation scope: %+v", policy.requests)
			}

			rows, err := s.List(ctx, "tenant", instance.ListOptions{})
			if err != nil || len(rows.Items) != runtime.calls {
				t.Fatalf("instance writes: %+v %v", rows, err)
			}

			if allow {
				name := "changed"
				if _, err := svc.Update(ctx, inst.ID, instance.UpdateRequest{Name: &name}); err != nil {
					t.Fatal(err)
				}

				last := policy.requests[len(policy.requests)-1]
				if last.Action != "instances.update" || last.ResourceID != inst.ID.String() {
					t.Fatalf("existing instance scope: %+v", last)
				}
			}
		})
	}
}
