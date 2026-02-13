package mongo

import (
	"context"
	"errors"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/secrets"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// InsertSecret persists a new secret.
func (s *Store) InsertSecret(ctx context.Context, secret *secrets.Secret) error {
	m := toSecretModel(secret)

	_, err := s.col(colSecrets).InsertOne(ctx, m)
	if err != nil {
		if isDuplicateKeyError(err) {
			return fmt.Errorf("mongo: insert secret: %w: %s", ctrlplane.ErrAlreadyExists, m.Key)
		}

		return fmt.Errorf("mongo: insert secret: %w", err)
	}

	return nil
}

// GetSecretByKey retrieves a secret by instance ID and key.
func (s *Store) GetSecretByKey(ctx context.Context, tenantID string, instanceID id.ID, key string) (*secrets.Secret, error) {
	filter := bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "instance_id", Value: idStr(instanceID)},
		{Key: "key", Value: key},
	}

	var m secretModel

	err := s.col(colSecrets).FindOne(ctx, filter).Decode(&m)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("mongo: get secret: %w: %s", ctrlplane.ErrNotFound, key)
		}

		return nil, fmt.Errorf("mongo: get secret: %w", err)
	}

	return fromSecretModel(&m), nil
}

// ListSecrets returns all secrets for an instance (values omitted).
func (s *Store) ListSecrets(ctx context.Context, tenantID string, instanceID id.ID) ([]secrets.Secret, error) {
	filter := bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "instance_id", Value: idStr(instanceID)},
	}

	// Omit the value field from the projection for security.
	projection := bson.D{{Key: "value", Value: 0}}
	opts := options.Find().SetProjection(projection)

	cursor, err := s.col(colSecrets).Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("mongo: list secrets: %w", err)
	}
	defer cursor.Close(ctx)

	items := make([]secrets.Secret, 0)

	for cursor.Next(ctx) {
		var m secretModel

		if err := cursor.Decode(&m); err != nil {
			return nil, fmt.Errorf("mongo: list secrets decode: %w", err)
		}

		items = append(items, *fromSecretModel(&m))
	}

	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("mongo: list secrets cursor: %w", err)
	}

	return items, nil
}

// UpdateSecret persists changes to a secret.
func (s *Store) UpdateSecret(ctx context.Context, secret *secrets.Secret) error {
	secret.UpdatedAt = now()
	m := toSecretModel(secret)

	filter := bson.D{
		{Key: "tenant_id", Value: m.TenantID},
		{Key: "instance_id", Value: m.InstanceID},
		{Key: "key", Value: m.Key},
	}

	result, err := s.col(colSecrets).ReplaceOne(ctx, filter, m)
	if err != nil {
		return fmt.Errorf("mongo: update secret: %w", err)
	}

	if result.MatchedCount == 0 {
		return fmt.Errorf("mongo: update secret: %w: %s", ctrlplane.ErrNotFound, m.Key)
	}

	return nil
}

// DeleteSecret removes a secret by instance ID and key.
func (s *Store) DeleteSecret(ctx context.Context, tenantID string, instanceID id.ID, key string) error {
	filter := bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "instance_id", Value: idStr(instanceID)},
		{Key: "key", Value: key},
	}

	result, err := s.col(colSecrets).DeleteOne(ctx, filter)
	if err != nil {
		return fmt.Errorf("mongo: delete secret: %w", err)
	}

	if result.DeletedCount == 0 {
		return fmt.Errorf("mongo: delete secret: %w: %s", ctrlplane.ErrNotFound, key)
	}

	return nil
}

// CountSecretsByTenant returns the number of secrets for a tenant.
func (s *Store) CountSecretsByTenant(ctx context.Context, tenantID string) (int, error) {
	filter := bson.D{{Key: "tenant_id", Value: tenantID}}

	count, err := s.col(colSecrets).CountDocuments(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("mongo: count secrets: %w", err)
	}

	return int(count), nil
}
