package render

import (
	"testing"

	"github.com/xraph/ctrlplane/provider"
)

func TestRender_ArgoCD(t *testing.T) {
	src := provider.DeploymentSource{
		Type: provider.SourceArgoCD,
		ArgoCD: &provider.ArgoCDSource{
			Project:        "default",
			RepoURL:        "https://github.com/{{ .var.org }}/repo.git",
			Path:           "apps/{{ .var.app }}",
			TargetRevision: "main",
			DestNamespace:  "{{ .tenant.id }}",
		},
	}

	out, err := Render(src, scopeWith(map[string]any{"org": "acme", "app": "web"}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if out.Type != provider.SourceArgoCD || out.ArgoCD == nil {
		t.Fatalf("unexpected output: %+v", out)
	}

	a := out.ArgoCD
	if a.RepoURL != "https://github.com/acme/repo.git" {
		t.Errorf("repo = %q", a.RepoURL)
	}

	if a.Path != "apps/web" {
		t.Errorf("path = %q", a.Path)
	}

	if a.DestNamespace != "tnt_1" {
		t.Errorf("dest namespace = %q", a.DestNamespace)
	}

	if a.Project != "default" || a.TargetRevision != "main" {
		t.Errorf("static fields altered: %+v", a)
	}
}

func TestRender_SecretNotInlined(t *testing.T) {
	// dbpass is a secret-typed variable, so the resolver excluded it from
	// the scope. Referencing it inline must fail rather than render empty.
	src := provider.DeploymentSource{
		Type:      provider.SourceManifests,
		Manifests: &provider.ManifestSource{Inline: "password: {{ .var.dbpass }}"},
	}

	if _, err := Render(src, scopeWith(map[string]any{})); err == nil {
		t.Fatal("expected error referencing an out-of-scope secret variable, got nil")
	}
}
