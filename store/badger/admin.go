package badger

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/dgraph-io/badger/v4"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/admin"
)

func (s *Store) InsertTenant(_ context.Context, tenant *admin.Tenant) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixTenant + idStr(tenant.ID)

		exists, err := s.exists(txn, key)
		if err != nil {
			return err
		}

		if exists {
			return fmt.Errorf("%w: tenant %s", ctrlplane.ErrAlreadyExists, tenant.ID)
		}

		slugKey := prefixTenantSlug + tenant.Slug

		slugExists, err := s.exists(txn, slugKey)
		if err != nil {
			return err
		}

		if slugExists {
			return fmt.Errorf("%w: slug %s", ctrlplane.ErrAlreadyExists, tenant.Slug)
		}

		if err := s.set(txn, key, tenant); err != nil {
			return err
		}

		return s.set(txn, slugKey, idStr(tenant.ID))
	})
}

func (s *Store) GetTenant(_ context.Context, tenantID string) (*admin.Tenant, error) {
	var tenant admin.Tenant

	err := s.db.View(func(txn *badger.Txn) error {
		key := prefixTenant + tenantID

		return s.get(txn, key, &tenant)
	})
	if err != nil {
		return nil, err
	}

	return &tenant, nil
}

func (s *Store) GetTenantBySlug(_ context.Context, slug string) (*admin.Tenant, error) {
	var tenant admin.Tenant

	err := s.db.View(func(txn *badger.Txn) error {
		slugKey := prefixTenantSlug + slug

		var tenantID string
		if err := s.get(txn, slugKey, &tenantID); err != nil {
			return fmt.Errorf("%w: slug %s", ctrlplane.ErrNotFound, slug)
		}

		key := prefixTenant + tenantID

		return s.get(txn, key, &tenant)
	})
	if err != nil {
		return nil, err
	}

	return &tenant, nil
}

func (s *Store) ListTenants(_ context.Context, opts admin.ListTenantsOptions) (*admin.TenantListResult, error) {
	var items []*admin.Tenant

	err := s.db.View(func(txn *badger.Txn) error {
		return s.iterate(txn, prefixTenant, func(_ string, val []byte) error {
			var tenant admin.Tenant
			if err := json.Unmarshal(val, &tenant); err != nil {
				return fmt.Errorf("badger: json unmarshal failed: %w", err)
			}

			if opts.Status != "" && string(tenant.Status) != opts.Status {
				return nil
			}

			items = append(items, &tenant)

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

	return &admin.TenantListResult{
		Items: items,
		Total: total,
	}, nil
}

func (s *Store) UpdateTenant(_ context.Context, tenant *admin.Tenant) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixTenant + idStr(tenant.ID)

		var existing admin.Tenant
		if err := s.get(txn, key, &existing); err != nil {
			return fmt.Errorf("%w: tenant %s", ctrlplane.ErrNotFound, tenant.ID)
		}

		tenant.UpdatedAt = now()

		return s.set(txn, key, tenant)
	})
}

func (s *Store) DeleteTenant(_ context.Context, tenantID string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixTenant + tenantID

		var tenant admin.Tenant
		if err := s.get(txn, key, &tenant); err != nil {
			return fmt.Errorf("%w: tenant %s", ctrlplane.ErrNotFound, tenantID)
		}

		slugKey := prefixTenantSlug + tenant.Slug
		if err := s.delete(txn, slugKey); err != nil {
			return err
		}

		return s.delete(txn, key)
	})
}

func (s *Store) CountTenants(_ context.Context) (int, error) {
	count := 0

	err := s.db.View(func(txn *badger.Txn) error {
		return s.iterate(txn, prefixTenant, func(_ string, _ []byte) error {
			count++

			return nil
		})
	})
	if err != nil {
		return 0, err
	}

	return count, nil
}

func (s *Store) CountTenantsByStatus(_ context.Context, status admin.TenantStatus) (int, error) {
	count := 0

	err := s.db.View(func(txn *badger.Txn) error {
		return s.iterate(txn, prefixTenant, func(_ string, val []byte) error {
			var tenant admin.Tenant
			if err := json.Unmarshal(val, &tenant); err != nil {
				return fmt.Errorf("badger: json unmarshal failed: %w", err)
			}

			if tenant.Status == status {
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

func (s *Store) InsertAuditEntry(_ context.Context, entry *admin.AuditEntry) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := fmt.Sprintf("%s%s:%s", prefixAudit, entry.TenantID, entry.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"))

		return s.set(txn, key, entry)
	})
}

func (s *Store) QueryAuditLog(_ context.Context, opts admin.AuditQuery) (*admin.AuditResult, error) {
	var items []admin.AuditEntry

	err := s.db.View(func(txn *badger.Txn) error {
		prefix := prefixAudit

		if opts.TenantID != "" {
			prefix = prefixAudit + opts.TenantID + ":"
		}

		return s.iterate(txn, prefix, func(_ string, val []byte) error {
			var entry admin.AuditEntry
			if err := json.Unmarshal(val, &entry); err != nil {
				return fmt.Errorf("badger: json unmarshal failed: %w", err)
			}

			if opts.TenantID != "" && entry.TenantID != opts.TenantID {
				return nil
			}

			if opts.ActorID != "" && entry.ActorID != opts.ActorID {
				return nil
			}

			if opts.Action != "" && entry.Action != opts.Action {
				return nil
			}

			if opts.Resource != "" && entry.Resource != opts.Resource {
				return nil
			}

			if !opts.Since.IsZero() && entry.CreatedAt.Before(opts.Since) {
				return nil
			}

			if !opts.Until.IsZero() && entry.CreatedAt.After(opts.Until) {
				return nil
			}

			items = append(items, entry)

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

	return &admin.AuditResult{
		Items: items,
		Total: total,
	}, nil
}
