package kubernetes

import (
	"testing"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

func podSpecRequest(services ...provider.ServiceSpec) provider.ProvisionRequest {
	return provider.ProvisionRequest{
		InstanceID: id.New(id.PrefixInstance),
		TenantID:   "ten_test",
		Name:       "web",
		Kind:       provider.KindDeployment,
		Services:   services,
	}
}

// TestBuildPodSpec_AutomountAnnotation verifies the well-known
// ctrlplane.io/automount-service-account-token annotation lands on
// PodSpec.AutomountServiceAccountToken.
func TestBuildPodSpec_AutomountAnnotation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		services []provider.ServiceSpec
		want     *bool // nil = field must stay unset
	}{
		{
			name: "false disables automount",
			services: []provider.ServiceSpec{
				{
					Name:  "main",
					Image: "app:1",
					Role:  provider.RoleMain,
					Annotations: map[string]string{
						annotationAutomountToken: "false",
					},
				},
			},
			want: new(false),
		},
		{
			name: "true enables automount explicitly",
			services: []provider.ServiceSpec{
				{
					Name:  "main",
					Image: "app:1",
					Role:  provider.RoleMain,
					Annotations: map[string]string{
						annotationAutomountToken: "true",
					},
				},
			},
			want: new(true),
		},
		{
			name: "absent annotation leaves field unset",
			services: []provider.ServiceSpec{
				{Name: "main", Image: "app:1", Role: provider.RoleMain},
			},
			want: nil,
		},
		{
			name: "malformed value is ignored",
			services: []provider.ServiceSpec{
				{
					Name:  "main",
					Image: "app:1",
					Role:  provider.RoleMain,
					Annotations: map[string]string{
						annotationAutomountToken: "banana",
					},
				},
			},
			want: nil,
		},
		{
			name: "false on any service wins over true (most restrictive)",
			services: []provider.ServiceSpec{
				{
					Name:  "main",
					Image: "app:1",
					Role:  provider.RoleMain,
					Annotations: map[string]string{
						annotationAutomountToken: "true",
					},
				},
				{
					Name:  "sidecar",
					Image: "proxy:1",
					Role:  provider.RoleSidecar,
					Annotations: map[string]string{
						annotationAutomountToken: "false",
					},
				},
			},
			want: new(false),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spec := buildPodSpec(podSpecRequest(tt.services...), nil)

			switch {
			case tt.want == nil:
				if spec.AutomountServiceAccountToken != nil {
					t.Fatalf("automount: want unset, got %v", *spec.AutomountServiceAccountToken)
				}
			case spec.AutomountServiceAccountToken == nil:
				t.Fatalf("automount: want %v, got unset", *tt.want)
			case *spec.AutomountServiceAccountToken != *tt.want:
				t.Fatalf("automount: want %v, got %v", *tt.want, *spec.AutomountServiceAccountToken)
			}
		})
	}
}

// TestBuildPodSpec_NodeSelectorAnnotation verifies the well-known
// ctrlplane.io/node-selector annotation ("k=v,k2=v2") lands on
// PodSpec.NodeSelector, unioned across services.
func TestBuildPodSpec_NodeSelectorAnnotation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		services []provider.ServiceSpec
		want     map[string]string // nil = field must stay unset
	}{
		{
			name: "single pair",
			services: []provider.ServiceSpec{
				{
					Name:  "main",
					Image: "app:1",
					Role:  provider.RoleMain,
					Annotations: map[string]string{
						annotationNodeSelector: "kubernetes.io/arch=amd64",
					},
				},
			},
			want: map[string]string{"kubernetes.io/arch": "amd64"},
		},
		{
			name: "multiple pairs with whitespace",
			services: []provider.ServiceSpec{
				{
					Name:  "main",
					Image: "app:1",
					Role:  provider.RoleMain,
					Annotations: map[string]string{
						annotationNodeSelector: "kubernetes.io/arch=amd64, disktype=ssd",
					},
				},
			},
			want: map[string]string{
				"kubernetes.io/arch": "amd64",
				"disktype":           "ssd",
			},
		},
		{
			name: "union across services",
			services: []provider.ServiceSpec{
				{
					Name:  "main",
					Image: "app:1",
					Role:  provider.RoleMain,
					Annotations: map[string]string{
						annotationNodeSelector: "kubernetes.io/arch=amd64",
					},
				},
				{
					Name:  "sidecar",
					Image: "proxy:1",
					Role:  provider.RoleSidecar,
					Annotations: map[string]string{
						annotationNodeSelector: "disktype=ssd",
					},
				},
			},
			want: map[string]string{
				"kubernetes.io/arch": "amd64",
				"disktype":           "ssd",
			},
		},
		{
			name: "malformed pairs are skipped",
			services: []provider.ServiceSpec{
				{
					Name:  "main",
					Image: "app:1",
					Role:  provider.RoleMain,
					Annotations: map[string]string{
						annotationNodeSelector: "no-equals-sign,=novalue-key,kubernetes.io/arch=amd64,",
					},
				},
			},
			want: map[string]string{"kubernetes.io/arch": "amd64"},
		},
		{
			name: "absent annotation leaves field unset",
			services: []provider.ServiceSpec{
				{Name: "main", Image: "app:1", Role: provider.RoleMain},
			},
			want: nil,
		},
		{
			name: "all-malformed annotation leaves field unset",
			services: []provider.ServiceSpec{
				{
					Name:  "main",
					Image: "app:1",
					Role:  provider.RoleMain,
					Annotations: map[string]string{
						annotationNodeSelector: "garbage",
					},
				},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spec := buildPodSpec(podSpecRequest(tt.services...), nil)

			if tt.want == nil {
				if spec.NodeSelector != nil {
					t.Fatalf("nodeSelector: want unset, got %v", spec.NodeSelector)
				}

				return
			}

			if len(spec.NodeSelector) != len(tt.want) {
				t.Fatalf("nodeSelector: want %v, got %v", tt.want, spec.NodeSelector)
			}

			for k, v := range tt.want {
				if spec.NodeSelector[k] != v {
					t.Fatalf("nodeSelector[%q]: want %q, got %q", k, v, spec.NodeSelector[k])
				}
			}
		})
	}
}

// TestBuildDeployment_PodAnnotationsPropagate verifies the annotation
// knobs survive the full Deployment build, not just buildPodSpec.
func TestBuildDeployment_PodAnnotationsPropagate(t *testing.T) {
	t.Parallel()

	req := podSpecRequest(provider.ServiceSpec{
		Name:  "main",
		Image: "app:1",
		Role:  provider.RoleMain,
		Annotations: map[string]string{
			annotationAutomountToken: "false",
			annotationNodeSelector:   "kubernetes.io/arch=amd64",
		},
	})

	dep := buildDeployment(req, "ten-ns", map[string]string{"app": "web"}, nil)
	podSpec := dep.Spec.Template.Spec

	if podSpec.AutomountServiceAccountToken == nil || *podSpec.AutomountServiceAccountToken {
		t.Fatalf("automount: want false, got %v", podSpec.AutomountServiceAccountToken)
	}

	if podSpec.NodeSelector["kubernetes.io/arch"] != "amd64" {
		t.Fatalf("nodeSelector: want arch=amd64, got %v", podSpec.NodeSelector)
	}
}
