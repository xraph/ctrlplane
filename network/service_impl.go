package network

import (
	"context"
	"errors"
	"fmt"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
)

// service implements the Service interface.
type service struct {
	store  Store
	router Router
	events event.Bus
	auth   auth.Provider
}

// NewService creates a new network service.
func NewService(store Store, router Router, events event.Bus, auth auth.Provider) Service {
	return &service{
		store:  store,
		router: router,
		events: events,
		auth:   auth,
	}
}

// AddDomain registers a custom domain for an instance.
func (s *service) AddDomain(ctx context.Context, req AddDomainRequest) (*Domain, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("add domain: %w", err)
	}

	domain := &Domain{
		Entity:      ctrlplane.NewEntity(id.PrefixDomain),
		TenantID:    claims.TenantID,
		InstanceID:  req.InstanceID,
		Hostname:    req.Hostname,
		Verified:    false,
		TLSEnabled:  req.TLSEnabled,
		VerifyToken: id.New(id.PrefixDomain).String(),
	}

	if err := s.store.InsertDomain(ctx, domain); err != nil {
		return nil, fmt.Errorf("add domain: insert: %w", err)
	}

	if s.router != nil {
		if err := s.router.AddDomain(ctx, domain); err != nil {
			return nil, fmt.Errorf("add domain: router: %w", err)
		}
	}

	// Fire-and-forget event.
	_ = s.events.Publish(ctx, event.NewEvent(event.DomainAdded, claims.TenantID).
		WithInstance(req.InstanceID).
		WithActor(claims.SubjectID).
		WithPayload(map[string]any{
			"domain_id": domain.ID.String(),
			"hostname":  req.Hostname,
		}))

	return domain, nil
}

// VerifyDomain confirms DNS ownership of a domain.
func (s *service) VerifyDomain(ctx context.Context, domainID id.ID) (*Domain, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("verify domain: %w", err)
	}

	domain, err := s.store.GetDomain(ctx, claims.TenantID, domainID)
	if err != nil {
		return nil, fmt.Errorf("verify domain: get: %w", err)
	}

	domain.Verified = true
	domain.UpdatedAt = time.Now().UTC()

	if err := s.store.UpdateDomain(ctx, domain); err != nil {
		return nil, fmt.Errorf("verify domain: update: %w", err)
	}

	// Fire-and-forget event.
	_ = s.events.Publish(ctx, event.NewEvent(event.DomainVerified, claims.TenantID).
		WithInstance(domain.InstanceID).
		WithActor(claims.SubjectID).
		WithPayload(map[string]any{
			"domain_id": domain.ID.String(),
			"hostname":  domain.Hostname,
		}))

	return domain, nil
}

// RemoveDomain removes a custom domain.
func (s *service) RemoveDomain(ctx context.Context, domainID id.ID) error {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return fmt.Errorf("remove domain: %w", err)
	}

	domain, err := s.store.GetDomain(ctx, claims.TenantID, domainID)
	if err != nil {
		return fmt.Errorf("remove domain: get: %w", err)
	}

	if err := s.store.DeleteDomain(ctx, claims.TenantID, domainID); err != nil {
		return fmt.Errorf("remove domain: delete: %w", err)
	}

	if s.router != nil {
		if err := s.router.RemoveDomain(ctx, domainID); err != nil {
			return fmt.Errorf("remove domain: router: %w", err)
		}
	}

	// Fire-and-forget event.
	_ = s.events.Publish(ctx, event.NewEvent(event.DomainRemoved, claims.TenantID).
		WithInstance(domain.InstanceID).
		WithActor(claims.SubjectID).
		WithPayload(map[string]any{
			"domain_id": domain.ID.String(),
			"hostname":  domain.Hostname,
		}))

	return nil
}

// ListDomains returns all domains for an instance.
func (s *service) ListDomains(ctx context.Context, instanceID id.ID) ([]Domain, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}

	domains, err := s.store.ListDomains(ctx, claims.TenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}

	return domains, nil
}

