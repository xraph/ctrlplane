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
