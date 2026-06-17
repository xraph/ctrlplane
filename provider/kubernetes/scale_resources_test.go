package kubernetes

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// TestScale_PatchesContainerResources is the regression for the bug where
// Provider.Scale ignored CPU/memory and only acted on replicas — leaving a
// provisioned workload's resources immutable. A resize must now patch the
// pod template's container requests AND limits.
func TestScale_PatchesContainerResources(t *testing.T) {
	instID := id.New(id.PrefixInstance)
	name := deploymentName(instID)

	const ns = "default"

	big := provider.ResourceSpec{CPUMillis: 500, MemoryMB: 512}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "twinos",
						Image: "img:1",
						Resources: corev1.ResourceRequirements{
							Requests: buildResourceList(big),
							Limits:   buildResourceList(big),
						},
					}},
				},
			},
		},
	}

	p := &Provider{
		cfg:    Config{Namespace: ns},
		client: k8sfake.NewSimpleClientset(dep),
	}

	// Shrink to values that fit a small node.
	err := p.Scale(context.Background(), instID, provider.ResourceSpec{CPUMillis: 150, MemoryMB: 256})
	if err != nil {
		t.Fatalf("Scale: %v", err)
	}

	got, err := p.client.AppsV1().Deployments(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}

	c := got.Spec.Template.Spec.Containers[0]

	if cpu := c.Resources.Requests.Cpu().MilliValue(); cpu != 150 {
		t.Errorf("cpu request: want 150m, got %dm", cpu)
	}

	if cpu := c.Resources.Limits.Cpu().MilliValue(); cpu != 150 {
		t.Errorf("cpu limit: want 150m, got %dm", cpu)
	}

	if memMB := c.Resources.Requests.Memory().Value() / (1024 * 1024); memMB != 256 {
		t.Errorf("mem request: want 256Mi, got %dMi", memMB)
	}
}

// TestScale_noResourceChange_isNoop verifies that a ScaleRequest carrying
// neither CPU nor memory nor replicas leaves the workload untouched (and
// crucially does not attempt the resource patch).
func TestScale_noResourceChange_isNoop(t *testing.T) {
	instID := id.New(id.PrefixInstance)
	name := deploymentName(instID)

	const ns = "default"

	orig := provider.ResourceSpec{CPUMillis: 500, MemoryMB: 512}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:      "twinos",
						Image:     "img:1",
						Resources: corev1.ResourceRequirements{Requests: buildResourceList(orig), Limits: buildResourceList(orig)},
					}},
				},
			},
		},
	}

	p := &Provider{cfg: Config{Namespace: ns}, client: k8sfake.NewSimpleClientset(dep)}

	if err := p.Scale(context.Background(), instID, provider.ResourceSpec{}); err != nil {
		t.Fatalf("Scale (empty spec): %v", err)
	}

	got, err := p.client.AppsV1().Deployments(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}

	if cpu := got.Spec.Template.Spec.Containers[0].Resources.Requests.Cpu().MilliValue(); cpu != 500 {
		t.Errorf("empty scale must not change cpu: got %dm, want 500m", cpu)
	}
}

// TestProvision_propagatesRequestLabelsToPods is the regression for pods
// carrying only ctrlplane labels: a caller's per-instance labels (e.g.
// twinos.workspace / twinos.component) must reach the Deployment AND its
// pod template so pods are identifiable/queryable, while the reserved
// ctrlplane.io/* keys stay authoritative.
func TestProvision_propagatesRequestLabelsToPods(t *testing.T) {
	instID := id.New(id.PrefixInstance)

	const ns = "default"

	p := &Provider{
		cfg:    Config{Namespace: ns},
		client: k8sfake.NewSimpleClientset(),
	}

	req := provider.ProvisionRequest{
		InstanceID: instID,
		TenantID:   "ten_abc",
		Kind:       provider.KindDeployment,
		Labels: map[string]string{
			"twinos.workspace": "ws_acme",
			"twinos.component": "twinos",
			// A caller trying to clobber a reserved key must NOT win.
			labelInstanceID: "spoofed",
		},
		Services: []provider.ServiceSpec{{
			Name:  "twinos",
			Image: "img:1",
			Role:  provider.RoleMain,
		}},
	}

	if _, err := p.Provision(context.Background(), req); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	dep, err := p.client.AppsV1().Deployments(ns).Get(context.Background(), deploymentName(instID), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}

	pod := dep.Spec.Template.Labels
	if pod["twinos.workspace"] != "ws_acme" || pod["twinos.component"] != "twinos" {
		t.Fatalf("caller labels missing from pod template: %v", pod)
	}

	if pod[labelInstanceID] != instID.String() {
		t.Fatalf("reserved instance-id label was clobbered: got %q, want %q", pod[labelInstanceID], instID.String())
	}
}
