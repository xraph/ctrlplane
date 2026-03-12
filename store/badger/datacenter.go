package badger

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/dgraph-io/badger/v4"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/datacenter"
	"github.com/xraph/ctrlplane/id"
)

const (
	prefixDatacenter     = "dctr:"
	prefixDatacenterSlug = "dcsg:"
)

// InsertDatacenter persists a new datacenter.
func (s *Store) InsertDatacenter(_ context.Context, dc *datacenter.Datacenter) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixDatacenter + idStr(dc.ID)

		exists, err := s.exists(txn, key)
		if err != nil {
			return err
		}

		if exists {
			return fmt.Errorf("%w: datacenter %s", ctrlplane.ErrAlreadyExists, dc.ID)
		}

		slugKey := prefixDatacenterSlug + dc.TenantID + ":" + dc.Slug

		slugExists, err := s.exists(txn, slugKey)
		if err != nil {
			return err
		}

		if slugExists {
			return fmt.Errorf("%w: slug %s", ctrlplane.ErrAlreadyExists, dc.Slug)
		}

		if err := s.set(txn, key, dc); err != nil {
			return err
		}

		return s.set(txn, slugKey, dc.ID.String())
	})
}

// GetDatacenterByID retrieves a datacenter by its ID within a tenant.
func (s *Store) GetDatacenterByID(_ context.Context, tenantID string, datacenterID id.ID) (*datacenter.Datacenter, error) {
	var dc datacenter.Datacenter

	err := s.db.View(func(txn *badger.Txn) error {
		return s.get(txn, prefixDatacenter+idStr(datacenterID), &dc)
	})
	if err != nil {
		return nil, fmt.Errorf("%w: datacenter %s", ctrlplane.ErrNotFound, datacenterID)
	}

	if dc.TenantID != tenantID {
		return nil, fmt.Errorf("%w: datacenter %s", ctrlplane.ErrNotFound, datacenterID)
	}

	return &dc, nil
}

// GetDatacenterBySlug retrieves a datacenter by slug within a tenant.
func (s *Store) GetDatacenterBySlug(_ context.Context, tenantID string, slug string) (*datacenter.Datacenter, error) {
	var dcID string

	err := s.db.View(func(txn *badger.Txn) error {
		return s.get(txn, prefixDatacenterSlug+tenantID+":"+slug, &dcID)
	})
	if err != nil {
		return nil, fmt.Errorf("%w: datacenter slug %s", ctrlplane.ErrNotFound, slug)
	}

	var dc datacenter.Datacenter

	err = s.db.View(func(txn *badger.Txn) error {
		return s.get(txn, prefixDatacenter+dcID, &dc)
	})
	if err != nil {
		return nil, fmt.Errorf("%w: datacenter slug %s", ctrlplane.ErrNotFound, slug)
	}

	return &dc, nil
}

// ListDatacenters returns a filtered, paginated list of datacenters for a tenant.
func (s *Store) ListDatacenters(_ context.Context, tenantID string, opts datacenter.ListOptions) (*datacenter.ListResult, error) {
	var items []*datacenter.Datacenter

	err := s.db.View(func(txn *badger.Txn) error {
		return s.iterate(txn, prefixDatacenter, func(_ string, val []byte) error {
			var dc datacenter.Datacenter
			if err := json.Unmarshal(val, &dc); err != nil {
				return err
			}

			if dc.TenantID != tenantID {
				return nil
			}

			if opts.Status != "" && string(dc.Status) != opts.Status {
				return nil
			}

			if opts.Provider != "" && dc.ProviderName != opts.Provider {
				return nil
			}

			if opts.Region != "" && dc.Region != opts.Region {
				return nil
			}

			items = append(items, &dc)

			return nil
		})
	})
	if err != nil {
		return nil, fmt.Errorf("badger: list datacenters: %w", err)
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})

	total := len(items)

	limit := opts.Limit
	if limit <= 0 || limit > total {
		limit = total
	}

	items = items[:limit]

	return &datacenter.ListResult{
		Items: items,
		Total: total,
	}, nil
}

// UpdateDatacenter persists changes to an existing datacenter.
func (s *Store) UpdateDatacenter(_ context.Context, dc *datacenter.Datacenter) error {
	dc.UpdatedAt = now()

	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixDatacenter + idStr(dc.ID)

		exists, err := s.exists(txn, key)
		if err != nil {
			return err
		}

		if !exists {
			return fmt.Errorf("%w: datacenter %s", ctrlplane.ErrNotFound, dc.ID)
		}

		return s.set(txn, key, dc)
	})
}

// DeleteDatacenter removes a datacenter from the store.
func (s *Store) DeleteDatacenter(_ context.Context, tenantID string, datacenterID id.ID) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixDatacenter + idStr(datacenterID)

		var dc datacenter.Datacenter
		if err := s.get(txn, key, &dc); err != nil {
			return fmt.Errorf("%w: datacenter %s", ctrlplane.ErrNotFound, datacenterID)
		}

		if dc.TenantID != tenantID {
			return fmt.Errorf("%w: datacenter %s", ctrlplane.ErrNotFound, datacenterID)
		}

		if err := s.delete(txn, key); err != nil {
			return err
		}

		slugKey := prefixDatacenterSlug + tenantID + ":" + dc.Slug

		return s.delete(txn, slugKey)
	})
}

// CountDatacentersByTenant returns the total number of datacenters for a tenant.
func (s *Store) CountDatacentersByTenant(_ context.Context, tenantID string) (int, error) {
	count := 0

	err := s.db.View(func(txn *badger.Txn) error {
		return s.iterate(txn, prefixDatacenter, func(_ string, val []byte) error {
			var dc datacenter.Datacenter
			if err := json.Unmarshal(val, &dc); err != nil {
				return err
			}

			if dc.TenantID == tenantID {
				count++
			}

			return nil
		})
	})
	if err != nil {
		return 0, fmt.Errorf("badger: count datacenters: %w", err)
	}

	return count, nil
}

// CountInstancesByDatacenter returns the number of instances linked to a datacenter.
func (s *Store) CountInstancesByDatacenter(_ context.Context, tenantID string, datacenterID id.ID) (int, error) {
	count := 0
	dcKey := idStr(datacenterID)

	err := s.db.View(func(txn *badger.Txn) error {
		return s.iterate(txn, prefixInstance, func(_ string, val []byte) error {
			var raw map[string]any
			if err := json.Unmarshal(val, &raw); err != nil {
				return err
			}

			if raw["tenant_id"] == tenantID && raw["datacenter_id"] == dcKey {
				count++
			}

			return nil
		})
	})
	if err != nil {
		return 0, fmt.Errorf("badger: count instances by datacenter: %w", err)
	}

	return count, nil
}
