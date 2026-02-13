package api

import (
	"net/http"

	"github.com/xraph/forge"

	"github.com/xraph/ctrlplane/deploy"
)

// deployInstance handles POST /v1/instances/:instanceId/deploy.
func (a *API) deployInstance(ctx forge.Context, req *DeployAPIRequest) (*deploy.Deployment, error) {
	domainReq := deploy.DeployRequest{
		InstanceID: req.InstanceID,
		Image:      req.Image,
		Env:        req.Env,
		Strategy:   req.Strategy,
		Notes:      req.Notes,
		CommitSHA:  req.CommitSHA,
	}

	deployment, err := a.cp.Deploys.Deploy(ctx.Context(), domainReq)
	if err != nil {
		return nil, mapError(err)
	}

	_ = ctx.JSON(http.StatusCreated, deployment)

	//nolint:nilnil // response already written via ctx.JSON/ctx.NoContent.
	return nil, nil
}

// listDeployments handles GET /v1/instances/:instanceId/deployments.
func (a *API) listDeployments(ctx forge.Context, req *ListDeploymentsRequest) (*deploy.DeployListResult, error) {
	limit := req.Limit
	if limit == 0 {
		limit = 20
	}

	opts := deploy.ListOptions{
		Cursor: req.Cursor,
		Limit:  limit,
	}

	result, err := a.cp.Deploys.ListDeployments(ctx.Context(), req.InstanceID, opts)
	if err != nil {
		return nil, mapError(err)
	}

	return result, nil
}

// getDeployment handles GET /v1/deployments/:deploymentId.
func (a *API) getDeployment(ctx forge.Context, req *GetDeploymentRequest) (*deploy.Deployment, error) {
	deployment, err := a.cp.Deploys.GetDeployment(ctx.Context(), req.DeploymentID)
	if err != nil {
		return nil, mapError(err)
	}

	return deployment, nil
}

// cancelDeployment handles POST /v1/deployments/:deploymentId/cancel.
func (a *API) cancelDeployment(ctx forge.Context, req *CancelDeploymentRequest) (*deploy.Deployment, error) {
	if err := a.cp.Deploys.Cancel(ctx.Context(), req.DeploymentID); err != nil {
		return nil, mapError(err)
	}

	_ = ctx.NoContent(http.StatusNoContent)

	//nolint:nilnil // response already written via ctx.JSON/ctx.NoContent.
	return nil, nil
}

// rollback handles POST /v1/instances/:instanceId/rollback.
func (a *API) rollback(ctx forge.Context, req *RollbackRequest) (*deploy.Deployment, error) {
	deployment, err := a.cp.Deploys.Rollback(ctx.Context(), req.InstanceID, req.ReleaseID)
	if err != nil {
		return nil, mapError(err)
	}

	_ = ctx.JSON(http.StatusCreated, deployment)

	//nolint:nilnil // response already written via ctx.JSON/ctx.NoContent.
	return nil, nil
}

// listReleases handles GET /v1/instances/:instanceId/releases.
func (a *API) listReleases(ctx forge.Context, req *ListReleasesRequest) (*deploy.ReleaseListResult, error) {
	limit := req.Limit
	if limit == 0 {
		limit = 20
	}

	opts := deploy.ListOptions{
		Cursor: req.Cursor,
		Limit:  limit,
	}

	result, err := a.cp.Deploys.ListReleases(ctx.Context(), req.InstanceID, opts)
	if err != nil {
		return nil, mapError(err)
	}

	return result, nil
}

// getRelease handles GET /v1/releases/:releaseID.
func (a *API) getRelease(ctx forge.Context, req *GetReleaseRequest) (*deploy.Release, error) {
	release, err := a.cp.Deploys.GetRelease(ctx.Context(), req.ReleaseID)
	if err != nil {
		return nil, mapError(err)
	}

	return release, nil
}
