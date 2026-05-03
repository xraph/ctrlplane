package docker

import (
	"testing"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// TestProjectNaming verifies the project / network / container name
// scheme is stable and predictable from an instance ID.
func TestProjectNaming(t *testing.T) {
	t.Parallel()

	iid := id.New(id.PrefixInstance)

	if got, want := projectName(iid), "cp-"+iid.String(); got != want {
		t.Fatalf("projectName: want %q, got %q", want, got)
	}

	if got, want := projectNetwork(iid), "cp-"+iid.String()+"-net"; got != want {
		t.Fatalf("projectNetwork: want %q, got %q", want, got)
	}

	if got, want := serviceContainerName(iid, "api"), "cp-"+iid.String()+"-api"; got != want {
		t.Fatalf("serviceContainerName: want %q, got %q", want, got)
	}
}

// TestClassifyServices_GroupsByRole verifies init/main/sidecar
// classification.
func TestClassifyServices_GroupsByRole(t *testing.T) {
	t.Parallel()

	services := []provider.ServiceSpec{
		{Name: "init-db", Role: provider.RoleInit},
		{Name: "main", Role: provider.RoleMain},
		{Name: "envoy", Role: provider.RoleSidecar},
		{Name: "init-fixtures", Role: provider.RoleInit},
	}

	inits, mains, sidecars := classifyServices(services)

	if len(inits) != 2 {
		t.Fatalf("inits: want 2, got %d", len(inits))
	}

	if len(mains) != 1 || mains[0].Name != "main" {
		t.Fatalf("mains: want [main], got %+v", mains)
	}

	if len(sidecars) != 1 || sidecars[0].Name != "envoy" {
		t.Fatalf("sidecars: want [envoy], got %+v", sidecars)
	}
}

// TestClassifyServices_DefaultRoleIsMain verifies a service with no
// Role set is treated as Main (matches single-service workload
// back-compat behaviour).
func TestClassifyServices_DefaultRoleIsMain(t *testing.T) {
	t.Parallel()

	services := []provider.ServiceSpec{
		{Name: "main"}, // no Role
	}

	_, mains, _ := classifyServices(services)
	if len(mains) != 1 {
		t.Fatalf("default-role service should classify as Main, got %+v", mains)
	}
}

// TestSortByDeps_OrdersDependenciesFirst verifies a service that
// declares DependsOn comes after its dependency in the sorted slice.
func TestSortByDeps_OrdersDependenciesFirst(t *testing.T) {
	t.Parallel()

	services := []provider.ServiceSpec{
		{Name: "second", DependsOn: []string{"first"}},
		{Name: "first"},
	}

	sortByDeps(services)

	if services[0].Name != "first" || services[1].Name != "second" {
		t.Fatalf("dep ordering: want [first, second], got [%s, %s]", services[0].Name, services[1].Name)
	}
}

// TestSortByDeps_StableForUnrelatedServices verifies services with no
// declared relationship preserve declaration order.
func TestSortByDeps_StableForUnrelatedServices(t *testing.T) {
	t.Parallel()

	services := []provider.ServiceSpec{
		{Name: "a"},
		{Name: "b"},
		{Name: "c"},
	}

	sortByDeps(services)

	for i, want := range []string{"a", "b", "c"} {
		if services[i].Name != want {
			t.Fatalf("services[%d]: want %s, got %s", i, want, services[i].Name)
		}
	}
}

// TestProjectLabels_StampsRequiredKeys verifies the labels every
// project member carries.
func TestProjectLabels_StampsRequiredKeys(t *testing.T) {
	t.Parallel()

	iid := id.New(id.PrefixInstance)
	labels := projectLabels(iid, "ten_xyz", "api", provider.RoleMain, map[string]string{"env": "prod"})

	if labels["ctrlplane.instance"] != iid.String() {
		t.Fatalf("instance label: %+v", labels)
	}

	if labels["ctrlplane.project"] != projectName(iid) {
		t.Fatalf("project label: %+v", labels)
	}

	if labels["ctrlplane.tenant"] != "ten_xyz" {
		t.Fatalf("tenant label: %+v", labels)
	}

	if labels["ctrlplane.service"] != "api" {
		t.Fatalf("service label: %+v", labels)
	}

	if labels["ctrlplane.role"] != "main" {
		t.Fatalf("role label: %+v", labels)
	}

	if labels["env"] != "prod" {
		t.Fatalf("extra label dropped: %+v", labels)
	}
}

// TestBuildServiceContainerConfig_InitOverrides verifies Init
// containers don't get host port bindings or restart policies — they
// run-once and exit.
func TestBuildServiceContainerConfig_InitOverrides(t *testing.T) {
	t.Parallel()

	p := &Provider{}

	req := provider.ProvisionRequest{
		InstanceID: id.New(id.PrefixInstance),
		TenantID:   "ten_t",
	}

	svc := provider.ServiceSpec{
		Name:  "init",
		Image: "alpine:3",
		Role:  provider.RoleInit,
		Ports: []provider.PortSpec{{Container: 8080, Host: 8080, Protocol: "tcp"}},
	}

	cfg, hostCfg, _, err := p.buildServiceContainerConfig(req, svc)
	if err != nil {
		t.Fatalf("buildServiceContainerConfig: %v", err)
	}

	if len(cfg.ExposedPorts) != 0 {
		t.Fatalf("init: expected no ExposedPorts, got %+v", cfg.ExposedPorts)
	}

	if hostCfg.PortBindings != nil {
		t.Fatalf("init: expected no PortBindings, got %+v", hostCfg.PortBindings)
	}

	if string(hostCfg.RestartPolicy.Name) != "no" {
		t.Fatalf("init: expected restart policy 'no', got %q", hostCfg.RestartPolicy.Name)
	}
}

// TestBuildServiceContainerConfig_MainServiceWiring verifies a
// Main/Sidecar container gets the full network-alias + restart-
// policy wiring.
func TestBuildServiceContainerConfig_MainServiceWiring(t *testing.T) {
	t.Parallel()

	p := &Provider{}
	iid := id.New(id.PrefixInstance)

	req := provider.ProvisionRequest{
		InstanceID: iid,
		TenantID:   "ten_t",
	}

	svc := provider.ServiceSpec{
		Name:    "api",
		Image:   "myapi:1.0",
		Role:    provider.RoleMain,
		Ports:   []provider.PortSpec{{Container: 8080, Protocol: "tcp"}},
		Env:     map[string]string{"LOG_LEVEL": "info"},
		Command: []string{"/usr/local/bin/api"},
		Args:    []string{"--port=8080"},
	}

	cfg, hostCfg, netCfg, err := p.buildServiceContainerConfig(req, svc)
	if err != nil {
		t.Fatalf("buildServiceContainerConfig: %v", err)
	}

	if cfg.Image != "myapi:1.0" {
		t.Fatalf("image: want myapi:1.0, got %q", cfg.Image)
	}

	if len(cfg.Entrypoint) == 0 || cfg.Entrypoint[0] != "/usr/local/bin/api" {
		t.Fatalf("entrypoint: %+v", cfg.Entrypoint)
	}

	if len(cfg.Cmd) == 0 || cfg.Cmd[0] != "--port=8080" {
		t.Fatalf("cmd: %+v", cfg.Cmd)
	}

	if hostCfg.PortBindings == nil {
		t.Fatalf("Main: expected port bindings, got nil")
	}

	if string(hostCfg.RestartPolicy.Name) != "unless-stopped" {
		t.Fatalf("Main: expected restart policy 'unless-stopped', got %q", hostCfg.RestartPolicy.Name)
	}

	netName := projectNetwork(iid)

	endpoints, ok := netCfg.EndpointsConfig[netName]
	if !ok {
		t.Fatalf("expected network %q in EndpointsConfig: %+v", netName, netCfg.EndpointsConfig)
	}

	if len(endpoints.Aliases) == 0 || endpoints.Aliases[0] != "api" {
		t.Fatalf("expected alias 'api' on the project network, got %+v", endpoints.Aliases)
	}
}

// TestPickMainService_PrefersExplicitRole verifies that even when a
// Sidecar appears first in the slice the Main is picked.
func TestPickMainService_PrefersExplicitRole(t *testing.T) {
	t.Parallel()

	services := []provider.ServiceSpec{
		{Name: "logger", Role: provider.RoleSidecar},
		{Name: "api", Role: provider.RoleMain},
	}

	main := pickMainService(services)
	if main == nil || main.Name != "api" {
		t.Fatalf("want main=api, got %+v", main)
	}
}

// TestPickMainService_NilForNoMain verifies callers can detect a
// services slice with no Main service.
func TestPickMainService_NilForNoMain(t *testing.T) {
	t.Parallel()

	services := []provider.ServiceSpec{
		{Name: "init", Role: provider.RoleInit},
		{Name: "logger", Role: provider.RoleSidecar},
	}

	if main := pickMainService(services); main != nil {
		t.Fatalf("want nil, got %+v", main)
	}
}
