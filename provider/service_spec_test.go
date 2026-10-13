package provider

import (
	"slices"
	"testing"
)

func TestServiceDeploySpec_Snapshot(t *testing.T) {
	t.Parallel()

	prior := &ServiceSnapshot{
		Name:      "main",
		Image:     "api:1",
		Command:   []string{"/bin/api"},
		Args:      []string{"serve"},
		Ports:     []PortSpec{{Container: 8080}},
		Resources: &ResourceSpec{CPUMillis: 500},
	}

	tests := []struct {
		name      string
		spec      ServiceDeploySpec
		prior     *ServiceSnapshot
		wantCmd   []string
		wantPort  int
		wantCPU   int
		wantNoRes bool
	}{
		{
			name:     "unset fields inherit",
			spec:     ServiceDeploySpec{Name: "main", Image: "api:2"},
			prior:    prior,
			wantCmd:  []string{"/bin/api"},
			wantPort: 8080,
			wantCPU:  500,
		},
		{
			name: "set fields win",
			spec: ServiceDeploySpec{
				Name: "main", Image: "api:2",
				Command:   []string{"/bin/worker"},
				Ports:     []PortSpec{{Container: 9090}},
				Resources: &ResourceSpec{CPUMillis: 2000},
			},
			prior:    prior,
			wantCmd:  []string{"/bin/worker"},
			wantPort: 9090,
			wantCPU:  2000,
		},
		{
			name:      "no prior snapshot",
			spec:      ServiceDeploySpec{Name: "main", Image: "api:2"},
			wantNoRes: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.spec.Snapshot(tt.prior)

			if got.Image != "api:2" {
				t.Fatalf("Image = %q, want api:2", got.Image)
			}

			if tt.wantNoRes {
				if got.Resources != nil || got.Command != nil || got.Ports != nil {
					t.Fatalf("snapshot = %+v, want optional fields empty", got)
				}

				return
			}

			if !slices.Equal(got.Command, tt.wantCmd) || got.Ports[0].Container != tt.wantPort || got.Resources.CPUMillis != tt.wantCPU {
				t.Fatalf("snapshot = %+v", got)
			}

			back := got.DeploySpec()
			if !slices.Equal(back.Command, got.Command) || back.Resources != got.Resources {
				t.Fatalf("DeploySpec() = %+v does not restore %+v", back, got)
			}
		})
	}
}

func TestSnapshotOf_OmitsZeroResources(t *testing.T) {
	t.Parallel()

	if got := SnapshotOf(ServiceSpec{Name: "main", Image: "api:1"}); got.Resources != nil {
		t.Fatalf("Resources = %+v, want nil for an unsized service", got.Resources)
	}

	got := SnapshotOf(ServiceSpec{Name: "main", Image: "api:1", Resources: ResourceSpec{MemoryMB: 256}})
	if got.Resources == nil || got.Resources.MemoryMB != 256 {
		t.Fatalf("Resources = %+v, want 256MB", got.Resources)
	}
}
