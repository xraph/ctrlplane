package provider

import (
	"encoding/json"
	"errors"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
)

func TestDeploymentSource_Validate(t *testing.T) {
	tests := []struct {
		name    string
		src     DeploymentSource
		wantErr bool
	}{
		{
			name: "valid services",
			src:  DeploymentSource{Type: SourceServices, Services: []ServiceSpec{{Name: "web", Image: "nginx"}}},
		},
		{
			name: "valid helm",
			src:  DeploymentSource{Type: SourceHelm, Helm: &HelmSource{Chart: "redis"}},
		},
		{
			name: "valid manifests inline",
			src:  DeploymentSource{Type: SourceManifests, Manifests: &ManifestSource{Inline: "kind: Pod"}},
		},
		{
			name: "valid manifests kustomize",
			src:  DeploymentSource{Type: SourceManifests, Manifests: &ManifestSource{Kustomize: &KustomizeSource{Files: map[string]string{"kustomization.yaml": ""}}}},
		},
		{
			name: "valid argocd",
			src:  DeploymentSource{Type: SourceArgoCD, ArgoCD: &ArgoCDSource{RepoURL: "https://example.com/repo.git"}},
		},
		{
			name:    "services without services",
			src:     DeploymentSource{Type: SourceServices},
			wantErr: true,
		},
		{
			name:    "helm without chart",
			src:     DeploymentSource{Type: SourceHelm, Helm: &HelmSource{}},
			wantErr: true,
		},
		{
			name:    "manifests with neither",
			src:     DeploymentSource{Type: SourceManifests, Manifests: &ManifestSource{}},
			wantErr: true,
		},
		{
			name:    "argocd without repo",
			src:     DeploymentSource{Type: SourceArgoCD, ArgoCD: &ArgoCDSource{}},
			wantErr: true,
		},
		{
			name:    "unknown type",
			src:     DeploymentSource{Type: SourceType("weird")},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.src.Validate()
			if tt.wantErr {
				if !errors.Is(err, ctrlplane.ErrInvalidSource) {
					t.Errorf("expected ErrInvalidSource, got %v", err)
				}

				return
			}

			if err != nil {
				t.Errorf("expected valid, got %v", err)
			}
		})
	}
}

func TestHelmSource_JSONRoundTrip(t *testing.T) {
	in := DeploymentSource{
		Type: SourceHelm,
		Helm: &HelmSource{
			Repo:        "oci://registry.example.com/charts",
			Chart:       "api",
			Version:     "1.2.3",
			ReleaseName: "api-prod",
			Namespace:   "prod",
			Values:      map[string]any{"replicaCount": float64(3)},
		},
	}

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out DeploymentSource
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if out.Type != SourceHelm || out.Helm == nil || out.Helm.Chart != "api" || out.Helm.Values["replicaCount"] != float64(3) {
		t.Errorf("round-trip mismatch: %+v", out.Helm)
	}
}

func TestRenderedSource_JSONRoundTrip(t *testing.T) {
	in := RenderedSource{
		Type:      SourceManifests,
		Manifests: &RenderedManifests{Docs: []string{"kind: Pod", "kind: Service"}},
	}

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out RenderedSource
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if out.Type != SourceManifests || out.Manifests == nil || len(out.Manifests.Docs) != 2 {
		t.Errorf("round-trip mismatch: %+v", out.Manifests)
	}
}

func TestSourceCapabilities_Defined(t *testing.T) {
	want := map[Capability]string{
		CapHelm:      "source:helm",
		CapManifests: "source:manifests",
		CapArgoCD:    "source:argocd",
	}

	for capability, str := range want {
		if string(capability) != str {
			t.Errorf("capability = %q, want %q", capability, str)
		}
	}
}
