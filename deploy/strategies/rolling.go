package strategies

import (
	"context"
	"fmt"

	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/provider"
)

// Rolling implements a rolling update strategy that gradually replaces
// instances with the new version.
type Rolling struct{}

// NewRolling returns a new rolling update strategy.
func NewRolling() *Rolling {
	return &Rolling{}
}

// Name returns the strategy identifier.
func (s *Rolling) Name() string {
	return "rolling"
}

// Execute performs a rolling deployment by gradually replacing instances.
func (s *Rolling) Execute(ctx context.Context, params deploy.StrategyParams) error {
	params.OnProgress("deploying", 0, "starting rolling update")

	_, err := params.Provider.Deploy(ctx, provider.DeployRequest{
		InstanceID: params.Deployment.InstanceID,
		ReleaseID:  params.Deployment.ReleaseID,
		Image:      params.Deployment.Image,
		Env:        params.Deployment.Env,
		Strategy:   "rolling",
	})
	if err != nil {
		return fmt.Errorf("strategy %s: %w", s.Name(), err)
	}

	params.OnProgress("deploying", 100, "rolling update complete")

	return nil
}
