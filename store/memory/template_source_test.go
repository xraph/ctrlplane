package memory

import (
	"context"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/template"
	"github.com/xraph/ctrlplane/vars"
)

// TestTemplateStore_VariablesAndSourceRoundTrip confirms a template's
// Variables and Source survive persistence and that the stored copy is
// isolated from later mutation of the caller's slice.
func TestTemplateStore_VariablesAndSourceRoundTrip(t *testing.T) {
	t.Parallel()

	store := New()
	ctx := context.Background()

	tmpl := &template.Template{
		Entity:   ctrlplane.NewEntity(id.PrefixTemplate),
		TenantID: "ten_1",
		Name:     "t1",
		Variables: []vars.Definition{
			{Name: "tag", Type: vars.TypeString, Default: "latest"},
		},
		Source: provider.DeploymentSource{
			Type: provider.SourceHelm,
			Helm: &provider.HelmSource{Chart: "redis"},
		},
	}

	if err := store.InsertTemplate(ctx, tmpl); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Mutate the caller's slice after insert — the stored copy must not change.
	tmpl.Variables[0].Default = "MUTATED"

	got, err := store.GetTemplate(ctx, "ten_1", tmpl.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.Source.Type != provider.SourceHelm || got.Source.Helm == nil || got.Source.Helm.Chart != "redis" {
		t.Errorf("source not preserved: %+v", got.Source)
	}

	if len(got.Variables) != 1 {
		t.Fatalf("variables = %d, want 1", len(got.Variables))
	}

	if got.Variables[0].Default != "latest" {
		t.Errorf("variable default = %v, want latest (stored copy not isolated)", got.Variables[0].Default)
	}
}
