package network

import (
	"context"

	"github.com/xraph/ctrlplane/id"
)

// Service manages domains, routes, and certificates for instances.
type Service interface {
	// AddDomain registers a custom domain for an instance.
	AddDomain(ctx context.Context, req AddDomainRequest) (*Domain, error)

	// VerifyDomain confirms DNS ownership of a domain.
	VerifyDomain(ctx context.Context, domainID id.ID) (*Domain, error)

	// RemoveDomain removes a custom domain.
	RemoveDomain(ctx context.Context, domainID id.ID) error

	// ListDomains returns all domains for an instance.
	ListDomains(ctx context.Context, instanceID id.ID) ([]Domain, error)

	// AddRoute creates a traffic route to an instance.
	AddRoute(ctx context.Context, req AddRouteRequest) (*Route, error)

	// UpdateRoute modifies an existing route.
	UpdateRoute(ctx context.Context, routeID id.ID, req UpdateRouteRequest) (*Route, error)

	// RemoveRoute removes a traffic route.
	RemoveRoute(ctx context.Context, routeID id.ID) error

	// ListRoutes returns all routes for an instance.
	ListRoutes(ctx context.Context, instanceID id.ID) ([]Route, error)

	// ProvisionCert obtains or renews a TLS certificate for a domain.
	ProvisionCert(ctx context.Context, domainID id.ID) (*Certificate, error)

	// ListCerts returns all certificates for an instance.
	ListCerts(ctx context.Context, instanceID id.ID) ([]Certificate, error)
}

// AddDomainRequest holds the parameters for adding a custom domain.
type AddDomainRequest struct {
	InstanceID id.ID  `json:"instance_id" validate:"required"`
	Hostname   string `json:"hostname"    validate:"required,fqdn"`
	TLSEnabled bool   `json:"tls_enabled"`
}

// AddRouteRequest holds the parameters for creating a traffic route.
//
// ServiceName optionally targets a specific service inside a
// multi-service instance — leave empty to route to the instance's
// Main service (the default for single-service workloads).
//
// Hostname optionally scopes the route to a single host — leave empty
// to answer on every host the gateway listener serves.
type AddRouteRequest struct {
	InstanceID        id.ID  `json:"instance_id"                   validate:"required"`
	ServiceName       string `json:"service_name,omitempty"`
	Path              string `json:"path"                          validate:"required"`
	Port              int    `json:"port"                          validate:"required"`
	Protocol          string `default:"http"                       json:"protocol"`
	Weight            int    `default:"100"                        json:"weight"`
	StripPrefix       bool   `json:"strip_prefix,omitempty"`
	RewriteRedirects  bool   `json:"rewrite_redirects,omitempty"`
	RewriteCookiePath bool   `json:"rewrite_cookie_path,omitempty"`
	UpstreamOrigin    string `json:"upstream_origin,omitempty"`
	TLSVerify         *bool  `json:"tls_verify,omitempty"`
	Hostname          string `json:"hostname,omitempty"`
}

// UpdateRouteRequest holds the parameters for modifying a route. Every
// field is a pointer so an omitted key leaves the stored value alone.
//
// The proxy fields mirror AddRouteRequest. UpstreamOrigin is a *string
// rather than a string so a caller can tell "leave it" (nil) apart from
// "clear it and go back to the in-cluster backend" (pointer to "").
//
// Clearing the origin also resets TLSVerify to true, even against an
// explicit tls_verify=false in the same request. Verification can be
// turned off again once a new origin is set.
type UpdateRouteRequest struct {
	ServiceName *string `json:"service_name,omitempty"`
	Hostname    *string `json:"hostname,omitempty"`
	Path        *string `json:"path,omitempty"`
	Weight      *int    `json:"weight,omitempty"`
	StripPrefix *bool   `json:"strip_prefix,omitempty"`

	RewriteRedirects  *bool   `json:"rewrite_redirects,omitempty"`
	RewriteCookiePath *bool   `json:"rewrite_cookie_path,omitempty"`
	UpstreamOrigin    *string `json:"upstream_origin,omitempty"`
	TLSVerify         *bool   `json:"tls_verify,omitempty"`
}
