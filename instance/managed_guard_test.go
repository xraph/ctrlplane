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
