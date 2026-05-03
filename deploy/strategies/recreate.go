package strategies

import (
	"context"
	"fmt"

	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/provider"
)

// Recreate implements a recreate deployment strategy that stops the current
// version before starting the new one, resulting in brief downtime.
type Recreate struct{}

// NewRecreate returns a new recreate deployment strategy.
func NewRecreate() *Recreate {
	return &Recreate{}
}

// Name returns the strategy identifier.
func (s *Recreate) Name() string {
	return "recreate"
}

// Execute performs a recreate deployment by stopping the current version
// and starting the new one. Like Rolling, this advances every service
// through the same state in lockstep — the provider does the actual
// stop/start.
func (s *Recreate) Execute(ctx context.Context, params deploy.StrategyParams) error {
	params.OnProgress("stopping", 0, "stopping current version")

	markAll(params, deploy.ServiceStateRunning)

	_, err := params.Provider.Deploy(ctx, provider.DeployRequest{
		InstanceID: params.Deployment.InstanceID,
		ReleaseID:  params.Deployment.ReleaseID,
		Services:   params.Deployment.Services,
		Strategy:   "recreate",
	})
	if err != nil {
		markAll(params, deploy.ServiceStateFailed)

		return fmt.Errorf("strategy %s: %w", s.Name(), err)
	}

	markAll(params, deploy.ServiceStateSucceeded)
	params.OnProgress("starting", 50, "starting new version")
	params.OnProgress("complete", 100, "recreate deployment complete")

	return nil
}
