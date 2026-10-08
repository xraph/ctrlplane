package contract

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/workload"
)

func assertStoredValue(t *testing.T, got, want any) {
	t.Helper()

	actual, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}

	expected, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}

	if string(actual) != string(expected) {
		t.Fatalf("stored value changed:\ngot  %s\nwant %s", actual, expected)
	}
}

func TestInstancePlacementAndLifecyclePersistence(t *testing.T) {
	for _, backend := range []string{"memory", "badger", "sqlite", "postgres", "mongo"} {
		t.Run(backend, func(t *testing.T) {
			cp, _ := storageHarness(t, backend)
			s := cp.Store()
			ctx := context.Background()
			stamp := time.Date(2026, 10, 8, 0, 0, 0, 123000000, time.UTC)

			inst := &instance.Instance{
				Entity:   ctrlplane.Entity{ID: id.New(id.PrefixInstance), CreatedAt: stamp, UpdatedAt: stamp},
				TenantID: "alpha", Name: "API", Slug: "api", DatacenterID: id.New(id.PrefixDatacenter), ProviderName: "demo", ProviderRef: "api-project", Region: "local", State: provider.StateStopped, Kind: provider.KindStatefulSet,
				Services:    []provider.ServiceSpec{{Name: "main", Image: "api:v1", Role: provider.RoleMain, Env: map[string]string{"MODE": "test"}}, {Name: "proxy", Image: "proxy:v1", Role: provider.RoleSidecar}},
				ServiceRefs: map[string]string{"main": "container-main"}, Labels: map[string]string{"ctrlplane.workload": "example"}, Endpoints: []provider.Endpoint{{ServiceName: "main", URL: "https://api.example.test", Port: 8080, Public: true}},
				CurrentRelease: id.New(id.PrefixRelease), SuspendedAt: &stamp,
				Source: provider.DeploymentSource{Type: provider.SourceServices},
			}
			if err := s.Insert(ctx, inst); err != nil {
				t.Fatal(err)
			}

			got, err := s.GetByID(ctx, "alpha", inst.ID)
			if err != nil {
				t.Fatal(err)
			}

			assertStoredValue(t, got, inst)
			// Same placement in another tenant must never appear in a tenant's list.
			foreign := *inst
			foreign.ID = id.New(id.PrefixInstance)

			foreign.TenantID = "beta"
			if err := s.Insert(ctx, &foreign); err != nil {
				t.Fatal(err)
			}

			other := *inst
			other.ID = id.New(id.PrefixInstance)
			other.Slug = "elsewhere"

			other.DatacenterID = id.New(id.PrefixDatacenter)
			if err := s.Insert(ctx, &other); err != nil {
				t.Fatal(err)
			}

			rows, err := s.List(ctx, "alpha", instance.ListOptions{Datacenter: inst.DatacenterID.String(), Provider: "demo", State: string(provider.StateStopped), Label: "ctrlplane.workload=example", Limit: 1})
			if err != nil {
				t.Fatal(err)
			}

			if rows.Total != 1 || len(rows.Items) != 1 || rows.Items[0].ID != inst.ID || rows.NextCursor != "" {
				t.Fatalf("placement filter: %#v", rows)
			}

			if _, err := s.GetByID(ctx, "beta", inst.ID); !errors.Is(err, ctrlplane.ErrNotFound) {
				t.Fatalf("foreign read: %v", err)
			}

			attack := *inst
			attack.TenantID = "beta"

			attack.Name = "Unauthorized"
			if err := s.Update(ctx, &attack); !errors.Is(err, ctrlplane.ErrNotFound) {
				t.Fatalf("foreign update: %v", err)
			}

			if err := s.Delete(ctx, "beta", inst.ID); !errors.Is(err, ctrlplane.ErrNotFound) {
				t.Fatalf("foreign delete: %v", err)
			}
			// Clear lifecycle and placement values explicitly, then reload.
			inst.DatacenterID = id.Nil
			inst.CurrentRelease = id.Nil
			inst.SuspendedAt = nil

			inst.State = provider.StateRunning
			if err := s.Update(ctx, inst); err != nil {
				t.Fatal(err)
			}

			got, err = s.GetByID(ctx, "alpha", inst.ID)
			if err != nil {
				t.Fatal(err)
			}

			inst.UpdatedAt = got.UpdatedAt
			assertStoredValue(t, got, inst)
		})
	}
}

