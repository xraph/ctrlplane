package network

import (
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// Domain represents a custom domain bound to an instance.
type Domain struct {
	ctrlplane.Entity

	TenantID    string     `db:"tenant_id"    json:"tenant_id"`
	InstanceID  id.ID      `db:"instance_id"  json:"instance_id"`
	Hostname    string     `db:"hostname"     json:"hostname"`
	Verified    bool       `db:"verified"     json:"verified"`
	TLSEnabled  bool       `db:"tls_enabled"  json:"tls_enabled"`
	CertExpiry  *time.Time `db:"cert_expiry"  json:"cert_expiry,omitempty"`
	DNSTarget   string     `db:"dns_target"   json:"dns_target"`
	VerifyToken string     `db:"verify_token" json:"verify_token"`
}

// Route maps traffic from an endpoint to an instance. ServiceName
// optionally targets a specific service inside a multi-service
// instance — empty resolves to the instance's Main service so single-
// service workloads keep working without explicit configuration.
type Route struct {
	ctrlplane.Entity

	TenantID    string `db:"tenant_id"    json:"tenant_id"`
	InstanceID  id.ID  `db:"instance_id"  json:"instance_id"`
	ServiceName string `db:"service_name" json:"service_name,omitempty"`
	Path        string `db:"path"         json:"path"`
	Port        int    `db:"port"         json:"port"`
	Protocol    string `db:"protocol"     json:"protocol"`
	Weight      int    `db:"weight"       json:"weight"`
	StripPrefix bool   `db:"strip_prefix" json:"strip_prefix"`
	// Hostname, when set, scopes the route to a single host (the
	// workspace's API hostname). The OctopusRouter uses it as the
	// Gateway API HTTPRoute's `hostnames` entry so per-workspace path
	// routes don't collide on the shared *.api wildcard listener.
	// Transient: carried from AddRouteRequest to the router at create
	// time; not persisted (stores map via their own models).
	Hostname string `json:"hostname,omitempty"`
}

// Certificate holds TLS certificate state.
type Certificate struct {
	ctrlplane.Entity

	DomainID  id.ID     `db:"domain_id"  json:"domain_id"`
	TenantID  string    `db:"tenant_id"  json:"tenant_id"`
	Issuer    string    `db:"issuer"     json:"issuer"`
	ExpiresAt time.Time `db:"expires_at" json:"expires_at"`
	AutoRenew bool      `db:"auto_renew" json:"auto_renew"`
}

// SelectEndpoint picks the endpoint within `endpoints` that should
// receive traffic for `route`. Selection rules, in priority order:
//
//  1. Exact match on (ServiceName, Port). When the route names a
//     service AND a port, only an endpoint with both fields matching
//     is eligible.
//  2. ServiceName match on any port. When the route names a service
//     but the listed port doesn't match a published endpoint, fall
//     through to any endpoint owned by that service.
//  3. Port match on any service. When the route doesn't name a
//     service (legacy single-service routes), pick the first endpoint
//     publishing the right port.
//  4. First endpoint as a last resort. Single-endpoint instances and
//     legacy routes that don't specify a port still resolve to
//     "the obvious one".
//
// Returns nil when `endpoints` is empty.
func SelectEndpoint(route *Route, endpoints []provider.Endpoint) *provider.Endpoint {
	if len(endpoints) == 0 {
		return nil
	}

	if route != nil && route.ServiceName != "" {
		for i := range endpoints {
			if endpoints[i].ServiceName == route.ServiceName && (route.Port == 0 || endpoints[i].Port == route.Port) {
				return &endpoints[i]
			}
		}
		// Service named but no port match — fall back to any endpoint
		// owned by that service rather than picking another service's.
		for i := range endpoints {
			if endpoints[i].ServiceName == route.ServiceName {
				return &endpoints[i]
			}
		}
	}

	if route != nil && route.Port > 0 {
		for i := range endpoints {
			if endpoints[i].Port == route.Port {
				return &endpoints[i]
			}
		}
	}

	return &endpoints[0]
}
