package template

import (
	"testing"

	"github.com/xraph/ctrlplane/provider"
)

func TestTemplate_NormalizeSource_LegacyServices(t *testing.T) {
	tmpl := &Template{
		Services: []provider.ServiceSpec{{Name: "web", Image: "nginx"}},
	}

	tmpl.NormalizeSource()

	if tmpl.Source.Type != provider.SourceServices {
		t.Errorf("type = %q, want services", tmpl.Source.Type)
	}

	if len(tmpl.Source.Services) != 1 || tmpl.Source.Services[0].Name != "web" {
		t.Errorf("services not carried into source: %+v", tmpl.Source.Services)
	}
}

func TestTemplate_NormalizeSource_ExplicitUnchanged(t *testing.T) {
	tmpl := &Template{
		Source: provider.DeploymentSource{
			Type: provider.SourceHelm,
			Helm: &provider.HelmSource{Chart: "redis"},
		},
	}

	tmpl.NormalizeSource()

	if tmpl.Source.Type != provider.SourceHelm || tmpl.Source.Helm == nil || tmpl.Source.Helm.Chart != "redis" {
		t.Errorf("explicit source changed: %+v", tmpl.Source)
	}
}

func TestTemplate_NormalizeSource_Idempotent(t *testing.T) {
	tmpl := &Template{Services: []provider.ServiceSpec{{Name: "web", Image: "nginx"}}}

	tmpl.NormalizeSource()
	tmpl.NormalizeSource()

	if tmpl.Source.Type != provider.SourceServices || len(tmpl.Source.Services) != 1 {
		t.Errorf("normalize not idempotent: %+v", tmpl.Source)
	}
}
