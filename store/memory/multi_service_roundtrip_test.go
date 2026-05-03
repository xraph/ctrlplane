package memory

import (
	"context"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/template"
	"github.com/xraph/ctrlplane/workload"
)

// TestPhase4_InstanceRoundTrip confirms instance persistence carries
// multi-service shape end-to-end without any synthesis or legacy
// Image fallback.
func TestPhase4_InstanceRoundTrip(t *testing.T) {
	t.Parallel()

	store := New()
	ctx := context.Background()

	inst := &instance.Instance{
		Entity:       ctrlplane.NewEntity(id.PrefixInstance),
		TenantID:     "ten_test",
		Name:         "web-1",
		Slug:         "web-1",
		ProviderName: "kubernetes",
		Kind:         provider.KindDeployment,
		State:        provider.StateRunning,
		Services: []provider.ServiceSpec{
			{Name: "main", Image: "myapp:1.0", Role: provider.RoleMain},
			{Name: "envoy", Image: "envoy:v1", Role: provider.RoleSidecar},
			{Name: "init-db", Image: "alpine:3", Role: provider.RoleInit},
		},
		ServiceRefs: map[string]string{
			"main":    "main-container-id",
			"envoy":   "envoy-container-id",
			"init-db": "init-container-id",
		},
		Labels: map[string]string{"app": "billing"},
	}

	if err := store.Insert(ctx, inst); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := store.GetByID(ctx, "ten_test", inst.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if len(got.Services) != 3 {
		t.Fatalf("services: want 3, got %d", len(got.Services))
	}

	if main := got.MainService(); main == nil || main.Image != "myapp:1.0" {
		t.Fatalf("main: %+v", main)
	}

	if got.ServiceRefs["envoy"] != "envoy-container-id" {
		t.Fatalf("service refs lost: %+v", got.ServiceRefs)
	}

	if got.Kind != provider.KindDeployment {
		t.Fatalf("kind: want deployment, got %q", got.Kind)
	}
}

// TestPhase4_TemplateRoundTrip confirms template persistence is
// multi-service-only with no fallback to per-spec legacy fields.
func TestPhase4_TemplateRoundTrip(t *testing.T) {
	t.Parallel()

	store := New()
	ctx := context.Background()

	tmpl := &template.Template{
		Entity:          ctrlplane.NewEntity(id.PrefixTemplate),
		TenantID:        "ten_test",
		Name:            "billing-template",
		Description:     "billing workload blueprint",
		DefaultKind:     provider.KindStatefulSet,
		DefaultStrategy: "rolling",
		Services: []provider.ServiceSpec{
			{
				Name:  "main",
				Image: "postgres:16",
				Role:  provider.RoleMain,
				Resources: provider.ResourceSpec{
					CPUMillis: 1000,
					MemoryMB:  2048,
					Replicas:  3,
				},
				Volumes: []provider.VolumeSpec{
					{Name: "data", MountPath: "/var/lib/postgresql/data", SizeMB: 10240},
				},
			},
		},
		Labels: map[string]string{"team": "platform"},
		Notes:  "round-trip integrity check",
	}

	if err := store.InsertTemplate(ctx, tmpl); err != nil {
		t.Fatalf("insert template: %v", err)
	}

	got, err := store.GetTemplate(ctx, "ten_test", tmpl.ID)
	if err != nil {
		t.Fatalf("get template: %v", err)
	}

	main := got.MainService()
	if main == nil || main.Image != "postgres:16" || main.Resources.Replicas != 3 {
		t.Fatalf("main service: %+v", main)
	}

	if len(main.Volumes) != 1 {
		t.Fatalf("volumes: %+v", main.Volumes)
	}

	if got.DefaultKind != provider.KindStatefulSet {
		t.Fatalf("default kind: %q", got.DefaultKind)
	}
}

// TestPhase4_WorkloadRoundTrip confirms workload persistence is
// multi-service-only.
func TestPhase4_WorkloadRoundTrip(t *testing.T) {
	t.Parallel()

	store := New()
	ctx := context.Background()

	w := &workload.Workload{
		Entity:       ctrlplane.NewEntity(id.PrefixWorkload),
		TenantID:     "ten_test",
		Name:         "billing",
		Slug:         "billing",
		ProviderName: "kubernetes",
		Kind:         provider.KindDeployment,
		Services: []provider.ServiceSpec{
			{Name: "api", Image: "api:1", Role: provider.RoleMain, Resources: provider.ResourceSpec{Replicas: 5}},
			{Name: "metrics", Image: "metrics:1", Role: provider.RoleSidecar},
		},
		ReplicaCount: 5,
		State:        workload.StateActive,
	}

	if err := store.InsertWorkload(ctx, w); err != nil {
		t.Fatalf("insert workload: %v", err)
	}

	got, err := store.GetWorkloadByID(ctx, "ten_test", w.ID)
	if err != nil {
		t.Fatalf("get workload: %v", err)
	}

	if len(got.Services) != 2 {
		t.Fatalf("services: want 2, got %d", len(got.Services))
	}

	if got.ReplicaCount != 5 {
		t.Fatalf("replicas: %d", got.ReplicaCount)
	}
}

// TestPhase4_ReleaseRoundTrip confirms release self-contained per-
// service snapshots persist correctly.
func TestPhase4_ReleaseRoundTrip(t *testing.T) {
	t.Parallel()

	store := New()
	ctx := context.Background()

	rel := &deploy.Release{
		Entity:     ctrlplane.NewEntity(id.PrefixRelease),
		TenantID:   "ten_test",
		InstanceID: id.New(id.PrefixInstance),
		Version:    7,
		Services: []provider.ServiceSnapshot{
			{Name: "api", Image: "api:v7", Env: map[string]string{"FOO": "bar"}},
			{Name: "worker", Image: "worker:v7"},
		},
		Active: true,
	}

	if err := store.InsertRelease(ctx, rel); err != nil {
		t.Fatalf("insert release: %v", err)
	}

	got, err := store.GetRelease(ctx, "ten_test", rel.ID)
	if err != nil {
		t.Fatalf("get release: %v", err)
	}

	if len(got.Services) != 2 {
		t.Fatalf("services: want 2, got %d", len(got.Services))
	}

	if got.Services[0].Name != "api" || got.Services[0].Image != "api:v7" {
		t.Fatalf("api snapshot: %+v", got.Services[0])
	}

	if got.Services[0].Env["FOO"] != "bar" {
		t.Fatalf("env not preserved: %+v", got.Services[0].Env)
	}
}
