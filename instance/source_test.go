package instance

import (
	"context"
	"io"
	"testing"

	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/vars"
)

// srcProvider is a fake provider that implements the ManifestEngine, used to
// verify that a manifests-source instance provisions via the engine rather
// than the core Provision path.
type srcProvider struct {
	applied  bool
	deleted  bool
	manifest bool
}

func (p *srcProvider) Info() provider.ProviderInfo { return provider.ProviderInfo{Name: "kubernetes"} }

func (p *srcProvider) Capabilities() []provider.Capability {
	return []provider.Capability{provider.CapProvision, provider.CapManifests}
}

func (p *srcProvider) Provision(context.Context, provider.ProvisionRequest) (*provider.ProvisionResult, error) {
	return &provider.ProvisionResult{ProviderRef: "core"}, nil
}
func (p *srcProvider) Deprovision(context.Context, id.ID) error { return nil }
func (p *srcProvider) Start(context.Context, id.ID) error       { return nil }
func (p *srcProvider) Stop(context.Context, id.ID) error        { return nil }
func (p *srcProvider) Restart(context.Context, id.ID) error     { return nil }

func (p *srcProvider) Status(context.Context, id.ID) (*provider.InstanceStatus, error) {
	return &provider.InstanceStatus{State: provider.StateRunning}, nil
}

func (p *srcProvider) Deploy(context.Context, provider.DeployRequest) (*provider.DeployResult, error) {
	return &provider.DeployResult{}, nil
}
func (p *srcProvider) Rollback(context.Context, id.ID, id.ID) error              { return nil }
func (p *srcProvider) Scale(context.Context, id.ID, provider.ResourceSpec) error { return nil }

func (p *srcProvider) Resources(context.Context, id.ID) (*provider.ResourceUsage, error) {
	return &provider.ResourceUsage{}, nil
}

func (p *srcProvider) Logs(context.Context, id.ID, provider.LogOptions) (io.ReadCloser, error) {
	return nil, nil
}

func (p *srcProvider) Exec(context.Context, id.ID, provider.ExecRequest) (*provider.ExecResult, error) {
	return &provider.ExecResult{}, nil
}

func (p *srcProvider) ApplyManifests(context.Context, provider.ManifestApplyRequest) (*provider.ProvisionResult, error) {
	p.applied = true
	p.manifest = true

	return &provider.ProvisionResult{ProviderRef: "k8s:manifests"}, nil
}

func (p *srcProvider) DeleteManifests(context.Context, id.ID) error {
	p.deleted = true

	return nil
}

func (p *srcProvider) ManifestStatus(context.Context, id.ID) (*provider.InstanceStatus, error) {
	return &provider.InstanceStatus{State: provider.StateRunning}, nil
}

func srcCtx() context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{TenantID: "ten_1", SubjectID: "u"})
}

func TestCreate_ManifestsSource_DispatchesToEngine(t *testing.T) {
	store := newDelStore()
	prov := &srcProvider{}
	registry := provider.NewRegistry()
	registry.Register("kubernetes", prov)

	svc := NewService(store, registry, event.NewInMemoryBus(), nil, nil)

	inst, err := svc.Create(srcCtx(), CreateRequest{
		Name:         "raw",
		ProviderName: "kubernetes",
		Source: provider.DeploymentSource{
			Type:      provider.SourceManifests,
			Manifests: &provider.ManifestSource{Inline: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .var.n }}\n"},
		},
		Variables:      []vars.Definition{{Name: "n", Type: vars.TypeString, Default: "x"}},
		VariableValues: map[string]any{"n": "cm1"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if !prov.applied {
		t.Error("expected ApplyManifests to be called for a manifests source")
	}

	if inst.Source.Type != provider.SourceManifests {
		t.Errorf("instance source type = %q, want manifests", inst.Source.Type)
	}

	// Delete should route to the manifests engine.
	if err := svc.Delete(srcCtx(), inst.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if !prov.deleted {
		t.Error("expected DeleteManifests to be called on delete")
	}
}
