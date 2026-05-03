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

// Execute performs a rolling deployment by handing the entire Services
// slice to the provider in one shot — the provider's runtime
// (k8s rollingUpdate, nomad update, docker recreate-per-service) does
// the per-replica gradient. ServiceProgress is updated in lockstep:
// every service goes pending → running → succeeded together.
func (s *Rolling) Execute(ctx context.Context, params deploy.StrategyParams) error {
	params.OnProgress("deploying", 0, "starting rolling update")

	markAll(params, deploy.ServiceStateRunning)

	_, err := params.Provider.Deploy(ctx, provider.DeployRequest{
		InstanceID: params.Deployment.InstanceID,
		ReleaseID:  params.Deployment.ReleaseID,
		Services:   params.Deployment.Services,
		Strategy:   "rolling",
	})
	if err != nil {
		markAll(params, deploy.ServiceStateFailed)

		return fmt.Errorf("strategy %s: %w", s.Name(), err)
	}

	markAll(params, deploy.ServiceStateSucceeded)
	params.OnProgress("deploying", 100, "rolling update complete")

	return nil
}

// markAll updates every service in the deployment to the same state.
// Strategies that don't have per-service granularity (rolling /
// recreate / blue-green) use this; canary sets state per-service.
func markAll(params deploy.StrategyParams, state string) {
	if params.OnServiceProgress == nil {
		return
	}

	for _, sd := range params.Deployment.Services {
		params.OnServiceProgress(sd.Name, state)
	}
}
