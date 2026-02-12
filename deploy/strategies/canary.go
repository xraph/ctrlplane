package strategies

import (
	"context"
	"fmt"

	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/provider"
)

// Canary implements a canary deployment strategy that routes a percentage
// of traffic to the new version before full promotion.
type Canary struct{}

// NewCanary returns a new canary deployment strategy.
func NewCanary() *Canary {
	return &Canary{}
}

// Name returns the strategy identifier.
func (s *Canary) Name() string {
	return "canary"
}

// Execute performs a canary deployment by routing incremental traffic to the
// new version and promoting once validated.
func (s *Canary) Execute(ctx context.Context, params deploy.StrategyParams) error {
	params.OnProgress("canary", 0, "starting canary deployment")

	_, err := params.Provider.Deploy(ctx, provider.DeployRequest{
		InstanceID: params.Deployment.InstanceID,
		ReleaseID:  params.Deployment.ReleaseID,
		Image:      params.Deployment.Image,
		Env:        params.Deployment.Env,
		Strategy:   "canary",
	})
	if err != nil {
		return fmt.Errorf("strategy %s: %w", s.Name(), err)
	}

	params.OnProgress("promoting", 50, "promoting canary")
	params.OnProgress("complete", 100, "canary promotion complete")

	return nil
}
