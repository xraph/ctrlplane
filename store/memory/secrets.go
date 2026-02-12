package memory

import (
	"context"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/secrets"
)

func secretKey(instanceID id.ID, key string) string {
	return idStr(instanceID) + ":" + key
}

func (s *Store) InsertSecret(_ context.Context, secret *secrets.Secret) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := secretKey(secret.InstanceID, secret.Key)
	if _, exists := s.secretStore[key]; exists {
		return fmt.Errorf("%w: secret %s", ctrlplane.ErrAlreadyExists, secret.Key)
	}

	clone := *secret
	s.secretStore[key] = &clone

	return nil
}

func (s *Store) GetSecretByKey(_ context.Context, tenantID string, instanceID id.ID, key string) (*secrets.Secret, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	secret, ok := s.secretStore[secretKey(instanceID, key)]
	if !ok || secret.TenantID != tenantID {
		return nil, fmt.Errorf("%w: secret %s", ctrlplane.ErrNotFound, key)
	}

	clone := *secret

	return &clone, nil
}

func (s *Store) ListSecrets(_ context.Context, tenantID string, instanceID id.ID) ([]secrets.Secret, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	instKey := idStr(instanceID)

	var result []secrets.Secret

	for _, secret := range s.secretStore {
		if secret.TenantID == tenantID && idStr(secret.InstanceID) == instKey {
			clone := *secret
			clone.Value = nil

			result = append(result, clone)
		}
	}

	return result, nil
}

func (s *Store) UpdateSecret(_ context.Context, secret *secrets.Secret) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := secretKey(secret.InstanceID, secret.Key)
	if _, ok := s.secretStore[key]; !ok {
		return fmt.Errorf("%w: secret %s", ctrlplane.ErrNotFound, secret.Key)
	}

	secret.UpdatedAt = now()
	clone := *secret
	s.secretStore[key] = &clone

	return nil
}

func (s *Store) DeleteSecret(_ context.Context, tenantID string, instanceID id.ID, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sKey := secretKey(instanceID, key)

	secret, ok := s.secretStore[sKey]
	if !ok || secret.TenantID != tenantID {
		return fmt.Errorf("%w: secret %s", ctrlplane.ErrNotFound, key)
	}

	delete(s.secretStore, sKey)

	return nil
}

func (s *Store) CountSecretsByTenant(_ context.Context, tenantID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0

	for _, secret := range s.secretStore {
		if secret.TenantID == tenantID {
			count++
		}
	}

	return count, nil
}
