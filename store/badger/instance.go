package badger

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/dgraph-io/badger/v4"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
)

func (s *Store) Insert(_ context.Context, inst *instance.Instance) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixInstance + idStr(inst.ID)

		exists, err := s.exists(txn, key)
		if err != nil {
			return err
		}

		if exists {
			return fmt.Errorf("%w: instance %s", ctrlplane.ErrAlreadyExists, inst.ID)
		}

		slugKey := prefixInstanceSlug + inst.TenantID + ":" + inst.Slug

		slugExists, err := s.exists(txn, slugKey)
		if err != nil {
			return err
		}

		if slugExists {
			return fmt.Errorf("%w: slug %s", ctrlplane.ErrAlreadyExists, inst.Slug)
		}

		if err := s.set(txn, key, inst); err != nil {
			return err
		}

		return s.set(txn, slugKey, idStr(inst.ID))
	})
}

func (s *Store) GetByID(_ context.Context, tenantID string, instanceID id.ID) (*instance.Instance, error) {
	var inst instance.Instance

	err := s.db.View(func(txn *badger.Txn) error {
		key := prefixInstance + idStr(instanceID)

		if err := s.get(txn, key, &inst); err != nil {
			return err
		}

		if inst.TenantID != tenantID {
			return fmt.Errorf("%w: instance %s", ctrlplane.ErrNotFound, instanceID)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &inst, nil
}

func (s *Store) GetBySlug(_ context.Context, tenantID string, slug string) (*instance.Instance, error) {
	var inst instance.Instance

	err := s.db.View(func(txn *badger.Txn) error {
		slugKey := prefixInstanceSlug + tenantID + ":" + slug

		var instanceIDStr string
		if err := s.get(txn, slugKey, &instanceIDStr); err != nil {
			return fmt.Errorf("%w: slug %s", ctrlplane.ErrNotFound, slug)
		}

		key := prefixInstance + instanceIDStr

		return s.get(txn, key, &inst)
	})
	if err != nil {
		return nil, err
	}

	return &inst, nil
}

func (s *Store) List(_ context.Context, tenantID string, opts instance.ListOptions) (*instance.ListResult, error) {
	var items []*instance.Instance

	err := s.db.View(func(txn *badger.Txn) error {
		return s.iterate(txn, prefixInstance, func(_ string, val []byte) error {
			var inst instance.Instance
			if err := json.Unmarshal(val, &inst); err != nil {
				return fmt.Errorf("badger: json unmarshal failed: %w", err)
			}

			if inst.TenantID != tenantID {
				return nil
			}

			if opts.State != "" && string(inst.State) != opts.State {
				return nil
			}

			if opts.Provider != "" && inst.ProviderName != opts.Provider {
				return nil
			}

			items = append(items, &inst)

			return nil
		})
	})
	if err != nil {
		return nil, err
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

	return &instance.ListResult{
		Items: items,
		Total: total,
	}, nil
}

func (s *Store) Update(_ context.Context, inst *instance.Instance) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixInstance + idStr(inst.ID)

		var existing instance.Instance
		if err := s.get(txn, key, &existing); err != nil {
			return fmt.Errorf("%w: instance %s", ctrlplane.ErrNotFound, inst.ID)
		}

		inst.UpdatedAt = now()

		return s.set(txn, key, inst)
	})
}

func (s *Store) Delete(_ context.Context, tenantID string, instanceID id.ID) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixInstance + idStr(instanceID)

		var inst instance.Instance
		if err := s.get(txn, key, &inst); err != nil {
			return fmt.Errorf("%w: instance %s", ctrlplane.ErrNotFound, instanceID)
		}

		if inst.TenantID != tenantID {
			return fmt.Errorf("%w: instance %s", ctrlplane.ErrNotFound, instanceID)
		}

		slugKey := prefixInstanceSlug + inst.TenantID + ":" + inst.Slug
		if err := s.delete(txn, slugKey); err != nil {
			return err
		}

		return s.delete(txn, key)
	})
}

func (s *Store) CountByTenant(_ context.Context, tenantID string) (int, error) {
	count := 0

	err := s.db.View(func(txn *badger.Txn) error {
		return s.iterate(txn, prefixInstance, func(_ string, val []byte) error {
			var inst instance.Instance
			if err := json.Unmarshal(val, &inst); err != nil {
				return fmt.Errorf("badger: json unmarshal failed: %w", err)
			}

			if inst.TenantID == tenantID {
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
