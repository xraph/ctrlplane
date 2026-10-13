package nomad

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// deployServer stands in for the Nomad API during a Deploy. It serves
// the job buildJob produces for req on GET and records the job body
// Deploy submits on POST /v1/jobs.
func deployServer(t *testing.T, req provider.ProvisionRequest) (*Provider, func() *nomadJob) {
	t.Helper()

	served, err := json.Marshal(buildJob(Config{}, req).Job)
	if err != nil {
		t.Fatal(err)
	}

	var submitted *nomadJobRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/job/"+jobName(req.InstanceID):
			_, _ = w.Write(served)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/jobs":
			submitted = &nomadJobRequest{}
			if err := json.NewDecoder(r.Body).Decode(submitted); err != nil {
				t.Errorf("decode submitted job: %v", err)
			}

			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	p := &Provider{cfg: Config{Address: srv.URL}, client: srv.Client()}

	return p, func() *nomadJob {
		t.Helper()

		if submitted == nil {
			t.Fatal("deploy did not submit a job")
		}

		return submitted.Job
	}
}

// deployWorkload is a three-service workload: an init, the main api
// and a proxy sidecar.
func deployWorkload() provider.ProvisionRequest {
	return provider.ProvisionRequest{
		InstanceID: id.New(id.PrefixInstance),
		Services: []provider.ServiceSpec{
			{Name: "migrate", Image: "m:v1", Role: provider.RoleInit},
			{
				Name:      "api",
				Image:     "app:v1",
				Role:      provider.RoleMain,
				Command:   []string{"/bin/old"},
				Args:      []string{"--old"},
				Ports:     []provider.PortSpec{{Container: 8080}},
				Resources: provider.ResourceSpec{CPUMillis: 250, MemoryMB: 128, DiskMB: 100, Replicas: 2},
			},
			{
				Name:      "proxy",
				Image:     "envoy:v1",
				Role:      provider.RoleSidecar,
				Ports:     []provider.PortSpec{{Container: 9901}},
				Resources: provider.ResourceSpec{CPUMillis: 100, MemoryMB: 64},
			},
		},
	}
}

func taskNamed(t *testing.T, job *nomadJob, name string) *nomadTask {
	t.Helper()

	for _, task := range job.TaskGroups[0].Tasks {
		if task.Name == name {
			return task
		}
	}

	t.Fatalf("no task %q", name)

	return nil
}

// stringList reads a list-valued driver option back from decoded JSON.
func stringList(v any) []string {
	items, _ := v.([]any)
	if items == nil {
		return nil
	}

	out := make([]string, len(items))
	for i, item := range items {
		out[i], _ = item.(string)
	}

	return out
}

func portsByLabel(n *nomadNetwork) map[string]nomadPortLabel {
	out := make(map[string]nomadPortLabel, len(n.DynamicPorts))
	for _, p := range n.DynamicPorts {
		out[p.Label] = *p
	}

	return out
}

func servicesByName(group *nomadTaskGroup) map[string]nomadService {
	out := make(map[string]nomadService, len(group.Services))
	for _, s := range group.Services {
		out[s.Name] = *s
	}

	return out
}

