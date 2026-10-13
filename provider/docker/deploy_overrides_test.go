package docker

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// deployFake stands in for the docker daemon during a Deploy. It lists
// one container for service "api" with the given role, answers inspect
// with inspectJSON, and records every call plus the create body.
type deployFake struct {
	calls   []string
	created *container.CreateRequest
}

func newDeployFake(t *testing.T, role provider.ServiceRole, inspectJSON string) (*Provider, *deployFake) {
	t.Helper()

	fake := &deployFake{}

	p := compatProvider(t, func(r *http.Request) (*http.Response, error) {
		fake.calls = append(fake.calls, r.Method+" "+r.URL.Path)
		code, data := http.StatusNoContent, ""

		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			code = http.StatusOK
			data = `[{"Id":"api-old","Names":["/api"],"Labels":{"ctrlplane.service":"api","ctrlplane.role":"` + string(role) + `"}}]`
		case strings.HasSuffix(r.URL.Path, "/api-old/json"):
			code = http.StatusOK
			data = inspectJSON
		case strings.HasSuffix(r.URL.Path, "/images/create"):
			code = http.StatusOK
			data = `{"status":"pulled"}`
		case strings.HasSuffix(r.URL.Path, "/containers/create"):
			var created container.CreateRequest
			if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
				t.Fatal(err)
			}

			fake.created = &created
			code = http.StatusCreated
			data = `{"Id":"api-new"}`
		}

		resp, _ := compatResponse(r, code, data)

		return resp, nil
	})

	return p, fake
}

func (f *deployFake) recreated() bool {
	for _, call := range f.calls {
		if strings.HasSuffix(call, "/containers/create") || strings.Contains(call, "DELETE") {
			return true
		}
	}

	return false
}

const runningAPI = `{"Config":{"Image":"app:v1","Env":["KEEP=yes"],"Labels":{"ctrlplane.service":"api"},` +
	`"ExposedPorts":{"8080/tcp":{}},"Entrypoint":["/bin/old"],"Cmd":["--old"]},` +
	`"HostConfig":{"Binds":["volume:/data"],"NetworkMode":"project-net",` +
	`"PortBindings":{"8080/tcp":[{"HostIp":"0.0.0.0","HostPort":""}]},` +
	`"NanoCpus":250000000,"Memory":134217728,"MemorySwap":268435456}}`

// TestDeploy_AppliesCommandArgsPortsResources checks that every optional
// deploy field reaches the container create call, and that the rest of
// the running spec is carried over.
func TestDeploy_AppliesCommandArgsPortsResources(t *testing.T) {
	p, fake := newDeployFake(t, provider.RoleMain, runningAPI)

	_, err := p.Deploy(t.Context(), provider.DeployRequest{InstanceID: id.New(id.PrefixInstance), Services: []provider.ServiceDeploySpec{{
		Name:      "api",
		Image:     "app:v2",
		Command:   []string{"/bin/new"},
		Args:      []string{"--port", "9090"},
		Ports:     []provider.PortSpec{{Container: 9090}, {Container: 53, Host: 5353, Protocol: "udp"}},
		Resources: &provider.ResourceSpec{CPUMillis: 500, MemoryMB: 512, Replicas: 3},
	}}})
	if err != nil {
		t.Fatal(err)
	}

	got := fake.created
	if got == nil {
		t.Fatal("deploy did not create a container")
	}

	if !reflect.DeepEqual(got.Entrypoint, []string{"/bin/new"}) || !reflect.DeepEqual(got.Cmd, []string{"--port", "9090"}) {
		t.Fatalf("entrypoint=%v cmd=%v", got.Entrypoint, got.Cmd)
	}

	wantExposed := network.PortSet{network.MustParsePort("9090/tcp"): {}, network.MustParsePort("53/udp"): {}}
	if !reflect.DeepEqual(got.ExposedPorts, wantExposed) {
		t.Fatalf("exposed=%v", got.ExposedPorts)
	}

	bindings := got.HostConfig.PortBindings
	if len(bindings) != 2 || bindings[network.MustParsePort("53/udp")][0].HostPort != "5353" || bindings[network.MustParsePort("9090/tcp")][0].HostPort != "" {
		t.Fatalf("bindings=%v", bindings)
	}

	if got.HostConfig.NanoCPUs != 500_000_000 || got.HostConfig.Memory != 512*1024*1024 || got.HostConfig.MemorySwap != 0 {
		t.Fatalf("limits nano=%d mem=%d swap=%d", got.HostConfig.NanoCPUs, got.HostConfig.Memory, got.HostConfig.MemorySwap)
	}

	if got.Image != "app:v2" || !reflect.DeepEqual(got.Env, []string{"KEEP=yes"}) || !reflect.DeepEqual(got.HostConfig.Binds, []string{"volume:/data"}) {
		t.Fatalf("carried config lost: image=%s env=%v binds=%v", got.Image, got.Env, got.HostConfig.Binds)
	}
}

