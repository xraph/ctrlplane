package badger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/dgraph-io/badger/v4"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/bootstrap"
	"github.com/xraph/ctrlplane/id"
)

const (
	// prefixBootstrap is the primary-key prefix for bootstrap rows
	// (`btsw:<id>` → JSON-encoded BootstrapWorkload).
	prefixBootstrap = "btsw:"

	// prefixBootstrapByDC is the secondary-index prefix used to
	// answer GetBootstrapByName + ListBootstraps without scanning
	// every primary row. Keys are `btsd:<datacenterID>:<name>` and
	// the value is the bootstrap row's ID string.
	prefixBootstrapByDC = "btsd:"
)

// bootstrapDCKey returns the secondary-index key for a (datacenter,
// name) pair. Used both to dedupe inserts and to scan a single
// datacenter's bootstrap rows.
func bootstrapDCKey(datacenterID id.ID, name string) string {
	return prefixBootstrapByDC + idStr(datacenterID) + ":" + name
}

// InsertBootstrap persists a new bootstrap workload row plus the
// (datacenter, name) secondary index. Defends against duplicate
// inserts in two ways:
//
//   - primary-key existence check rejects re-inserting the same row.
//   - secondary-index existence check rejects two rows with the same
//     (DatacenterID, Name) pair, mirroring the unique index from
//     postgres + sqlite.
func (s *Store) InsertBootstrap(_ context.Context, bw *bootstrap.BootstrapWorkload) error {
	if bw == nil {
		return errors.New("badger: insert bootstrap: nil row")
	}

	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixBootstrap + idStr(bw.ID)

		exists, err := s.exists(txn, key)
		if err != nil {
			return err
		}

		if exists {
			return fmt.Errorf("%w: bootstrap %s", ctrlplane.ErrAlreadyExists, bw.ID)
		}

		idxKey := bootstrapDCKey(bw.DatacenterID, bw.Name)

		idxExists, err := s.exists(txn, idxKey)
		if err != nil {
			return err
		}

		if idxExists {
			return fmt.Errorf("%w: bootstrap %s/%s", ctrlplane.ErrAlreadyExists, bw.DatacenterID, bw.Name)
		}

		if err := s.set(txn, key, bw); err != nil {
			return err
		}

		return s.set(txn, idxKey, bw.ID.String())
	})
}

// GetBootstrap retrieves a bootstrap workload by ID.
func (s *Store) GetBootstrap(_ context.Context, bootstrapID id.ID) (*bootstrap.BootstrapWorkload, error) {
	var bw bootstrap.BootstrapWorkload

	err := s.db.View(func(txn *badger.Txn) error {
		return s.get(txn, prefixBootstrap+idStr(bootstrapID), &bw)
	})
	if err != nil {
		return nil, fmt.Errorf("%w: bootstrap %s", ctrlplane.ErrNotFound, bootstrapID)
	}

	return &bw, nil
}

// GetBootstrapByName resolves the row whose (DatacenterID, Name)
// pair matches via the secondary index.
func (s *Store) GetBootstrapByName(_ context.Context, datacenterID id.ID, name string) (*bootstrap.BootstrapWorkload, error) {
	var bootstrapID string

	err := s.db.View(func(txn *badger.Txn) error {
		return s.get(txn, bootstrapDCKey(datacenterID, name), &bootstrapID)
	})
	if err != nil {
		return nil, fmt.Errorf("%w: bootstrap %s/%s", ctrlplane.ErrNotFound, datacenterID, name)
	}

	var bw bootstrap.BootstrapWorkload

	err = s.db.View(func(txn *badger.Txn) error {
		return s.get(txn, prefixBootstrap+bootstrapID, &bw)
	})
	if err != nil {
		return nil, fmt.Errorf("%w: bootstrap %s/%s", ctrlplane.ErrNotFound, datacenterID, name)
	}

	return &bw, nil
}

// ListBootstraps iterates the (datacenter, name) secondary index to
// enumerate every row attached to the datacenter. Returns rows
// sorted by created_at for stable reconciler iteration.
func (s *Store) ListBootstraps(_ context.Context, datacenterID id.ID) ([]*bootstrap.BootstrapWorkload, error) {
	dcPrefix := prefixBootstrapByDC + idStr(datacenterID) + ":"

	var ids []string

	err := s.db.View(func(txn *badger.Txn) error {
		return s.iterate(txn, dcPrefix, func(_ string, val []byte) error {
			var bootstrapID string
			if err := json.Unmarshal(val, &bootstrapID); err != nil {
				return err
			}

			ids = append(ids, bootstrapID)

			return nil
		})
	})
	if err != nil {
		return nil, fmt.Errorf("badger: list bootstraps: %w", err)
	}

	out := make([]*bootstrap.BootstrapWorkload, 0, len(ids))

	err = s.db.View(func(txn *badger.Txn) error {
		for _, bootstrapID := range ids {
			var bw bootstrap.BootstrapWorkload
			if err := s.get(txn, prefixBootstrap+bootstrapID, &bw); err != nil {
				// Index points at a missing primary row — could
				// happen mid-Delete if the index hasn't been
				// pruned yet. Skip rather than fail the whole
				// list call.
				continue
			}

			out = append(out, &bw)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("badger: list bootstraps: hydrate: %w", err)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})

	return out, nil
}

// UpdateBootstrap persists changes to an existing row.
func (s *Store) UpdateBootstrap(_ context.Context, bw *bootstrap.BootstrapWorkload) error {
	if bw == nil {
		return errors.New("badger: update bootstrap: nil row")
	}

	bw.UpdatedAt = now()

	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixBootstrap + idStr(bw.ID)

		exists, err := s.exists(txn, key)
		if err != nil {
			return err
		}

		if !exists {
			return fmt.Errorf("%w: bootstrap %s", ctrlplane.ErrNotFound, bw.ID)
		}

		return s.set(txn, key, bw)
	})
}

// DeleteBootstrap removes a row + its secondary index entry.
// Idempotent: deleting a non-existent ID is a no-op.
func (s *Store) DeleteBootstrap(_ context.Context, bootstrapID id.ID) error {
	return s.db.Update(func(txn *badger.Txn) error {
		key := prefixBootstrap + idStr(bootstrapID)

		var bw bootstrap.BootstrapWorkload
		if err := s.get(txn, key, &bw); err != nil {
			//nolint:nilerr // idempotent delete: already gone is success
			return nil
		}

		if err := txn.Delete([]byte(key)); err != nil {
			return err
		}

		return txn.Delete([]byte(bootstrapDCKey(bw.DatacenterID, bw.Name)))
	})
}
