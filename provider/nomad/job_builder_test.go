package nomad

import (
	"testing"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

func TestBuildJob_MultiService(t *testing.T) {
	t.Parallel()

	cfg := Config{Region: "us-east", Namespace: "ctrlplane", Datacenter: "dc1"}
	iid := id.New(id.PrefixInstance)

	req := provider.ProvisionRequest{
		InstanceID: iid,
		TenantID:   "ten_test",
		Name:       "web",
		Kind:       provider.KindDeployment,
		Services: []provider.ServiceSpec{
			{
				Name:  "init-db",
				Image: "alpine:3",
				Role:  provider.RoleInit,
			},
			{
				Name:  "main",
				Image: "myapp:1.0",
				Role:  provider.RoleMain,
				Resources: provider.ResourceSpec{
					CPUMillis: 500,
					MemoryMB:  256,
					Replicas:  3,
				},
				Ports: []provider.PortSpec{{Container: 8080, Protocol: "tcp"}},
			},
			{
				Name:  "envoy",
				Image: "envoyproxy/envoy:v1.30",
				Role:  provider.RoleSidecar,
			},
		},
	}

	body := buildJob(cfg, req)
	if body == nil || body.Job == nil {
		t.Fatalf("buildJob returned nil")
	}

	if body.Job.ID != jobName(iid) {
		t.Fatalf("job ID: want %q, got %q", jobName(iid), body.Job.ID)
	}

	if body.Job.Region != "us-east" || body.Job.Namespace != "ctrlplane" {
		t.Fatalf("region/namespace not propagated: %+v", body.Job)
	}

	if len(body.Job.Datacenters) != 1 || body.Job.Datacenters[0] != "dc1" {
		t.Fatalf("datacenter not propagated: %+v", body.Job.Datacenters)
	}

	if len(body.Job.TaskGroups) != 1 {
		t.Fatalf("task groups: want 1, got %d", len(body.Job.TaskGroups))
	}

	group := body.Job.TaskGroups[0]
	if group.Count != 3 {
		t.Fatalf("count: want 3 (from Main.Replicas), got %d", group.Count)
	}

	if len(group.Tasks) != 3 {
		t.Fatalf("tasks: want 3, got %d", len(group.Tasks))
	}

	// Verify lifecycle hooks: Init=prestart, Sidecar=poststart with
	// sidecar=true, Main=no lifecycle.
	for _, task := range group.Tasks {
		switch task.Name {
		case "init-db":
			if task.Lifecycle == nil || task.Lifecycle.Hook != "prestart" {
				t.Fatalf("init-db: want lifecycle prestart, got %+v", task.Lifecycle)
			}

			if task.Lifecycle.Sidecar {
				t.Fatalf("init-db: must not be sidecar=true")
			}

		case "main":
			if task.Lifecycle != nil {
				t.Fatalf("main: want no lifecycle, got %+v", task.Lifecycle)
			}

		case "envoy":
			if task.Lifecycle == nil || task.Lifecycle.Hook != "poststart" || !task.Lifecycle.Sidecar {
				t.Fatalf("envoy: want lifecycle poststart sidecar=true, got %+v", task.Lifecycle)
			}
		}
	}

	// Verify the Main task carries resource requests.
	for _, task := range group.Tasks {
		if task.Name != "main" {
			continue
		}

		if task.Resources == nil {
			t.Fatalf("main: expected resources, got nil")
		}

		if task.Resources.CPU != 500 || task.Resources.MemoryMB != 256 {
			t.Fatalf("main resources: want 500m/256MB, got %+v", task.Resources)
		}
	}
}

func TestBuildJob_DefaultsCountToOne(t *testing.T) {
	t.Parallel()

	cfg := Config{}
	req := provider.ProvisionRequest{
		InstanceID: id.New(id.PrefixInstance),
		Services: []provider.ServiceSpec{
			{Name: "main", Image: "x:1", Role: provider.RoleMain},
		},
	}

	body := buildJob(cfg, req)
	if body.Job.TaskGroups[0].Count != 1 {
		t.Fatalf("default count should be 1, got %d", body.Job.TaskGroups[0].Count)
	}
}

func TestBuildJob_SinglePortLabelPerServicePort(t *testing.T) {
	t.Parallel()

	cfg := Config{}
	req := provider.ProvisionRequest{
		InstanceID: id.New(id.PrefixInstance),
		Services: []provider.ServiceSpec{
			{
				Name:  "main",
				Image: "x:1",
				Role:  provider.RoleMain,
				Ports: []provider.PortSpec{
					{Container: 8080},
					{Container: 9090},
				},
			},
		},
	}

	body := buildJob(cfg, req)
	group := body.Job.TaskGroups[0]

	if len(group.Networks) != 1 {
		t.Fatalf("networks: want 1, got %d", len(group.Networks))
	}

	if len(group.Networks[0].DynamicPorts) != 2 {
		t.Fatalf("dynamic ports: want 2, got %d", len(group.Networks[0].DynamicPorts))
	}

	wantLabels := []string{"main-0", "main-1"}
	for i, want := range wantLabels {
		if group.Networks[0].DynamicPorts[i].Label != want {
			t.Fatalf("port[%d] label: want %q, got %q", i, want, group.Networks[0].DynamicPorts[i].Label)
		}
	}
}

func TestPickMain_NilWhenAllSidecarsOrInits(t *testing.T) {
	t.Parallel()

	services := []provider.ServiceSpec{
		{Name: "i", Role: provider.RoleInit},
		{Name: "s", Role: provider.RoleSidecar},
	}

	if got := pickMain(services); got != nil {
		t.Fatalf("want nil, got %+v", got)
	}
}

func TestPickMain_DefaultRoleIsMain(t *testing.T) {
	t.Parallel()

	services := []provider.ServiceSpec{
		{Name: "main"}, // empty role
	}

	got := pickMain(services)
	if got == nil || got.Name != "main" {
		t.Fatalf("want main, got %+v", got)
	}
}