// AddRoute creates a traffic route to an instance.
func (s *service) AddRoute(ctx context.Context, req AddRouteRequest) (*Route, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("add route: %w", err)
	}

	protocol := req.Protocol
	if protocol == "" {
		protocol = "http"
	}

	weight := req.Weight
	if weight == 0 {
		weight = 100
	}

	route := &Route{
		Entity:     ctrlplane.NewEntity(id.PrefixRoute),
		TenantID:   claims.TenantID,
		InstanceID: req.InstanceID,
		Path:       req.Path,
		Port:       req.Port,
		Protocol:   protocol,
		Weight:     weight,
	}

	if err := s.store.InsertRoute(ctx, route); err != nil {
		return nil, fmt.Errorf("add route: insert: %w", err)
	}

	if s.router != nil {
		if err := s.router.AddRoute(ctx, route); err != nil {
			return nil, fmt.Errorf("add route: router: %w", err)
		}
	}

	return route, nil
}

// UpdateRoute modifies an existing route.
func (s *service) UpdateRoute(ctx context.Context, routeID id.ID, req UpdateRouteRequest) (*Route, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("update route: %w", err)
	}

	route, err := s.store.GetRoute(ctx, claims.TenantID, routeID)
	if err != nil {
		return nil, fmt.Errorf("update route: get: %w", err)
	}

	if req.Path != nil {
		route.Path = *req.Path
	}

	if req.Weight != nil {
		route.Weight = *req.Weight
	}

	if req.StripPrefix != nil {
		route.StripPrefix = *req.StripPrefix
	}

	route.UpdatedAt = time.Now().UTC()

	if err := s.store.UpdateRoute(ctx, route); err != nil {
		return nil, fmt.Errorf("update route: store: %w", err)
	}

	if s.router != nil {
		if err := s.router.UpdateRoute(ctx, route); err != nil {
			return nil, fmt.Errorf("update route: router: %w", err)
		}
	}

	return route, nil
}

// RemoveRoute removes a traffic route.
func (s *service) RemoveRoute(ctx context.Context, routeID id.ID) error {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return fmt.Errorf("remove route: %w", err)
	}

	_, err = s.store.GetRoute(ctx, claims.TenantID, routeID)
	if err != nil {
		return fmt.Errorf("remove route: get: %w", err)
	}

	if err := s.store.DeleteRoute(ctx, claims.TenantID, routeID); err != nil {
		return fmt.Errorf("remove route: delete: %w", err)
	}

	if s.router != nil {
		if err := s.router.RemoveRoute(ctx, routeID); err != nil {
			return fmt.Errorf("remove route: router: %w", err)
		}
	}

	return nil
}

// ListRoutes returns all routes for an instance.
func (s *service) ListRoutes(ctx context.Context, instanceID id.ID) ([]Route, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("list routes: %w", err)
	}

	routes, err := s.store.ListRoutes(ctx, claims.TenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("list routes: %w", err)
	}

	return routes, nil
}

// ProvisionCert obtains or renews a TLS certificate for a domain.
func (s *service) ProvisionCert(ctx context.Context, domainID id.ID) (*Certificate, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("provision cert: %w", err)
	}

	domain, err := s.store.GetDomain(ctx, claims.TenantID, domainID)
	if err != nil {
		return nil, fmt.Errorf("provision cert: get domain: %w", err)
	}

	if s.router == nil {
		return nil, errors.New("provision cert: no router configured")
	}

	cert, err := s.router.ProvisionCert(ctx, domain)
	if err != nil {
		return nil, fmt.Errorf("provision cert: router: %w", err)
	}

	if err := s.store.InsertCertificate(ctx, cert); err != nil {
		return nil, fmt.Errorf("provision cert: insert: %w", err)
	}

	// Fire-and-forget event.
	_ = s.events.Publish(ctx, event.NewEvent(event.CertProvisioned, claims.TenantID).
		WithInstance(domain.InstanceID).
		WithActor(claims.SubjectID).
		WithPayload(map[string]any{
			"domain_id":      domain.ID.String(),
			"certificate_id": cert.ID.String(),
			"hostname":       domain.Hostname,
		}))

	return cert, nil
}

// ListCerts returns all certificates for an instance.
func (s *service) ListCerts(ctx context.Context, instanceID id.ID) ([]Certificate, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("list certs: %w", err)
	}

	certs, err := s.store.ListCertificates(ctx, claims.TenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("list certs: %w", err)
	}

	return certs, nil
}
