package sqlite

import (
	"context"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/network"
)

func (s *Store) InsertDomain(ctx context.Context, domain *network.Domain) error {
	model := toDomainModel(domain)

	_, err := s.sdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: insert domain failed: %w", err)
	}

	return nil
}

func (s *Store) GetDomain(ctx context.Context, tenantID string, domainID id.ID) (*network.Domain, error) {
	var model domainModel

	err := s.sdb.NewSelect(&model).
		Where("id = ? AND tenant_id = ?", domainID.String(), tenantID).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: domain %s", ctrlplane.ErrNotFound, domainID)
		}

		return nil, fmt.Errorf("sqlite: get domain failed: %w", err)
	}

	return fromDomainModel(&model), nil
}

func (s *Store) GetDomainByHostname(ctx context.Context, hostname string) (*network.Domain, error) {
	var model domainModel

	err := s.sdb.NewSelect(&model).
		Where("hostname = ?", hostname).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: hostname %s", ctrlplane.ErrNotFound, hostname)
		}

		return nil, fmt.Errorf("sqlite: get domain by hostname failed: %w", err)
	}

	return fromDomainModel(&model), nil
}

func (s *Store) ListDomains(ctx context.Context, tenantID string, instanceID id.ID) ([]network.Domain, error) {
	var models []domainModel

	err := s.sdb.NewSelect(&models).
		Where("tenant_id = ? AND instance_id = ?", tenantID, instanceID.String()).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list domains failed: %w", err)
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

	res, err := s.sdb.NewUpdate(model).WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: update domain failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: domain %s", ctrlplane.ErrNotFound, domain.ID)
	}

	return nil
}

func (s *Store) DeleteDomain(ctx context.Context, tenantID string, domainID id.ID) error {
	res, err := s.sdb.NewDelete((*domainModel)(nil)).
		Where("id = ? AND tenant_id = ?", domainID.String(), tenantID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: delete domain failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: domain %s", ctrlplane.ErrNotFound, domainID)
	}

	return nil
}

func (s *Store) InsertRoute(ctx context.Context, route *network.Route) error {
	model := toRouteModel(route)

	_, err := s.sdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: insert route failed: %w", err)
	}

	return nil
}

func (s *Store) GetRoute(ctx context.Context, tenantID string, routeID id.ID) (*network.Route, error) {
	var model routeModel

	err := s.sdb.NewSelect(&model).
		Where("id = ? AND tenant_id = ?", routeID.String(), tenantID).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: route %s", ctrlplane.ErrNotFound, routeID)
		}

		return nil, fmt.Errorf("sqlite: get route failed: %w", err)
	}

	return fromRouteModel(&model), nil
}

func (s *Store) ListRoutes(ctx context.Context, tenantID string, instanceID id.ID) ([]network.Route, error) {
	var models []routeModel

	err := s.sdb.NewSelect(&models).
		Where("tenant_id = ? AND instance_id = ?", tenantID, instanceID.String()).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list routes failed: %w", err)
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

	res, err := s.sdb.NewUpdate(model).WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: update route failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: route %s", ctrlplane.ErrNotFound, route.ID)
	}

	return nil
}

func (s *Store) DeleteRoute(ctx context.Context, tenantID string, routeID id.ID) error {
	res, err := s.sdb.NewDelete((*routeModel)(nil)).
		Where("id = ? AND tenant_id = ?", routeID.String(), tenantID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: delete route failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: route %s", ctrlplane.ErrNotFound, routeID)
	}

	return nil
}

func (s *Store) InsertCertificate(ctx context.Context, cert *network.Certificate) error {
	model := toCertificateModel(cert)

	_, err := s.sdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: insert certificate failed: %w", err)
	}

	return nil
}

func (s *Store) GetCertificate(ctx context.Context, tenantID string, certID id.ID) (*network.Certificate, error) {
	var model certificateModel

	err := s.sdb.NewSelect(&model).
		Where("id = ? AND tenant_id = ?", certID.String(), tenantID).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: certificate %s", ctrlplane.ErrNotFound, certID)
		}

		return nil, fmt.Errorf("sqlite: get certificate failed: %w", err)
	}

	return fromCertificateModel(&model), nil
}

func (s *Store) ListCertificates(ctx context.Context, tenantID string, instanceID id.ID) ([]network.Certificate, error) {
	var models []certificateModel

	err := s.sdb.NewSelect(&models).
		Where("tenant_id = ? AND instance_id = ?", tenantID, instanceID.String()).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list certificates failed: %w", err)
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

	res, err := s.sdb.NewUpdate(model).WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: update certificate failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: certificate %s", ctrlplane.ErrNotFound, cert.ID)
	}

	return nil
}

func (s *Store) DeleteCertificate(ctx context.Context, tenantID string, certID id.ID) error {
	res, err := s.sdb.NewDelete((*certificateModel)(nil)).
		Where("id = ? AND tenant_id = ?", certID.String(), tenantID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: delete certificate failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: certificate %s", ctrlplane.ErrNotFound, certID)
	}

	return nil
}

func (s *Store) CountDomainsByTenant(ctx context.Context, tenantID string) (int, error) {
	count, err := s.sdb.NewSelect((*domainModel)(nil)).
		Where("tenant_id = ?", tenantID).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("sqlite: count domains by tenant failed: %w", err)
	}

	return int(count), nil
}
