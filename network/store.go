package network

import (
	"context"

	"github.com/xraph/ctrlplane/id"
)

// Store is the persistence interface for domains, routes, and certificates.
type Store interface {
	// InsertDomain persists a new domain.
	InsertDomain(ctx context.Context, domain *Domain) error

	// GetDomain retrieves a domain by ID.
	GetDomain(ctx context.Context, tenantID string, domainID id.ID) (*Domain, error)

	// GetDomainByHostname retrieves a domain by its hostname.
	GetDomainByHostname(ctx context.Context, hostname string) (*Domain, error)

	// ListDomains returns all domains for an instance.
	ListDomains(ctx context.Context, tenantID string, instanceID id.ID) ([]Domain, error)

	// UpdateDomain persists changes to a domain.
	UpdateDomain(ctx context.Context, domain *Domain) error

	// DeleteDomain removes a domain.
	DeleteDomain(ctx context.Context, tenantID string, domainID id.ID) error

	// InsertRoute persists a new route.
	InsertRoute(ctx context.Context, route *Route) error

	// GetRoute retrieves a route by ID.
	GetRoute(ctx context.Context, tenantID string, routeID id.ID) (*Route, error)

	// ListRoutes returns all routes for an instance.
	ListRoutes(ctx context.Context, tenantID string, instanceID id.ID) ([]Route, error)

	// UpdateRoute persists changes to a route.
	UpdateRoute(ctx context.Context, route *Route) error

	// DeleteRoute removes a route.
	DeleteRoute(ctx context.Context, tenantID string, routeID id.ID) error

	// InsertCertificate persists a new certificate.
	InsertCertificate(ctx context.Context, cert *Certificate) error

	// GetCertificate retrieves a certificate by ID.
	GetCertificate(ctx context.Context, tenantID string, certID id.ID) (*Certificate, error)

	// ListCertificates returns all certificates for an instance.
	ListCertificates(ctx context.Context, tenantID string, instanceID id.ID) ([]Certificate, error)

	// UpdateCertificate persists changes to a certificate.
	UpdateCertificate(ctx context.Context, cert *Certificate) error

	// DeleteCertificate removes a certificate.
	DeleteCertificate(ctx context.Context, tenantID string, certID id.ID) error

	// CountDomainsByTenant returns the number of domains for a tenant.
	CountDomainsByTenant(ctx context.Context, tenantID string) (int, error)
}
