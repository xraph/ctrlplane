package api

import (
	"errors"
	"net/http"

	"github.com/xraph/forge"

	ctrlplane "github.com/xraph/ctrlplane"
)

// mapError converts ctrlplane sentinel errors to Forge HTTP errors.
func mapError(err error) error {
	switch {
	case errors.Is(err, ctrlplane.ErrNotFound):
		return forge.NotFound(err.Error())
	case errors.Is(err, ctrlplane.ErrAlreadyExists):
		return forge.NewHTTPError(http.StatusConflict, err.Error())
	case errors.Is(err, ctrlplane.ErrInvalidState):
		return forge.NewHTTPError(http.StatusConflict, err.Error())
	case errors.Is(err, ctrlplane.ErrUnauthorized):
		return forge.Unauthorized(err.Error())
	case errors.Is(err, ctrlplane.ErrForbidden):
		return forge.Forbidden(err.Error())
	case errors.Is(err, ctrlplane.ErrQuotaExceeded):
		return forge.NewHTTPError(http.StatusTooManyRequests, err.Error())
	case errors.Is(err, ctrlplane.ErrProviderNotFound):
		return forge.BadRequest(err.Error())
	case errors.Is(err, ctrlplane.ErrProviderUnavail):
		return forge.NewHTTPError(http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, ctrlplane.ErrDeploymentFailed):
		return forge.InternalError(err)
	case errors.Is(err, ctrlplane.ErrInvalidConfig):
		return forge.BadRequest(err.Error())
	default:
		return forge.InternalError(err)
	}
}
