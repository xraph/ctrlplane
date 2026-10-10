package workload_test

import (
	"context"
	"errors"
	"reflect"
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
type failedOwner struct {
	*memory.Store

	reads int
}

func (s *failedOwner) InspectManagedOwnerProtection(context.Context, managed.OwnerKey) (managed.Protection, error) {
	s.reads++

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

				var childAuthority instance.Store = s

				failed := &failedOwner{Store: s}

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
					authority = failed
					childAuthority = failed
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

				instances := instance.NewService(childAuthority, provider.NewRegistry(), event.NewInMemoryBus(), p, nil)
				deploys := deploy.NewService(s, childAuthority, provider.NewRegistry(), event.NewInMemoryBus(), p, nil)
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

				if mode == "owner read failure" && (!errors.Is(err, managed.ErrUnavailable) || failed.reads != 1) {
					t.Fatalf("owner reader did not refuse: %v reads=%d", err, failed.reads)
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

type hiddenPolicy struct{ instance.Service }

func (s hiddenPolicy) UsesManagedAuthority(candidate any) bool {
	return s.Service.(interface{ UsesManagedAuthority(candidate any) bool }).UsesManagedAuthority(candidate)
}

type hiddenDeployPolicy struct{ deploy.Service }

func (s hiddenDeployPolicy) UsesManagedAuthority(candidate any) bool {
	return s.Service.(interface{ UsesManagedAuthority(candidate any) bool }).UsesManagedAuthority(candidate)
}

type valuePolicy struct {
	*auth.NoopProvider

	values []string
}

type mutationProvider struct {
	provider.Provider

	calls int
}

func (p *mutationProvider) Info() provider.ProviderInfo {
	return provider.ProviderInfo{Name: "fixture"}
}
func (p *mutationProvider) Provision(context.Context, provider.ProvisionRequest) (*provider.ProvisionResult, error) {
	p.calls++

	return &provider.ProvisionResult{ProviderRef: "provisioned"}, nil
}
func (p *mutationProvider) Deprovision(context.Context, id.ID) error {
	p.calls++

	return nil
}
func (p *mutationProvider) Deploy(context.Context, provider.DeployRequest) (*provider.DeployResult, error) {
	p.calls++

	return &provider.DeployResult{}, nil
}

func TestWorkloadRejectsDifferentChildPoliciesBeforeMutation(t *testing.T) {
	for _, mode := range []string{"different policy", "missing proof", "typed nil", "indeterminate"} {
		for _, op := range []string{"pause", "scale", "delete", "deploy"} {
			t.Run(mode+"/"+op, func(t *testing.T) {
				s := memory.New()
				parent := &parentPolicy{}

				var child auth.Provider = &parentPolicy{deny: "instances.delete"}
				if op == "deploy" {
					child = &parentPolicy{deny: "deployments.create"}
				}

				if mode == "typed nil" {
					child = (*parentPolicy)(nil)
				}

				if mode == "indeterminate" {
					child = valuePolicy{NoopProvider: &auth.NoopProvider{}, values: []string{"not an identity"}}
				}

				ctx := auth.WithClaims(t.Context(), &auth.Claims{SubjectID: "operator", TenantID: "tenant"})
				w := workload.NewWorkload()
				w.TenantID, w.Slug, w.State, w.ReplicaCount, w.PreviousReplicas = "tenant", "worker", workload.StateActive, 1, 7

				w.Services = []provider.ServiceSpec{{Name: "worker", Image: "before", Role: provider.RoleMain}}
				if err := s.InsertWorkload(ctx, w); err != nil {
					t.Fatal(err)
				}

				inst := &instance.Instance{TenantID: "tenant", State: provider.StateDestroyed, ProviderName: "fixture", Services: w.Services, Labels: map[string]string{"ctrlplane.workload": w.ID.String()}}

				inst.ID = id.New(id.PrefixInstance)
				if err := s.Insert(ctx, inst); err != nil {
					t.Fatal(err)
				}

				runtime := &mutationProvider{}
				registry := provider.NewRegistry()
				registry.Register("fixture", runtime)
				instances := instance.NewService(s, registry, event.NewInMemoryBus(), child, nil)

				var deployments deploy.Service = deploy.NewService(s, s, registry, event.NewInMemoryBus(), parent, nil)
				if op == "deploy" {
					instances = instance.NewService(s, registry, event.NewInMemoryBus(), parent, nil)
					deployments = deploy.NewService(s, s, registry, event.NewInMemoryBus(), child, nil)
				}

				if mode == "missing proof" {
					if op == "deploy" {
						deployments = hiddenDeployPolicy{deployments}
					} else {
						instances = hiddenPolicy{instances}
					}
				}

				svc := workload.NewService(s, instances, deployments, nil, nil, nil, nil, event.NewInMemoryBus(), parent)

				var err error

				switch op {
				case "pause":
					err = svc.Pause(ctx, w.ID)
				case "scale":
					_, err = svc.Scale(ctx, w.ID, 0)
				case "delete":
					err = svc.Delete(ctx, w.ID)
				case "deploy":
					_, err = svc.Deploy(ctx, w.ID, workload.DeployRequest{Services: []provider.ServiceDeploySpec{{Name: "worker", Image: "after"}}})
				}

				if !errors.Is(err, managed.ErrAuthority) {
					t.Fatalf("policy composition refusal: %v", err)
				}

				got, err := s.GetWorkloadByID(ctx, "tenant", w.ID)
				if err != nil || !reflect.DeepEqual(w, got) {
					t.Fatalf("parent changed: %+v %v", got, err)
				}

				gotInst, err := s.GetByID(ctx, "tenant", inst.ID)
				if err != nil || !reflect.DeepEqual(inst, gotInst) {
					t.Fatalf("child changed: %+v %v", gotInst, err)
				}

				assertNoReleaseWrites(t, s, inst.ID)

				if runtime.calls != 0 {
					t.Fatalf("runtime calls: %d", runtime.calls)
				}
			})
		}
	}
}

func assertNoReleaseWrites(t *testing.T, s *memory.Store, instanceID id.ID) {
	t.Helper()

	releases, err := s.ListReleases(t.Context(), "tenant", instanceID, deploy.ListOptions{})
	if err != nil || len(releases.Items) != 0 {
		t.Fatalf("release writes: %+v %v", releases, err)
	}

	deployments, err := s.ListDeployments(t.Context(), "tenant", instanceID, deploy.ListOptions{})
	if err != nil || len(deployments.Items) != 0 {
		t.Fatalf("deployment writes: %+v %v", deployments, err)
	}
}

type creationPolicy struct {
	auth.NoopProvider

	allowTenant bool
	denyInitial bool
	requests    []auth.AuthzRequest
}

func (p *creationPolicy) Authorize(_ context.Context, r auth.AuthzRequest) (bool, error) {
	p.requests = append(p.requests, r)
	if r.Action == "instances.create" {
		return (r.ResourceID == r.TenantID) == p.allowTenant, nil
	}

	return !p.denyInitial || r.Action != "deployments.create", nil
}

func TestWorkloadCreationUsesTenantScopeBeforeMutation(t *testing.T) {
	for _, op := range []string{"create", "scale", "resume"} {
		for _, mode := range []string{"allow tenant", "deny tenant", "different deploy policy", "different deploy store", "optional deploy absent", "initial release denied"} {
			t.Run(op+"/"+mode, func(t *testing.T) {
				s := memory.New()
				policy := &creationPolicy{allowTenant: mode != "deny tenant", denyInitial: mode == "initial release denied"}
				ctx := auth.WithClaims(t.Context(), &auth.Claims{SubjectID: "operator", TenantID: "tenant"})
				runtime := &mutationProvider{}
				registry := provider.NewRegistry()
				registry.Register("fixture", runtime)
				instances := instance.NewService(s, registry, event.NewInMemoryBus(), policy, nil)

				var deploymentPolicy auth.Provider = policy

				deploymentStore := s

				if mode == "different deploy policy" {
					deploymentPolicy = &creationPolicy{allowTenant: true}
				}

				if mode == "different deploy store" {
					deploymentStore = memory.New()
				}

				var deployments deploy.Service = deploy.NewService(s, deploymentStore, registry, event.NewInMemoryBus(), deploymentPolicy, nil)
				if mode == "optional deploy absent" {
					deployments = nil
				}

				svc := workload.NewService(s, instances, deployments, nil, nil, nil, nil, event.NewInMemoryBus(), policy)
				w := workload.NewWorkload()
				w.TenantID, w.Slug, w.Name, w.State, w.ProviderName = "tenant", "worker", "worker", workload.StateActive, "fixture"

				w.Services = []provider.ServiceSpec{{Name: "worker", Image: "before", Role: provider.RoleMain}}
				if op != "create" {
					if err := s.InsertWorkload(ctx, w); err != nil {
						t.Fatal(err)
					}
				}

				var result *workload.Workload

				var err error

				switch op {
				case "create":
					result, err = svc.Create(ctx, workload.CreateRequest{Name: "worker", ProviderName: "fixture", Services: w.Services, Replicas: 1})
				case "scale":
					result, err = svc.Scale(ctx, w.ID, 1)
				case "resume":
					err = svc.Resume(ctx, w.ID)
					if err == nil {
						result, err = s.GetWorkloadByID(ctx, "tenant", w.ID)
					}
				}

				refused := mode == "deny tenant" || mode == "different deploy policy" || mode == "different deploy store"
				if refused {
					want := managed.ErrAuthority
					if mode == "deny tenant" {
						want = ctrlplane.ErrForbidden
					}

					if !errors.Is(err, want) {
						t.Fatalf("creation refusal: %v want %v", err, want)
					}
				} else if err != nil || result.ReplicaCount != 1 || runtime.calls != 1 {
					t.Fatalf("creation failed: %+v %v calls=%d", result, err, runtime.calls)
				}

				parents, err := s.ListWorkloads(ctx, "tenant", workload.ListOptions{})
				if err != nil {
					t.Fatal(err)
				}

				children, err := s.List(ctx, "tenant", instance.ListOptions{})
				if err != nil {
					t.Fatal(err)
				}

				if refused {
					if runtime.calls != 0 || len(children.Items) != 0 {
						t.Fatalf("refusal had effects: calls=%d children=%d", runtime.calls, len(children.Items))
					}

					if op == "create" && len(parents.Items) != 0 {
						t.Fatalf("parent inserted: %+v", parents)
					}

					if op != "create" && (len(parents.Items) != 1 || !reflect.DeepEqual(parents.Items[0], w)) {
						t.Fatalf("parent changed: %+v", parents)
					}
				} else if len(children.Items) != 1 {
					t.Fatalf("children: %+v", children)
				}

				creates, initials := 0, 0

				for _, request := range policy.requests {
					if request.Action == "instances.create" {
						creates++

						want := auth.AuthzRequest{SubjectID: "operator", TenantID: "tenant", Resource: "ctrlplane", Action: "instances.create", ResourceID: "tenant"}
						if request != want {
							t.Fatalf("wrong creation scope: %+v", request)
						}
					}

					if request.Action == "deployments.create" {
						initials++

						if len(children.Items) != 1 || request.ResourceID != children.Items[0].ID.String() {
							t.Fatalf("initial release scope: %+v", request)
						}
					}
				}

				wantCreates := 2
				if mode == "deny tenant" {
					wantCreates = 1
				} else if refused {
					wantCreates = 0
				}

				if creates != wantCreates {
					t.Fatalf("creation checks: %d want %d", creates, wantCreates)
				}

				if refused || mode == "optional deploy absent" {
					if initials != 0 {
						t.Fatalf("initial calls: %d", initials)
					}
				} else if initials != 1 {
					t.Fatalf("initial calls: %d", initials)
				}

				if len(children.Items) == 0 {
					assertNoReleaseWrites(t, s, id.ID{})
				} else if mode == "optional deploy absent" || mode == "initial release denied" {
					assertNoReleaseWrites(t, s, children.Items[0].ID)
				}
			})
		}
	}
}
