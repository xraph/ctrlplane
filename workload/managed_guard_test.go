package workload_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/store/memory"
	"github.com/xraph/ctrlplane/workload"
)

type parentPolicy struct {
	auth.NoopProvider

	deny string
	err  error
}

func (p *parentPolicy) Authorize(_ context.Context, r auth.AuthzRequest) (bool, error) {
	return r.Action != p.deny, p.err
}

type unsupportedOwner struct{ workload.Store }
type failedOwner struct{ *memory.Store }

func (s failedOwner) InspectManagedOwnerProtection(context.Context, managed.OwnerKey) (managed.Protection, error) {
	return managed.Protection{}, errors.New("owner read failed")
}

func TestWorkloadRefusalErrorPreservesParentFields(t *testing.T) {
	for _, mode := range []string{"missing claims", "denied", "policy error", "unsupported", "owner read failure", "child denied", "scale denied"} {
		for _, op := range []string{"scale", "pause", "deploy", "delete"} {
			t.Run(mode+"/"+op, func(t *testing.T) {
				s := memory.New()
				p := &parentPolicy{}
				ctx := auth.WithClaims(t.Context(), &auth.Claims{SubjectID: "operator", TenantID: "tenant"})
				w := workload.NewWorkload()
				w.TenantID = "tenant"
				w.Slug = "worker"
				w.State = workload.StateActive
				w.ReplicaCount = 1
				w.PreviousReplicas = 7

				w.Services = []provider.ServiceSpec{{Name: "worker", Image: "before", Role: provider.RoleMain}}
				if err := s.InsertWorkload(ctx, w); err != nil {
					t.Fatal(err)
				}

				replica := &instance.Instance{TenantID: "tenant", Labels: map[string]string{"ctrlplane.workload": w.ID.String()}, State: provider.StateDestroyed}

				replica.ID = id.New(id.PrefixInstance)
				if err := s.Insert(ctx, replica); err != nil {
					t.Fatal(err)
				}

				var authority workload.Store = s

				switch mode {
				case "missing claims":
					ctx = t.Context()
				case "denied":
					p.deny = "workloads." + op
				case "policy error":
					p.err = errors.New("policy unavailable")
				case "unsupported":
					authority = unsupportedOwner{s}
				case "owner read failure":
					authority = failedOwner{s}
				case "child denied":
					p.deny = "instances.delete"
					if op == "deploy" {
						p.deny = "deployments.create"
					}
				case "scale denied":
					if op != "pause" {
						return
					}

					p.deny = "workloads.scale"
				}

				instances := instance.NewService(s, provider.NewRegistry(), event.NewInMemoryBus(), p, nil)
				deploys := deploy.NewService(s, s, provider.NewRegistry(), event.NewInMemoryBus(), p, nil)
				svc := workload.NewService(authority, instances, deploys, nil, nil, nil, nil, event.NewInMemoryBus(), p)

				var err error

				switch op {
				case "scale":
					_, err = svc.Scale(ctx, w.ID, 0)
				case "pause":
					err = svc.Pause(ctx, w.ID)
				case "deploy":
					_, err = svc.Deploy(ctx, w.ID, workload.DeployRequest{Services: []provider.ServiceDeploySpec{{Name: "worker", Image: "after"}}})
				case "delete":
					err = svc.Delete(ctx, w.ID)
				}

				if err == nil {
					t.Fatal("mutation allowed")
				}

				got, err := s.GetWorkloadByID(t.Context(), "tenant", w.ID)
				if err != nil || !reflect.DeepEqual(got, w) {
					t.Fatalf("parent changed: %+v %v", got, err)
				}
			})
		}
	}
}

// hiddenInstanceAuthority models a wrapper that omits the composition proof.
type hiddenInstanceAuthority struct{ instance.Service }

func TestWorkloadRejectsMixedAndUnprovenAuthorities(t *testing.T) {
	for _, mode := range []string{"different instance store", "missing instance predicate", "different deploy store"} {
		t.Run(mode, func(t *testing.T) {
			parent := memory.New()
			other := memory.New()
			policy := &parentPolicy{}
			ctx := auth.WithClaims(t.Context(), &auth.Claims{SubjectID: "operator", TenantID: "tenant"})
			w := workload.NewWorkload()
			w.TenantID = "tenant"
			w.Slug = "worker"
			w.State = workload.StateActive
			w.PreviousReplicas = 7

			w.Services = []provider.ServiceSpec{{Name: "worker", Image: "before", Role: provider.RoleMain}}
			if err := parent.InsertWorkload(ctx, w); err != nil {
				t.Fatal(err)
			}

			var instances = instance.NewService(parent, provider.NewRegistry(), event.NewInMemoryBus(), policy, nil)

			deploymentAuthority := parent

			switch mode {
			case "different instance store":
				instances = instance.NewService(other, provider.NewRegistry(), event.NewInMemoryBus(), policy, nil)
			case "missing instance predicate":
				instances = hiddenInstanceAuthority{instances}
			case "different deploy store":
				deploymentAuthority = other
			}

			deploys := deploy.NewService(parent, deploymentAuthority, provider.NewRegistry(), event.NewInMemoryBus(), policy, nil)
			svc := workload.NewService(parent, instances, deploys, nil, nil, nil, nil, event.NewInMemoryBus(), policy)

			_, err := svc.Deploy(ctx, w.ID, workload.DeployRequest{Services: []provider.ServiceDeploySpec{{Name: "worker", Image: "after"}}})
			if !errors.Is(err, managed.ErrAuthority) {
				t.Fatalf("got %v", err)
			}

			got, err := parent.GetWorkloadByID(ctx, "tenant", w.ID)
			if err != nil || !reflect.DeepEqual(w, got) {
				t.Fatalf("parent changed: %+v %v", got, err)
			}
		})
	}
}
