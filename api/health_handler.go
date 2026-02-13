package api

import (
	"net/http"

	"github.com/xraph/forge"

	"github.com/xraph/ctrlplane/health"
)

// configureHealthCheck handles POST /v1/instances/:instanceId/health/checks.
func (a *API) configureHealthCheck(ctx forge.Context, req *ConfigureHealthCheckAPIRequest) (*health.HealthCheck, error) {
	domainReq := health.ConfigureRequest{
		InstanceID: req.InstanceID,
		Name:       req.Name,
		Type:       health.CheckType(req.Type),
		Target:     req.Target,
		Interval:   req.Interval,
		Timeout:    req.Timeout,
		Retries:    req.Retries,
	}

	check, err := a.cp.Health.Configure(ctx.Context(), domainReq)
	if err != nil {
		return nil, mapError(err)
	}

	_ = ctx.JSON(http.StatusCreated, check)

	//nolint:nilnil // response already written via ctx.JSON/ctx.NoContent.
	return nil, nil
}

// getInstanceHealth handles GET /v1/instances/:instanceId/health.
func (a *API) getInstanceHealth(ctx forge.Context, req *GetInstanceHealthRequest) (*health.InstanceHealth, error) {
	ih, err := a.cp.Health.GetHealth(ctx.Context(), req.InstanceID)
	if err != nil {
		return nil, mapError(err)
	}

	return ih, nil
}

// listHealthChecks handles GET /v1/instances/:instanceId/health/checks.
func (a *API) listHealthChecks(ctx forge.Context, req *ListHealthChecksRequest) ([]health.HealthCheck, error) {
	checks, err := a.cp.Health.ListChecks(ctx.Context(), req.InstanceID)
	if err != nil {
		return nil, mapError(err)
	}

	return checks, nil
}

// runHealthCheck handles POST /v1/health/checks/:checkID/run.
func (a *API) runHealthCheck(ctx forge.Context, req *RunHealthCheckRequest) (*health.HealthResult, error) {
	result, err := a.cp.Health.RunCheck(ctx.Context(), req.CheckID)
	if err != nil {
		return nil, mapError(err)
	}

	return result, nil
}

// removeHealthCheck handles DELETE /v1/health/checks/:checkID.
func (a *API) removeHealthCheck(ctx forge.Context, req *RemoveHealthCheckRequest) (*health.HealthCheck, error) {
	if err := a.cp.Health.Remove(ctx.Context(), req.CheckID); err != nil {
		return nil, mapError(err)
	}

	_ = ctx.NoContent(http.StatusNoContent)

	//nolint:nilnil // response already written via ctx.JSON/ctx.NoContent.
	return nil, nil
}
