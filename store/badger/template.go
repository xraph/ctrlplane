package badger

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/dgraph-io/badger/v4"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/id"
)

// InsertTemplate persists a new deployment template.
func (s *Store) InsertTemplate(_ context.Context, t *deploy.Template) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixTemplate + idStr(t.ID)

		exists, err := s.exists(txn, key)
		if err != nil {
			return err
		}

		if exists {
			return fmt.Errorf("%w: template %s", ctrlplane.ErrAlreadyExists, t.ID)
		}

		return s.set(txn, key, t)
	})
}

// GetTemplate retrieves a deployment template by ID within a tenant.
func (s *Store) GetTemplate(_ context.Context, tenantID string, templateID id.ID) (*deploy.Template, error) {
	var t deploy.Template

	err := s.db.View(func(txn *badger.Txn) error {
		key := prefixTemplate + idStr(templateID)

		if err := s.get(txn, key, &t); err != nil {
			return err
		}

		if t.TenantID != tenantID {
			return fmt.Errorf("%w: template %s", ctrlplane.ErrNotFound, templateID)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &t, nil
}

// UpdateTemplate persists changes to an existing deployment template.
func (s *Store) UpdateTemplate(_ context.Context, t *deploy.Template) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixTemplate + idStr(t.ID)

		var existing deploy.Template
		if err := s.get(txn, key, &existing); err != nil {
			return fmt.Errorf("%w: template %s", ctrlplane.ErrNotFound, t.ID)
		}

		t.UpdatedAt = now()

		return s.set(txn, key, t)
	})
}

// DeleteTemplate removes a deployment template.
func (s *Store) DeleteTemplate(_ context.Context, tenantID string, templateID id.ID) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixTemplate + idStr(templateID)

		var t deploy.Template
		if err := s.get(txn, key, &t); err != nil {
			return fmt.Errorf("%w: template %s", ctrlplane.ErrNotFound, templateID)
		}

		if t.TenantID != tenantID {
			return fmt.Errorf("%w: template %s", ctrlplane.ErrNotFound, templateID)
		}

		return s.delete(txn, key)
	})
}

// ListTemplates returns a paginated list of deployment templates for a tenant.
func (s *Store) ListTemplates(_ context.Context, tenantID string, opts deploy.ListOptions) (*deploy.TemplateListResult, error) {
	var items []*deploy.Template

	err := s.db.View(func(txn *badger.Txn) error {
		return s.iterate(txn, prefixTemplate, func(_ string, val []byte) error {
			var t deploy.Template
			if err := json.Unmarshal(val, &t); err != nil {
				return fmt.Errorf("badger: json unmarshal failed: %w", err)
			}

			if t.TenantID != tenantID {
				return nil
			}

			items = append(items, &t)

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

	return &deploy.TemplateListResult{
		Items: items,
		Total: total,
	}, nil
}
