package bun

import (
	"context"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/network"
)

func (s *Store) InsertDomain(ctx context.Context, domain *network.Domain) error {
	model := toDomainModel(domain)

	_, err := s.db.NewInsert().Model(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("bun: insert domain failed: %w", err)
	}

	return nil
}

func (s *Store) GetDomain(ctx context.Context, tenantID string, domainID id.ID) (*network.Domain, error) {
	var model domainModel

	err := s.db.NewSelect().
		Model(&model).
		Where("id = ? AND tenant_id = ?", domainID.String(), tenantID).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: domain %s", ctrlplane.ErrNotFound, domainID)
	}

	domain := &network.Domain{
		Entity: ctrlplane.Entity{
			ID:        id.MustParse(model.ID),
			CreatedAt: model.CreatedAt,
			UpdatedAt: model.UpdatedAt,
		},
		TenantID:    model.TenantID,
		InstanceID:  id.MustParse(model.InstanceID),
		Hostname:    model.Hostname,
		Verified:    model.Verified,
		TLSEnabled:  model.TLSEnabled,
		CertExpiry:  model.CertExpiry,
		DNSTarget:   model.DNSTarget,
		VerifyToken: model.VerifyToken,
	}

	return domain, nil
}

func (s *Store) GetDomainByHostname(ctx context.Context, hostname string) (*network.Domain, error) {
	var model domainModel

	err := s.db.NewSelect().
		Model(&model).
		Where("hostname = ?", hostname).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: hostname %s", ctrlplane.ErrNotFound, hostname)
	}

	domain := &network.Domain{
		Entity: ctrlplane.Entity{
			ID:        id.MustParse(model.ID),
			CreatedAt: model.CreatedAt,
			UpdatedAt: model.UpdatedAt,
		},
		TenantID:    model.TenantID,
		InstanceID:  id.MustParse(model.InstanceID),
		Hostname:    model.Hostname,
		Verified:    model.Verified,
		TLSEnabled:  model.TLSEnabled,
		CertExpiry:  model.CertExpiry,
		DNSTarget:   model.DNSTarget,
		VerifyToken: model.VerifyToken,
	}

	return domain, nil
}

func (s *Store) ListDomains(ctx context.Context, tenantID string, instanceID id.ID) ([]network.Domain, error) {
	var models []domainModel

	err := s.db.NewSelect().
		Model(&models).
		Where("tenant_id = ? AND instance_id = ?", tenantID, instanceID.String()).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("bun: list domains failed: %w", err)
	}

	items := make([]network.Domain, 0, len(models))

	for _, model := range models {
		domain := network.Domain{
			Entity: ctrlplane.Entity{
				ID:        id.MustParse(model.ID),
				CreatedAt: model.CreatedAt,
				UpdatedAt: model.UpdatedAt,
			},
			TenantID:    model.TenantID,
			InstanceID:  id.MustParse(model.InstanceID),
			Hostname:    model.Hostname,
			Verified:    model.Verified,
			TLSEnabled:  model.TLSEnabled,
			CertExpiry:  model.CertExpiry,
			DNSTarget:   model.DNSTarget,
			VerifyToken: model.VerifyToken,
		}
		items = append(items, domain)
	}

	return items, nil
}

func (s *Store) UpdateDomain(ctx context.Context, domain *network.Domain) error {
	domain.UpdatedAt = now()
	model := toDomainModel(domain)

	result, err := s.db.NewUpdate().
		Model(model).
		WherePK().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("bun: update domain failed: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("bun: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: domain %s", ctrlplane.ErrNotFound, domain.ID)
	}

	return nil
}

func (s *Store) DeleteDomain(ctx context.Context, tenantID string, domainID id.ID) error {
	result, err := s.db.NewDelete().
		Model((*domainModel)(nil)).
		Where("id = ? AND tenant_id = ?", domainID.String(), tenantID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("bun: delete domain failed: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("bun: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: domain %s", ctrlplane.ErrNotFound, domainID)
	}

	return nil
}

func (s *Store) InsertRoute(ctx context.Context, route *network.Route) error {
	model := toRouteModel(route)

	_, err := s.db.NewInsert().Model(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("bun: insert route failed: %w", err)
	}

	return nil
}

func (s *Store) GetRoute(ctx context.Context, tenantID string, routeID id.ID) (*network.Route, error) {
	var model routeModel

	err := s.db.NewSelect().
		Model(&model).
		Where("id = ? AND tenant_id = ?", routeID.String(), tenantID).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: route %s", ctrlplane.ErrNotFound, routeID)
	}

	route := &network.Route{
		Entity: ctrlplane.Entity{
			ID:        id.MustParse(model.ID),
			CreatedAt: model.CreatedAt,
			UpdatedAt: model.UpdatedAt,
		},
		TenantID:    model.TenantID,
		InstanceID:  id.MustParse(model.InstanceID),
		Path:        model.Path,
		Port:        model.Port,
		Protocol:    model.Protocol,
		Weight:      model.Weight,
		StripPrefix: model.StripPrefix,
	}

	return route, nil
}

