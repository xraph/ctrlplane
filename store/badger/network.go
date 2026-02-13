package badger

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dgraph-io/badger/v4"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/network"
)

func (s *Store) InsertDomain(_ context.Context, domain *network.Domain) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixDomain + idStr(domain.ID)

		exists, err := s.exists(txn, key)
		if err != nil {
			return err
		}

		if exists {
			return fmt.Errorf("%w: domain %s", ctrlplane.ErrAlreadyExists, domain.ID)
		}

		return s.set(txn, key, domain)
	})
}

func (s *Store) GetDomain(_ context.Context, tenantID string, domainID id.ID) (*network.Domain, error) {
	var domain network.Domain

	err := s.db.View(func(txn *badger.Txn) error {
		key := prefixDomain + idStr(domainID)

		if err := s.get(txn, key, &domain); err != nil {
			return err
		}

		if domain.TenantID != tenantID {
			return fmt.Errorf("%w: domain %s", ctrlplane.ErrNotFound, domainID)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &domain, nil
}

func (s *Store) GetDomainByHostname(_ context.Context, hostname string) (*network.Domain, error) {
	var found *network.Domain

	err := s.db.View(func(txn *badger.Txn) error {
		return s.iterate(txn, prefixDomain, func(_ string, val []byte) error {
			var domain network.Domain
			if err := json.Unmarshal(val, &domain); err != nil {
				return fmt.Errorf("badger: json unmarshal failed: %w", err)
			}

			if domain.Hostname == hostname {
				found = &domain

				return nil
			}

			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	if found == nil {
		return nil, fmt.Errorf("%w: hostname %s", ctrlplane.ErrNotFound, hostname)
	}

	return found, nil
}

func (s *Store) ListDomains(_ context.Context, tenantID string, instanceID id.ID) ([]network.Domain, error) {
	var items []network.Domain

	err := s.db.View(func(txn *badger.Txn) error {
		return s.iterate(txn, prefixDomain, func(_ string, val []byte) error {
			var domain network.Domain
			if err := json.Unmarshal(val, &domain); err != nil {
				return fmt.Errorf("badger: json unmarshal failed: %w", err)
			}

			if domain.TenantID == tenantID && domain.InstanceID == instanceID {
				items = append(items, domain)
			}

			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	return items, nil
}

func (s *Store) UpdateDomain(_ context.Context, domain *network.Domain) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixDomain + idStr(domain.ID)

		var existing network.Domain
		if err := s.get(txn, key, &existing); err != nil {
			return fmt.Errorf("%w: domain %s", ctrlplane.ErrNotFound, domain.ID)
		}

		domain.UpdatedAt = now()

		return s.set(txn, key, domain)
	})
}

func (s *Store) DeleteDomain(_ context.Context, tenantID string, domainID id.ID) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixDomain + idStr(domainID)

		var domain network.Domain
		if err := s.get(txn, key, &domain); err != nil {
			return fmt.Errorf("%w: domain %s", ctrlplane.ErrNotFound, domainID)
		}

		if domain.TenantID != tenantID {
			return fmt.Errorf("%w: domain %s", ctrlplane.ErrNotFound, domainID)
		}

		return s.delete(txn, key)
	})
}

func (s *Store) InsertRoute(_ context.Context, route *network.Route) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixRoute + idStr(route.ID)

		exists, err := s.exists(txn, key)
		if err != nil {
			return err
		}

		if exists {
			return fmt.Errorf("%w: route %s", ctrlplane.ErrAlreadyExists, route.ID)
		}

		return s.set(txn, key, route)
	})
}

func (s *Store) GetRoute(_ context.Context, tenantID string, routeID id.ID) (*network.Route, error) {
	var route network.Route

	err := s.db.View(func(txn *badger.Txn) error {
		key := prefixRoute + idStr(routeID)

		if err := s.get(txn, key, &route); err != nil {
			return err
		}

		if route.TenantID != tenantID {
			return fmt.Errorf("%w: route %s", ctrlplane.ErrNotFound, routeID)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &route, nil
}

func (s *Store) ListRoutes(_ context.Context, tenantID string, instanceID id.ID) ([]network.Route, error) {
	var items []network.Route

	err := s.db.View(func(txn *badger.Txn) error {
		return s.iterate(txn, prefixRoute, func(_ string, val []byte) error {
			var route network.Route
			if err := json.Unmarshal(val, &route); err != nil {
				return fmt.Errorf("badger: json unmarshal failed: %w", err)
			}

			if route.TenantID == tenantID && route.InstanceID == instanceID {
				items = append(items, route)
			}

			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	return items, nil
}

func (s *Store) UpdateRoute(_ context.Context, route *network.Route) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixRoute + idStr(route.ID)

		var existing network.Route
		if err := s.get(txn, key, &existing); err != nil {
			return fmt.Errorf("%w: route %s", ctrlplane.ErrNotFound, route.ID)
		}

		route.UpdatedAt = now()

		return s.set(txn, key, route)
	})
}

func (s *Store) DeleteRoute(_ context.Context, tenantID string, routeID id.ID) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixRoute + idStr(routeID)

		var route network.Route
		if err := s.get(txn, key, &route); err != nil {
			return fmt.Errorf("%w: route %s", ctrlplane.ErrNotFound, routeID)
		}

		if route.TenantID != tenantID {
			return fmt.Errorf("%w: route %s", ctrlplane.ErrNotFound, routeID)
		}

		return s.delete(txn, key)
	})
}

func (s *Store) InsertCertificate(_ context.Context, cert *network.Certificate) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixCertificate + idStr(cert.ID)

		exists, err := s.exists(txn, key)
		if err != nil {
			return err
		}

		if exists {
			return fmt.Errorf("%w: certificate %s", ctrlplane.ErrAlreadyExists, cert.ID)
		}

		return s.set(txn, key, cert)
	})
}

