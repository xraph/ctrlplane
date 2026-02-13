package api

import (
	"net/http"

	"github.com/xraph/forge"

	"github.com/xraph/ctrlplane/secrets"
)

// setSecret handles POST /v1/instances/:instanceId/secrets.
func (a *API) setSecret(ctx forge.Context, req *SetSecretAPIRequest) (*secrets.Secret, error) {
	domainReq := secrets.SetRequest{
		InstanceID: req.InstanceID,
		Key:        req.Key,
		Value:      req.Value,
		Type:       secrets.SecretType(req.Type),
	}

	secret, err := a.cp.Secrets.Set(ctx.Context(), domainReq)
	if err != nil {
		return nil, mapError(err)
	}

	_ = ctx.JSON(http.StatusCreated, secret)

	//nolint:nilnil // response already written via ctx.JSON/ctx.NoContent.
	return nil, nil
}

// listSecrets handles GET /v1/instances/:instanceId/secrets.
func (a *API) listSecrets(ctx forge.Context, req *ListSecretsRequest) ([]secrets.Secret, error) {
	list, err := a.cp.Secrets.List(ctx.Context(), req.InstanceID)
	if err != nil {
		return nil, mapError(err)
	}

	return list, nil
}

// deleteSecret handles DELETE /v1/instances/:instanceId/secrets/:key.
func (a *API) deleteSecret(ctx forge.Context, req *DeleteSecretRequest) (*secrets.Secret, error) {
	if err := a.cp.Secrets.Delete(ctx.Context(), req.InstanceID, req.Key); err != nil {
		return nil, mapError(err)
	}

	_ = ctx.NoContent(http.StatusNoContent)

	//nolint:nilnil // response already written via ctx.JSON/ctx.NoContent.
	return nil, nil
}
