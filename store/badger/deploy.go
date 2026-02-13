package badger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/dgraph-io/badger/v4"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/id"
)

func (s *Store) InsertDeployment(_ context.Context, d *deploy.Deployment) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixDeployment + idStr(d.ID)

		exists, err := s.exists(txn, key)
		if err != nil {
			return err
		}

		if exists {
			return fmt.Errorf("%w: deployment %s", ctrlplane.ErrAlreadyExists, d.ID)
		}

		return s.set(txn, key, d)
	})
}

func (s *Store) GetDeployment(_ context.Context, tenantID string, deployID id.ID) (*deploy.Deployment, error) {
	var d deploy.Deployment

	err := s.db.View(func(txn *badger.Txn) error {
		key := prefixDeployment + idStr(deployID)

		if err := s.get(txn, key, &d); err != nil {
			return err
		}

		if d.TenantID != tenantID {
			return fmt.Errorf("%w: deployment %s", ctrlplane.ErrNotFound, deployID)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &d, nil
}

func (s *Store) UpdateDeployment(_ context.Context, d *deploy.Deployment) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixDeployment + idStr(d.ID)

		var existing deploy.Deployment
		if err := s.get(txn, key, &existing); err != nil {
			return fmt.Errorf("%w: deployment %s", ctrlplane.ErrNotFound, d.ID)
		}

		d.UpdatedAt = now()

		return s.set(txn, key, d)
	})
}

func (s *Store) ListDeployments(_ context.Context, tenantID string, instanceID id.ID, opts deploy.ListOptions) (*deploy.DeployListResult, error) {
	var items []*deploy.Deployment

	err := s.db.View(func(txn *badger.Txn) error {
		return s.iterate(txn, prefixDeployment, func(_ string, val []byte) error {
			var d deploy.Deployment
			if err := json.Unmarshal(val, &d); err != nil {
				return fmt.Errorf("badger: json unmarshal failed: %w", err)
			}

			if d.TenantID != tenantID || d.InstanceID != instanceID {
				return nil
			}

			items = append(items, &d)

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

	return &deploy.DeployListResult{
		Items: items,
		Total: total,
	}, nil
}

func (s *Store) InsertRelease(_ context.Context, r *deploy.Release) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixRelease + idStr(r.ID)

		exists, err := s.exists(txn, key)
		if err != nil {
			return err
		}

		if exists {
			return fmt.Errorf("%w: release %s", ctrlplane.ErrAlreadyExists, r.ID)
		}

		return s.set(txn, key, r)
	})
}

func (s *Store) GetRelease(_ context.Context, tenantID string, releaseID id.ID) (*deploy.Release, error) {
	var r deploy.Release

	err := s.db.View(func(txn *badger.Txn) error {
		key := prefixRelease + idStr(releaseID)

		if err := s.get(txn, key, &r); err != nil {
			return err
		}

		if r.TenantID != tenantID {
			return fmt.Errorf("%w: release %s", ctrlplane.ErrNotFound, releaseID)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &r, nil
}

func (s *Store) ListReleases(_ context.Context, tenantID string, instanceID id.ID, opts deploy.ListOptions) (*deploy.ReleaseListResult, error) {
	var items []*deploy.Release

	err := s.db.View(func(txn *badger.Txn) error {
		return s.iterate(txn, prefixRelease, func(_ string, val []byte) error {
			var r deploy.Release
			if err := json.Unmarshal(val, &r); err != nil {
				return fmt.Errorf("badger: json unmarshal failed: %w", err)
			}

			if r.TenantID != tenantID || r.InstanceID != instanceID {
				return nil
			}

			items = append(items, &r)

			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Version > items[j].Version
	})

	total := len(items)

	limit := opts.Limit
	if limit <= 0 || limit > total {
		limit = total
	}

	items = items[:limit]

	return &deploy.ReleaseListResult{
		Items: items,
		Total: total,
	}, nil
}

func (s *Store) NextReleaseVersion(_ context.Context, tenantID string, instanceID id.ID) (int, error) {
	versionKey := prefixReleaseVersion + tenantID + ":" + idStr(instanceID)

	var nextVersion int

	err := s.db.Update(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(versionKey))
		if err != nil {
			if errors.Is(err, badger.ErrKeyNotFound) {
				nextVersion = 1
			} else {
				return fmt.Errorf("badger: get version failed: %w", err)
			}
		} else {
			err = item.Value(func(val []byte) error {
				version, parseErr := strconv.Atoi(string(val))
				if parseErr != nil {
					return fmt.Errorf("badger: parse version failed: %w", parseErr)
				}

				nextVersion = version + 1

				return nil
			})
			if err != nil {
				return err
			}
		}

		return txn.Set([]byte(versionKey), []byte(strconv.Itoa(nextVersion)))
	})
	if err != nil {
		return 0, err
	}

	return nextVersion, nil
}
