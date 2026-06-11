package dispatch

import (
	"context"
	"errors"
	"io"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// fakeProvider implements provider.Provider plus all three engine
// interfaces, recording the last engine method invoked. Its advertised
// capabilities are configurable so tests can withhold a capability.
type fakeProvider struct {
	caps   []provider.Capability
	called string
}

func (f *fakeProvider) Info() provider.ProviderInfo      { return provider.ProviderInfo{Name: "fake"} }
func (f *fakeProvider) Capabilities() []provider.Capability { return f.caps }

func (f *fakeProvider) Provision(context.Context, provider.ProvisionRequest) (*provider.ProvisionResult, error) {
	f.called = "Provision"

	return &provider.ProvisionResult{ProviderRef: "fake"}, nil
}

func (f *fakeProvider) Deprovision(context.Context, id.ID) error { f.called = "Deprovision"; return nil }
func (f *fakeProvider) Start(context.Context, id.ID) error       { return nil }
func (f *fakeProvider) Stop(context.Context, id.ID) error        { return nil }
func (f *fakeProvider) Restart(context.Context, id.ID) error     { return nil }

func (f *fakeProvider) Status(context.Context, id.ID) (*provider.InstanceStatus, error) {
	f.called = "Status"

	return &provider.InstanceStatus{State: provider.StateRunning}, nil
}

func (f *fakeProvider) Deploy(context.Context, provider.DeployRequest) (*provider.DeployResult, error) {
	return &provider.DeployResult{}, nil
}
func (f *fakeProvider) Rollback(context.Context, id.ID, id.ID) error            { return nil }
func (f *fakeProvider) Scale(context.Context, id.ID, provider.ResourceSpec) error { return nil }

func (f *fakeProvider) Resources(context.Context, id.ID) (*provider.ResourceUsage, error) {
	return &provider.ResourceUsage{}, nil
}

func (f *fakeProvider) Logs(context.Context, id.ID, provider.LogOptions) (io.ReadCloser, error) {
	return nil, nil
}

func (f *fakeProvider) Exec(context.Context, id.ID, provider.ExecRequest) (*provider.ExecResult, error) {
	return &provider.ExecResult{}, nil
}

func (f *fakeProvider) ApplyManifests(context.Context, provider.ManifestApplyRequest) (*provider.ProvisionResult, error) {
	f.called = "ApplyManifests"

	return &provider.ProvisionResult{ProviderRef: "fake"}, nil
}
func (f *fakeProvider) DeleteManifests(context.Context, id.ID) error { f.called = "DeleteManifests"; return nil }

func (f *fakeProvider) ManifestStatus(context.Context, id.ID) (*provider.InstanceStatus, error) {
	f.called = "ManifestStatus"

	return &provider.InstanceStatus{State: provider.StateRunning}, nil
}

func (f *fakeProvider) HelmInstall(context.Context, provider.HelmInstallRequest) (*provider.ProvisionResult, error) {
	f.called = "HelmInstall"

	return &provider.ProvisionResult{ProviderRef: "fake"}, nil
}

func (f *fakeProvider) HelmUpgrade(context.Context, provider.HelmUpgradeRequest) (*provider.DeployResult, error) {
	return &provider.DeployResult{}, nil
}
func (f *fakeProvider) HelmUninstall(context.Context, id.ID) error { f.called = "HelmUninstall"; return nil }

func (f *fakeProvider) HelmStatus(context.Context, id.ID) (*provider.InstanceStatus, error) {
	f.called = "HelmStatus"

	return &provider.InstanceStatus{State: provider.StateRunning}, nil
}

func (f *fakeProvider) ArgoApply(context.Context, provider.ArgoApplyRequest) (*provider.ProvisionResult, error) {
	f.called = "ArgoApply"

	return &provider.ProvisionResult{ProviderRef: "fake"}, nil
}
func (f *fakeProvider) ArgoDelete(context.Context, id.ID) error { f.called = "ArgoDelete"; return nil }

func (f *fakeProvider) ArgoStatus(context.Context, id.ID) (*provider.InstanceStatus, error) {
	f.called = "ArgoStatus"

	return &provider.InstanceStatus{State: provider.StateRunning}, nil
}

func allCaps() []provider.Capability {
	return []provider.Capability{provider.CapProvision, provider.CapManifests, provider.CapHelm, provider.CapArgoCD}
}

func TestProvision_RoutesByType(t *testing.T) {
	tests := []struct {
		name   string
		source provider.RenderedSource
		want   string
	}{
		{"services", provider.RenderedSource{Type: provider.SourceServices, Services: []provider.ServiceSpec{{Name: "w", Image: "nginx"}}}, "Provision"},
		{"manifests", provider.RenderedSource{Type: provider.SourceManifests, Manifests: &provider.RenderedManifests{Docs: []string{"kind: Pod"}}}, "ApplyManifests"},
		{"helm", provider.RenderedSource{Type: provider.SourceHelm, Helm: &provider.RenderedHelm{Chart: "redis"}}, "HelmInstall"},
		{"argocd", provider.RenderedSource{Type: provider.SourceArgoCD, ArgoCD: &provider.ArgoCDSource{RepoURL: "x"}}, "ArgoApply"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &fakeProvider{caps: allCaps()}

			if _, err := Provision(context.Background(), p, Request{InstanceID: id.New(id.PrefixInstance), Source: tt.source}); err != nil {
				t.Fatalf("provision: %v", err)
			}

			if p.called != tt.want {
				t.Errorf("called %q, want %q", p.called, tt.want)
			}
		})
	}
}

func TestProvision_MissingCapability(t *testing.T) {
	// Implements HelmEngine but does NOT advertise CapHelm.
	p := &fakeProvider{caps: []provider.Capability{provider.CapProvision}}

	_, err := Provision(context.Background(), p, Request{
		InstanceID: id.New(id.PrefixInstance),
		Source:     provider.RenderedSource{Type: provider.SourceHelm, Helm: &provider.RenderedHelm{Chart: "redis"}},
	})
	if !errors.Is(err, ctrlplane.ErrUnsupportedSource) {
		t.Fatalf("expected ErrUnsupportedSource, got %v", err)
	}
}
