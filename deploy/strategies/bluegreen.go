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

// Execute performs a blue-green deployment by spinning up a new environment
// and switching traffic to it.
func (s *BlueGreen) Execute(ctx context.Context, params deploy.StrategyParams) error {
	params.OnProgress("provisioning", 0, "provisioning new environment")

	_, err := params.Provider.Deploy(ctx, provider.DeployRequest{
		InstanceID: params.Deployment.InstanceID,
		ReleaseID:  params.Deployment.ReleaseID,
		Image:      params.Deployment.Image,
		Env:        params.Deployment.Env,
		Strategy:   "blue-green",
	})
	if err != nil {
		return fmt.Errorf("strategy %s: %w", s.Name(), err)
	}

	params.OnProgress("switching", 50, "switching traffic")
	params.OnProgress("complete", 100, "blue-green deployment complete")

	return nil
}
