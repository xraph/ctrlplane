package secrets

import (
	"context"
	"fmt"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/id"
)

// service implements the Service interface.
type service struct {
	store Store
	vault Vault
	auth  auth.Provider
}

// NewService creates a new secrets service.
func NewService(store Store, vault Vault, auth auth.Provider) Service {
	return &service{
		store: store,
		vault: vault,
		auth:  auth,
	}
}

// Set creates or updates a secret for an instance.
func (s *service) Set(ctx context.Context, req SetRequest) (*Secret, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("set secret: %w", err)
	}

	vaultKey := fmt.Sprintf("%s/%s/%s", claims.TenantID, req.InstanceID, req.Key)

	// Try to find an existing secret with this key.
	existing, err := s.store.GetSecretByKey(ctx, claims.TenantID, req.InstanceID, req.Key)
	if err == nil && existing != nil {
		// Update existing secret.
		existing.Version++
		existing.UpdatedAt = time.Now().UTC()

		if err := s.vault.Store(ctx, vaultKey, []byte(req.Value)); err != nil {
			return nil, fmt.Errorf("set secret: vault store: %w", err)
		}

		if err := s.store.UpdateSecret(ctx, existing); err != nil {
			return nil, fmt.Errorf("set secret: update: %w", err)
		}

		return existing, nil
	}

	// Create new secret.
	secretType := req.Type
	if secretType == "" {
		secretType = SecretEnvVar
	}

	secret := &Secret{
		Entity:     ctrlplane.NewEntity(id.PrefixSecret),
		TenantID:   claims.TenantID,
		InstanceID: req.InstanceID,
		Key:        req.Key,
		Type:       secretType,
		Version:    1,
	}

	if err := s.vault.Store(ctx, vaultKey, []byte(req.Value)); err != nil {
		return nil, fmt.Errorf("set secret: vault store: %w", err)
	}

	if err := s.store.InsertSecret(ctx, secret); err != nil {
		return nil, fmt.Errorf("set secret: insert: %w", err)
	}

	return secret, nil
}

// Get retrieves a secret's metadata by instance and key.
func (s *service) Get(ctx context.Context, instanceID id.ID, key string) (*Secret, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("get secret: %w", err)
	}

	secret, err := s.store.GetSecretByKey(ctx, claims.TenantID, instanceID, key)
	if err != nil {
		return nil, fmt.Errorf("get secret: %w", err)
	}

	return secret, nil
}

// Delete removes a secret from an instance.
func (s *service) Delete(ctx context.Context, instanceID id.ID, key string) error {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return fmt.Errorf("delete secret: %w", err)
	}

	vaultKey := fmt.Sprintf("%s/%s/%s", claims.TenantID, instanceID, key)

	if err := s.vault.Delete(ctx, vaultKey); err != nil {
		return fmt.Errorf("delete secret: vault delete: %w", err)
	}

	if err := s.store.DeleteSecret(ctx, claims.TenantID, instanceID, key); err != nil {
		return fmt.Errorf("delete secret: store delete: %w", err)
	}

	return nil
}

// List returns all secrets for an instance (values omitted).
func (s *service) List(ctx context.Context, instanceID id.ID) ([]Secret, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("list secrets: %w", err)
	}

	secrets, err := s.store.ListSecrets(ctx, claims.TenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("list secrets: %w", err)
	}

	return secrets, nil
}

// Inject resolves all env-type secrets for an instance into a key-value map.
func (s *service) Inject(ctx context.Context, instanceID id.ID) (map[string]string, error) {
	claims, err := auth.RequireClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("inject secrets: %w", err)
	}

	secrets, err := s.store.ListSecrets(ctx, claims.TenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("inject secrets: list: %w", err)
	}

	result := make(map[string]string)

	for _, secret := range secrets {
		if secret.Type != SecretEnvVar {
			continue
		}

		vaultKey := fmt.Sprintf("%s/%s/%s", claims.TenantID, instanceID, secret.Key)

		value, err := s.vault.Retrieve(ctx, vaultKey)
		if err != nil {
			return nil, fmt.Errorf("inject secrets: retrieve %q: %w", secret.Key, err)
		}

		result[secret.Key] = string(value)
	}

	return result, nil
}
