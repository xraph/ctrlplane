package memory

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/xraph/ctrlplane/id"
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
		writerFloorAuthority:     s.writerFloorAuthority,
		writerFloors:             s.writerFloors, // Immutable under the held parent mutex.
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
	if err := t.checkManagedMutationFloor(ctx); err != nil {
		return err
	}

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

// ReadReceipt requires the caller's validated target under the existing mutex.
// Reads use the staged maps so writes and removals are immediately visible.
func (t managedControllerTransaction) ReadReceipt(ctx context.Context, key managed.Key, operation id.ID, digest string) (managed.Receipt, error) {
	if err := ctx.Err(); err != nil {
		return managed.Receipt{}, err
	}

	parent := t.store.managedTargets[key.InstanceID.String()]
	if parent == nil {
		return managed.Receipt{}, managed.ErrNotFound
	}

	if parent.TenantID != key.TenantID {
		return managed.Receipt{}, managed.ErrAuthority
	}

	receipt, ok := t.store.managedReceipts[key.InstanceID.String()+":"+operation.String()]
	if !ok {
		return managed.Receipt{}, managed.ErrNotFound
	}

	if receipt.ID != operation {
		return managed.Receipt{}, managed.ErrUnavailable
	}

	if receipt.Digest != digest {
		return managed.Receipt{}, managed.ErrConflict
	}

	return receipt, nil
}

// ReadImmutableRecords selects bounded immutable rows from the current staged maps.
func (t managedControllerTransaction) ReadImmutableRecords(ctx context.Context, keys []managedstate.ControllerRecordKey) ([]managedstate.ControllerRecordResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := managedstate.ValidateImmutableRecordKeys(keys); err != nil {
		return nil, err
	}

	return t.readSelectedRecords(ctx, keys, managedstate.ImmutableRecordLimit)
}

// ReadHeldRecords selects provider rows only for the caller's no-write interval.
func (t managedControllerTransaction) ReadHeldRecords(ctx context.Context, keys []managedstate.ControllerRecordKey) ([]managedstate.ControllerRecordResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := managedstate.ValidateHeldRecordKeys(keys); err != nil {
		return nil, err
	}

	return t.readSelectedRecords(ctx, keys, managedstate.HeldRecordLimit)
}

func (t managedControllerTransaction) readSelectedRecords(ctx context.Context, keys []managedstate.ControllerRecordKey, limit func(string) int) ([]managedstate.ControllerRecordResult, error) {
	out := make([]managedstate.ControllerRecordResult, 0, len(keys))
	for _, selected := range keys {
		raw, found := t.store.managedControllerRecords[selected.Kind+":"+selected.Scope][selected.Key]

		result := managedstate.ControllerRecordResult{ControllerRecordKey: selected, Found: found, Size: len(raw)}
		if len(raw) <= limit(selected.Kind) {
			result.Data = slices.Clone(raw)
		}

		out = append(out, result)
	}

	return out, ctx.Err()
}

// ReadImmutableReceipts selects exact unchanged receipts under the held parent lock.
func (t managedControllerTransaction) ReadImmutableReceipts(ctx context.Context, key managed.Key, operations []id.ID) ([]managedstate.ControllerReceiptResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := managedstate.ValidateImmutableReceiptKeys(key, operations); err != nil {
		return nil, err
	}

	parent := t.store.managedTargets[key.InstanceID.String()]
	if parent == nil {
		return nil, managed.ErrNotFound
	}

	if parent.TenantID != key.TenantID {
		return nil, managed.ErrAuthority
	}

	if parent.Key != key {
		return nil, managed.ErrUnavailable
	}

	out := make([]managedstate.ControllerReceiptResult, 0, len(operations))
	for _, operation := range operations {
		receipt, found := t.store.managedReceipts[key.InstanceID.String()+":"+operation.String()]
		if found {
			raw, err := json.Marshal(receipt)
			if err != nil || len(raw) > managed.MaxAttemptReceipt || receipt.ID != operation {
				return nil, managed.ErrUnavailable
			}
		}

		out = append(out, managedstate.ControllerReceiptResult{OperationID: operation, Found: found, Receipt: receipt})
	}

	return out, ctx.Err()
}

// ApplyQuiescenceObservationBatch uses one bound staging adapter inside controller authority.
func (t managedControllerTransaction) ApplyQuiescenceObservationBatch(ctx context.Context, q managed.QuiescenceBatchAcceptanceRequest) (managed.QuiescenceBatchReceipt, error) {
	if !t.held {
		return managed.QuiescenceBatchReceipt{}, managed.ErrUnsupported
	}

	revision, err := q.Command.Revision.Uint64()
	if err != nil {
		return managed.QuiescenceBatchReceipt{}, err
	}

	hook := &quiescenceBatchStager{tx: t, key: q.Command.Key, batch: q.Command.ID, revision: revision}

	return managedstate.RunHeldQuiescenceObservationBatch(ctx, t, q, hook)
}

type quiescenceBatchStager struct {
	tx       managedControllerTransaction
	key      managed.Key
	batch    id.ID
	revision uint64
	used     bool
}

func (h *quiescenceBatchStager) FreshQuiescenceBatchTime(ctx context.Context) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}

	if !h.tx.held || h.used {
		return time.Time{}, managed.ErrBlocked
	}

	return now(), nil
}
func (h *quiescenceBatchStager) StageQuiescenceBatch(ctx context.Context, c *managedstate.QuiescenceBatchCandidate) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if !h.tx.held || h.used {
		return managed.ErrBlocked
	}

	if err := h.tx.checkManagedMutationFloor(ctx); err != nil {
		return err
	}

	old := h.tx.store.managedTargets[h.key.InstanceID.String()]

	target, err := c.StageTarget(old)
	if err != nil {
		return err
	}

	stage, err := c.Take(h.key, h.batch, h.revision)
	if err != nil {
		return err
	}

	h.used = true

	receipts := make([]managed.Receipt, 0, len(stage.Children)+1)
	for _, child := range stage.Children {
		receipts = append(receipts, child.Receipt)
	}

	receipts = append(receipts, stage.Receipt)
	for _, receipt := range receipts {
		key := h.key.InstanceID.String() + ":" + receipt.ID.String()
		if _, exists := h.tx.store.managedReceipts[key]; exists {
			return managed.ErrConflict
		}

		h.tx.store.managedReceipts[key] = receipt
	}

	for _, child := range stage.Children {
		if err = h.tx.WriteRecord(ctx, managed.QuiescenceHistory, child.Scope, child.OperationID.String(), child.History, true); err != nil {
			return err
		}

		if err = h.tx.WriteRecord(ctx, managed.QuiescenceCurrent, child.Scope, "current", child.Current, child.First); err != nil {
			return err
		}
	}

	if err = h.tx.WriteRecord(ctx, managed.QuiescenceBatchHistory, stage.Scope, h.batch.String(), stage.Manifest, true); err != nil {
		return err
	}

	h.tx.store.managedTargets[h.key.InstanceID.String()] = target

	return ctx.Err()
}

var _ managed.QuiescenceBatchStore = (*Store)(nil)
