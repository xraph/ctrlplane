package app_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/app"
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

type guardPolicy struct {
	auth.NoopProvider

	deny     bool
	err      error
	requests []auth.AuthzRequest
}

func (p *guardPolicy) Authorize(_ context.Context, r auth.AuthzRequest) (bool, error) {
	p.requests = append(p.requests, r)

	return !p.deny, p.err
}
func guardIdentity(owner id.ID) managed.Identity {
	iid := id.New(id.PrefixInstance)
	spec := []byte(`{"image":"fixture"}`)

	return managed.Identity{Key: managed.Key{TenantID: "tenant", InstanceID: iid}, ProvisionOperationID: id.New(managed.PrefixOperation), Physical: managed.Physical{Provider: "missing", Placement: "fixture", Project: "cp-" + iid.String(), Generation: iid.String()}, Manifest: managed.Manifest{Version: 1, Spec: spec, SpecDigest: managed.Digest(spec), Members: []managed.Member{{Name: "worker", Service: "worker", OwnerID: owner, Installation: "installation", Namespace: "namespace", Queue: "queue", Build: "build", ArtifactDigest: managed.Digest([]byte("image")), ConfigurationDigest: managed.Digest([]byte("config"))}}}}
}
func guardContext(t *testing.T) context.Context {
	t.Helper()

	return auth.WithClaims(t.Context(), &auth.Claims{SubjectID: "operator", TenantID: "tenant", Roles: []string{"system:admin"}})
}

func TestManagedGuardAppAndDirectConstructors(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(map[bool]string{false: "app without controller", true: "direct without ports"}[direct], func(t *testing.T) {
			for _, state := range []string{"running", "destroyed", "missing row", "tombstone"} {
				t.Run(state, func(t *testing.T) {
					s := memory.New()
					p := &guardPolicy{}
					ctx := guardContext(t)

					identity := guardIdentity(id.ID{})
					if _, err := s.CreateManagedTarget(ctx, identity); err != nil {
						t.Fatal(err)
					}

					if state == "tombstone" {
						command := managed.Command{Key: identity.Key, ID: id.New(managed.PrefixOperation), Revision: 1, Owner: "fixture"}

						lease, err := s.ClaimManagedLease(ctx, managed.LeaseRequest{Command: command, Duration: managed.MaxLease})
						if err != nil {
							t.Fatal(err)
						}

						command.ID = id.New(managed.PrefixOperation)
						command.Revision = lease.Revision
						command.LeaseEpoch = lease.LeaseEpoch

						sealed, err := s.SealManagedTarget(ctx, command)
						if err != nil {
							t.Fatal(err)
						}

						command.ID = id.New(managed.PrefixOperation)

						command.Revision = sealed.Revision
						if _, err = s.TombstoneManagedUnissuedTarget(ctx, command); err != nil {
							t.Fatal(err)
						}
					}

					inst := &instance.Instance{Entity: ctrlplane.NewEntity(id.PrefixInstance), TenantID: "tenant", ProviderName: "missing", Name: "worker", State: provider.StateRunning, Labels: map[string]string{}}

					inst.ID = identity.InstanceID
					if state == "destroyed" {
						inst.State = provider.StateDestroyed
					}

					if state != "missing row" && state != "tombstone" {
						if err := s.Insert(ctx, inst); err != nil {
							t.Fatal(err)
						}
					}

					cp, err := app.New(app.WithStore(s), app.WithAuth(p))
					if err != nil {
						t.Fatal(err)
					}

					if direct {
						cp.Instances = instance.NewService(s, provider.NewRegistry(), event.NewInMemoryBus(), p, nil)
						cp.Deploys = deploy.NewService(s, s, provider.NewRegistry(), event.NewInMemoryBus(), p, nil)
					}

					pending := &deploy.Deployment{Entity: ctrlplane.NewEntity(id.PrefixDeployment), TenantID: "tenant", InstanceID: inst.ID, State: deploy.DeployPending}
					if err := s.InsertDeployment(ctx, pending); err != nil {
						t.Fatal(err)
					}

					name := "changed"

					operations := []struct {
						name string
						run  func() error
					}{
						{"update", func() error {
							_, err := cp.Instances.Update(ctx, inst.ID, instance.UpdateRequest{Name: &name, Labels: map[string]string{"changed": "yes"}})

							return err
						}},
						{"delete", func() error { return cp.Instances.Delete(ctx, inst.ID) }},
						{"cancel", func() error { return cp.Deploys.Cancel(ctx, pending.ID) }},
						{"start", func() error { return cp.Instances.Start(ctx, inst.ID) }},
						{"stop", func() error { return cp.Instances.Stop(ctx, inst.ID) }},
						{"restart", func() error { return cp.Instances.Restart(ctx, inst.ID) }},
						{"scale", func() error { return cp.Instances.Scale(ctx, inst.ID, instance.ScaleRequest{}) }},
						{"suspend", func() error { return cp.Instances.Suspend(ctx, inst.ID, "test") }},
						{"unsuspend", func() error { return cp.Instances.Unsuspend(ctx, inst.ID) }},
						{"deploy", func() error {
							_, err := cp.Deploys.Deploy(ctx, deploy.DeployRequest{InstanceID: inst.ID})

							return err
						}},
						{"initial", func() error {
							_, err := cp.Deploys.RecordInitial(ctx, inst.ID)

							return err
						}},
						{"rollback", func() error {
							_, err := cp.Deploys.Rollback(ctx, inst.ID, id.New(id.PrefixRelease))

							return err
						}},
					}
					for _, op := range operations {
						t.Run(op.name, func(t *testing.T) {
							if err := op.run(); !errors.Is(err, managed.ErrBlocked) {
								t.Fatalf("got %v", err)
							}
						})
					}

					got, err := s.GetByID(ctx, "tenant", inst.ID)
					if state == "missing row" || state == "tombstone" {
						if !errors.Is(err, ctrlplane.ErrNotFound) {
							t.Fatal(err)
						}
					} else if err != nil || !reflect.DeepEqual(inst, got) {
						t.Fatalf("instance changed: %+v %v", got, err)
					}

					releases, err := s.ListReleases(ctx, "tenant", inst.ID, deploy.ListOptions{})
					if err != nil || len(releases.Items) != 0 {
						t.Fatalf("release write: %+v %v", releases, err)
					}

					deployments, err := s.ListDeployments(ctx, "tenant", inst.ID, deploy.ListOptions{})
					if err != nil || len(deployments.Items) != 1 || deployments.Items[0].State != deploy.DeployPending || deployments.Items[0].FinishedAt != nil {
						t.Fatalf("deployment write: %+v %v", deployments, err)
					}

					if err := cp.Admin.DeleteTenant(ctx, "tenant"); !errors.Is(err, managed.ErrBlocked) {
						t.Fatalf("tenant deletion: %v", err)
					}
				})
			}
		})
	}
}

