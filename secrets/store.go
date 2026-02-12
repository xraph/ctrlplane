package secrets

import (
	"context"

	"github.com/xraph/ctrlplane/id"
)

// Store is the persistence interface for secrets.
type Store interface {
	// InsertSecret persists a new secret.
	InsertSecret(ctx context.Context, secret *Secret) error

	// GetSecretByKey retrieves a secret by instance ID and key.
	GetSecretByKey(ctx context.Context, tenantID string, instanceID id.ID, key string) (*Secret, error)

	// ListSecrets returns all secrets for an instance (values omitted).
	ListSecrets(ctx context.Context, tenantID string, instanceID id.ID) ([]Secret, error)

	// UpdateSecret persists changes to a secret.
	UpdateSecret(ctx context.Context, secret *Secret) error

	// DeleteSecret removes a secret by instance ID and key.
	DeleteSecret(ctx context.Context, tenantID string, instanceID id.ID, key string) error

	// CountSecretsByTenant returns the number of secrets for a tenant.
	CountSecretsByTenant(ctx context.Context, tenantID string) (int, error)
}
