package postgres

import (
	"context"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/network"
)

func (s *Store) InsertDomain(ctx context.Context, domain *network.Domain) error {
	model := toDomainModel(domain)

	_, err := s.pg.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: insert domain failed: %w", err)
	}

	return nil
}

func (s *Store) GetDomain(ctx context.Context, tenantID string, domainID id.ID) (*network.Domain, error) {
	var model domainModel

	err := s.pg.NewSelect(&model).
		Where("id = $1 AND tenant_id = $2", domainID.String(), tenantID).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: domain %s", ctrlplane.ErrNotFound, domainID)
		}

		return nil, fmt.Errorf("postgres: get domain failed: %w", err)
	}

	return fromDomainModel(&model), nil
}

func (s *Store) GetDomainByHostname(ctx context.Context, hostname string) (*network.Domain, error) {
	var model domainModel

	err := s.pg.NewSelect(&model).
		Where("hostname = $1", hostname).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: hostname %s", ctrlplane.ErrNotFound, hostname)
		}

		return nil, fmt.Errorf("postgres: get domain by hostname failed: %w", err)
	}

	return fromDomainModel(&model), nil
}

func (s *Store) ListDomains(ctx context.Context, tenantID string, instanceID id.ID) ([]network.Domain, error) {
	var models []domainModel

	err := s.pg.NewSelect(&models).
		Where("tenant_id = $1 AND instance_id = $2", tenantID, instanceID.String()).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: list domains failed: %w", err)
	}

	items := make([]network.Domain, 0, len(models))
	for i := range models {
		items = append(items, *fromDomainModel(&models[i]))
	}

	return items, nil
}

func (s *Store) UpdateDomain(ctx context.Context, domain *network.Domain) error {
	domain.UpdatedAt = now()
	model := toDomainModel(domain)

	res, err := s.pg.NewUpdate(model).WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: update domain failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: domain %s", ctrlplane.ErrNotFound, domain.ID)
	}

	return nil
}

func (s *Store) DeleteDomain(ctx context.Context, tenantID string, domainID id.ID) error {
	res, err := s.pg.NewDelete((*domainModel)(nil)).
		Where("id = $1 AND tenant_id = $2", domainID.String(), tenantID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: delete domain failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: domain %s", ctrlplane.ErrNotFound, domainID)
	}

	return nil
}

func (s *Store) InsertRoute(ctx context.Context, route *network.Route) error {
	model := toRouteModel(route)

	_, err := s.pg.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: insert route failed: %w", err)
	}

	return nil
}

func (s *Store) GetRoute(ctx context.Context, tenantID string, routeID id.ID) (*network.Route, error) {
	var model routeModel

	err := s.pg.NewSelect(&model).
		Where("id = $1 AND tenant_id = $2", routeID.String(), tenantID).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: route %s", ctrlplane.ErrNotFound, routeID)
		}

		return nil, fmt.Errorf("postgres: get route failed: %w", err)
	}

	return fromRouteModel(&model), nil
}

func (s *Store) ListRoutes(ctx context.Context, tenantID string, instanceID id.ID) ([]network.Route, error) {
	var models []routeModel

	err := s.pg.NewSelect(&models).
		Where("tenant_id = $1 AND instance_id = $2", tenantID, instanceID.String()).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: list routes failed: %w", err)
	}

	items := make([]network.Route, 0, len(models))
	for i := range models {
		items = append(items, *fromRouteModel(&models[i]))
	}

	return items, nil
}

func (s *Store) UpdateRoute(ctx context.Context, route *network.Route) error {
	route.UpdatedAt = now()
	model := toRouteModel(route)

	res, err := s.pg.NewUpdate(model).WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: update route failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: route %s", ctrlplane.ErrNotFound, route.ID)
	}

	return nil
}

func (s *Store) DeleteRoute(ctx context.Context, tenantID string, routeID id.ID) error {
	res, err := s.pg.NewDelete((*routeModel)(nil)).
		Where("id = $1 AND tenant_id = $2", routeID.String(), tenantID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: delete route failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: route %s", ctrlplane.ErrNotFound, routeID)
	}

	return nil
}

