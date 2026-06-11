package render

import (
	"strings"
	"testing"

	"github.com/xraph/ctrlplane/provider"
)

func TestRender_ManifestsInline(t *testing.T) {
	inline := `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .var.name }}
data:
  region: {{ .region }}
---
apiVersion: v1
kind: Service
metadata:
  name: {{ .var.name }}-svc
`

	src := provider.DeploymentSource{
		Type:      provider.SourceManifests,
		Manifests: &provider.ManifestSource{Inline: inline},
	}

	out, err := Render(src, scopeWith(map[string]any{"name": "app"}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if out.Type != provider.SourceManifests || out.Manifests == nil {
		t.Fatalf("unexpected output: %+v", out)
	}

	if len(out.Manifests.Docs) != 2 {
		t.Fatalf("docs = %d, want 2: %#v", len(out.Manifests.Docs), out.Manifests.Docs)
	}

	if !strings.Contains(out.Manifests.Docs[0], "name: app") || !strings.Contains(out.Manifests.Docs[0], "region: us-east") {
		t.Errorf("doc[0] missing substitutions: %q", out.Manifests.Docs[0])
	}

	if !strings.Contains(out.Manifests.Docs[1], "name: app-svc") {
		t.Errorf("doc[1] missing substitution: %q", out.Manifests.Docs[1])
	}
}

func TestRender_ManifestsInline_SkipsEmptyDocs(t *testing.T) {
	src := provider.DeploymentSource{
		Type:      provider.SourceManifests,
		Manifests: &provider.ManifestSource{Inline: "---\nkind: Pod\n---\n---\n"},
	}

	out, err := Render(src, scopeWith(nil))
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if len(out.Manifests.Docs) != 1 {
		t.Fatalf("docs = %d, want 1: %#v", len(out.Manifests.Docs), out.Manifests.Docs)
	}
}

func TestRender_ManifestsKustomize(t *testing.T) {
	files := map[string]string{
		"/kustomization.yaml": "namePrefix: prod-\nresources:\n  - deployment.yaml\n",
		"/deployment.yaml": "apiVersion: apps/v1\n" +
			"kind: Deployment\n" +
			"metadata:\n  name: app\n" +
			"spec:\n  replicas: {{ .var.replicas }}\n",
	}

	src := provider.DeploymentSource{
		Type: provider.SourceManifests,
		Manifests: &provider.ManifestSource{
			Kustomize: &provider.KustomizeSource{Files: files, Root: "/"},
		},
	}

	out, err := Render(src, scopeWith(map[string]any{"replicas": 2}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if out.Manifests == nil || len(out.Manifests.Docs) != 1 {
		t.Fatalf("docs = %#v, want 1", out.Manifests)
	}

	doc := out.Manifests.Docs[0]
	if !strings.Contains(doc, "name: prod-app") {
		t.Errorf("kustomize namePrefix not applied: %q", doc)
	}

	if !strings.Contains(doc, "replicas: 2") {
		t.Errorf("variable not templated before build: %q", doc)
	}
}
