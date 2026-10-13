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
//
// OnEndpoints (optional) receives the endpoints a provider returns from
// Deploy. The deploy service stores the last set it receives on the
// instance. Strategies that call Deploy through DeployProvider report
// them without extra work; a strategy that calls Provider.Deploy
// directly leaves the instance's endpoints as they were.
type StrategyParams struct {
	Deployment        *Deployment
	Provider          provider.Provider
	OnProgress        func(phase string, percent int, message string)
	OnServiceProgress func(serviceName string, state string)
	OnEndpoints       func(endpoints []provider.Endpoint)
}

// DeployProvider calls Provider.Deploy and passes any endpoints the
// result carries to OnEndpoints. A nil result, nil Endpoints or a nil
// OnEndpoints reports nothing.
func (p StrategyParams) DeployProvider(ctx context.Context, req provider.DeployRequest) (*provider.DeployResult, error) {
	result, err := p.Provider.Deploy(ctx, req)
	if err != nil {
		return result, err
	}

	if result != nil && result.Endpoints != nil && p.OnEndpoints != nil {
		p.OnEndpoints(result.Endpoints)
	}

	return result, nil
}

// Service-level progress states. Match the keys used in
// Deployment.ServiceProgress so callers can compare directly.
const (
	ServiceStatePending   = "pending"
	ServiceStateRunning   = "running"
	ServiceStateSucceeded = "succeeded"
	ServiceStateFailed    = "failed"
)
