package kubernetes

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// TestBuildPodSpec_MultiService verifies that a workload with a Main +
// Sidecar + Init service produces a PodSpec with the correct container
// distribution: Main + Sidecar in containers[], Init in
// initContainers[].
func TestBuildPodSpec_MultiService(t *testing.T) {
	t.Parallel()

	req := provider.ProvisionRequest{
		InstanceID: id.New(id.PrefixInstance),
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
					Replicas:  2,
				},
				Env: map[string]string{"LOG_LEVEL": "info"},
				Ports: []provider.PortSpec{
					{Container: 8080, Protocol: "TCP"},
				},
			},
			{
				Name:  "envoy",
				Image: "envoyproxy/envoy:v1.30",
				Role:  provider.RoleSidecar,
				Ports: []provider.PortSpec{
					{Container: 9901, Protocol: "TCP"},
				},
			},
		},
	}

	spec := buildPodSpec(req, nil)

	// Init goes to initContainers[]; Main + Sidecar to containers[].
	if len(spec.InitContainers) != 1 {
		t.Fatalf("init containers: want 1, got %d", len(spec.InitContainers))
	}

	if spec.InitContainers[0].Name != "init-db" {
		t.Fatalf("init container name: want init-db, got %q", spec.InitContainers[0].Name)
	}

	if len(spec.Containers) != 2 {
		t.Fatalf("containers: want 2 (main + sidecar), got %d", len(spec.Containers))
	}

	containerNames := []string{spec.Containers[0].Name, spec.Containers[1].Name}
	hasMain := containerNames[0] == "main" || containerNames[1] == "main"
	hasSidecar := containerNames[0] == "envoy" || containerNames[1] == "envoy"

	if !hasMain || !hasSidecar {
		t.Fatalf("containers: want [main, envoy], got %v", containerNames)
	}
}

// TestBuildDeployment_ReplicasFromMain verifies the Deployment's
// replica count comes from the Main service (non-Main services don't
// own a replica count of their own).
func TestBuildDeployment_ReplicasFromMain(t *testing.T) {
	t.Parallel()

	req := provider.ProvisionRequest{
		InstanceID: id.New(id.PrefixInstance),
		TenantID:   "ten_test",
		Kind:       provider.KindDeployment,
		Services: []provider.ServiceSpec{
			{
				Name:  "main",
				Image: "app:1",
				Role:  provider.RoleMain,
				Resources: provider.ResourceSpec{
					Replicas: 3,
				},
			},
		},
	}

	dep := buildDeployment(req, "default", map[string]string{}, nil)

	if dep.Spec.Replicas == nil {
		t.Fatalf("expected replicas pointer, got nil")
	}

	if *dep.Spec.Replicas != 3 {
		t.Fatalf("replicas: want 3, got %d", *dep.Spec.Replicas)
	}
}

// TestBuildStatefulSet_PVCTemplates verifies that volumes on a
// stateful_set workload turn into volumeClaimTemplates rather than
// pod-level emptyDir volumes.
func TestBuildStatefulSet_PVCTemplates(t *testing.T) {
	t.Parallel()

	req := provider.ProvisionRequest{
		InstanceID: id.New(id.PrefixInstance),
		TenantID:   "ten_test",
		Kind:       provider.KindStatefulSet,
		Services: []provider.ServiceSpec{
			{
				Name:  "main",
				Image: "postgres:16",
				Role:  provider.RoleMain,
				Volumes: []provider.VolumeSpec{
					{Name: "data", MountPath: "/var/lib/postgresql/data", SizeMB: 10240},
				},
			},
		},
	}

	ss := buildStatefulSet(req, "default", map[string]string{}, nil)

	if len(ss.Spec.VolumeClaimTemplates) != 1 {
		t.Fatalf("volume claim templates: want 1, got %d", len(ss.Spec.VolumeClaimTemplates))
	}

	if ss.Spec.VolumeClaimTemplates[0].Name != "data" {
		t.Fatalf("PVC name: want data, got %q", ss.Spec.VolumeClaimTemplates[0].Name)
	}

	// The same volume name must NOT appear at pod-level — k8s rejects
	// the spec when a Pod volume name collides with a claim template.
	for _, v := range ss.Spec.Template.Spec.Volumes {
		if v.Name == "data" {
			t.Fatalf("volume %q must not appear at pod level when PVC template covers it", v.Name)
		}
	}
}

// TestBuildService_HeadlessForStatefulSet verifies StatefulSet
// workloads get a headless Service (ClusterIP=None) for stable DNS.
func TestBuildService_HeadlessForStatefulSet(t *testing.T) {
	t.Parallel()

	req := provider.ProvisionRequest{
		InstanceID: id.New(id.PrefixInstance),
		TenantID:   "ten_test",
		Kind:       provider.KindStatefulSet,
		Services: []provider.ServiceSpec{
			{
				Name:  "main",
				Image: "postgres:16",
				Role:  provider.RoleMain,
				Ports: []provider.PortSpec{
					{Container: 5432, Protocol: "TCP"},
				},
			},
		},
	}

	svc := buildService(req, "default", map[string]string{}, true /*headless*/)
	if svc == nil {
		t.Fatalf("expected Service, got nil")
	}

	if svc.Spec.ClusterIP != corev1.ClusterIPNone {
		t.Fatalf("headless: want ClusterIP=None, got %q", svc.Spec.ClusterIP)
	}
}