func TestWorkloadStorageConformance(t *testing.T) {
	for _, backend := range []string{"memory", "badger", "sqlite", "postgres", "mongo"} {
		t.Run(backend, func(t *testing.T) {
			cp, _ := storageHarness(t, backend)
			s := cp.Store()
			ctx := context.Background()
			stamp := time.Date(2026, 10, 8, 0, 0, 0, 123000000, time.UTC)

			w := &workload.Workload{
				Entity: ctrlplane.Entity{ID: id.New(id.PrefixWorkload), CreatedAt: stamp, UpdatedAt: stamp}, TenantID: "alpha", Name: "API", Slug: "api", DatacenterID: id.New(id.PrefixDatacenter), ProviderName: "demo", Region: "local", Kind: provider.KindStatefulSet,
				Services: []provider.ServiceSpec{{Name: "main", Role: provider.RoleMain, Image: "api:v1", Env: map[string]string{"MODE": "test"}}, {Name: "proxy", Role: provider.RoleSidecar, Image: "proxy:v1"}}, Labels: map[string]string{"team": "platform"}, TemplateID: id.New(id.PrefixTemplate), CurrentReleaseID: id.New(id.PrefixRelease), ReplicaCount: 0, PreviousReplicas: 3, State: workload.StatePaused, PausedAt: &stamp,
			}
			if err := s.InsertWorkload(ctx, w); err != nil {
				t.Fatal(err)
			}

			got, err := s.GetWorkloadByID(ctx, "alpha", w.ID)
			if err != nil {
				t.Fatal(err)
			}

			assertStoredValue(t, got, w)

			bySlug, err := s.GetWorkloadBySlug(ctx, "alpha", w.Slug)
			if err != nil {
				t.Fatal(err)
			}

			assertStoredValue(t, bySlug, w)
			foreign := *w
			foreign.ID = id.New(id.PrefixWorkload)

			foreign.TenantID = "beta"
			if err := s.InsertWorkload(ctx, &foreign); err != nil {
				t.Fatal(err)
			}

			duplicate := *w

			duplicate.ID = id.New(id.PrefixWorkload)
			if err := s.InsertWorkload(ctx, &duplicate); !errors.Is(err, ctrlplane.ErrAlreadyExists) {
				t.Fatalf("duplicate slug: %v", err)
			}

			if err := s.InsertWorkload(ctx, w); !errors.Is(err, ctrlplane.ErrAlreadyExists) {
				t.Fatalf("duplicate ID: %v", err)
			}

			rows, err := s.ListWorkloads(ctx, "alpha", workload.ListOptions{State: workload.StatePaused, ProviderName: "demo", Region: "local", Limit: 1})
			if err != nil {
				t.Fatal(err)
			}

			if rows.Total != 1 || len(rows.Items) != 1 || rows.Items[0].ID != w.ID {
				t.Fatalf("filters: %#v", rows)
			}

			empty, err := s.ListWorkloads(ctx, "alpha", workload.ListOptions{Region: "elsewhere", Limit: 1})
			if err != nil || empty == nil || empty.Total != 0 || len(empty.Items) != 0 {
				t.Fatalf("empty filter: %#v %v", empty, err)
			}

			if _, err := s.GetWorkloadByID(ctx, "beta", w.ID); !errors.Is(err, ctrlplane.ErrNotFound) {
				t.Fatalf("foreign read: %v", err)
			}

			attack := *w
			attack.TenantID = "beta"

			attack.Name = "Unauthorized"
			if err := s.UpdateWorkload(ctx, &attack); !errors.Is(err, ctrlplane.ErrNotFound) {
				t.Fatalf("foreign update: %v", err)
			}

			if err := s.DeleteWorkload(ctx, "beta", w.ID); !errors.Is(err, ctrlplane.ErrNotFound) {
				t.Fatalf("foreign delete: %v", err)
			}

			w.Name = "Updated API"
			w.ReplicaCount = 3
			w.PausedAt = nil
			w.DatacenterID = id.Nil
			w.TemplateID = id.Nil
			w.CurrentReleaseID = id.Nil
			w.Labels = nil

			w.State = workload.StateActive
			if err := s.UpdateWorkload(ctx, w); err != nil {
				t.Fatal(err)
			}

			got, err = s.GetWorkloadByID(ctx, "alpha", w.ID)
			if err != nil {
				t.Fatal(err)
			}

			w.UpdatedAt = got.UpdatedAt
			assertStoredValue(t, got, w)

			if err := s.DeleteWorkload(ctx, "alpha", w.ID); err != nil {
				t.Fatal(err)
			}

			if _, err := s.GetWorkloadByID(ctx, "alpha", w.ID); !errors.Is(err, ctrlplane.ErrNotFound) {
				t.Fatalf("deleted read: %v", err)
			}
		})
	}
}

func TestWorkloadContinuationUsesChronologicalOrder(t *testing.T) {
	for _, backend := range []string{"memory", "badger", "sqlite", "postgres", "mongo"} {
		t.Run(backend, func(t *testing.T) {
			cp, _ := storageHarness(t, backend)
			s := cp.Store()
			ctx := context.Background()
			base := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
			// Fractional seconds and the next whole second must sort chronologically.
			times := []time.Duration{0, 900 * time.Millisecond, 10 * time.Millisecond, time.Second, 100 * time.Millisecond}

			want := make([]id.ID, len(times))
			for i, offset := range times {
				w := workload.NewWorkload()
				w.TenantID = "alpha"
				w.Slug = string(rune('a' + i))

				w.CreatedAt = base.Add(offset)
				if err := s.InsertWorkload(ctx, w); err != nil {
					t.Fatal(err)
				}

				want[i] = w.ID
			}

			expected := []id.ID{want[3], want[1], want[4], want[2], want[0]}

			cursor := ""
			for _, target := range expected {
				page, err := s.ListWorkloads(ctx, "alpha", workload.ListOptions{Cursor: cursor, Limit: 1})
				if err != nil {
					t.Fatal(err)
				}

				if len(page.Items) != 1 || page.Items[0].ID != target {
					t.Fatalf("incorrect chronological continuation: %#v, want %s", page, target)
				}

				cursor = page.NextCursor
			}

			if cursor != "" {
				t.Fatalf("unexpected continuation: %s", cursor)
			}
		})
	}
}
