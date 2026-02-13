package mongo

import (
	"context"
	"errors"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/network"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// ── Domains ─────────────────────────────────────────────────────────────────

// InsertDomain persists a new domain.
func (s *Store) InsertDomain(ctx context.Context, domain *network.Domain) error {
	m := toDomainModel(domain)

	_, err := s.col(colDomains).InsertOne(ctx, m)
	if err != nil {
		if isDuplicateKeyError(err) {
			return fmt.Errorf("mongo: insert domain: %w: %s", ctrlplane.ErrAlreadyExists, m.Hostname)
		}

		return fmt.Errorf("mongo: insert domain: %w", err)
	}

	return nil
}

// GetDomain retrieves a domain by ID.
func (s *Store) GetDomain(ctx context.Context, tenantID string, domainID id.ID) (*network.Domain, error) {
	filter := bson.D{
		{Key: "_id", Value: idStr(domainID)},
		{Key: "tenant_id", Value: tenantID},
	}

	var m domainModel

	err := s.col(colDomains).FindOne(ctx, filter).Decode(&m)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("mongo: get domain: %w: %s", ctrlplane.ErrNotFound, domainID)
		}

		return nil, fmt.Errorf("mongo: get domain: %w", err)
	}

	return fromDomainModel(&m), nil
}

// GetDomainByHostname retrieves a domain by its hostname.
func (s *Store) GetDomainByHostname(ctx context.Context, hostname string) (*network.Domain, error) {
	filter := bson.D{{Key: "hostname", Value: hostname}}

	var m domainModel

	err := s.col(colDomains).FindOne(ctx, filter).Decode(&m)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("mongo: get domain by hostname: %w: %s", ctrlplane.ErrNotFound, hostname)
		}

		return nil, fmt.Errorf("mongo: get domain by hostname: %w", err)
	}

	return fromDomainModel(&m), nil
}

// ListDomains returns all domains for an instance.
func (s *Store) ListDomains(ctx context.Context, tenantID string, instanceID id.ID) ([]network.Domain, error) {
	filter := bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "instance_id", Value: idStr(instanceID)},
	}

	cursor, err := s.col(colDomains).Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("mongo: list domains: %w", err)
	}
	defer cursor.Close(ctx)

	items := make([]network.Domain, 0)

	for cursor.Next(ctx) {
		var m domainModel

		if err := cursor.Decode(&m); err != nil {
			return nil, fmt.Errorf("mongo: list domains decode: %w", err)
		}

		items = append(items, *fromDomainModel(&m))
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("mongo: list domains cursor: %w", err)
	}

	return items, nil
}

// UpdateDomain persists changes to a domain.
func (s *Store) UpdateDomain(ctx context.Context, domain *network.Domain) error {
	domain.UpdatedAt = now()
	m := toDomainModel(domain)

	result, err := s.col(colDomains).ReplaceOne(
		ctx,
		bson.D{{Key: "_id", Value: m.ID}},
		m,
	)
	if err != nil {
		return fmt.Errorf("mongo: update domain: %w", err)
	}

	if result.MatchedCount == 0 {
		return fmt.Errorf("mongo: update domain: %w: %s", ctrlplane.ErrNotFound, m.ID)
	}

	return nil
}

// DeleteDomain removes a domain.
func (s *Store) DeleteDomain(ctx context.Context, tenantID string, domainID id.ID) error {
	filter := bson.D{
		{Key: "_id", Value: idStr(domainID)},
		{Key: "tenant_id", Value: tenantID},
	}

	result, err := s.col(colDomains).DeleteOne(ctx, filter)
	if err != nil {
		return fmt.Errorf("mongo: delete domain: %w", err)
	}

	if result.DeletedCount == 0 {
		return fmt.Errorf("mongo: delete domain: %w: %s", ctrlplane.ErrNotFound, domainID)
	}

	return nil
}

// CountDomainsByTenant returns the number of domains for a tenant.
func (s *Store) CountDomainsByTenant(ctx context.Context, tenantID string) (int, error) {
	filter := bson.D{{Key: "tenant_id", Value: tenantID}}

	count, err := s.col(colDomains).CountDocuments(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("mongo: count domains: %w", err)
	}

	return int(count), nil
}

// ── Routes ──────────────────────────────────────────────────────────────────

// InsertRoute persists a new route.
func (s *Store) InsertRoute(ctx context.Context, route *network.Route) error {
	m := toRouteModel(route)

	_, err := s.col(colRoutes).InsertOne(ctx, m)
	if err != nil {
		if isDuplicateKeyError(err) {
			return fmt.Errorf("mongo: insert route: %w: %s", ctrlplane.ErrAlreadyExists, m.ID)
		}

		return fmt.Errorf("mongo: insert route: %w", err)
	}

	return nil
}

// GetRoute retrieves a route by ID.
func (s *Store) GetRoute(ctx context.Context, tenantID string, routeID id.ID) (*network.Route, error) {
	filter := bson.D{
		{Key: "_id", Value: idStr(routeID)},
		{Key: "tenant_id", Value: tenantID},
	}

	var m routeModel

	err := s.col(colRoutes).FindOne(ctx, filter).Decode(&m)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("mongo: get route: %w: %s", ctrlplane.ErrNotFound, routeID)
		}

		return nil, fmt.Errorf("mongo: get route: %w", err)
	}

	return fromRouteModel(&m), nil
}

