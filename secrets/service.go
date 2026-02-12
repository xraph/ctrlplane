package secrets

import (
	"context"

	"github.com/xraph/ctrlplane/id"
)

// Service manages secrets for instances.
type Service interface {
	// Set creates or updates a secret for an instance.
	Set(ctx context.Context, req SetRequest) (*Secret, error)

	// Get retrieves a secret's metadata by instance and key.
	Get(ctx context.Context, instanceID id.ID, key string) (*Secret, error)

	// Delete removes a secret from an instance.
	Delete(ctx context.Context, instanceID id.ID, key string) error

	// List returns all secrets for an instance (values omitted).
	List(ctx context.Context, instanceID id.ID) ([]Secret, error)

	// Inject resolves all env-type secrets for an instance into a key-value map.
	Inject(ctx context.Context, instanceID id.ID) (map[string]string, error)
}

// SetRequest holds the parameters for creating or updating a secret.
type SetRequest struct {
	InstanceID id.ID      `json:"instance_id" validate:"required"`
	Key        string     `json:"key"         validate:"required"`
	Value      string     `json:"value"       validate:"required"`
	Type       SecretType `default:"env"      json:"type"`
}