func TestManagedWorkloadGuardPreservesParent(t *testing.T) {
	for _, derivedOwner := range []bool{true, false} {
		for _, op := range []string{"update", "scale", "pause", "resume", "restart", "deploy", "delete"} {
			t.Run(op+map[bool]string{true: " immutable owner", false: " child protection"}[derivedOwner], func(t *testing.T) {
				s := memory.New()
				p := &guardPolicy{}
				ctx := guardContext(t)
				w := workload.NewWorkload()
				w.TenantID = "tenant"
				w.Name = "workload"
				w.Slug = "workload"
				w.State = workload.StateActive
				w.ReplicaCount = 3
				w.PreviousReplicas = 7
				w.Services = []provider.ServiceSpec{{Name: "worker", Image: "before", Role: provider.RoleMain}}

				owner := id.ID{}
				if derivedOwner {
					owner = w.ID
				}

				identity := guardIdentity(owner)
				if _, err := s.CreateManagedTarget(ctx, identity); err != nil {
					t.Fatal(err)
				}

				if err := s.InsertWorkload(ctx, w); err != nil {
					t.Fatal(err)
				}

				if !derivedOwner {
					inst := &instance.Instance{Entity: ctrlplane.NewEntity(id.PrefixInstance), TenantID: "tenant", Labels: map[string]string{"ctrlplane.workload": w.ID.String()}, State: provider.StateRunning}

					inst.ID = identity.InstanceID
					if err := s.Insert(ctx, inst); err != nil {
						t.Fatal(err)
					}
				}

				cp, err := app.New(app.WithStore(s), app.WithAuth(p))
				if err != nil {
					t.Fatal(err)
				}

				switch op {
				case "update":
					name := "after"
					_, err = cp.Workloads.Update(ctx, w.ID, workload.UpdateRequest{Name: &name})
				case "scale":
					_, err = cp.Workloads.Scale(ctx, w.ID, 0)
				case "pause":
					err = cp.Workloads.Pause(ctx, w.ID)
				case "resume":
					err = cp.Workloads.Resume(ctx, w.ID)
				case "restart":
					err = cp.Workloads.Restart(ctx, w.ID)
				case "deploy":
					_, err = cp.Workloads.Deploy(ctx, w.ID, workload.DeployRequest{Services: []provider.ServiceDeploySpec{{Name: "worker", Image: "after"}}})
				case "delete":
					err = cp.Workloads.Delete(ctx, w.ID)
				}

				if !errors.Is(err, managed.ErrBlocked) {
					t.Fatalf("got %v", err)
				}

				after, err := s.GetWorkloadByID(ctx, "tenant", w.ID)
				if err != nil || !reflect.DeepEqual(w, after) {
					t.Fatalf("parent changed: %+v %v", after, err)
				}
			})
		}
	}
}