func (s *Store) InsertCertificate(ctx context.Context, cert *network.Certificate) error {
	model := toCertificateModel(cert)

	_, err := s.pg.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: insert certificate failed: %w", err)
	}

	return nil
}

func (s *Store) GetCertificate(ctx context.Context, tenantID string, certID id.ID) (*network.Certificate, error) {
	var model certificateModel

	err := s.pg.NewSelect(&model).
		Where("id = $1 AND tenant_id = $2", certID.String(), tenantID).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: certificate %s", ctrlplane.ErrNotFound, certID)
		}

		return nil, fmt.Errorf("postgres: get certificate failed: %w", err)
	}

	return fromCertificateModel(&model), nil
}

func (s *Store) ListCertificates(ctx context.Context, tenantID string, instanceID id.ID) ([]network.Certificate, error) {
	var models []certificateModel

	err := s.pg.NewSelect(&models).
		Where("tenant_id = $1 AND instance_id = $2", tenantID, instanceID.String()).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: list certificates failed: %w", err)
	}

	items := make([]network.Certificate, 0, len(models))
	for i := range models {
		items = append(items, *fromCertificateModel(&models[i]))
	}

	return items, nil
}

func (s *Store) UpdateCertificate(ctx context.Context, cert *network.Certificate) error {
	cert.UpdatedAt = now()
	model := toCertificateModel(cert)

	res, err := s.pg.NewUpdate(model).WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: update certificate failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: certificate %s", ctrlplane.ErrNotFound, cert.ID)
	}

	return nil
}

func (s *Store) DeleteCertificate(ctx context.Context, tenantID string, certID id.ID) error {
	res, err := s.pg.NewDelete((*certificateModel)(nil)).
		Where("id = $1 AND tenant_id = $2", certID.String(), tenantID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: delete certificate failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: certificate %s", ctrlplane.ErrNotFound, certID)
	}

	return nil
}

func (s *Store) ListCertificatesByDomain(ctx context.Context, tenantID string, domainID id.ID) ([]network.Certificate, error) {
	var models []certificateModel

	err := s.pg.NewSelect(&models).
		Where("tenant_id = $1 AND domain_id = $2", tenantID, domainID.String()).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: list certificates by domain failed: %w", err)
	}

	items := make([]network.Certificate, 0, len(models))
	for i := range models {
		items = append(items, *fromCertificateModel(&models[i]))
	}

	return items, nil
}

func (s *Store) CountDomainsByTenant(ctx context.Context, tenantID string) (int, error) {
	count, err := s.pg.NewSelect((*domainModel)(nil)).
		Where("tenant_id = $1", tenantID).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgres: count domains by tenant failed: %w", err)
	}

	return int(count), nil
}

// --- from model helpers for network types ---

func fromDomainModel(m *domainModel) *network.Domain {
	return &network.Domain{
		Entity: ctrlplane.Entity{
			ID:        id.MustParse(m.ID),
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		},
		TenantID:    m.TenantID,
		InstanceID:  id.MustParse(m.InstanceID),
		Hostname:    m.Hostname,
		Verified:    m.Verified,
		TLSEnabled:  m.TLSEnabled,
		CertExpiry:  m.CertExpiry,
		DNSTarget:   m.DNSTarget,
		VerifyToken: m.VerifyToken,
	}
}

func fromRouteModel(m *routeModel) *network.Route {
	return &network.Route{
		Entity: ctrlplane.Entity{
			ID:        id.MustParse(m.ID),
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		},
		TenantID:    m.TenantID,
		InstanceID:  id.MustParse(m.InstanceID),
		Path:        m.Path,
		Port:        m.Port,
		Protocol:    m.Protocol,
		Weight:      m.Weight,
		StripPrefix: m.StripPrefix,
	}
}

func fromCertificateModel(m *certificateModel) *network.Certificate {
	return &network.Certificate{
		Entity: ctrlplane.Entity{
			ID:        id.MustParse(m.ID),
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		},
		DomainID:  id.MustParse(m.DomainID),
		TenantID:  m.TenantID,
		Issuer:    m.Issuer,
		ExpiresAt: m.ExpiresAt,
		AutoRenew: m.AutoRenew,
	}
}
