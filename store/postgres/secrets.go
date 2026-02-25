package postgres

import (
	"context"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/secrets"
)

func (s *Store) InsertSecret(ctx context.Context, secret *secrets.Secret) error {
	model := toSecretModel(secret)

	_, err := s.pg.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: insert secret failed: %w", err)
	}

	return nil
}

func (s *Store) GetSecretByKey(ctx context.Context, tenantID string, instanceID id.ID, key string) (*secrets.Secret, error) {
	var model secretModel

	err := s.pg.NewSelect(&model).
		Where("tenant_id = $1 AND instance_id = $2 AND key = $3", tenantID, instanceID.String(), key).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: secret %s", ctrlplane.ErrNotFound, key)
		}

		return nil, fmt.Errorf("postgres: get secret by key failed: %w", err)
	}

	secret := &secrets.Secret{
		Entity: ctrlplane.Entity{
			CreatedAt: model.CreatedAt,
			UpdatedAt: model.UpdatedAt,
		},
		TenantID:   model.TenantID,
		InstanceID: id.MustParse(model.InstanceID),
		Key:        model.Key,
		Value:      model.Value,
	}

	return secret, nil
}

func (s *Store) ListSecrets(ctx context.Context, tenantID string, instanceID id.ID) ([]secrets.Secret, error) {
	var models []secretModel

	err := s.pg.NewSelect(&models).
		Where("tenant_id = $1 AND instance_id = $2", tenantID, instanceID.String()).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: list secrets failed: %w", err)
	}

	items := make([]secrets.Secret, 0, len(models))
	for _, model := range models {
		secret := secrets.Secret{
			Entity: ctrlplane.Entity{
				CreatedAt: model.CreatedAt,
				UpdatedAt: model.UpdatedAt,
			},
			TenantID:   model.TenantID,
			InstanceID: id.MustParse(model.InstanceID),
			Key:        model.Key,
			Value:      nil,
		}
		items = append(items, secret)
	}

	return items, nil
}

func (s *Store) UpdateSecret(ctx context.Context, secret *secrets.Secret) error {
	secret.UpdatedAt = now()
	model := toSecretModel(secret)

	res, err := s.pg.NewUpdate(model).
		Where("tenant_id = $1 AND instance_id = $2 AND key = $3", secret.TenantID, secret.InstanceID.String(), secret.Key).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: update secret failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: secret %s", ctrlplane.ErrNotFound, secret.Key)
	}

	return nil
}

func (s *Store) DeleteSecret(ctx context.Context, tenantID string, instanceID id.ID, key string) error {
	res, err := s.pg.NewDelete((*secretModel)(nil)).
		Where("tenant_id = $1 AND instance_id = $2 AND key = $3", tenantID, instanceID.String(), key).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("postgres: delete secret failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: secret %s", ctrlplane.ErrNotFound, key)
	}

	return nil
}

func (s *Store) CountSecretsByTenant(ctx context.Context, tenantID string) (int, error) {
	count, err := s.pg.NewSelect((*secretModel)(nil)).
		Where("tenant_id = $1", tenantID).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgres: count secrets failed: %w", err)
	}

	return int(count), nil
}