func TestOrdinaryInstanceRequiresAuthoritativeAbsence(t *testing.T) {
	s := memory.New()
	p := &guardPolicy{}
	ctx := guardContext(t)

	inst := &instance.Instance{Entity: ctrlplane.NewEntity(id.PrefixInstance), TenantID: "tenant", State: provider.StateDestroyed, Name: "before"}
	if err := s.Insert(ctx, inst); err != nil {
		t.Fatal(err)
	}

	cp, err := app.New(app.WithStore(s), app.WithAuth(p))
	if err != nil {
		t.Fatal(err)
	}

	name := "after"
	if _, err := cp.Instances.Update(ctx, inst.ID, instance.UpdateRequest{Name: &name}); err != nil {
		t.Fatal(err)
	}

	if err := cp.Instances.Delete(ctx, inst.ID); err != nil {
		t.Fatal(err)
	}

	if len(p.requests) != 2 || p.requests[0].Action != "instances.update" || p.requests[1].Action != "instances.delete" || p.requests[1].ResourceID != inst.ID.String() {
		t.Fatalf("actual actions missing: %+v", p.requests)
	}
}

type ordinaryDeployProvider struct {
	provider.Provider

	calls int
}

func (p *ordinaryDeployProvider) Info() provider.ProviderInfo {
	return provider.ProviderInfo{Name: "fixture"}
}
func (p *ordinaryDeployProvider) Deploy(context.Context, provider.DeployRequest) (*provider.DeployResult, error) {
	p.calls++

	return &provider.DeployResult{}, nil
}

func TestSameAuthorityOrdinaryWorkloadDeploy(t *testing.T) {
	s := memory.New()
	policy := &guardPolicy{}
	ctx := guardContext(t)
	p := &ordinaryDeployProvider{}

	cp, err := app.New(app.WithStore(s), app.WithAuth(policy))
	if err != nil {
		t.Fatal(err)
	}

	cp.Providers().Register("fixture", p)

	w := workload.NewWorkload()
	w.TenantID = "tenant"
	w.Slug = "worker"
	w.State = workload.StateActive

	w.Services = []provider.ServiceSpec{{Name: "worker", Image: "before", Role: provider.RoleMain}}
	if err := s.InsertWorkload(ctx, w); err != nil {
		t.Fatal(err)
	}

	inst := &instance.Instance{Entity: ctrlplane.NewEntity(id.PrefixInstance), TenantID: "tenant", State: provider.StateRunning, ProviderName: "fixture", Services: w.Services, Labels: map[string]string{"ctrlplane.workload": w.ID.String()}}
	if err := s.Insert(ctx, inst); err != nil {
		t.Fatal(err)
	}

	result, err := cp.Workloads.Deploy(ctx, w.ID, workload.DeployRequest{Services: []provider.ServiceDeploySpec{{Name: "worker", Image: "after"}}, Strategy: "recreate"})
	if err != nil || result.State != deploy.DeploySucceeded || p.calls != 1 {
		t.Fatalf("ordinary deploy: %+v %v calls=%d", result, err, p.calls)
	}

	got, err := s.GetWorkloadByID(ctx, "tenant", w.ID)
	if err != nil || got.Services[0].Image != "after" || got.CurrentReleaseID != result.ReleaseID {
		t.Fatalf("parent result: %+v %v", got, err)
	}
}
