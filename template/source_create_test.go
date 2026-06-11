package template

import (
	"errors"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/vars"
)

func TestCreate_HelmSource(t *testing.T) {
	svc := NewService(newMemStore(), nil)
	ctx := tenantCtx("ten_1")

	tmpl, err := svc.Create(ctx, CreateRequest{
		Name:      "redis",
		Source:    provider.DeploymentSource{Type: provider.SourceHelm, Helm: &provider.HelmSource{Chart: "redis"}},
		Variables: []vars.Definition{{Name: "tag", Type: vars.TypeString, Default: "latest"}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if tmpl.Source.Type != provider.SourceHelm || tmpl.Source.Helm == nil || tmpl.Source.Helm.Chart != "redis" {
		t.Errorf("source not stored: %+v", tmpl.Source)
	}

	if len(tmpl.Variables) != 1 || tmpl.Variables[0].Name != "tag" {
		t.Errorf("variables not stored: %+v", tmpl.Variables)
	}
}

func TestCreate_HelmSourceInvalid(t *testing.T) {
	svc := NewService(newMemStore(), nil)
	ctx := tenantCtx("ten_1")

	_, err := svc.Create(ctx, CreateRequest{
		Name:   "redis",
		Source: provider.DeploymentSource{Type: provider.SourceHelm, Helm: &provider.HelmSource{}},
	})
	if !errors.Is(err, ctrlplane.ErrInvalidSource) {
		t.Fatalf("expected ErrInvalidSource, got %v", err)
	}
}

func TestCreate_InvalidVariable(t *testing.T) {
	svc := NewService(newMemStore(), nil)
	ctx := tenantCtx("ten_1")

	_, err := svc.Create(ctx, CreateRequest{
		Name:      "redis",
		Source:    provider.DeploymentSource{Type: provider.SourceHelm, Helm: &provider.HelmSource{Chart: "redis"}},
		Variables: []vars.Definition{{Name: "bad", Type: vars.TypeEnum}}, // enum without members
	})
	if !errors.Is(err, vars.ErrInvalidDefinition) {
		t.Fatalf("expected ErrInvalidDefinition, got %v", err)
	}
}

func TestCreate_LegacyServicesNormalizes(t *testing.T) {
	svc := NewService(newMemStore(), nil)
	ctx := tenantCtx("ten_1")

	tmpl, err := svc.Create(ctx, CreateRequest{
		Name:     "web",
		Services: []provider.ServiceSpec{{Name: "web", Image: "nginx"}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if tmpl.Source.Type != provider.SourceServices || len(tmpl.Source.Services) != 1 {
		t.Errorf("legacy services not normalized to source: %+v", tmpl.Source)
	}
}
