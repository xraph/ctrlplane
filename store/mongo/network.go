package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/network"
)

// ── Domains ─────────────────────────────────────────────────────────────────

func (s *Store) InsertDomain(ctx context.Context, domain *network.Domain) error {
	model := toDomainModel(domain)

	_, err := s.mdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: insert domain failed: %w", err)
	}

	return nil
}

func (s *Store) GetDomain(ctx context.Context, tenantID string, domainID id.ID) (*network.Domain, error) {
	var model domainModel

	err := s.mdb.NewFind(&model).
		Filter(bson.M{"_id": domainID.String(), "tenant_id": tenantID}).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, fmt.Errorf("%w: domain %s", ctrlplane.ErrNotFound, domainID)
		}

		return nil, fmt.Errorf("mongo: get domain failed: %w", err)
	}

	return fromDomainModel(&model), nil
}

func (s *Store) GetDomainByHostname(ctx context.Context, hostname string) (*network.Domain, error) {
	var model domainModel

	err := s.mdb.NewFind(&model).
		Filter(bson.M{"hostname": hostname}).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, fmt.Errorf("%w: hostname %s", ctrlplane.ErrNotFound, hostname)
		}

		return nil, fmt.Errorf("mongo: get domain by hostname failed: %w", err)
	}

	return fromDomainModel(&model), nil
}

func (s *Store) ListDomains(ctx context.Context, tenantID string, instanceID id.ID) ([]network.Domain, error) {
	var models []domainModel

	err := s.mdb.NewFind(&models).
		Filter(bson.M{"tenant_id": tenantID, "instance_id": instanceID.String()}).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("mongo: list domains failed: %w", err)
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

	res, err := s.mdb.NewUpdate(model).
		Filter(bson.M{"_id": model.ID}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: update domain failed: %w", err)
	}

	if res.MatchedCount() == 0 {
		return fmt.Errorf("%w: domain %s", ctrlplane.ErrNotFound, domain.ID)
	}

	return nil
}

func (s *Store) DeleteDomain(ctx context.Context, tenantID string, domainID id.ID) error {
	res, err := s.mdb.NewDelete((*domainModel)(nil)).
		Filter(bson.M{"_id": domainID.String(), "tenant_id": tenantID}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: delete domain failed: %w", err)
	}

	if res.DeletedCount() == 0 {
		return fmt.Errorf("%w: domain %s", ctrlplane.ErrNotFound, domainID)
	}

	return nil
}

func (s *Store) CountDomainsByTenant(ctx context.Context, tenantID string) (int, error) {
	count, err := s.mdb.NewFind((*domainModel)(nil)).
		Filter(bson.M{"tenant_id": tenantID}).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("mongo: count domains by tenant failed: %w", err)
	}

	return int(count), nil
}

// ── Routes ──────────────────────────────────────────────────────────────────

func (s *Store) InsertRoute(ctx context.Context, route *network.Route) error {
	model := toRouteModel(route)

	_, err := s.mdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: insert route failed: %w", err)
	}

	return nil
}

func (s *Store) GetRoute(ctx context.Context, tenantID string, routeID id.ID) (*network.Route, error) {
	var model routeModel

	err := s.mdb.NewFind(&model).
		Filter(bson.M{"_id": routeID.String(), "tenant_id": tenantID}).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, fmt.Errorf("%w: route %s", ctrlplane.ErrNotFound, routeID)
		}

		return nil, fmt.Errorf("mongo: get route failed: %w", err)
	}

	return fromRouteModel(&model), nil
}

func (s *Store) ListRoutes(ctx context.Context, tenantID string, instanceID id.ID) ([]network.Route, error) {
	var models []routeModel

	err := s.mdb.NewFind(&models).
		Filter(bson.M{"tenant_id": tenantID, "instance_id": instanceID.String()}).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("mongo: list routes failed: %w", err)
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

	res, err := s.mdb.NewUpdate(model).
		Filter(bson.M{"_id": model.ID}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: update route failed: %w", err)
	}

	if res.MatchedCount() == 0 {
		return fmt.Errorf("%w: route %s", ctrlplane.ErrNotFound, route.ID)
	}

	return nil
}

func (s *Store) DeleteRoute(ctx context.Context, tenantID string, routeID id.ID) error {
	res, err := s.mdb.NewDelete((*routeModel)(nil)).
		Filter(bson.M{"_id": routeID.String(), "tenant_id": tenantID}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: delete route failed: %w", err)
	}

	if res.DeletedCount() == 0 {
		return fmt.Errorf("%w: route %s", ctrlplane.ErrNotFound, routeID)
	}

	return nil
}

// ── Certificates ────────────────────────────────────────────────────────────

func (s *Store) InsertCertificate(ctx context.Context, cert *network.Certificate) error {
	model := toCertificateModel(cert)

	_, err := s.mdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: insert certificate failed: %w", err)
	}

	return nil
}

func (s *Store) GetCertificate(ctx context.Context, tenantID string, certID id.ID) (*network.Certificate, error) {
	var model certificateModel

	err := s.mdb.NewFind(&model).
		Filter(bson.M{"_id": certID.String(), "tenant_id": tenantID}).
		Scan(ctx)
	if err != nil {
		if isNoDocuments(err) {
			return nil, fmt.Errorf("%w: certificate %s", ctrlplane.ErrNotFound, certID)
		}

		return nil, fmt.Errorf("mongo: get certificate failed: %w", err)
	}

	return fromCertificateModel(&model), nil
}

func (s *Store) ListCertificates(ctx context.Context, tenantID string, instanceID id.ID) ([]network.Certificate, error) {
	// First find all domain IDs for this instance.
	domains, err := s.ListDomains(ctx, tenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("mongo: list certificates: %w", err)
	}

	if len(domains) == 0 {
		return []network.Certificate{}, nil
	}

	domainIDs := make([]string, 0, len(domains))
	for _, d := range domains {
		domainIDs = append(domainIDs, idStr(d.ID))
	}

	var models []certificateModel

	err = s.mdb.NewFind(&models).
		Filter(bson.M{"tenant_id": tenantID, "domain_id": bson.M{"$in": domainIDs}}).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("mongo: list certificates failed: %w", err)
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

	res, err := s.mdb.NewUpdate(model).
		Filter(bson.M{"_id": model.ID}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: update certificate failed: %w", err)
	}

	if res.MatchedCount() == 0 {
		return fmt.Errorf("%w: certificate %s", ctrlplane.ErrNotFound, cert.ID)
	}

	return nil
}

func (s *Store) DeleteCertificate(ctx context.Context, tenantID string, certID id.ID) error {
	res, err := s.mdb.NewDelete((*certificateModel)(nil)).
		Filter(bson.M{"_id": certID.String(), "tenant_id": tenantID}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("mongo: delete certificate failed: %w", err)
	}

	if res.DeletedCount() == 0 {
		return fmt.Errorf("%w: certificate %s", ctrlplane.ErrNotFound, certID)
	}

	return nil
}
