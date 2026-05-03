package template

import (
	"context"
	"testing"

	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// TestCreateFromWorkload_RoundTripsAllServices verifies that forking a
// template from a multi-service workload preserves every service in
// order with their full per-service spec (image, role, env, resources).
func TestCreateFromWorkload_RoundTripsAllServices(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := NewService(store, nil)

	source := &WorkloadSpec{
		Kind: provider.KindStatefulSet,
		Services: []provider.ServiceSpec{
			{
				Name:  "init-db",
				Image: "alpine:3",
				Role:  provider.RoleInit,
			},
			{
				Name:  "main",
				Image: "postgres:16",
				Role:  provider.RoleMain,
				Resources: provider.ResourceSpec{
					CPUMillis: 1000,
					MemoryMB:  2048,
					Replicas:  3,
				},
				Env: map[string]string{"POSTGRES_DB": "app"},
				Volumes: []provider.VolumeSpec{
					{Name: "data", MountPath: "/var/lib/postgresql/data", SizeMB: 10240},
				},
			},
			{
				Name:  "metrics",
				Image: "postgres-exporter:0.15",
				Role:  provider.RoleSidecar,
			},
		},
		Labels: map[string]string{"app": "billing"},
	}

	svc.SetWorkloadReader(staticReader{spec: source})

	ctx := auth.WithClaims(context.Background(), &auth.Claims{TenantID: "ten_t", SubjectID: "ops"})

	tmpl, err := svc.CreateFromWorkload(ctx, id.New(id.PrefixWorkload), CreateFromWorkloadRequest{
		Name:        "billing-template",
		Description: "Billing workload (postgres + metrics + init)",
	})
	if err != nil {
		t.Fatalf("CreateFromWorkload: %v", err)
	}

	if tmpl.DefaultKind != provider.KindStatefulSet {
		t.Fatalf("DefaultKind: want stateful_set, got %q", tmpl.DefaultKind)
	}

	if len(tmpl.Services) != 3 {
		t.Fatalf("services: want 3, got %d", len(tmpl.Services))
	}

	expected := []string{"init-db", "main", "metrics"}
	for i, want := range expected {
		if tmpl.Services[i].Name != want {
			t.Fatalf("services[%d].Name: want %q, got %q", i, want, tmpl.Services[i].Name)
		}
	}

	main := tmpl.MainService()
	if main == nil {
		t.Fatalf("expected a Main service in the forked template")
	}

	if main.Resources.Replicas != 3 {
		t.Fatalf("main replicas: want 3, got %d", main.Resources.Replicas)
	}

	if len(main.Volumes) != 1 || main.Volumes[0].Name != "data" {
		t.Fatalf("main volumes: %+v", main.Volumes)
	}

	if tmpl.Labels["app"] != "billing" {
		t.Fatalf("labels: %+v", tmpl.Labels)
	}
}

// staticReader is a tiny WorkloadSpecReader for the fork test.
type staticReader struct {
	spec *WorkloadSpec
}

func (r staticReader) ReadWorkloadSpec(_ context.Context, _ string, _ id.ID) (*WorkloadSpec, error) {
	return r.spec, nil
}
