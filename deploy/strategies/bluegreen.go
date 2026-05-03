package strategies

import (
	"context"
	"fmt"

	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/provider"
)

// BlueGreen implements a blue-green deployment strategy that provisions a new
// environment and switches traffic once ready.
type BlueGreen struct{}

// NewBlueGreen returns a new blue-green deployment strategy.
func NewBlueGreen() *BlueGreen {
	return &BlueGreen{}
}

// Name returns the strategy identifier.
func (s *BlueGreen) Name() string {
	return "blue-green"
}

// Execute performs a blue-green deployment by spinning up a new
// environment and switching traffic to it. Per-service progress
// advances in lockstep — the cutover is atomic across all services.
func (s *BlueGreen) Execute(ctx context.Context, params deploy.StrategyParams) error {
	params.OnProgress("provisioning", 0, "provisioning new environment")

	markAll(params, deploy.ServiceStateRunning)

	_, err := params.Provider.Deploy(ctx, provider.DeployRequest{
		InstanceID: params.Deployment.InstanceID,
		ReleaseID:  params.Deployment.ReleaseID,
		Services:   params.Deployment.Services,
		Strategy:   "blue-green",
	})
	if err != nil {
		markAll(params, deploy.ServiceStateFailed)

		return fmt.Errorf("strategy %s: %w", s.Name(), err)
	}

	markAll(params, deploy.ServiceStateSucceeded)
	params.OnProgress("switching", 50, "switching traffic")
	params.OnProgress("complete", 100, "blue-green deployment complete")

	return nil
}
