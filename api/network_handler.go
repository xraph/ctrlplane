package api

import (
	"net/http"

	"github.com/xraph/ctrlplane/network"
)

// AddDomain registers a custom domain for an instance.
func (a *API) AddDomain(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	var req network.AddDomainRequest

	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	req.InstanceID = instanceID

	domain, err := a.cp.Network.AddDomain(r.Context(), req)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusCreated, domain)
}

// ListDomains returns all domains for an instance.
func (a *API) ListDomains(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	domains, err := a.cp.Network.ListDomains(r.Context(), instanceID)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, domains)
}

// VerifyDomain confirms DNS ownership of a domain.
func (a *API) VerifyDomain(w http.ResponseWriter, r *http.Request) {
	domainID, err := parseID(r, "domainID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	domain, err := a.cp.Network.VerifyDomain(r.Context(), domainID)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, domain)
}

// RemoveDomain removes a custom domain.
func (a *API) RemoveDomain(w http.ResponseWriter, r *http.Request) {
	domainID, err := parseID(r, "domainID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	if err := a.cp.Network.RemoveDomain(r.Context(), domainID); err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// AddRoute creates a traffic route to an instance.
func (a *API) AddRoute(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	var req network.AddRouteRequest

	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	req.InstanceID = instanceID

	route, err := a.cp.Network.AddRoute(r.Context(), req)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusCreated, route)
}

// ListRoutes returns all routes for an instance.
func (a *API) ListRoutes(w http.ResponseWriter, r *http.Request) {
	instanceID, err := parseID(r, "instanceID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	routes, err := a.cp.Network.ListRoutes(r.Context(), instanceID)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, routes)
}

// UpdateRoute modifies an existing route.
func (a *API) UpdateRoute(w http.ResponseWriter, r *http.Request) {
	routeID, err := parseID(r, "routeID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	var req network.UpdateRouteRequest

	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	route, err := a.cp.Network.UpdateRoute(r.Context(), routeID, req)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusOK, route)
}

// RemoveRoute removes a traffic route.
func (a *API) RemoveRoute(w http.ResponseWriter, r *http.Request) {
	routeID, err := parseID(r, "routeID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	if err := a.cp.Network.RemoveRoute(r.Context(), routeID); err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ProvisionCert obtains or renews a TLS certificate for a domain.
func (a *API) ProvisionCert(w http.ResponseWriter, r *http.Request) {
	domainID, err := parseID(r, "domainID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)

		return
	}

	cert, err := a.cp.Network.ProvisionCert(r.Context(), domainID)
	if err != nil {
		writeError(w, errorStatus(err), err)

		return
	}

	writeJSON(w, http.StatusCreated, cert)
}
