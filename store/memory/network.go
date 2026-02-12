package memory

import (
	"context"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/network"
)

func (s *Store) InsertDomain(_ context.Context, domain *network.Domain) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(domain.ID)
	if _, exists := s.domains[key]; exists {
		return fmt.Errorf("%w: domain %s", ctrlplane.ErrAlreadyExists, key)
	}

	clone := *domain
	s.domains[key] = &clone

	return nil
}

func (s *Store) GetDomain(_ context.Context, tenantID string, domainID id.ID) (*network.Domain, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	d, ok := s.domains[idStr(domainID)]
	if !ok || d.TenantID != tenantID {
		return nil, fmt.Errorf("%w: domain %s", ctrlplane.ErrNotFound, domainID)
	}

	clone := *d

	return &clone, nil
}

func (s *Store) GetDomainByHostname(_ context.Context, hostname string) (*network.Domain, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, d := range s.domains {
		if d.Hostname == hostname {
			clone := *d

			return &clone, nil
		}
	}

	return nil, fmt.Errorf("%w: hostname %s", ctrlplane.ErrNotFound, hostname)
}

func (s *Store) ListDomains(_ context.Context, tenantID string, instanceID id.ID) ([]network.Domain, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	instKey := idStr(instanceID)

	var result []network.Domain

	for _, d := range s.domains {
		if d.TenantID == tenantID && idStr(d.InstanceID) == instKey {
			result = append(result, *d)
		}
	}

	return result, nil
}

func (s *Store) UpdateDomain(_ context.Context, domain *network.Domain) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(domain.ID)
	if _, ok := s.domains[key]; !ok {
		return fmt.Errorf("%w: domain %s", ctrlplane.ErrNotFound, key)
	}

	domain.UpdatedAt = now()
	clone := *domain
	s.domains[key] = &clone

	return nil
}

func (s *Store) DeleteDomain(_ context.Context, tenantID string, domainID id.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(domainID)

	d, ok := s.domains[key]
	if !ok || d.TenantID != tenantID {
		return fmt.Errorf("%w: domain %s", ctrlplane.ErrNotFound, key)
	}

	delete(s.domains, key)

	return nil
}

func (s *Store) InsertRoute(_ context.Context, route *network.Route) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(route.ID)
	if _, exists := s.routes[key]; exists {
		return fmt.Errorf("%w: route %s", ctrlplane.ErrAlreadyExists, key)
	}

	clone := *route
	s.routes[key] = &clone

	return nil
}

func (s *Store) GetRoute(_ context.Context, tenantID string, routeID id.ID) (*network.Route, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	r, ok := s.routes[idStr(routeID)]
	if !ok || r.TenantID != tenantID {
		return nil, fmt.Errorf("%w: route %s", ctrlplane.ErrNotFound, routeID)
	}

	clone := *r

	return &clone, nil
}

func (s *Store) ListRoutes(_ context.Context, tenantID string, instanceID id.ID) ([]network.Route, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	instKey := idStr(instanceID)

	var result []network.Route

	for _, r := range s.routes {
		if r.TenantID == tenantID && idStr(r.InstanceID) == instKey {
			result = append(result, *r)
		}
	}

	return result, nil
}

func (s *Store) UpdateRoute(_ context.Context, route *network.Route) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(route.ID)
	if _, ok := s.routes[key]; !ok {
		return fmt.Errorf("%w: route %s", ctrlplane.ErrNotFound, key)
	}

	route.UpdatedAt = now()
	clone := *route
	s.routes[key] = &clone

	return nil
}

func (s *Store) DeleteRoute(_ context.Context, tenantID string, routeID id.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(routeID)

	r, ok := s.routes[key]
	if !ok || r.TenantID != tenantID {
		return fmt.Errorf("%w: route %s", ctrlplane.ErrNotFound, key)
	}

	delete(s.routes, key)

	return nil
}

func (s *Store) InsertCertificate(_ context.Context, cert *network.Certificate) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(cert.ID)
	if _, exists := s.certificates[key]; exists {
		return fmt.Errorf("%w: certificate %s", ctrlplane.ErrAlreadyExists, key)
	}

	clone := *cert
	s.certificates[key] = &clone

	return nil
}

func (s *Store) GetCertificate(_ context.Context, tenantID string, certID id.ID) (*network.Certificate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c, ok := s.certificates[idStr(certID)]
	if !ok || c.TenantID != tenantID {
		return nil, fmt.Errorf("%w: certificate %s", ctrlplane.ErrNotFound, certID)
	}

	clone := *c

	return &clone, nil
}

func (s *Store) ListCertificates(_ context.Context, tenantID string, instanceID id.ID) ([]network.Certificate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	instKey := idStr(instanceID)

	var result []network.Certificate

	for _, c := range s.certificates {
		if c.TenantID != tenantID {
			continue
		}

		d, ok := s.domains[idStr(c.DomainID)]
		if !ok || idStr(d.InstanceID) != instKey {
			continue
		}

		result = append(result, *c)
	}

	return result, nil
}

func (s *Store) UpdateCertificate(_ context.Context, cert *network.Certificate) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(cert.ID)
	if _, ok := s.certificates[key]; !ok {
		return fmt.Errorf("%w: certificate %s", ctrlplane.ErrNotFound, key)
	}

	cert.UpdatedAt = now()
	clone := *cert
	s.certificates[key] = &clone

	return nil
}

func (s *Store) DeleteCertificate(_ context.Context, tenantID string, certID id.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := idStr(certID)

	c, ok := s.certificates[key]
	if !ok || c.TenantID != tenantID {
		return fmt.Errorf("%w: certificate %s", ctrlplane.ErrNotFound, key)
	}

	delete(s.certificates, key)

	return nil
}

func (s *Store) CountDomainsByTenant(_ context.Context, tenantID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0

	for _, d := range s.domains {
		if d.TenantID == tenantID {
			count++
		}
	}

	return count, nil
}
