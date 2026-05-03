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
//
// OnServiceProgress (optional) is invoked when a strategy advances a
// single service's rollout state — e.g. canary moves service "api"
// from "pending" → "running" → "succeeded" without touching service
// "web". The deploy service uses these callbacks to update the
// Deployment.ServiceProgress map so dashboards and observers see
// per-service granularity. Strategies that don't have per-service
// granularity (rolling, recreate) update every service to the same
// state in lockstep.
type StrategyParams struct {
	Deployment        *Deployment
	Provider          provider.Provider
	OnProgress        func(phase string, percent int, message string)
	OnServiceProgress func(serviceName string, state string)
}

// Service-level progress states. Match the keys used in
// Deployment.ServiceProgress so callers can compare directly.
const (
	ServiceStatePending   = "pending"
	ServiceStateRunning   = "running"
	ServiceStateSucceeded = "succeeded"
	ServiceStateFailed    = "failed"
)
