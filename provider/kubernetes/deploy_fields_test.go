package kubernetes

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

const deployNS = "default"

// provisionForDeploy provisions a two-service workload (api + proxy
// sidecar) against a fake clientset and returns the provider, so Deploy
// runs against the objects Provision really creates.
func provisionForDeploy(t *testing.T, kind provider.WorkloadKind, apiPorts []provider.PortSpec) (*Provider, *k8sfake.Clientset, id.ID) {
	t.Helper()

	cs := k8sfake.NewSimpleClientset()
	p := &Provider{cfg: Config{Namespace: deployNS}, client: cs}
	instID := id.New(id.PrefixInstance)

	_, err := p.Provision(t.Context(), provider.ProvisionRequest{
		InstanceID: instID,
		TenantID:   "ten_test",
		Kind:       kind,
		Services: []provider.ServiceSpec{
			{
				Name:      "api",
				Image:     "app:v1",
				Role:      provider.RoleMain,
				Command:   []string{"/bin/old"},
				Args:      []string{"--old"},
				Ports:     apiPorts,
				Resources: provider.ResourceSpec{CPUMillis: 250, MemoryMB: 128, Replicas: 2},
			},
			{
				Name:      "proxy",
				Image:     "envoy:v1",
				Role:      provider.RoleSidecar,
				Args:      []string{"--proxy"},
				Ports:     []provider.PortSpec{{Container: 9901}},
				Resources: provider.ResourceSpec{CPUMillis: 100, MemoryMB: 64},
			},
		},
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	cs.ClearActions()

	return p, cs, instID
}

func podSpecOf(t *testing.T, p *Provider, instID id.ID) (*corev1.PodSpec, int32) {
	t.Helper()

	name := deploymentName(instID)

	if dep, err := p.client.AppsV1().Deployments(deployNS).Get(t.Context(), name, metav1.GetOptions{}); err == nil {
		return &dep.Spec.Template.Spec, *dep.Spec.Replicas
	}

	ss, err := p.client.AppsV1().StatefulSets(deployNS).Get(t.Context(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get workload: %v", err)
	}

	return &ss.Spec.Template.Spec, *ss.Spec.Replicas
}

func containerNamed(t *testing.T, spec *corev1.PodSpec, name string) corev1.Container {
	t.Helper()

	for _, c := range spec.Containers {
		if c.Name == name {
			return c
		}
	}

	t.Fatalf("no container %q", name)

	return corev1.Container{}
}

func assertResources(t *testing.T, c corev1.Container, cpuMilli, memMB int64) {
	t.Helper()

	for kind, list := range map[string]corev1.ResourceList{"request": c.Resources.Requests, "limit": c.Resources.Limits} {
		if got := list.Cpu().MilliValue(); got != cpuMilli {
			t.Errorf("%s %s cpu: got %dm, want %dm", c.Name, kind, got, cpuMilli)
		}

		if got := list.Memory().Value() / (1024 * 1024); got != memMB {
			t.Errorf("%s %s memory: got %dMi, want %dMi", c.Name, kind, got, memMB)
		}
	}
}

// TestDeploy_AppliesCommandArgsPortsResources checks that every optional
// field on a deploy reaches the pod template and the instance Service,
// and that the untargeted sidecar and the replica count stay put.
func TestDeploy_AppliesCommandArgsPortsResources(t *testing.T) {
	p, _, instID := provisionForDeploy(t, provider.KindDeployment, []provider.PortSpec{{Container: 8080}})

	before, err := p.client.CoreV1().Services(deployNS).Get(t.Context(), serviceName(instID), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get service: %v", err)
	}

	_, err = p.Deploy(t.Context(), provider.DeployRequest{InstanceID: instID, ReleaseID: id.New(id.PrefixRelease), Services: []provider.ServiceDeploySpec{{
		Name:      "api",
		Image:     "app:v2",
		Command:   []string{"/bin/new"},
		Args:      []string{"--port", "9090"},
		Ports:     []provider.PortSpec{{Container: 9090}, {Container: 53, Protocol: "udp"}},
		Resources: &provider.ResourceSpec{CPUMillis: 500, MemoryMB: 512, Replicas: 5},
	}}})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}

	spec, replicas := podSpecOf(t, p, instID)
	api := containerNamed(t, spec, "api")

	if api.Image != "app:v2" || !reflect.DeepEqual(api.Command, []string{"/bin/new"}) || !reflect.DeepEqual(api.Args, []string{"--port", "9090"}) {
		t.Fatalf("api image=%s command=%v args=%v", api.Image, api.Command, api.Args)
	}

	wantPorts := []corev1.ContainerPort{
		{Name: "port-0", ContainerPort: 9090, Protocol: corev1.ProtocolTCP},
		{Name: "port-1", ContainerPort: 53, Protocol: corev1.ProtocolUDP},
	}
	if !reflect.DeepEqual(api.Ports, wantPorts) {
		t.Fatalf("api ports=%+v", api.Ports)
	}

	assertResources(t, api, 500, 512)

	if replicas != 2 {
		t.Errorf("replicas: deploy must leave them to Scale, got %d want 2", replicas)
	}

	proxy := containerNamed(t, spec, "proxy")
	if proxy.Image != "envoy:v1" || !reflect.DeepEqual(proxy.Args, []string{"--proxy"}) || len(proxy.Ports) != 1 {
		t.Fatalf("untargeted sidecar changed: %+v", proxy)
	}

	assertResources(t, proxy, 100, 64)

	svc, err := p.client.CoreV1().Services(deployNS).Get(t.Context(), serviceName(instID), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get service: %v", err)
	}

	wantSvcPorts := []corev1.ServicePort{
		{Name: "api-0", Port: 9090, TargetPort: intstr.FromInt32(9090), Protocol: corev1.ProtocolTCP},
		{Name: "api-1", Port: 53, TargetPort: intstr.FromInt32(53), Protocol: corev1.ProtocolUDP},
		{Name: "proxy-0", Port: 9901, TargetPort: intstr.FromInt32(9901), Protocol: corev1.ProtocolTCP},
	}
	if !reflect.DeepEqual(svc.Spec.Ports, wantSvcPorts) {
		t.Fatalf("service ports=%+v", svc.Spec.Ports)
	}

	if !reflect.DeepEqual(svc.Spec.Selector, before.Spec.Selector) || !reflect.DeepEqual(svc.Labels, before.Labels) {
		t.Fatalf("service selector or labels changed: %v %v", svc.Spec.Selector, svc.Labels)
	}
}

// TestDeploy_NilFieldsKeepRunningSpec checks that a deploy without the
// optional fields changes only the image, and never touches the Service.
func TestDeploy_NilFieldsKeepRunningSpec(t *testing.T) {
	p, cs, instID := provisionForDeploy(t, provider.KindDeployment, []provider.PortSpec{{Container: 8080}})

	before, _ := podSpecOf(t, p, instID)
	want := containerNamed(t, before, "api")
	want.Image = "app:v2"

	cs.ClearActions()

	_, err := p.Deploy(t.Context(), provider.DeployRequest{InstanceID: instID, ReleaseID: id.New(id.PrefixRelease), Services: []provider.ServiceDeploySpec{{Name: "api", Image: "app:v2"}}})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}

	after, _ := podSpecOf(t, p, instID)
	if got := containerNamed(t, after, "api"); !reflect.DeepEqual(got, want) {
		t.Fatalf("api container:\n got %+v\nwant %+v", got, want)
	}

	for _, action := range cs.Actions() {
		if action.GetResource().Resource == "services" {
			t.Fatalf("deploy without ports touched the service: %s", action.GetVerb())
		}
	}
}

// TestDeploy_StatefulSetCreatesHeadlessService checks that ports added
// on deploy to a workload provisioned without any get the same headless
// Service Provision would have created.
func TestDeploy_StatefulSetCreatesHeadlessService(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	p := &Provider{cfg: Config{Namespace: deployNS}, client: cs}
	instID := id.New(id.PrefixInstance)

	_, err := p.Provision(t.Context(), provider.ProvisionRequest{
		InstanceID: instID,
		TenantID:   "ten_test",
		Kind:       provider.KindStatefulSet,
		Services:   []provider.ServiceSpec{{Name: "db", Image: "postgres:16", Role: provider.RoleMain}},
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	if _, err := cs.CoreV1().Services(deployNS).Get(t.Context(), serviceName(instID), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("precondition: want no service, got err=%v", err)
	}

	_, err = p.Deploy(t.Context(), provider.DeployRequest{InstanceID: instID, ReleaseID: id.New(id.PrefixRelease), Services: []provider.ServiceDeploySpec{{
		Name: "db", Image: "postgres:17", Ports: []provider.PortSpec{{Container: 5432}},
	}}})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}

	svc, err := cs.CoreV1().Services(deployNS).Get(t.Context(), serviceName(instID), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get service: %v", err)
	}

	if svc.Spec.ClusterIP != corev1.ClusterIPNone {
		t.Errorf("statefulset service must be headless, got ClusterIP=%q", svc.Spec.ClusterIP)
	}

	if svc.Spec.Selector[labelInstanceID] != instID.String() || svc.Labels[labelInstanceID] != instID.String() {
		t.Errorf("selector=%v labels=%v", svc.Spec.Selector, svc.Labels)
	}

	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Name != "db-0" || svc.Spec.Ports[0].Port != 5432 {
		t.Errorf("ports=%+v", svc.Spec.Ports)
	}
}

// TestDeploy_EmptyPortsDeletesService checks that clearing every port
// removes the Service, matching Provision, which creates none without
// ports.
func TestDeploy_EmptyPortsDeletesService(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	p := &Provider{cfg: Config{Namespace: deployNS}, client: cs}
	instID := id.New(id.PrefixInstance)

	_, err := p.Provision(t.Context(), provider.ProvisionRequest{
		InstanceID: instID,
		TenantID:   "ten_test",
		Services:   []provider.ServiceSpec{{Name: "worker", Image: "w:v1", Ports: []provider.PortSpec{{Container: 8080}}}},
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	_, err = p.Deploy(t.Context(), provider.DeployRequest{InstanceID: instID, ReleaseID: id.New(id.PrefixRelease), Services: []provider.ServiceDeploySpec{{
		Name: "worker", Image: "w:v2", Ports: []provider.PortSpec{},
	}}})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}

	if _, err := cs.CoreV1().Services(deployNS).Get(t.Context(), serviceName(instID), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("want service deleted, got err=%v", err)
	}

	spec, _ := podSpecOf(t, p, instID)
	if ports := containerNamed(t, spec, "worker").Ports; len(ports) != 0 {
		t.Fatalf("container ports=%+v", ports)
	}
}

// TestServicePortsFor_MatchesProvision checks that rebuilding Service
// ports from a provisioned pod template gives the ports Provision chose.
func TestServicePortsFor_MatchesProvision(t *testing.T) {
	t.Parallel()

	req := provider.ProvisionRequest{
		InstanceID: id.New(id.PrefixInstance),
		Services: []provider.ServiceSpec{
			{Name: "migrate", Image: "m:1", Role: provider.RoleInit},
			{Name: "api", Image: "a:1", Ports: []provider.PortSpec{{Container: 8080}, {Container: 8443}}},
			{Name: "proxy", Image: "p:1", Role: provider.RoleSidecar, Ports: []provider.PortSpec{{Container: 53, Protocol: "udp"}}},
		},
	}

	podSpec := buildPodSpec(req, nil)
	want := buildService(req, deployNS, nil, false).Spec.Ports

	if got := servicePortsFor(&podSpec); !reflect.DeepEqual(got, want) {
		t.Fatalf("ports:\n got %+v\nwant %+v", got, want)
	}
}

// TestDeploy_ReturnsEndpointsWhenPortsChange checks that a deploy which
// sets ports returns the instance's endpoints in Provision's shape, one
// that clears every port returns an empty list, and one without ports
// returns nil.
func TestDeploy_ReturnsEndpointsWhenPortsChange(t *testing.T) {
	p, _, instID := provisionForDeploy(t, provider.KindDeployment, []provider.PortSpec{{Container: 8080}})
	host := serviceName(instID) + "." + deployNS + ".svc.cluster.local"

	result, err := p.Deploy(t.Context(), provider.DeployRequest{InstanceID: instID, ReleaseID: id.New(id.PrefixRelease), Services: []provider.ServiceDeploySpec{{
		Name: "api", Image: "app:v2", Ports: []provider.PortSpec{{Container: 9090}, {Container: 9091}},
	}}})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}

	want := []provider.Endpoint{
		{ServiceName: "api", URL: "http://" + host + ":9090", Port: 9090, Protocol: "TCP"},
		{ServiceName: "proxy", URL: "http://" + host + ":9901", Port: 9901, Protocol: "TCP"},
	}
	if !reflect.DeepEqual(result.Endpoints, want) {
		t.Fatalf("endpoints:\n got %+v\nwant %+v", result.Endpoints, want)
	}

	result, err = p.Deploy(t.Context(), provider.DeployRequest{InstanceID: instID, ReleaseID: id.New(id.PrefixRelease), Services: []provider.ServiceDeploySpec{{Name: "api", Image: "app:v3"}}})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}

	if result.Endpoints != nil {
		t.Fatalf("deploy without ports returned endpoints %+v", result.Endpoints)
	}

	result, err = p.Deploy(t.Context(), provider.DeployRequest{InstanceID: instID, ReleaseID: id.New(id.PrefixRelease), Services: []provider.ServiceDeploySpec{
		{Name: "api", Image: "app:v4", Ports: []provider.PortSpec{}},
		{Name: "proxy", Image: "envoy:v2", Ports: []provider.PortSpec{}},
	}})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}

	if result.Endpoints == nil || len(result.Endpoints) != 0 {
		t.Fatalf("clearing every port: endpoints = %#v, want an empty non-nil list", result.Endpoints)
	}
}

// TestPodEndpoints_MatchesProvision checks that endpoints rebuilt from a
// provisioned pod template equal the ones Provision returned.
func TestPodEndpoints_MatchesProvision(t *testing.T) {
	t.Parallel()

	req := provider.ProvisionRequest{
		InstanceID: id.New(id.PrefixInstance),
		Services: []provider.ServiceSpec{
			{Name: "migrate", Image: "m:1", Role: provider.RoleInit},
			{Name: "api", Image: "a:1", Ports: []provider.PortSpec{{Container: 8080}, {Container: 8443}}},
			{Name: "worker", Image: "w:1", Role: provider.RoleSidecar},
			{Name: "proxy", Image: "p:1", Role: provider.RoleSidecar, Ports: []provider.PortSpec{{Container: 9901}}},
		},
	}

	podSpec := buildPodSpec(req, nil)

	if got, want := podEndpoints(req.InstanceID, deployNS, &podSpec), buildEndpoints(req, deployNS); !reflect.DeepEqual(got, want) {
		t.Fatalf("endpoints:\n got %+v\nwant %+v", got, want)
	}
}
