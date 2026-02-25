package sqlite

import (
	"context"
	"fmt"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/secrets"
)

func (s *Store) InsertSecret(ctx context.Context, secret *secrets.Secret) error {
	model := toSecretModel(secret)

	_, err := s.sdb.NewInsert(model).Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: insert secret failed: %w", err)
	}

	return nil
}

func (s *Store) GetSecretByKey(ctx context.Context, tenantID string, instanceID id.ID, key string) (*secrets.Secret, error) {
	var model secretModel

	err := s.sdb.NewSelect(&model).
		Where("tenant_id = ? AND instance_id = ? AND key = ?", tenantID, instanceID.String(), key).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%w: secret %s", ctrlplane.ErrNotFound, key)
		}

		return nil, fmt.Errorf("sqlite: get secret by key failed: %w", err)
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

	err := s.sdb.NewSelect(&models).
		Where("tenant_id = ? AND instance_id = ?", tenantID, instanceID.String()).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list secrets failed: %w", err)
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

	res, err := s.sdb.NewUpdate(model).
		Where("tenant_id = ? AND instance_id = ? AND key = ?", secret.TenantID, secret.InstanceID.String(), secret.Key).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: update secret failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: secret %s", ctrlplane.ErrNotFound, secret.Key)
	}

	return nil
}

func (s *Store) DeleteSecret(ctx context.Context, tenantID string, instanceID id.ID, key string) error {
	res, err := s.sdb.NewDelete((*secretModel)(nil)).
		Where("tenant_id = ? AND instance_id = ? AND key = ?", tenantID, instanceID.String(), key).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("sqlite: delete secret failed: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: rows affected check failed: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("%w: secret %s", ctrlplane.ErrNotFound, key)
	}

	return nil
}

func (s *Store) CountSecretsByTenant(ctx context.Context, tenantID string) (int, error) {
	count, err := s.sdb.NewSelect((*secretModel)(nil)).
		Where("tenant_id = ?", tenantID).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("sqlite: count secrets failed: %w", err)
	}

	return int(count), nil
}