func (s *Store) ListRoutes(ctx context.Context, tenantID string, instanceID id.ID) ([]network.Route, error) {
	var models []routeModel

	err := s.db.NewSelect().
		Model(&models).
		Where("tenant_id = ? AND instance_id = ?", tenantID, instanceID.String()).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("bun: list routes failed: %w", err)
	}

	items := make([]network.Route, 0, len(models))

	for _, model := range models {
		route := network.Route{
			Entity: ctrlplane.Entity{
				ID:        id.MustParse(model.ID),
				CreatedAt: model.CreatedAt,
				UpdatedAt: model.UpdatedAt,
			},
			TenantID:    model.TenantID,
			InstanceID:  id.MustParse(model.InstanceID),
			Path:        model.Path,
			Port:        model.Port,
			Protocol:    model.Protocol,
			Weight:      model.Weight,
			StripPrefix: model.StripPrefix,
		}
		items = append(items, route)
	}

	return items, nil
}

func (s *Store) UpdateRoute(ctx context.Context, route *network.Route) error {
	route.UpdatedAt = now()
	model := toRouteModel(route)

	result, err := s.db.NewUpdate().
		Model(model).
		WherePK().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("bun: update route failed: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("bun: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: route %s", ctrlplane.ErrNotFound, route.ID)
	}

	return nil
}

func (s *Store) DeleteRoute(ctx context.Context, tenantID string, routeID id.ID) error {
	result, err := s.db.NewDelete().
		Model((*routeModel)(nil)).
		Where("id = ? AND tenant_id = ?", routeID.String(), tenantID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("bun: delete route failed: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("bun: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: route %s", ctrlplane.ErrNotFound, routeID)
	}

	return nil
}

func (s *Store) InsertCertificate(ctx context.Context, cert *network.Certificate) error {
	model := toCertificateModel(cert)

	_, err := s.db.NewInsert().Model(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("bun: insert certificate failed: %w", err)
	}

	return nil
}

func (s *Store) GetCertificate(ctx context.Context, tenantID string, certID id.ID) (*network.Certificate, error) {
	var model certificateModel

	err := s.db.NewSelect().
		Model(&model).
		Where("id = ? AND tenant_id = ?", certID.String(), tenantID).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: certificate %s", ctrlplane.ErrNotFound, certID)
	}

	cert := &network.Certificate{
		Entity: ctrlplane.Entity{
			ID:        id.MustParse(model.ID),
			CreatedAt: model.CreatedAt,
			UpdatedAt: model.UpdatedAt,
		},
		DomainID:  id.MustParse(model.DomainID),
		TenantID:  model.TenantID,
		Issuer:    model.Issuer,
		ExpiresAt: model.ExpiresAt,
		AutoRenew: model.AutoRenew,
	}

	return cert, nil
}

func (s *Store) ListCertificates(ctx context.Context, tenantID string, instanceID id.ID) ([]network.Certificate, error) {
	var models []certificateModel

	err := s.db.NewSelect().
		Model(&models).
		Where("tenant_id = ? AND instance_id = ?", tenantID, instanceID.String()).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("bun: list certificates failed: %w", err)
	}

	items := make([]network.Certificate, 0, len(models))

	for _, model := range models {
		cert := network.Certificate{
			Entity: ctrlplane.Entity{
				ID:        id.MustParse(model.ID),
				CreatedAt: model.CreatedAt,
				UpdatedAt: model.UpdatedAt,
			},
			DomainID:  id.MustParse(model.DomainID),
			TenantID:  model.TenantID,
			Issuer:    model.Issuer,
			ExpiresAt: model.ExpiresAt,
			AutoRenew: model.AutoRenew,
		}
		items = append(items, cert)
	}

	return items, nil
}

func (s *Store) UpdateCertificate(ctx context.Context, cert *network.Certificate) error {
	cert.UpdatedAt = now()
	model := toCertificateModel(cert)

	result, err := s.db.NewUpdate().
		Model(model).
		WherePK().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("bun: update certificate failed: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("bun: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: certificate %s", ctrlplane.ErrNotFound, cert.ID)
	}

	return nil
}

func (s *Store) DeleteCertificate(ctx context.Context, tenantID string, certID id.ID) error {
	result, err := s.db.NewDelete().
		Model((*certificateModel)(nil)).
		Where("id = ? AND tenant_id = ?", certID.String(), tenantID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("bun: delete certificate failed: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("bun: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: certificate %s", ctrlplane.ErrNotFound, certID)
	}

	return nil
}

func (s *Store) ListCertificatesByDomain(ctx context.Context, tenantID string, domainID id.ID) ([]network.Certificate, error) {
	var models []certificateModel

	err := s.db.NewSelect().
		Model(&models).
		Where("tenant_id = ? AND domain_id = ?", tenantID, domainID.String()).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("bun: list certificates by domain failed: %w", err)
	}

	items := make([]network.Certificate, 0, len(models))

	for _, model := range models {
		cert := network.Certificate{
			Entity: ctrlplane.Entity{
				ID:        id.MustParse(model.ID),
				CreatedAt: model.CreatedAt,
				UpdatedAt: model.UpdatedAt,
			},
			DomainID:  id.MustParse(model.DomainID),
			TenantID:  model.TenantID,
			Issuer:    model.Issuer,
			ExpiresAt: model.ExpiresAt,
			AutoRenew: model.AutoRenew,
		}
		items = append(items, cert)
	}

	return items, nil
}

func (s *Store) CountDomainsByTenant(ctx context.Context, tenantID string) (int, error) {
	count, err := s.db.NewSelect().
		Model((*domainModel)(nil)).
		Where("tenant_id = ?", tenantID).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("bun: count domains by tenant failed: %w", err)
	}

	return count, nil
}
