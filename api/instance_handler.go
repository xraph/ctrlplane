package api

import (
	"net/http"

	"github.com/xraph/forge"

	"github.com/xraph/ctrlplane/instance"
)

// createInstance handles POST /v1/instances.
func (a *API) createInstance(ctx forge.Context, req *CreateInstanceRequest) (*instance.Instance, error) {
	inst, err := a.cp.Instances.Create(ctx.Context(), req.CreateRequest)
	if err != nil {
		return nil, mapError(err)
	}

	_ = ctx.JSON(http.StatusCreated, inst)

	//nolint:nilnil // response already written via ctx.JSON/ctx.NoContent.
	return nil, nil
}

// listInstances handles GET /v1/instances.
func (a *API) listInstances(ctx forge.Context, req *ListInstancesRequest) (*instance.ListResult, error) {
	limit := req.Limit
	if limit == 0 {
		limit = 20
	}

	opts := instance.ListOptions{
		State:    req.State,
		Label:    req.Label,
		Provider: req.Provider,
		Cursor:   req.Cursor,
		Limit:    limit,
	}

	result, err := a.cp.Instances.List(ctx.Context(), opts)
	if err != nil {
		return nil, mapError(err)
	}

	return result, nil
}

// getInstance handles GET /v1/instances/:instanceID.
func (a *API) getInstance(ctx forge.Context, req *GetInstanceRequest) (*instance.Instance, error) {
	inst, err := a.cp.Instances.Get(ctx.Context(), req.InstanceID)
	if err != nil {
		return nil, mapError(err)
	}

	return inst, nil
}

// updateInstance handles PATCH /v1/instances/:instanceID.
func (a *API) updateInstance(ctx forge.Context, req *UpdateInstanceRequest) (*instance.Instance, error) {
	inst, err := a.cp.Instances.Update(ctx.Context(), req.InstanceID, req.UpdateRequest)
	if err != nil {
		return nil, mapError(err)
	}

	return inst, nil
}

// deleteInstance handles DELETE /v1/instances/:instanceID.
func (a *API) deleteInstance(ctx forge.Context, req *DeleteInstanceRequest) (*instance.Instance, error) {
	if err := a.cp.Instances.Delete(ctx.Context(), req.InstanceID); err != nil {
		return nil, mapError(err)
	}

	_ = ctx.NoContent(http.StatusNoContent)

	//nolint:nilnil // response already written via ctx.JSON/ctx.NoContent.
	return nil, nil
}

// startInstance handles POST /v1/instances/:instanceID/start.
func (a *API) startInstance(ctx forge.Context, req *InstanceActionRequest) (*instance.Instance, error) {
	if err := a.cp.Instances.Start(ctx.Context(), req.InstanceID); err != nil {
		return nil, mapError(err)
	}

	_ = ctx.NoContent(http.StatusNoContent)

	//nolint:nilnil // response already written via ctx.JSON/ctx.NoContent.
	return nil, nil
}

// stopInstance handles POST /v1/instances/:instanceID/stop.
func (a *API) stopInstance(ctx forge.Context, req *InstanceActionRequest) (*instance.Instance, error) {
	if err := a.cp.Instances.Stop(ctx.Context(), req.InstanceID); err != nil {
		return nil, mapError(err)
	}

	_ = ctx.NoContent(http.StatusNoContent)

	//nolint:nilnil // response already written via ctx.JSON/ctx.NoContent.
	return nil, nil
}

// restartInstance handles POST /v1/instances/:instanceID/restart.
func (a *API) restartInstance(ctx forge.Context, req *InstanceActionRequest) (*instance.Instance, error) {
	if err := a.cp.Instances.Restart(ctx.Context(), req.InstanceID); err != nil {
		return nil, mapError(err)
	}

	_ = ctx.NoContent(http.StatusNoContent)

	//nolint:nilnil // response already written via ctx.JSON/ctx.NoContent.
	return nil, nil
}

// scaleInstance handles POST /v1/instances/:instanceID/scale.
func (a *API) scaleInstance(ctx forge.Context, req *ScaleInstanceRequest) (*instance.Instance, error) {
	if err := a.cp.Instances.Scale(ctx.Context(), req.InstanceID, req.ScaleRequest); err != nil {
		return nil, mapError(err)
	}

	_ = ctx.NoContent(http.StatusNoContent)

	//nolint:nilnil // response already written via ctx.JSON/ctx.NoContent.
	return nil, nil
}

// suspendInstance handles POST /v1/instances/:instanceID/suspend.
func (a *API) suspendInstance(ctx forge.Context, req *SuspendInstanceRequest) (*instance.Instance, error) {
	if err := a.cp.Instances.Suspend(ctx.Context(), req.InstanceID, req.Reason); err != nil {
		return nil, mapError(err)
	}

	_ = ctx.NoContent(http.StatusNoContent)

	//nolint:nilnil // response already written via ctx.JSON/ctx.NoContent.
	return nil, nil
}

// unsuspendInstance handles POST /v1/instances/:instanceID/unsuspend.
func (a *API) unsuspendInstance(ctx forge.Context, req *InstanceActionRequest) (*instance.Instance, error) {
	if err := a.cp.Instances.Unsuspend(ctx.Context(), req.InstanceID); err != nil {
		return nil, mapError(err)
	}

	_ = ctx.NoContent(http.StatusNoContent)

	//nolint:nilnil // response already written via ctx.JSON/ctx.NoContent.
	return nil, nil
}
