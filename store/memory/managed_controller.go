package memory

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

type managedControllerTransaction struct{ managedBackend }

func (b managedBackend) ControllerAtomic(ctx context.Context, _ string, _ []managed.Key, change func(managedstate.ControllerTransaction) error) error {
	s := b.store
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}
	// Only the staged maps are used by this transaction-scoped backend. Its held
	// flag prevents nested Repository calls from acquiring or releasing this lock.
	staged := &Store{
		managedTargets:           maps.Clone(s.managedTargets),
		managedPhysical:          maps.Clone(s.managedPhysical),
		managedReceipts:          maps.Clone(s.managedReceipts),
		managedControllerRecords: make(map[string]map[string][]byte, len(s.managedControllerRecords)),
		instances:                maps.Clone(s.instances), workloads: maps.Clone(s.workloads), releases: maps.Clone(s.releases),
	}
	for key, rows := range s.managedControllerRecords {
		staged.managedControllerRecords[key] = maps.Clone(rows)
	}

	tx := managedControllerTransaction{managedBackend{store: staged, held: true, clock: now()}}
	if err := change(tx); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	s.managedTargets = staged.managedTargets
	s.managedPhysical = staged.managedPhysical
	s.managedReceipts = staged.managedReceipts
	s.managedControllerRecords = staged.managedControllerRecords
	s.instances = staged.instances
	s.workloads = staged.workloads
	s.releases = staged.releases

	return nil
}

func (t managedControllerTransaction) Now() time.Time { return t.clock }

func (t managedControllerTransaction) ReadRecord(ctx context.Context, kind, scope, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	raw, ok := t.store.managedControllerRecords[kind+":"+scope][key]
	if !ok {
		return nil, managed.ErrNotFound
	}

	return slices.Clone(raw), nil
}

func (t managedControllerTransaction) WriteRecord(ctx context.Context, kind, scope, key string, raw []byte, insert bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	bucket := kind + ":" + scope

	rows := t.store.managedControllerRecords[bucket]
	if rows == nil {
		rows = map[string][]byte{}
		t.store.managedControllerRecords[bucket] = rows
	}

	_, exists := rows[key]
	if insert && exists {
		return managed.ErrConflict
	}

	if !insert && !exists {
		return managed.ErrNotFound
	}

	rows[key] = slices.Clone(raw)

	return nil
}

func (t managedControllerTransaction) ListRecords(ctx context.Context, kind, scope, after string, limit int) ([]managedstate.ControllerRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	records := []managedstate.ControllerRecord{}

	for key, raw := range t.store.managedControllerRecords[kind+":"+scope] {
		if key > after {
			records = append(records, managedstate.ControllerRecord{Key: key, Data: slices.Clone(raw)})
		}
	}

	slices.SortFunc(records, func(a, b managedstate.ControllerRecord) int { return strings.Compare(a.Key, b.Key) })

	if len(records) > limit {
		records = records[:limit]
	}

	return records, nil
}
