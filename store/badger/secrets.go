package badger

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dgraph-io/badger/v4"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/secrets"
)

func (s *Store) InsertSecret(_ context.Context, secret *secrets.Secret) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixSecret + idStr(secret.InstanceID) + ":" + secret.Key

		exists, err := s.exists(txn, key)
		if err != nil {
			return err
		}

		if exists {
			return fmt.Errorf("%w: secret %s", ctrlplane.ErrAlreadyExists, secret.Key)
		}

		return s.set(txn, key, secret)
	})
}

func (s *Store) GetSecretByKey(_ context.Context, tenantID string, instanceID id.ID, key string) (*secrets.Secret, error) {
	var secret secrets.Secret

	err := s.db.View(func(txn *badger.Txn) error {
		secretKey := prefixSecret + idStr(instanceID) + ":" + key

		if err := s.get(txn, secretKey, &secret); err != nil {
			return fmt.Errorf("%w: secret %s", ctrlplane.ErrNotFound, key)
		}

		if secret.TenantID != tenantID {
			return fmt.Errorf("%w: secret %s", ctrlplane.ErrNotFound, key)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &secret, nil
}

func (s *Store) ListSecrets(_ context.Context, tenantID string, instanceID id.ID) ([]secrets.Secret, error) {
	var items []secrets.Secret

	err := s.db.View(func(txn *badger.Txn) error {
		prefix := prefixSecret + idStr(instanceID) + ":"

		return s.iterate(txn, prefix, func(_ string, val []byte) error {
			var secret secrets.Secret
			if err := json.Unmarshal(val, &secret); err != nil {
				return fmt.Errorf("badger: json unmarshal failed: %w", err)
			}

			if secret.TenantID != tenantID {
				return nil
			}

			secret.Value = nil

			items = append(items, secret)

			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	return items, nil
}

func (s *Store) UpdateSecret(_ context.Context, secret *secrets.Secret) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixSecret + idStr(secret.InstanceID) + ":" + secret.Key

		var existing secrets.Secret
		if err := s.get(txn, key, &existing); err != nil {
			return fmt.Errorf("%w: secret %s", ctrlplane.ErrNotFound, secret.Key)
		}

		secret.UpdatedAt = now()

		return s.set(txn, key, secret)
	})
}

func (s *Store) DeleteSecret(_ context.Context, tenantID string, instanceID id.ID, key string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		secretKey := prefixSecret + idStr(instanceID) + ":" + key

		var secret secrets.Secret
		if err := s.get(txn, secretKey, &secret); err != nil {
			return fmt.Errorf("%w: secret %s", ctrlplane.ErrNotFound, key)
		}

		if secret.TenantID != tenantID {
			return fmt.Errorf("%w: secret %s", ctrlplane.ErrNotFound, key)
		}

		return s.delete(txn, secretKey)
	})
}

func (s *Store) CountSecretsByTenant(_ context.Context, tenantID string) (int, error) {
	count := 0

	err := s.db.View(func(txn *badger.Txn) error {
		return s.iterate(txn, prefixSecret, func(_ string, val []byte) error {
			var secret secrets.Secret
			if err := json.Unmarshal(val, &secret); err != nil {
				return fmt.Errorf("badger: json unmarshal failed: %w", err)
			}

			if secret.TenantID == tenantID {
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
