package kubernetes

import (
	"context"
	"fmt"
	"strconv"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// releaseName is the Helm release name for an instance. It is derived
// solely from the instance id so uninstall and status — which receive only
// the id — can reconstruct it deterministically. A chart's ReleaseName hint
// is intentionally not used as the release identity.
func releaseName(instanceID id.ID) string {
	return deploymentName(instanceID)
}

// helmNamespace returns the effective namespace for a helm operation.
func (p *Provider) helmNamespace(reqNamespace string) string {
	if reqNamespace != "" {
		return reqNamespace
	}

	return p.cfg.Namespace
}

// runInstall installs a chart as a new release.
func runInstall(cfg *action.Configuration, ch *chart.Chart, name, namespace string, values map[string]any) (*release.Release, error) {
	inst := action.NewInstall(cfg)
	inst.ReleaseName = name
	inst.Namespace = namespace

	rel, err := inst.Run(ch, values)
	if err != nil {
		return nil, fmt.Errorf("helm install: %w", err)
	}

	return rel, nil
}

// HelmInstall installs the rendered chart as a new release for the instance.
func (p *Provider) HelmInstall(_ context.Context, req provider.HelmInstallRequest) (*provider.ProvisionResult, error) {
	ns := p.helmNamespace(req.Namespace)

	cfg, err := p.helmConfig(ns)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: helm config: %w", err)
	}

	ch, err := p.loadChart(req.Chart)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: load chart: %w", err)
	}

	name := releaseName(req.InstanceID)

	rel, err := runInstall(cfg, ch, name, ns, req.Chart.Values)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: helm install %s: %w", name, err)
	}

	return &provider.ProvisionResult{
		ProviderRef: "helm:" + rel.Namespace + "/" + rel.Name,
		Metadata:    map[string]string{"revision": strconv.Itoa(rel.Version)},
	}, nil
}

// runUpgrade upgrades an existing release to a new chart/values revision.
func runUpgrade(cfg *action.Configuration, ch *chart.Chart, name, namespace string, values map[string]any) (*release.Release, error) {
	up := action.NewUpgrade(cfg)
	up.Namespace = namespace

	rel, err := up.Run(name, ch, values)
	if err != nil {
		return nil, fmt.Errorf("helm upgrade: %w", err)
	}

	return rel, nil
}

// HelmUpgrade upgrades the instance's existing release.
func (p *Provider) HelmUpgrade(_ context.Context, req provider.HelmUpgradeRequest) (*provider.DeployResult, error) {
	ns := p.helmNamespace(req.Namespace)

	cfg, err := p.helmConfig(ns)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: helm config: %w", err)
	}

	ch, err := p.loadChart(req.Chart)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: load chart: %w", err)
	}

	name := releaseName(req.InstanceID)

	rel, err := runUpgrade(cfg, ch, name, ns, req.Chart.Values)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: helm upgrade %s: %w", name, err)
	}

	return &provider.DeployResult{
		ProviderRef: "helm:" + rel.Namespace + "/" + rel.Name,
		Status:      string(rel.Info.Status),
	}, nil
}

// runUninstall removes a release.
func runUninstall(cfg *action.Configuration, name string) error {
	if _, err := action.NewUninstall(cfg).Run(name); err != nil {
		return fmt.Errorf("helm uninstall: %w", err)
	}

	return nil
}

// HelmUninstall removes the instance's release.
func (p *Provider) HelmUninstall(_ context.Context, instanceID id.ID) error {
	cfg, err := p.helmConfig(p.cfg.Namespace)
	if err != nil {
		return fmt.Errorf("kubernetes: helm config: %w", err)
	}

	name := releaseName(instanceID)
	if err := runUninstall(cfg, name); err != nil {
		return fmt.Errorf("kubernetes: helm uninstall %s: %w", name, err)
	}

	return nil
}

// runStatus fetches the current release.
func runStatus(cfg *action.Configuration, name string) (*release.Release, error) {
	rel, err := action.NewGet(cfg).Run(name)
	if err != nil {
		return nil, fmt.Errorf("helm get: %w", err)
	}

	return rel, nil
}

// HelmStatus reports the instance release's state.
func (p *Provider) HelmStatus(_ context.Context, instanceID id.ID) (*provider.InstanceStatus, error) {
	cfg, err := p.helmConfig(p.cfg.Namespace)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: helm config: %w", err)
	}

	name := releaseName(instanceID)

	rel, err := runStatus(cfg, name)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: helm status %s: %w", name, err)
	}

	state := helmStateFor(rel.Info.Status)

	return &provider.InstanceStatus{
		State:   state,
		Ready:   state == provider.StateRunning,
		Message: rel.Info.Description,
	}, nil
}

// Compile-time check that Provider implements the HelmEngine optional
// interface.
var _ provider.HelmEngine = (*Provider)(nil)

// helmStateFor maps a Helm release status to a ctrlplane InstanceState.
func helmStateFor(status release.Status) provider.InstanceState {
	switch status {
	case release.StatusDeployed:
		return provider.StateRunning
	case release.StatusFailed:
		return provider.StateFailed
	case release.StatusPendingInstall, release.StatusPendingUpgrade, release.StatusPendingRollback:
		return provider.StateStarting
	case release.StatusUninstalling:
		return provider.StateStopping
	case release.StatusUninstalled:
		return provider.StateStopped
	default:
		return provider.StateProvisioning
	}
}
