package api

import (
	"net/http"

	"github.com/xraph/forge"

	"github.com/xraph/ctrlplane/network"
)

// addDomain handles POST /v1/instances/:instanceID/domains.
func (a *API) addDomain(ctx forge.Context, req *AddDomainAPIRequest) (*network.Domain, error) {
	domainReq := network.AddDomainRequest{
		InstanceID: req.InstanceID,
		Hostname:   req.Hostname,
		TLSEnabled: req.TLSEnabled,
	}

	domain, err := a.cp.Network.AddDomain(ctx.Context(), domainReq)
	if err != nil {
		return nil, mapError(err)
	}

	_ = ctx.JSON(http.StatusCreated, domain)

	//nolint:nilnil // response already written via ctx.JSON/ctx.NoContent.
	return nil, nil
}

// listDomains handles GET /v1/instances/:instanceID/domains.
func (a *API) listDomains(ctx forge.Context, req *ListDomainsRequest) ([]network.Domain, error) {
	domains, err := a.cp.Network.ListDomains(ctx.Context(), req.InstanceID)
	if err != nil {
		return nil, mapError(err)
	}

	return domains, nil
}

// verifyDomain handles POST /v1/domains/:domainID/verify.
func (a *API) verifyDomain(ctx forge.Context, req *VerifyDomainRequest) (*network.Domain, error) {
	domain, err := a.cp.Network.VerifyDomain(ctx.Context(), req.DomainID)
	if err != nil {
		return nil, mapError(err)
	}

	return domain, nil
}

// removeDomain handles DELETE /v1/domains/:domainID.
func (a *API) removeDomain(ctx forge.Context, req *RemoveDomainRequest) (*network.Domain, error) {
	if err := a.cp.Network.RemoveDomain(ctx.Context(), req.DomainID); err != nil {
		return nil, mapError(err)
	}

	_ = ctx.NoContent(http.StatusNoContent)

	//nolint:nilnil // response already written via ctx.JSON/ctx.NoContent.
	return nil, nil
}

// addRoute handles POST /v1/instances/:instanceID/routes.
func (a *API) addRoute(ctx forge.Context, req *AddRouteAPIRequest) (*network.Route, error) {
	routeReq := network.AddRouteRequest{
		InstanceID: req.InstanceID,
		Path:       req.Path,
		Port:       req.Port,
		Protocol:   req.Protocol,
		Weight:     req.Weight,
	}

	route, err := a.cp.Network.AddRoute(ctx.Context(), routeReq)
	if err != nil {
		return nil, mapError(err)
	}

	_ = ctx.JSON(http.StatusCreated, route)

	//nolint:nilnil // response already written via ctx.JSON/ctx.NoContent.
	return nil, nil
}

// listRoutes handles GET /v1/instances/:instanceID/routes.
func (a *API) listRoutes(ctx forge.Context, req *ListRoutesRequest) ([]network.Route, error) {
	routes, err := a.cp.Network.ListRoutes(ctx.Context(), req.InstanceID)
	if err != nil {
		return nil, mapError(err)
	}

	return routes, nil
}

// updateRoute handles PATCH /v1/routes/:routeID.
func (a *API) updateRoute(ctx forge.Context, req *UpdateRouteAPIRequest) (*network.Route, error) {
	updateReq := network.UpdateRouteRequest{
		Path:        req.Path,
		Weight:      req.Weight,
		StripPrefix: req.StripPrefix,
	}

	route, err := a.cp.Network.UpdateRoute(ctx.Context(), req.RouteID, updateReq)
	if err != nil {
		return nil, mapError(err)
	}

	return route, nil
}

// removeRoute handles DELETE /v1/routes/:routeID.
func (a *API) removeRoute(ctx forge.Context, req *RemoveRouteRequest) (*network.Route, error) {
	if err := a.cp.Network.RemoveRoute(ctx.Context(), req.RouteID); err != nil {
		return nil, mapError(err)
	}

	_ = ctx.NoContent(http.StatusNoContent)

	//nolint:nilnil // response already written via ctx.JSON/ctx.NoContent.
	return nil, nil
}

// provisionCert handles POST /v1/domains/:domainID/cert.
func (a *API) provisionCert(ctx forge.Context, req *ProvisionCertRequest) (*network.Certificate, error) {
	cert, err := a.cp.Network.ProvisionCert(ctx.Context(), req.DomainID)
	if err != nil {
		return nil, mapError(err)
	}

	_ = ctx.JSON(http.StatusCreated, cert)

	//nolint:nilnil // response already written via ctx.JSON/ctx.NoContent.
	return nil, nil
}