// TestBuildConfigMaps_PerService verifies each service with env vars
// produces its own ConfigMap (so service A's env doesn't leak into
// service B's container via a shared map).
func TestBuildConfigMaps_PerService(t *testing.T) {
	t.Parallel()

	req := provider.ProvisionRequest{
		InstanceID: id.New(id.PrefixInstance),
		TenantID:   "ten_test",
		Services: []provider.ServiceSpec{
			{
				Name:  "main",
				Image: "app:1",
				Role:  provider.RoleMain,
				Env:   map[string]string{"DB_URL": "postgres://..."},
			},
			{
				Name:  "logger",
				Image: "fluent:1",
				Role:  provider.RoleSidecar,
				Env:   map[string]string{"LOG_DEST": "stdout"},
			},
			{
				// No env — should not produce a ConfigMap.
				Name:  "init",
				Image: "alpine:3",
				Role:  provider.RoleInit,
			},
		},
	}

	cms := buildConfigMaps(req, "default", map[string]string{})
	if len(cms) != 2 {
		t.Fatalf("config maps: want 2 (main + logger), got %d", len(cms))
	}
}

// TestBuildPodSpec_ImagePullSecrets verifies that imagePullSecrets are
// forwarded to the PodSpec so private registry images can be pulled.
func TestBuildPodSpec_ImagePullSecrets(t *testing.T) {
	t.Parallel()

	req := provider.ProvisionRequest{
		InstanceID: id.New(id.PrefixInstance),
		TenantID:   "ten_test",
		Services: []provider.ServiceSpec{
			{
				Name:  "main",
				Image: "ghcr.io/private/app:1.0",
				Role:  provider.RoleMain,
			},
		},
	}

	spec := buildPodSpec(req, []string{"ghcr-pull"})

	if len(spec.ImagePullSecrets) != 1 {
		t.Fatalf("imagePullSecrets: want 1, got %d", len(spec.ImagePullSecrets))
	}

	if spec.ImagePullSecrets[0].Name != "ghcr-pull" {
		t.Fatalf("imagePullSecrets[0].Name: want ghcr-pull, got %q", spec.ImagePullSecrets[0].Name)
	}
}

// TestBuildDeployment_ImagePullSecrets verifies that imagePullSecrets
// propagate through buildDeployment into the pod template spec.
func TestBuildDeployment_ImagePullSecrets(t *testing.T) {
	t.Parallel()

	req := provider.ProvisionRequest{
		InstanceID: id.New(id.PrefixInstance),
		TenantID:   "ten_test",
		Kind:       provider.KindDeployment,
		Services: []provider.ServiceSpec{
			{
				Name:  "main",
				Image: "ghcr.io/private/app:1.0",
				Role:  provider.RoleMain,
				Ports: []provider.PortSpec{
					{Container: 7903, Protocol: "TCP"},
				},
			},
		},
	}

	dep := buildDeployment(req, "default", map[string]string{}, []string{"ghcr-pull"})

	got := dep.Spec.Template.Spec.ImagePullSecrets
	if len(got) != 1 {
		t.Fatalf("deployment imagePullSecrets: want 1, got %d", len(got))
	}

	if got[0].Name != "ghcr-pull" {
		t.Fatalf("deployment imagePullSecrets[0].Name: want ghcr-pull, got %q", got[0].Name)
	}
}

// TestBuildEndpoints verifies that buildEndpoints produces the correct
// in-cluster DNS URL for each service with ports.
func TestBuildEndpoints(t *testing.T) {
	t.Parallel()

	req := provider.ProvisionRequest{
		InstanceID: id.New(id.PrefixInstance),
		TenantID:   "ten_test",
		Services: []provider.ServiceSpec{
			{
				Name:  "main",
				Image: "app:1",
				Role:  provider.RoleMain,
				Ports: []provider.PortSpec{
					{Container: 7903, Protocol: "TCP"},
				},
			},
			{
				// No ports — should not produce an endpoint.
				Name:  "init-db",
				Image: "alpine:3",
				Role:  provider.RoleInit,
			},
		},
	}

	endpoints := buildEndpoints(req, "staging")

	if len(endpoints) != 1 {
		t.Fatalf("endpoints: want 1 (main only), got %d", len(endpoints))
	}

	ep := endpoints[0]

	if ep.ServiceName != "main" {
		t.Fatalf("endpoint ServiceName: want main, got %q", ep.ServiceName)
	}

	if ep.Port != 7903 {
		t.Fatalf("endpoint Port: want 7903, got %d", ep.Port)
	}

	svcName := serviceName(req.InstanceID)
	wantURL := "http://" + svcName + ".staging.svc.cluster.local:7903"

	if ep.URL != wantURL {
		t.Fatalf("endpoint URL:\n  want %q\n   got %q", wantURL, ep.URL)
	}
}
