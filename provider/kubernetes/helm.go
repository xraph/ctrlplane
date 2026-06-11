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

// releaseName is the Helm release name for an instance — the chart's
// explicit ReleaseName when set, otherwise derived from the instance id.
func releaseName(instanceID id.ID, ch provider.RenderedHelm) string {
	if ch.ReleaseName != "" {
		return ch.ReleaseName
	}

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

	name := releaseName(req.InstanceID, req.Chart)

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

	name := releaseName(req.InstanceID, req.Chart)

	rel, err := runUpgrade(cfg, ch, name, ns, req.Chart.Values)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: helm upgrade %s: %w", name, err)
	}

	return &provider.DeployResult{
		ProviderRef: "helm:" + rel.Namespace + "/" + rel.Name,
		Status:      string(rel.Info.Status),
	}, nil
}

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
