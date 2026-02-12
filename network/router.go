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
