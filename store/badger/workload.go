package badger

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dgraph-io/badger/v4"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/pagination"
	"github.com/xraph/ctrlplane/workload"
)

const prefixWorkload = "wkld:"

// InsertWorkload persists a workload with a unique tenant and slug.
func (s *Store) InsertWorkload(ctx context.Context, w *workload.Workload) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	return s.db.Update(func(txn *badger.Txn) error {
		exists, err := s.exists(txn, prefixWorkload+w.ID.String())
		if err != nil {
			return err
		}

		if exists {
			return fmt.Errorf("workload %s: %w", w.ID, ctrlplane.ErrAlreadyExists)
		}

		if err := s.workloadSlugAvailable(txn, w); err != nil {
			return err
		}

		return s.set(txn, prefixWorkload+w.ID.String(), w)
	})
}

func (s *Store) workloadSlugAvailable(txn *badger.Txn, w *workload.Workload) error {
	return s.iterate(txn, prefixWorkload, func(_ string, data []byte) error {
		var existing workload.Workload
		if err := json.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("decode workload: %w", err)
		}

		if existing.ID != w.ID && existing.TenantID == w.TenantID && existing.Slug == w.Slug {
			return fmt.Errorf("workload slug %s: %w", w.Slug, ctrlplane.ErrAlreadyExists)
		}

		return nil
	})
}

// GetWorkloadByID retrieves a workload. An empty tenant is the system admin view.
func (s *Store) GetWorkloadByID(ctx context.Context, tenant string, target id.ID) (*workload.Workload, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var row workload.Workload

	err := s.db.View(func(txn *badger.Txn) error { return s.get(txn, prefixWorkload+target.String(), &row) })
	if err != nil {
		return nil, err
	}

	if tenant != "" && row.TenantID != tenant {
		return nil, ctrlplane.ErrNotFound
	}

	return &row, nil
}

// GetWorkloadBySlug retrieves a workload in the supplied tenant.
func (s *Store) GetWorkloadBySlug(ctx context.Context, tenant, slug string) (*workload.Workload, error) {
	rows, err := s.ListWorkloads(ctx, tenant, workload.ListOptions{})
	if err != nil {
		return nil, err
	}

	for _, row := range rows.Items {
		if row.Slug == slug {
			return row, nil
		}
	}

	return nil, ctrlplane.ErrNotFound
}

// ListWorkloads returns a stable bounded list and the full matching count.
func (s *Store) ListWorkloads(ctx context.Context, tenant string, opts workload.ListOptions) (*workload.ListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	items := make([]*workload.Workload, 0)

	err := s.db.View(func(txn *badger.Txn) error {
		return s.iterate(txn, prefixWorkload, func(_ string, data []byte) error {
			if err := ctx.Err(); err != nil {
				return err
			}

			var row workload.Workload
			if err := json.Unmarshal(data, &row); err != nil {
				return fmt.Errorf("decode workload: %w", err)
			}

			if tenant != "" && row.TenantID != tenant {
				return nil
			}

			if opts.State != "" && row.State != opts.State {
				return nil
			}

			if opts.ProviderName != "" && row.ProviderName != opts.ProviderName {
				return nil
			}

			if opts.Region != "" && row.Region != opts.Region {
				return nil
			}

			items = append(items, &row)

			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	items, next, total, err := pagination.Page(items, opts.Cursor, opts.Limit, func(v *workload.Workload) ctrlplane.Entity { return v.Entity })
	if err != nil {
		return nil, err
	}

	return &workload.ListResult{Items: items, NextCursor: next, Total: total}, nil
}

func (s *Store) UpdateWorkload(ctx context.Context, w *workload.Workload) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	return s.db.Update(func(txn *badger.Txn) error {
		var existing workload.Workload
		if err := s.get(txn, prefixWorkload+w.ID.String(), &existing); err != nil {
			return err
		}

		if existing.TenantID != w.TenantID {
			return ctrlplane.ErrNotFound
		}

		if err := s.workloadSlugAvailable(txn, w); err != nil {
			return err
		}

		updated := *w
		updated.UpdatedAt = now()

		return s.set(txn, prefixWorkload+w.ID.String(), &updated)
	})
}

// DeleteWorkload removes a workload scoped to its tenant.
func (s *Store) DeleteWorkload(ctx context.Context, tenant string, target id.ID) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	return s.db.Update(func(txn *badger.Txn) error {
		var existing workload.Workload
		if err := s.get(txn, prefixWorkload+target.String(), &existing); err != nil {
			return err
		}

		if tenant != "" && existing.TenantID != tenant {
			return ctrlplane.ErrNotFound
		}

		return s.delete(txn, prefixWorkload+target.String())
	})
}
