package badger

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/dgraph-io/badger/v4"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/health"
	"github.com/xraph/ctrlplane/id"
)

func (s *Store) InsertCheck(_ context.Context, check *health.HealthCheck) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixHealthCheck + idStr(check.ID)

		exists, err := s.exists(txn, key)
		if err != nil {
			return err
		}

		if exists {
			return fmt.Errorf("%w: health check %s", ctrlplane.ErrAlreadyExists, check.ID)
		}

		return s.set(txn, key, check)
	})
}

func (s *Store) GetCheck(_ context.Context, tenantID string, checkID id.ID) (*health.HealthCheck, error) {
	var check health.HealthCheck

	err := s.db.View(func(txn *badger.Txn) error {
		key := prefixHealthCheck + idStr(checkID)

		if err := s.get(txn, key, &check); err != nil {
			return err
		}

		if check.TenantID != tenantID {
			return fmt.Errorf("%w: health check %s", ctrlplane.ErrNotFound, checkID)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &check, nil
}

func (s *Store) ListChecks(_ context.Context, tenantID string, instanceID id.ID) ([]health.HealthCheck, error) {
	var items []health.HealthCheck

	err := s.db.View(func(txn *badger.Txn) error {
		return s.iterate(txn, prefixHealthCheck, func(_ string, val []byte) error {
			var check health.HealthCheck
			if err := json.Unmarshal(val, &check); err != nil {
				return fmt.Errorf("badger: json unmarshal failed: %w", err)
			}

			if check.TenantID == tenantID && check.InstanceID == instanceID {
				items = append(items, check)
			}

			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	return items, nil
}

func (s *Store) UpdateCheck(_ context.Context, check *health.HealthCheck) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixHealthCheck + idStr(check.ID)

		var existing health.HealthCheck
		if err := s.get(txn, key, &existing); err != nil {
			return fmt.Errorf("%w: health check %s", ctrlplane.ErrNotFound, check.ID)
		}

		check.UpdatedAt = now()

		return s.set(txn, key, check)
	})
}

func (s *Store) DeleteCheck(_ context.Context, tenantID string, checkID id.ID) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixHealthCheck + idStr(checkID)

		var check health.HealthCheck
		if err := s.get(txn, key, &check); err != nil {
			return fmt.Errorf("%w: health check %s", ctrlplane.ErrNotFound, checkID)
		}

		if check.TenantID != tenantID {
			return fmt.Errorf("%w: health check %s", ctrlplane.ErrNotFound, checkID)
		}

		return s.delete(txn, key)
	})
}

func (s *Store) InsertResult(_ context.Context, result *health.HealthResult) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixHealthResult + idStr(result.CheckID) + ":" + result.CheckedAt.Format("2006-01-02T15:04:05.999999999Z07:00")

		return s.set(txn, key, result)
	})
}

func (s *Store) ListResults(_ context.Context, tenantID string, checkID id.ID, opts health.HistoryOptions) ([]health.HealthResult, error) {
	var items []health.HealthResult

	err := s.db.View(func(txn *badger.Txn) error {
		prefix := prefixHealthResult + idStr(checkID) + ":"

		return s.iterate(txn, prefix, func(_ string, val []byte) error {
			var result health.HealthResult
			if err := json.Unmarshal(val, &result); err != nil {
				return fmt.Errorf("badger: json unmarshal failed: %w", err)
			}

			if !opts.Since.IsZero() && result.CheckedAt.Before(opts.Since) {
				return nil
			}

			if !opts.Until.IsZero() && result.CheckedAt.After(opts.Until) {
				return nil
			}

			items = append(items, result)

			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].CheckedAt.After(items[j].CheckedAt)
	})

	if opts.Limit > 0 && len(items) > opts.Limit {
		items = items[:opts.Limit]
	}

	return items, nil
}

func (s *Store) GetLatestResult(_ context.Context, tenantID string, checkID id.ID) (*health.HealthResult, error) {
	var latest *health.HealthResult

	err := s.db.View(func(txn *badger.Txn) error {
		prefix := prefixHealthResult + idStr(checkID) + ":"

		return s.iterate(txn, prefix, func(_ string, val []byte) error {
			var result health.HealthResult
			if err := json.Unmarshal(val, &result); err != nil {
				return fmt.Errorf("badger: json unmarshal failed: %w", err)
			}

			if latest == nil || result.CheckedAt.After(latest.CheckedAt) {
				latest = &result
			}

			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	if latest == nil {
		return nil, fmt.Errorf("%w: no results for check %s", ctrlplane.ErrNotFound, checkID)
	}

	return latest, nil
}
