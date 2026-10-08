package contract

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	dash "github.com/xraph/forge/extensions/dashboard/contract"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/datacenter"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/workload"
)

func assertContinuation[T any](t *testing.T, list func(string) ([]T, string, int, error), identity func(T) id.ID, expected int) {
	t.Helper()

	seen := map[id.ID]bool{}

	cursor := ""
	for range expected + 1 {
		items, next, total, err := list(cursor)
		if err != nil {
			t.Fatal(err)
		}

		if total != expected || len(items) > 2 {
			t.Fatalf("wrong page count/limit: total %d items %d", total, len(items))
		}

		for _, item := range items {
			target := identity(item)
			if seen[target] {
				t.Fatalf("duplicate %s", target)
			}

			seen[target] = true
		}

		if next == "" {
			break
		}

		if next == cursor {
			t.Fatal("cursor did not advance")
		}

		cursor = next
	}

	if len(seen) != expected {
		t.Fatalf("got %d records, want %d", len(seen), expected)
	}

	if _, _, _, err := list("malformed"); err == nil {
		t.Fatal("ignored malformed cursor")
	}
}
func TestStoreContinuationConformance(t *testing.T) {
	for _, backend := range []string{"memory", "badger", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			cp, _ := storageHarness(t, backend)
			s := cp.Store()
			ctx := context.Background()
			parent := id.New(id.PrefixInstance)
			stamp := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)

			for i := range 5 {
				entity := func(prefix id.Prefix) ctrlplane.Entity {
					e := ctrlplane.NewEntity(prefix)
					e.CreatedAt = stamp

					return e
				}

				rows := []func() error{
					func() error {
						return s.Insert(ctx, &instance.Instance{Entity: entity(id.PrefixInstance), TenantID: "alpha", Slug: fmt.Sprintf("api-%d", i), Name: "API", State: provider.StateRunning})
					},
					func() error {
						return s.InsertDeployment(ctx, &deploy.Deployment{Entity: entity(id.PrefixDeployment), TenantID: "alpha", InstanceID: parent, ReleaseID: id.New(id.PrefixRelease)})
					},
					func() error {
						return s.InsertRelease(ctx, &deploy.Release{Entity: entity(id.PrefixRelease), TenantID: "alpha", InstanceID: parent, Version: i + 1})
					},
					func() error {
						return s.InsertTenant(ctx, &admin.Tenant{Entity: entity(id.PrefixTenant), Slug: fmt.Sprintf("tenant-%d", i), Name: "Tenant", Status: admin.TenantActive})
					},
					func() error {
						return s.InsertDatacenter(ctx, &datacenter.Datacenter{Entity: entity(id.PrefixDatacenter), TenantID: "alpha", Slug: fmt.Sprintf("dc-%d", i), Name: "DC", Status: datacenter.StatusActive})
					},
				}
				for _, insert := range rows {
					if err := insert(); err != nil {
						t.Fatal(err)
					}
				}

				if backend != "sqlite" {
					if err := s.InsertWorkload(ctx, &workload.Workload{Entity: entity(id.PrefixWorkload), TenantID: "alpha", Slug: fmt.Sprintf("workload-%d", i), Name: "Workload", State: workload.StateActive}); err != nil {
						t.Fatal(err)
					}
				}
			}

			t.Run("instances", func(t *testing.T) {
				assertContinuation(t, func(cursor string) ([]*instance.Instance, string, int, error) {
					r, err := s.List(ctx, "alpha", instance.ListOptions{Cursor: cursor, Limit: 2})
					if err != nil {
						return nil, "", 0, err
					}

					return r.Items, r.NextCursor, r.Total, nil
				}, func(v *instance.Instance) id.ID { return v.ID }, 5)
			})
			t.Run("deployments", func(t *testing.T) {
				assertContinuation(t, func(cursor string) ([]*deploy.Deployment, string, int, error) {
					r, err := s.ListDeployments(ctx, "alpha", parent, deploy.ListOptions{Cursor: cursor, Limit: 2})
					if err != nil {
						return nil, "", 0, err
					}

					return r.Items, r.NextCursor, r.Total, nil
				}, func(v *deploy.Deployment) id.ID { return v.ID }, 5)
			})
			t.Run("releases", func(t *testing.T) {
				assertContinuation(t, func(cursor string) ([]*deploy.Release, string, int, error) {
					r, err := s.ListReleases(ctx, "alpha", parent, deploy.ListOptions{Cursor: cursor, Limit: 2})
					if err != nil {
						return nil, "", 0, err
					}

					return r.Items, r.NextCursor, r.Total, nil
				}, func(v *deploy.Release) id.ID { return v.ID }, 5)
			})
			t.Run("tenants", func(t *testing.T) {
				assertContinuation(t, func(cursor string) ([]*admin.Tenant, string, int, error) {
					r, err := s.ListTenants(ctx, admin.ListTenantsOptions{Cursor: cursor, Limit: 2})
					if err != nil {
						return nil, "", 0, err
					}

					return r.Items, r.NextCursor, r.Total, nil
				}, func(v *admin.Tenant) id.ID { return v.ID }, 5)
			})
			t.Run("datacenters", func(t *testing.T) {
				assertContinuation(t, func(cursor string) ([]*datacenter.Datacenter, string, int, error) {
					r, err := s.ListDatacenters(ctx, "alpha", datacenter.ListOptions{Cursor: cursor, Limit: 2})
					if err != nil {
						return nil, "", 0, err
					}

					return r.Items, r.NextCursor, r.Total, nil
				}, func(v *datacenter.Datacenter) id.ID { return v.ID }, 5)
			})

			if backend != "sqlite" {
				t.Run("workloads", func(t *testing.T) {
					assertContinuation(t, func(cursor string) ([]*workload.Workload, string, int, error) {
						r, err := s.ListWorkloads(ctx, "alpha", workload.ListOptions{Cursor: cursor, Limit: 2})
						if err != nil {
							return nil, "", 0, err
						}

						return r.Items, r.NextCursor, r.Total, nil
					}, func(v *workload.Workload) id.ID { return v.ID }, 5)
				})
			}
		})
	}
}
func TestWorkloadDeploymentContinuationAcrossReplicas(t *testing.T) {
	for _, backend := range []string{"memory", "badger"} {
		t.Run(backend, func(t *testing.T) {
			cp, h := storageHarness(t, backend)
			ctx := context.Background()

			w := &workload.Workload{Entity: ctrlplane.NewEntity(id.PrefixWorkload), TenantID: "alpha", Slug: "api", Name: "API"}
			if err := cp.Store().InsertWorkload(ctx, w); err != nil {
				t.Fatal(err)
			}

			instances := []id.ID{id.New(id.PrefixInstance), id.New(id.PrefixInstance)}
			for i, target := range instances {
				if err := cp.Store().Insert(ctx, &instance.Instance{Entity: ctrlplane.Entity{ID: target, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}, TenantID: "alpha", Name: "API", Slug: fmt.Sprintf("api-%d", i), Labels: map[string]string{"ctrlplane.workload": w.ID.String()}}); err != nil {
					t.Fatal(err)
				}
			}

			for i := range 5 {
				d := &deploy.Deployment{Entity: ctrlplane.NewEntity(id.PrefixDeployment), TenantID: "alpha", InstanceID: instances[i%2]}

				d.CreatedAt = time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
				if err := cp.Store().InsertDeployment(ctx, d); err != nil {
					t.Fatal(err)
				}
			}

			assertContinuation(t, func(cursor string) ([]*deploy.Deployment, string, int, error) {
				r := request(t, h, principal("alpha", false), dash.KindQuery, "deployments.list", map[string]any{"workload_id": w.ID.String(), "limit": 2, "cursor": cursor})
				if !r.OK {
					return nil, "", 0, r.Error
				}

				var p deploy.DeployListResult
				if err := json.Unmarshal(r.Data, &p); err != nil {
					return nil, "", 0, err
				}

				return p.Items, p.NextCursor, p.Total, nil
			}, func(v *deploy.Deployment) id.ID { return v.ID }, 5)
		})
	}
}
