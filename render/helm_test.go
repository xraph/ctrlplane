package render

import (
	"testing"

	"github.com/xraph/ctrlplane/provider"
)

func TestRender_HelmValues(t *testing.T) {
	src := provider.DeploymentSource{
		Type: provider.SourceHelm,
		Helm: &provider.HelmSource{
			Repo:    "oci://registry/charts",
			Chart:   "api",
			Version: "1.0.0",
			Values: map[string]any{
				"image":        map[string]any{"tag": "{{ .var.tag }}", "repo": "ghcr/app"},
				"replicaCount": 3,
				"tls":          true,
				"hosts":        []any{"{{ .var.host }}", "static.example.com"},
			},
		},
	}

	out, err := Render(src, scopeWith(map[string]any{"tag": "1.2.3", "host": "api.example.com"}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if out.Type != provider.SourceHelm || out.Helm == nil {
		t.Fatalf("unexpected output: %+v", out)
	}

	h := out.Helm
	if h.Chart != "api" || h.Version != "1.0.0" || h.Repo != "oci://registry/charts" {
		t.Errorf("chart coords not carried: %+v", h)
	}

	img, ok := h.Values["image"].(map[string]any)
	if !ok || img["tag"] != "1.2.3" || img["repo"] != "ghcr/app" {
		t.Errorf("nested string leaves not templated: %#v", h.Values["image"])
	}

	if h.Values["replicaCount"] != 3 {
		t.Errorf("int leaf altered: %#v", h.Values["replicaCount"])
	}

	if h.Values["tls"] != true {
		t.Errorf("bool leaf altered: %#v", h.Values["tls"])
	}

	hosts, ok := h.Values["hosts"].([]any)
	if !ok || hosts[0] != "api.example.com" || hosts[1] != "static.example.com" {
		t.Errorf("slice leaves not templated: %#v", h.Values["hosts"])
	}
}
