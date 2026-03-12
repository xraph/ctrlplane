package network

import (
	"context"

	"github.com/xraph/ctrlplane/id"
)

// Router abstracts traffic routing implementation.
// Implement for your load balancer or ingress controller.
type Router interface {
	// AddRoute configures a route to an instance.
	AddRoute(ctx context.Context, route *Route) error

	// RemoveRoute removes a route.
	RemoveRoute(ctx context.Context, routeID id.ID) error

	// UpdateRoute modifies an existing route.
	UpdateRoute(ctx context.Context, route *Route) error

	// AddDomain configures a custom domain.
	AddDomain(ctx context.Context, domain *Domain) error

	// RemoveDomain removes a custom domain.
	RemoveDomain(ctx context.Context, domainID id.ID) error

	// ProvisionCert obtains or renews a TLS certificate.
	ProvisionCert(ctx context.Context, domain *Domain) (*Certificate, error)
}

// GatewayRouter extends Router with datacenter-aware gateway operations.
// Implement this interface for cross-datacenter traffic management.
// Phase 2: a Forge extension will provide the concrete implementation.
type GatewayRouter interface {
	Router

	// SyncRoutes synchronises all routes for a datacenter with the gateway.
	SyncRoutes(ctx context.Context, datacenterID id.ID) error

	// SetBackendHealth marks an instance backend as healthy or unhealthy.
	SetBackendHealth(ctx context.Context, instanceID id.ID, healthy bool) error

	// GetGatewayStatus returns the current operational status of the gateway.
	GetGatewayStatus(ctx context.Context) (*GatewayStatus, error)
}

// GatewayStatus describes the operational state of a gateway.
type GatewayStatus struct {
	Healthy       bool              `json:"healthy"`
	ActiveRoutes  int               `json:"active_routes"`
	ActiveDomains int               `json:"active_domains"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}
