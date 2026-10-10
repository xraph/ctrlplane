package deploy_test

import (
	"context"
	"errors"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/store/memory"
)

type policy struct {
	auth.NoopProvider

	denied   bool
	err      error
	requests []auth.AuthzRequest
}

func (p *policy) Authorize(_ context.Context, r auth.AuthzRequest) (bool, error) {
	p.requests = append(p.requests, r)

	return !p.denied, p.err
}

type unsupportedInstances struct{ instance.Store }
type failingInstances struct{ *memory.Store }

func (s failingInstances) InspectManagedProtection(context.Context, managed.Key) (managed.Protection, error) {
	return managed.Protection{}, errors.New("read failed")
}

func TestDeployAuthorizationAndAuthorityNoWrites(t *testing.T) {
	for _, mode := range []string{"missing claims", "empty subject", "wrong tenant", "denied", "policy error", "unsupported", "read error", "foreign release", "foreign release tenant"} {
		t.Run(mode, func(t *testing.T) {
			s := memory.New()
			p := &policy{}
			ctx := auth.WithClaims(t.Context(), &auth.Claims{SubjectID: "operator", TenantID: "tenant"})

			inst := &instance.Instance{Entity: ctrlplane.NewEntity(id.PrefixInstance), TenantID: "tenant", State: provider.StateRunning}
			if err := s.Insert(ctx, inst); err != nil {
				t.Fatal(err)
			}

			rel := &deploy.Release{Entity: ctrlplane.NewEntity(id.PrefixRelease), TenantID: "tenant", InstanceID: inst.ID, Version: 1}
			if mode == "foreign release" {
				rel.InstanceID = id.New(id.PrefixInstance)
			}

			if mode == "foreign release tenant" {
				rel.TenantID = "foreign"
			}

			if err := s.InsertRelease(ctx, rel); err != nil {
				t.Fatal(err)
			}

			var authority instance.Store = s

			switch mode {
			case "missing claims":
				ctx = t.Context()
			case "empty subject":
				ctx = auth.WithClaims(t.Context(), &auth.Claims{TenantID: "tenant"})
			case "wrong tenant":
				ctx = auth.WithClaims(t.Context(), &auth.Claims{SubjectID: "operator", TenantID: "foreign"})
			case "denied":
				p.denied = true
			case "policy error":
				p.err = errors.New("policy failed")
			case "unsupported":
				authority = unsupportedInstances{s}
			case "read error":
				authority = failingInstances{s}
			}

			svc := deploy.NewService(s, authority, provider.NewRegistry(), event.NewInMemoryBus(), p, nil)
			if _, err := svc.Rollback(ctx, inst.ID, rel.ID); err == nil {
				t.Fatal("rollback accepted")
			}

			rows, err := s.ListDeployments(t.Context(), "tenant", inst.ID, deploy.ListOptions{})
			if err != nil || len(rows.Items) != 0 {
				t.Fatalf("deployment changed: %+v %v", rows, err)
			}

			if mode == "denied" || mode == "policy error" {
				for _, run := range []func() error{func() error {
					_, err := svc.Deploy(ctx, deploy.DeployRequest{InstanceID: inst.ID})

					return err
				}, func() error {
					_, err := svc.RecordInitial(ctx, inst.ID)

					return err
				}} {
					if err := run(); err == nil {
						t.Fatal("release write authorized")
					}
				}
			}
		})
	}
}

func TestCancelAuthorizesDeploymentAndProtectsInstance(t *testing.T) {
	s := memory.New()
	p := &policy{}
	ctx := auth.WithClaims(t.Context(), &auth.Claims{SubjectID: "operator", TenantID: "tenant"})

	dep := &deploy.Deployment{Entity: ctrlplane.NewEntity(id.PrefixDeployment), TenantID: "tenant", InstanceID: id.New(id.PrefixInstance), State: deploy.DeployPending}
	if err := s.InsertDeployment(ctx, dep); err != nil {
		t.Fatal(err)
	}

	svc := deploy.NewService(s, failingInstances{s}, provider.NewRegistry(), event.NewInMemoryBus(), p, nil)
	if err := svc.Cancel(ctx, dep.ID); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal(err)
	}

	saved, err := s.GetDeployment(ctx, "tenant", dep.ID)
	if err != nil || saved.State != deploy.DeployPending || saved.FinishedAt != nil {
		t.Fatalf("cancel wrote: %+v %v", saved, err)
	}

	if len(p.requests) != 1 || p.requests[0].Action != "deployments.cancel" || p.requests[0].ResourceID != dep.ID.String() {
		t.Fatalf("policy request: %+v", p.requests)
	}
}
