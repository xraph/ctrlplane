package deploy_test

import (
	"context"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/store/memory"
)

// TestRecordInitial_PersistsV1Release covers the headline contract:
// a fresh instance gets a v1 Release whose service snapshots match
// the instance's Services slice, plus a synthetic deploy.DeploySucceeded
// Deployment row tagged strategy="initial".
func TestRecordInitial_PersistsV1Release(t *testing.T) {
	t.Parallel()

	ctx := adminCtxDeploy()
	store := memory.New()

	inst := &instance.Instance{
		Entity:       ctrlplane.NewEntity(id.PrefixInstance),
		TenantID:     "ten_test",
		Name:         "web-1",
		ProviderName: "kubernetes",
		Kind:         provider.KindDeployment,
		State:        provider.StateRunning,
		Services: []provider.ServiceSpec{
			{Name: "main", Image: "myapp:1.0", Role: provider.RoleMain, Env: map[string]string{"FOO": "bar"}},
			{Name: "envoy", Image: "envoy:v1", Role: provider.RoleSidecar},
		},
	}
	if err := store.Insert(ctx, inst); err != nil {
		t.Fatalf("insert instance: %v", err)
	}

	svc := deploy.NewService(store, store, nil, event.NewInMemoryBus(), &auth.NoopProvider{}, nil)

	rel, err := svc.RecordInitial(ctx, inst.ID)
	if err != nil {
		t.Fatalf("RecordInitial: %v", err)
	}

	if rel.Version != 1 {
		t.Fatalf("Version: want 1, got %d", rel.Version)
	}

	if !rel.Active {
		t.Fatal("Release.Active: want true (newly-recorded v1)")
	}

	if len(rel.Services) != 2 {
		t.Fatalf("Release.Services: want 2 snapshots, got %d", len(rel.Services))
	}

	// Per-service snapshot fidelity: env/image must round-trip.
	mainSnap := findSnapshot(rel.Services, "main")
	if mainSnap == nil || mainSnap.Image != "myapp:1.0" || mainSnap.Env["FOO"] != "bar" {
		t.Fatalf("main snapshot: %+v", mainSnap)
	}

	// Deployment row exists, succeeded, strategy=initial.
	deps, err := store.ListDeployments(ctx, "ten_test", inst.ID, deploy.ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("list deployments: %v", err)
	}

	if len(deps.Items) != 1 {
		t.Fatalf("deployments: want 1, got %d", len(deps.Items))
	}

	dep := deps.Items[0]
	if dep.State != deploy.DeploySucceeded {
		t.Fatalf("Deployment.State: want succeeded, got %s", dep.State)
	}

	if dep.Strategy != "initial" {
		t.Fatalf("Deployment.Strategy: want initial, got %s", dep.Strategy)
	}

	if dep.ReleaseID != rel.ID {
		t.Fatal("Deployment.ReleaseID does not point at the new Release")
	}

	// Per-service progress should report each service as succeeded.
	for _, svc := range inst.Services {
		if dep.ServiceProgress[svc.Name] != "succeeded" {
			t.Fatalf("ServiceProgress[%s]: want succeeded, got %s", svc.Name, dep.ServiceProgress[svc.Name])
		}
	}
}

// TestRecordInitial_Idempotent asserts a second call does not
// insert a duplicate v1 — instead returns the existing first
// Release. spawnReplica's adoption path can re-invoke against an
// instance that already has a release history (re-create after a
// crash), and this must be safe.
func TestRecordInitial_Idempotent(t *testing.T) {
	t.Parallel()

	ctx := adminCtxDeploy()
	store := memory.New()

	inst := &instance.Instance{
		Entity:       ctrlplane.NewEntity(id.PrefixInstance),
		TenantID:     "ten_test",
		Name:         "web-1",
		ProviderName: "kubernetes",
		Kind:         provider.KindDeployment,
		State:        provider.StateRunning,
		Services: []provider.ServiceSpec{
			{Name: "main", Image: "myapp:1.0", Role: provider.RoleMain},
		},
	}
	if err := store.Insert(ctx, inst); err != nil {
		t.Fatalf("insert instance: %v", err)
	}

	svc := deploy.NewService(store, store, nil, event.NewInMemoryBus(), &auth.NoopProvider{}, nil)

	first, err := svc.RecordInitial(ctx, inst.ID)
	if err != nil {
		t.Fatalf("RecordInitial #1: %v", err)
	}

	second, err := svc.RecordInitial(ctx, inst.ID)
	if err != nil {
		t.Fatalf("RecordInitial #2: %v", err)
	}

	if first.ID != second.ID {
		t.Fatal("RecordInitial should return the existing v1 Release on the second call")
	}

	rels, err := store.ListReleases(ctx, "ten_test", inst.ID, deploy.ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("list releases: %v", err)
	}

	if len(rels.Items) != 1 {
		t.Fatalf("releases: want 1 (idempotent), got %d", len(rels.Items))
	}

	deps, err := store.ListDeployments(ctx, "ten_test", inst.ID, deploy.ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("list deployments: %v", err)
	}

	if len(deps.Items) != 1 {
		t.Fatalf("deployments: want 1 (idempotent), got %d", len(deps.Items))
	}
}

// --- helpers ---

func adminCtxDeploy() context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{
		SubjectID: "test-user",
		TenantID:  "ten_test",
		Roles:     []string{"system:admin"},
	})
}

func findSnapshot(items []provider.ServiceSnapshot, name string) *provider.ServiceSnapshot {
	for i := range items {
		if items[i].Name == name {
			return &items[i]
		}
	}

	return nil
}