// TestDeploy_AppliesCommandArgsPortsResources checks that every optional
// deploy field reaches the submitted job, and that the other services
// and the group count stay as they were.
func TestDeploy_AppliesCommandArgsPortsResources(t *testing.T) {
	t.Parallel()

	req := deployWorkload()
	p, submitted := deployServer(t, req)

	_, err := p.Deploy(t.Context(), provider.DeployRequest{InstanceID: req.InstanceID, ReleaseID: id.New(id.PrefixRelease), Services: []provider.ServiceDeploySpec{{
		Name:      "api",
		Image:     "app:v2",
		Command:   []string{"/bin/new"},
		Args:      []string{"--port", "9090"},
		Ports:     []provider.PortSpec{{Container: 9090}, {Container: 9443, Host: 8443}},
		Resources: &provider.ResourceSpec{CPUMillis: 500, MemoryMB: 512, DiskMB: 200, Replicas: 5},
	}}})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}

	job := submitted()
	group := job.TaskGroups[0]
	api := taskNamed(t, job, "api")

	if api.Config["image"] != "app:v2" ||
		!reflect.DeepEqual(stringList(api.Config["entrypoint"]), []string{"/bin/new"}) ||
		!reflect.DeepEqual(stringList(api.Config["args"]), []string{"--port", "9090"}) ||
		!reflect.DeepEqual(stringList(api.Config["ports"]), []string{"api-0", "api-1"}) {
		t.Fatalf("api config=%v", api.Config)
	}

	if api.Resources == nil || api.Resources.CPU != 500 || api.Resources.MemoryMB != 512 || api.Resources.DiskMB != 200 {
		t.Fatalf("api resources=%+v", api.Resources)
	}

	wantPorts := map[string]nomadPortLabel{
		"api-0":   {Label: "api-0", To: 9090},
		"api-1":   {Label: "api-1", To: 9443, Value: 8443},
		"proxy-0": {Label: "proxy-0", To: 9901},
	}
	if got := portsByLabel(group.Networks[0]); !reflect.DeepEqual(got, wantPorts) {
		t.Fatalf("group ports=%+v", got)
	}

	services := servicesByName(group)
	if len(services) != 2 || services["api"].PortLabel != "api-0" || services["proxy"].PortLabel != "proxy-0" {
		t.Fatalf("consul services=%+v", services)
	}

	if group.Count != 2 {
		t.Errorf("count: deploy must leave it to Scale, got %d want 2", group.Count)
	}

	proxy := taskNamed(t, job, "proxy")
	if proxy.Config["image"] != "envoy:v1" || proxy.Resources.CPU != 100 || proxy.Resources.MemoryMB != 64 ||
		!reflect.DeepEqual(stringList(proxy.Config["ports"]), []string{"proxy-0"}) {
		t.Fatalf("untargeted proxy changed: config=%v resources=%+v", proxy.Config, proxy.Resources)
	}
}

// TestDeploy_NilFieldsKeepRunningSpec checks that a deploy without the
// optional fields submits the fetched job with only the image changed.
func TestDeploy_NilFieldsKeepRunningSpec(t *testing.T) {
	t.Parallel()

	req := deployWorkload()
	p, submitted := deployServer(t, req)

	_, err := p.Deploy(t.Context(), provider.DeployRequest{InstanceID: req.InstanceID, ReleaseID: id.New(id.PrefixRelease), Services: []provider.ServiceDeploySpec{{Name: "api", Image: "app:v2"}}})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}

	// Round-trip the expected job through JSON so both sides decode the
	// same way, then apply the one change a plain deploy makes.
	raw, err := json.Marshal(buildJob(Config{}, req).Job)
	if err != nil {
		t.Fatal(err)
	}

	var want nomadJob
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}

	taskNamed(t, &want, "api").Config["image"] = "app:v2"

	got := submitted()
	if !reflect.DeepEqual(got.TaskGroups, want.TaskGroups) {
		t.Fatalf("task groups:\n got %s\nwant %s", mustJSON(t, got.TaskGroups), mustJSON(t, want.TaskGroups))
	}
}