// ListRoutes returns all routes for an instance.
func (s *Store) ListRoutes(ctx context.Context, tenantID string, instanceID id.ID) ([]network.Route, error) {
	filter := bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "instance_id", Value: idStr(instanceID)},
	}

	cursor, err := s.col(colRoutes).Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("mongo: list routes: %w", err)
	}
	defer cursor.Close(ctx)

	items := make([]network.Route, 0)

	for cursor.Next(ctx) {
		var m routeModel

		if err := cursor.Decode(&m); err != nil {
			return nil, fmt.Errorf("mongo: list routes decode: %w", err)
		}

		items = append(items, *fromRouteModel(&m))
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("mongo: list routes cursor: %w", err)
	}

	return items, nil
}

// UpdateRoute persists changes to a route.
func (s *Store) UpdateRoute(ctx context.Context, route *network.Route) error {
	route.UpdatedAt = now()
	m := toRouteModel(route)

	result, err := s.col(colRoutes).ReplaceOne(
		ctx,
		bson.D{{Key: "_id", Value: m.ID}},
		m,
	)
	if err != nil {
		return fmt.Errorf("mongo: update route: %w", err)
	}

	if result.MatchedCount == 0 {
		return fmt.Errorf("mongo: update route: %w: %s", ctrlplane.ErrNotFound, m.ID)
	}

	return nil
}

// DeleteRoute removes a route.
func (s *Store) DeleteRoute(ctx context.Context, tenantID string, routeID id.ID) error {
	filter := bson.D{
		{Key: "_id", Value: idStr(routeID)},
		{Key: "tenant_id", Value: tenantID},
	}

	result, err := s.col(colRoutes).DeleteOne(ctx, filter)
	if err != nil {
		return fmt.Errorf("mongo: delete route: %w", err)
	}

	if result.DeletedCount == 0 {
		return fmt.Errorf("mongo: delete route: %w: %s", ctrlplane.ErrNotFound, routeID)
	}

	return nil
}

// ── Certificates ────────────────────────────────────────────────────────────

// InsertCertificate persists a new certificate.
func (s *Store) InsertCertificate(ctx context.Context, cert *network.Certificate) error {
	m := toCertificateModel(cert)

	_, err := s.col(colCertificates).InsertOne(ctx, m)
	if err != nil {
		if isDuplicateKeyError(err) {
			return fmt.Errorf("mongo: insert certificate: %w: %s", ctrlplane.ErrAlreadyExists, m.ID)
		}

		return fmt.Errorf("mongo: insert certificate: %w", err)
	}

	return nil
}

// GetCertificate retrieves a certificate by ID.
func (s *Store) GetCertificate(ctx context.Context, tenantID string, certID id.ID) (*network.Certificate, error) {
	filter := bson.D{
		{Key: "_id", Value: idStr(certID)},
		{Key: "tenant_id", Value: tenantID},
	}

	var m certificateModel

	err := s.col(colCertificates).FindOne(ctx, filter).Decode(&m)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("mongo: get certificate: %w: %s", ctrlplane.ErrNotFound, certID)
		}

		return nil, fmt.Errorf("mongo: get certificate: %w", err)
	}

	return fromCertificateModel(&m), nil
}

// ListCertificates returns all certificates for an instance.
// It looks up domains owned by the instance, then finds certificates by domain IDs.
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

	filter := bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "domain_id", Value: bson.D{{Key: "$in", Value: domainIDs}}},
	}

	cursor, err := s.col(colCertificates).Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("mongo: list certificates: %w", err)
	}
	defer cursor.Close(ctx)

	items := make([]network.Certificate, 0)

	for cursor.Next(ctx) {
		var m certificateModel

		if err := cursor.Decode(&m); err != nil {
			return nil, fmt.Errorf("mongo: list certificates decode: %w", err)
		}

		items = append(items, *fromCertificateModel(&m))
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("mongo: list certificates cursor: %w", err)
	}

	return items, nil
}

// UpdateCertificate persists changes to a certificate.
func (s *Store) UpdateCertificate(ctx context.Context, cert *network.Certificate) error {
	cert.UpdatedAt = now()
	m := toCertificateModel(cert)

	result, err := s.col(colCertificates).ReplaceOne(
		ctx,
		bson.D{{Key: "_id", Value: m.ID}},
		m,
	)
	if err != nil {
		return fmt.Errorf("mongo: update certificate: %w", err)
	}

	if result.MatchedCount == 0 {
		return fmt.Errorf("mongo: update certificate: %w: %s", ctrlplane.ErrNotFound, m.ID)
	}

	return nil
}

// DeleteCertificate removes a certificate.
func (s *Store) DeleteCertificate(ctx context.Context, tenantID string, certID id.ID) error {
	filter := bson.D{
		{Key: "_id", Value: idStr(certID)},
		{Key: "tenant_id", Value: tenantID},
	}

	result, err := s.col(colCertificates).DeleteOne(ctx, filter)
	if err != nil {
		return fmt.Errorf("mongo: delete certificate: %w", err)
	}

	if result.DeletedCount == 0 {
		return fmt.Errorf("mongo: delete certificate: %w: %s", ctrlplane.ErrNotFound, certID)
	}

	return nil
}