// TestDeploy_NilFieldsKeepRunningSpec checks that a deploy without the
// optional fields recreates the container exactly as it runs today,
// limits and swap included.
func TestDeploy_NilFieldsKeepRunningSpec(t *testing.T) {
	p, fake := newDeployFake(t, provider.RoleMain, runningAPI)

	_, err := p.Deploy(t.Context(), provider.DeployRequest{InstanceID: id.New(id.PrefixInstance), Services: []provider.ServiceDeploySpec{{Name: "api", Image: "app:v2"}}})
	if err != nil {
		t.Fatal(err)
	}

	got := fake.created
	if got == nil {
		t.Fatal("new image did not recreate the container")
	}

	if !reflect.DeepEqual(got.Entrypoint, []string{"/bin/old"}) || !reflect.DeepEqual(got.Cmd, []string{"--old"}) {
		t.Fatalf("entrypoint=%v cmd=%v", got.Entrypoint, got.Cmd)
	}

	if !reflect.DeepEqual(got.ExposedPorts, network.PortSet{network.MustParsePort("8080/tcp"): {}}) || len(got.HostConfig.PortBindings) != 1 {
		t.Fatalf("ports exposed=%v bindings=%v", got.ExposedPorts, got.HostConfig.PortBindings)
	}

	if got.HostConfig.NanoCPUs != 250_000_000 || got.HostConfig.Memory != 128*1024*1024 || got.HostConfig.MemorySwap != 256*1024*1024 {
		t.Fatalf("limits nano=%d mem=%d swap=%d", got.HostConfig.NanoCPUs, got.HostConfig.Memory, got.HostConfig.MemorySwap)
	}
}

// TestDeploy_SameImageSkipsUnlessFieldsChange covers the same-image
// shortcut: it still skips when nothing changes, and recreates when a
// set field differs from the running container.
func TestDeploy_SameImageSkipsUnlessFieldsChange(t *testing.T) {
	tests := []struct {
		name     string
		spec     provider.ServiceDeploySpec
		recreate bool
	}{
		{name: "image only", spec: provider.ServiceDeploySpec{Name: "api", Image: "app:v1"}},
		{name: "fields match running", spec: provider.ServiceDeploySpec{
			Name: "api", Image: "app:v1",
			Command:   []string{"/bin/old"},
			Args:      []string{"--old"},
			Ports:     []provider.PortSpec{{Container: 8080}},
			Resources: &provider.ResourceSpec{CPUMillis: 250, MemoryMB: 128},
		}},
		{name: "command differs", spec: provider.ServiceDeploySpec{Name: "api", Image: "app:v1", Command: []string{"/bin/new"}}, recreate: true},
		{name: "args differ", spec: provider.ServiceDeploySpec{Name: "api", Image: "app:v1", Args: []string{"--new"}}, recreate: true},
		{name: "ports differ", spec: provider.ServiceDeploySpec{Name: "api", Image: "app:v1", Ports: []provider.PortSpec{{Container: 8080, Host: 9000}}}, recreate: true},
		{name: "resources differ", spec: provider.ServiceDeploySpec{Name: "api", Image: "app:v1", Resources: &provider.ResourceSpec{CPUMillis: 250, MemoryMB: 256}}, recreate: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, fake := newDeployFake(t, provider.RoleMain, runningAPI)

			if _, err := p.Deploy(t.Context(), provider.DeployRequest{InstanceID: id.New(id.PrefixInstance), Services: []provider.ServiceDeploySpec{tt.spec}}); err != nil {
				t.Fatal(err)
			}

			if got := fake.recreated(); got != tt.recreate {
				t.Fatalf("recreated=%v want %v; calls=%v", got, tt.recreate, fake.calls)
			}
		})
	}
}

// TestDeploy_InitIgnoresPorts checks that an init container keeps its
// no-ports shape even when the deploy sets ports, as Provision does.
func TestDeploy_InitIgnoresPorts(t *testing.T) {
	inspect := `{"Config":{"Image":"migrate:v1","Labels":{"ctrlplane.service":"api"}},"HostConfig":{"NetworkMode":"project-net"}}`
	p, fake := newDeployFake(t, provider.RoleInit, inspect)

	_, err := p.Deploy(t.Context(), provider.DeployRequest{InstanceID: id.New(id.PrefixInstance), Services: []provider.ServiceDeploySpec{{
		Name: "api", Image: "migrate:v2", Args: []string{"up"}, Ports: []provider.PortSpec{{Container: 8080}},
	}}})
	if err != nil {
		t.Fatal(err)
	}

	got := fake.created
	if got == nil {
		t.Fatal("deploy did not create a container")
	}

	if len(got.ExposedPorts) != 0 || len(got.HostConfig.PortBindings) != 0 {
		t.Fatalf("init got ports: exposed=%v bindings=%v", got.ExposedPorts, got.HostConfig.PortBindings)
	}

	if !reflect.DeepEqual(got.Cmd, []string{"up"}) {
		t.Fatalf("cmd=%v", got.Cmd)
	}
}

// TestDeploy_RejectsBadPort checks that an invalid port fails the deploy
// before the running container is removed.
func TestDeploy_RejectsBadPort(t *testing.T) {
	p, fake := newDeployFake(t, provider.RoleMain, runningAPI)

	_, err := p.Deploy(t.Context(), provider.DeployRequest{InstanceID: id.New(id.PrefixInstance), Services: []provider.ServiceDeploySpec{{
		Name: "api", Image: "app:v2", Ports: []provider.PortSpec{{Container: 70000}},
	}}})
	if err == nil {
		t.Fatal("want error for out-of-range port")
	}

	if fake.recreated() {
		t.Fatalf("running container touched: %v", fake.calls)
	}
}
