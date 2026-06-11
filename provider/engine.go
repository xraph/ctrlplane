package provider

import (
	"context"

	"github.com/xraph/ctrlplane/id"
)

// ManifestEngine is an optional provider interface for deploying raw or
// kustomize-rendered Kubernetes manifests. Providers that can apply
// arbitrary objects implement it and advertise CapManifests; the workload
// dispatcher type-asserts for it when a source is SourceManifests. Modeled
// as an optional interface (like HealthChecker) so providers that cannot
// apply manifests simply do not implement it.
type ManifestEngine interface {
	// ApplyManifests applies every rendered document, labeling each object
	// for the instance, and records what it applied so the set can later be
	// deleted or inspected.
	ApplyManifests(ctx context.Context, req ManifestApplyRequest) (*ProvisionResult, error)

	// DeleteManifests removes every object previously applied for the
	// instance. Deleting an already-absent set is not an error.
	DeleteManifests(ctx context.Context, instanceID id.ID) error

	// ManifestStatus reports the aggregate state of the applied objects.
	ManifestStatus(ctx context.Context, instanceID id.ID) (*InstanceStatus, error)
}

// ManifestApplyRequest carries the rendered documents and metadata needed
// to apply a manifests source for one instance.
type ManifestApplyRequest struct {
	InstanceID id.ID             `json:"instance_id"`
	TenantID   string            `json:"tenant_id"`
	Namespace  string            `json:"namespace,omitempty"`
	Manifests  RenderedManifests `json:"manifests"`
	Labels     map[string]string `json:"labels,omitempty"`
}

// HelmEngine is an optional provider interface for deploying Helm charts.
// Providers that drive the Helm SDK implement it and advertise CapHelm; the
// workload dispatcher type-asserts for it when a source is SourceHelm.
type HelmEngine interface {
	// HelmInstall installs the chart as a new release for the instance.
	HelmInstall(ctx context.Context, req HelmInstallRequest) (*ProvisionResult, error)

	// HelmUpgrade upgrades the instance's existing release.
	HelmUpgrade(ctx context.Context, req HelmUpgradeRequest) (*DeployResult, error)

	// HelmUninstall removes the instance's release.
	HelmUninstall(ctx context.Context, instanceID id.ID) error

	// HelmStatus reports the release's state.
	HelmStatus(ctx context.Context, instanceID id.ID) (*InstanceStatus, error)
}

// HelmInstallRequest carries the rendered chart reference for a new release.
type HelmInstallRequest struct {
	InstanceID id.ID        `json:"instance_id"`
	TenantID   string       `json:"tenant_id"`
	Namespace  string       `json:"namespace,omitempty"`
	Chart      RenderedHelm `json:"chart"`
}

// HelmUpgradeRequest carries the rendered chart reference for upgrading an
// existing release.
type HelmUpgradeRequest struct {
	InstanceID id.ID        `json:"instance_id"`
	Namespace  string       `json:"namespace,omitempty"`
	Chart      RenderedHelm `json:"chart"`
}