// TestDeploy_EmptyListsClearOverrides checks that empty command, args
// and ports drop the driver options, the service's group ports and its
// Consul entry, which is what buildJob emits for a service without them.
func TestDeploy_EmptyListsClearOverrides(t *testing.T) {
	t.Parallel()

	req := deployWorkload()
	p, submitted := deployServer(t, req)

	_, err := p.Deploy(t.Context(), provider.DeployRequest{InstanceID: req.InstanceID, ReleaseID: id.New(id.PrefixRelease), Services: []provider.ServiceDeploySpec{{
		Name: "api", Image: "app:v2", Command: []string{}, Args: []string{}, Ports: []provider.PortSpec{},
	}}})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}

	job := submitted()
	api := taskNamed(t, job, "api")

	for _, key := range []string{"entrypoint", "args", "ports"} {
		if _, ok := api.Config[key]; ok {
			t.Errorf("api config still has %q: %v", key, api.Config[key])
		}
	}

	group := job.TaskGroups[0]
	if got := portsByLabel(group.Networks[0]); len(got) != 1 || got["proxy-0"].To != 9901 {
		t.Fatalf("group ports=%+v", got)
	}

	if services := servicesByName(group); len(services) != 1 || services["proxy"].PortLabel != "proxy-0" {
		t.Fatalf("consul services=%+v", services)
	}
}

// TestDeploy_NewPortsRegisterOnlyLongLivedServices checks that ports
// added to a sidecar register it with Consul, and ports added to an
// init task do not, matching buildJob.
func TestDeploy_NewPortsRegisterOnlyLongLivedServices(t *testing.T) {
	t.Parallel()

	req := deployWorkload()
	req.Services[2].Ports = nil // proxy starts without ports
	p, submitted := deployServer(t, req)

	_, err := p.Deploy(t.Context(), provider.DeployRequest{InstanceID: req.InstanceID, ReleaseID: id.New(id.PrefixRelease), Services: []provider.ServiceDeploySpec{
		{Name: "proxy", Image: "envoy:v2", Ports: []provider.PortSpec{{Container: 9901}}},
		{Name: "migrate", Image: "m:v2", Ports: []provider.PortSpec{{Container: 7000}}},
	}})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}

	group := submitted().TaskGroups[0]

	services := servicesByName(group)
	if _, ok := services["migrate"]; ok {
		t.Errorf("init task registered with consul: %+v", services)
	}

	proxy, ok := services["proxy"]
	if !ok || proxy.PortLabel != "proxy-0" || !reflect.DeepEqual(proxy.Tags, []string{"ctrlplane", "sidecar"}) {
		t.Errorf("proxy registration=%+v ok=%v", proxy, ok)
	}

	ports := portsByLabel(group.Networks[0])
	if ports["proxy-0"].To != 9901 || ports["migrate-0"].To != 7000 || ports["api-0"].To != 8080 {
		t.Fatalf("group ports=%+v", ports)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	return data
}

func TestIsPortLabelOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		label, service string
		want           bool
	}{
		{"api-0", "api", true},
		{"api-12", "api", true},
		{"api-gateway-0", "api", false},
		{"api-1-0", "api", false},
		{"api-1-0", "api-1", true},
		{"api-", "api", false},
		{"api-+1", "api", false},
		{"proxy-0", "api", false},
	}

	for _, tt := range tests {
		if got := isPortLabelOf(tt.label, tt.service); got != tt.want {
			t.Errorf("isPortLabelOf(%q, %q) = %v, want %v", tt.label, tt.service, got, tt.want)
		}
	}
}

// TestDeploy_LeavesEndpointsUnchanged checks that a deploy which sets
// ports still returns nil endpoints. Provision reports none for a Nomad
// job, so the instance's stored endpoints stay as they were.
func TestDeploy_LeavesEndpointsUnchanged(t *testing.T) {
	t.Parallel()

	req := deployWorkload()
	p, submitted := deployServer(t, req)

	result, err := p.Deploy(t.Context(), provider.DeployRequest{InstanceID: req.InstanceID, ReleaseID: id.New(id.PrefixRelease), Services: []provider.ServiceDeploySpec{{
		Name: "api", Image: "app:v2", Ports: []provider.PortSpec{{Container: 9090}},
	}}})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}

	if ports := portsByLabel(submitted().TaskGroups[0].Networks[0]); ports["api-0"].To != 9090 {
		t.Fatalf("ports not applied: %+v", ports)
	}

	if result.Endpoints != nil {
		t.Fatalf("endpoints = %+v, want nil", result.Endpoints)
	}
}
