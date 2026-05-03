package strategies

import (
	"context"
	"fmt"

	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/provider"
)

// Canary implements a canary deployment strategy that rolls services
// out one at a time, marking each as succeeded before moving to the
// next. If any service fails, the strategy aborts — services not yet
// rolled out stay pending; the deploy.Service marks the deployment
// itself Failed so callers can rollback the whole release.
//
// Canary uses partial-deploy semantics: each Deploy call to the
// provider lists exactly one service. The provider patches that
// service's container/task in place without disturbing the rest.
type Canary struct{}

// NewCanary returns a new canary deployment strategy.
func NewCanary() *Canary {
	return &Canary{}
}

// Name returns the strategy identifier.
func (s *Canary) Name() string {
	return "canary"
}

// Execute rolls services out one at a time, advancing
// ServiceProgress as each completes.
func (s *Canary) Execute(ctx context.Context, params deploy.StrategyParams) error {
	params.OnProgress("canary", 0, "starting canary deployment")

	total := len(params.Deployment.Services)
	if total == 0 {
		return fmt.Errorf("strategy %s: no services to deploy", s.Name())
	}

	for i, sd := range params.Deployment.Services {
		// Mark this service running; everything not yet started stays
		// "pending" by default (the deploy service initialises the
		// progress map that way).
		if params.OnServiceProgress != nil {
			params.OnServiceProgress(sd.Name, deploy.ServiceStateRunning)
		}

		_, err := params.Provider.Deploy(ctx, provider.DeployRequest{
			InstanceID: params.Deployment.InstanceID,
			ReleaseID:  params.Deployment.ReleaseID,
			Services:   []provider.ServiceDeploySpec{sd},
			Strategy:   "canary",
		})
		if err != nil {
			if params.OnServiceProgress != nil {
				params.OnServiceProgress(sd.Name, deploy.ServiceStateFailed)
			}

			return fmt.Errorf("strategy %s: service %q: %w", s.Name(), sd.Name, err)
		}

		if params.OnServiceProgress != nil {
			params.OnServiceProgress(sd.Name, deploy.ServiceStateSucceeded)
		}

		percent := (i + 1) * 100 / total
		params.OnProgress("promoting", percent, fmt.Sprintf("service %q promoted (%d/%d)", sd.Name, i+1, total))
	}

	params.OnProgress("complete", 100, "canary promotion complete")

	return nil
}