func (s *Store) GetCertificate(_ context.Context, tenantID string, certID id.ID) (*network.Certificate, error) {
	var cert network.Certificate

	err := s.db.View(func(txn *badger.Txn) error {
		key := prefixCertificate + idStr(certID)

		if err := s.get(txn, key, &cert); err != nil {
			return err
		}

		if cert.TenantID != tenantID {
			return fmt.Errorf("%w: certificate %s", ctrlplane.ErrNotFound, certID)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &cert, nil
}

func (s *Store) ListCertificates(_ context.Context, tenantID string, instanceID id.ID) ([]network.Certificate, error) {
	var items []network.Certificate

	instKey := idStr(instanceID)

	err := s.db.View(func(txn *badger.Txn) error {
		// Build set of domain IDs owned by the given instance.
		domainIDs := make(map[string]struct{})

		if err := s.iterate(txn, prefixDomain, func(_ string, val []byte) error {
			var domain network.Domain
			if err := json.Unmarshal(val, &domain); err != nil {
				return fmt.Errorf("badger: json unmarshal failed: %w", err)
			}

			if domain.TenantID == tenantID && idStr(domain.InstanceID) == instKey {
				domainIDs[idStr(domain.ID)] = struct{}{}
			}

			return nil
		}); err != nil {
			return err
		}

		// Collect certificates whose DomainID is in the set.
		return s.iterate(txn, prefixCertificate, func(_ string, val []byte) error {
			var cert network.Certificate
			if err := json.Unmarshal(val, &cert); err != nil {
				return fmt.Errorf("badger: json unmarshal failed: %w", err)
			}

			if cert.TenantID != tenantID {
				return nil
			}

			if _, ok := domainIDs[idStr(cert.DomainID)]; ok {
				items = append(items, cert)
			}

			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	return items, nil
}

func (s *Store) UpdateCertificate(_ context.Context, cert *network.Certificate) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixCertificate + idStr(cert.ID)

		var existing network.Certificate
		if err := s.get(txn, key, &existing); err != nil {
			return fmt.Errorf("%w: certificate %s", ctrlplane.ErrNotFound, cert.ID)
		}

		cert.UpdatedAt = now()

		return s.set(txn, key, cert)
	})
}

func (s *Store) DeleteCertificate(_ context.Context, tenantID string, certID id.ID) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixCertificate + idStr(certID)

		var cert network.Certificate
		if err := s.get(txn, key, &cert); err != nil {
			return fmt.Errorf("%w: certificate %s", ctrlplane.ErrNotFound, certID)
		}

		if cert.TenantID != tenantID {
			return fmt.Errorf("%w: certificate %s", ctrlplane.ErrNotFound, certID)
		}

		return s.delete(txn, key)
	})
}

func (s *Store) ListCertificatesByDomain(_ context.Context, tenantID string, domainID id.ID) ([]network.Certificate, error) {
	var items []network.Certificate

	err := s.db.View(func(txn *badger.Txn) error {
		return s.iterate(txn, prefixCertificate, func(_ string, val []byte) error {
			var cert network.Certificate
			if err := json.Unmarshal(val, &cert); err != nil {
				return fmt.Errorf("badger: json unmarshal failed: %w", err)
			}

			if cert.TenantID == tenantID && cert.DomainID == domainID {
				items = append(items, cert)
			}

			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	return items, nil
}

func (s *Store) CountDomainsByTenant(_ context.Context, tenantID string) (int, error) {
	count := 0

	err := s.db.View(func(txn *badger.Txn) error {
		return s.iterate(txn, prefixDomain, func(_ string, val []byte) error {
			var domain network.Domain
			if err := json.Unmarshal(val, &domain); err != nil {
				return fmt.Errorf("badger: json unmarshal failed: %w", err)
			}

			if domain.TenantID == tenantID {
				count++
			}

			return nil
		})
	})
	if err != nil {
		return 0, err
	}

	return count, nil
}
