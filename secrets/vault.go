package secrets

import "context"

// Vault abstracts the secret storage backend.
// Implement for HashiCorp Vault, AWS Secrets Manager, sealed secrets, etc.
type Vault interface {
	// Store encrypts and persists a secret value.
	Store(ctx context.Context, key string, value []byte) error

	// Retrieve decrypts and returns a secret value.
	Retrieve(ctx context.Context, key string) ([]byte, error)

	// Delete removes a secret from the vault.
	Delete(ctx context.Context, key string) error

	// Rotate generates a new encryption key version.
	Rotate(ctx context.Context, key string) error
}
