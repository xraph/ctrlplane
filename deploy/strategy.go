package deploy

import (
	"context"

	"github.com/xraph/ctrlplane/provider"
)

// Strategy defines how a deployment is executed.
// Implementations handle the mechanics of rolling, blue-green, canary, etc.
type Strategy interface {
	// Name returns the strategy identifier (e.g., "rolling", "blue-green").
	Name() string

	// Execute performs the deployment according to the strategy.
	Execute(ctx context.Context, params StrategyParams) error
}

// StrategyParams provides everything a strategy needs to execute a deployment.
type StrategyParams struct {
	Deployment *Deployment
	Provider   provider.Provider
	OnProgress func(phase string, percent int, message string)
}
