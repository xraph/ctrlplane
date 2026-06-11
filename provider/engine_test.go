package provider

import (
	"context"
	"testing"

	"github.com/xraph/ctrlplane/id"
)

type stubManifestEngine struct{}

func (stubManifestEngine) ApplyManifests(context.Context, ManifestApplyRequest) (*ProvisionResult, error) {
	return nil, nil
}

func (stubManifestEngine) DeleteManifests(context.Context, id.ID) error { return nil }

func (stubManifestEngine) ManifestStatus(context.Context, id.ID) (*InstanceStatus, error) {
	return nil, nil
}

func TestManifestEngine_Interface(t *testing.T) {
	var _ ManifestEngine = stubManifestEngine{}
}

type stubHelmEngine struct{}

func (stubHelmEngine) HelmInstall(context.Context, HelmInstallRequest) (*ProvisionResult, error) {
	return nil, nil
}

func (stubHelmEngine) HelmUpgrade(context.Context, HelmUpgradeRequest) (*DeployResult, error) {
	return nil, nil
}

func (stubHelmEngine) HelmUninstall(context.Context, id.ID) error { return nil }

func (stubHelmEngine) HelmStatus(context.Context, id.ID) (*InstanceStatus, error) {
	return nil, nil
}

func TestHelmEngine_Interface(t *testing.T) {
	var _ HelmEngine = stubHelmEngine{}
}
