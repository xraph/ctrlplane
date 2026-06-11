package kubernetes

import (
	"context"
	"io"
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	kubefake "helm.sh/helm/v3/pkg/kube/fake"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// memoryHelmConfig returns an action.Configuration backed by the in-memory
// release driver and a printing (no-op) kube client — no cluster needed.
func memoryHelmConfig() *action.Configuration {
	return &action.Configuration{
		Releases:     storage.Init(driver.NewMemory()),
		KubeClient:   &kubefake.PrintingKubeClient{Out: io.Discard, LogOutput: io.Discard},
		Capabilities: chartutil.DefaultCapabilities,
		Log:          func(string, ...any) {},
	}
}

// testChart builds a minimal installable chart with one templated ConfigMap.
func testChart() *chart.Chart {
	return &chart.Chart{
		Metadata: &chart.Metadata{APIVersion: "v2", Name: "test", Version: "0.1.0"},
		Templates: []*chart.File{{
			Name: "templates/cm.yaml",
			Data: []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}-cm\ndata:\n  k: {{ .Values.k }}\n"),
		}},
	}
}

// newHelmTestProvider wires a Provider to a shared memory-driver config and
// the in-memory test chart. The returned config is the same instance the
// provider uses, so tests can read back stored releases.
func newHelmTestProvider() (*Provider, *action.Configuration) {
	cfg := memoryHelmConfig()
	p := &Provider{
		cfg:        Config{Namespace: "default"},
		helmConfig: func(string) (*action.Configuration, error) { return cfg, nil },
		loadChart:  func(provider.RenderedHelm) (*chart.Chart, error) { return testChart(), nil },
	}

	return p, cfg
}

func TestHelmStateFor(t *testing.T) {
	tests := []struct {
		status release.Status
		want   provider.InstanceState
	}{
		{release.StatusDeployed, provider.StateRunning},
		{release.StatusFailed, provider.StateFailed},
		{release.StatusPendingInstall, provider.StateStarting},
		{release.StatusPendingUpgrade, provider.StateStarting},
		{release.StatusPendingRollback, provider.StateStarting},
		{release.StatusUninstalling, provider.StateStopping},
		{release.StatusUninstalled, provider.StateStopped},
		{release.StatusUnknown, provider.StateProvisioning},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := helmStateFor(tt.status); got != tt.want {
				t.Errorf("helmStateFor(%s) = %s, want %s", tt.status, got, tt.want)
			}
		})
	}
}

func TestHelmInstall(t *testing.T) {
	p, cfg := newHelmTestProvider()
	ctx := context.Background()
	instID := id.New(id.PrefixInstance)

	req := provider.HelmInstallRequest{
		InstanceID: instID,
		TenantID:   "ten_1",
		Namespace:  "default",
		Chart:      provider.RenderedHelm{Chart: "test", Values: map[string]any{"k": "hello"}},
	}

	res, err := p.HelmInstall(ctx, req)
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	if !strings.HasPrefix(res.ProviderRef, "helm:") {
		t.Errorf("provider ref = %q, want helm: prefix", res.ProviderRef)
	}

	rel, err := cfg.Releases.Last(releaseName(instID, req.Chart))
	if err != nil {
		t.Fatalf("read release: %v", err)
	}

	if rel.Info.Status != release.StatusDeployed {
		t.Errorf("status = %s, want deployed", rel.Info.Status)
	}

	if !strings.Contains(rel.Manifest, "hello") {
		t.Errorf("rendered manifest missing templated value:\n%s", rel.Manifest)
	}
}
