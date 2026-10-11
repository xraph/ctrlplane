package managedstate

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strconv"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

type quiescenceBatchMember struct {
	ReservationID id.ID  `json:"reservation_id"`
	OperationID   id.ID  `json:"operation_id"`
	RequestDigest string `json:"request_digest"`
	RecordDigest  string `json:"record_digest"`
}
type quiescenceBatchRecord struct {
	Schema  string                         `json:"schema"`
	Command managed.QuiescenceBatchCommand `json:"command"`
	Attempt managed.AttemptRef             `json:"attempt"`
	Entries []quiescenceBatchMember        `json:"entries"`
	Receipt managed.OriginalLocalReceipt   `json:"receipt"`
}

// QuiescenceBatchHooks is implemented only by a private, bound held-backend adapter.
type QuiescenceBatchHooks interface {
	FreshQuiescenceBatchTime(ctx context.Context) (time.Time, error)
	StageQuiescenceBatch(ctx context.Context, candidate *QuiescenceBatchCandidate) error
}

// QuiescenceBatchChildStage carries fixed history/current artifacts, never arbitrary row kinds.
type QuiescenceBatchChildStage struct {
	Scope       string
	OperationID id.ID
	History     []byte
	Current     []byte
	First       bool
	Receipt     managed.Receipt
}

// QuiescenceBatchStage contains only internally derived observation artifacts.
type QuiescenceBatchStage struct {
	Children []QuiescenceBatchChildStage
	Scope    string
	Manifest []byte
	Receipt  managed.Receipt
}

// QuiescenceBatchCandidate has no public constructor or mutable authority fields.
type QuiescenceBatchCandidate struct {
	ready         bool
	used          bool
	key           managed.Key
	batchID       id.ID
	baseRevision  uint64
	finalRevision uint64
	owner         string
	epoch         uint64
	leaseUntil    time.Time
	stage         QuiescenceBatchStage
}

// StageTarget preserves all authoritative fields except the validated revision delta.
func (c *QuiescenceBatchCandidate) StageTarget(t *managed.Target) (*managed.Target, error) {
	if c == nil || !c.ready || c.used || t == nil || t.Key != c.key || t.Revision != c.baseRevision || t.LeaseOwner != c.owner || t.LeaseEpoch != c.epoch || !t.LeaseUntil.Equal(c.leaseUntil) {
		return nil, managed.ErrConflict
	}

	clone, err := Clone(t)
	if err != nil {
		return nil, err
	}

	clone.Revision = c.finalRevision

	return clone, nil
}

// Take validates exact hook binding and returns owned artifacts once.
func (c *QuiescenceBatchCandidate) Take(k managed.Key, batch id.ID, revision uint64) (QuiescenceBatchStage, error) {
	var out QuiescenceBatchStage
	if c == nil || !c.ready || c.used || c.key != k || c.batchID != batch || c.baseRevision != revision {
		return out, managed.ErrAuthority
	}

	n := len(c.stage.Children)
	if n < 1 || n > 64 || uint64(n+1) > math.MaxInt64-revision || c.finalRevision != revision+uint64(n+1) || c.stage.Receipt.ID != batch || c.stage.Receipt.Revision != c.finalRevision {
		return out, managed.ErrUnavailable
	}

	seen := map[id.ID]bool{batch: true}
	for i, child := range c.stage.Children {
		if seen[child.OperationID] || child.Receipt.ID != child.OperationID || child.Receipt.Revision != revision+uint64(i)+1 || child.Receipt.LeaseEpoch != c.epoch || child.Receipt.LeaseUntil != c.leaseUntil || child.Receipt.AcceptedAt != c.stage.Receipt.AcceptedAt {
			return out, managed.ErrUnavailable
		}

		seen[child.OperationID] = true
	}

	out = c.stage
	out.Manifest = slices.Clone(c.stage.Manifest)

	out.Children = slices.Clone(c.stage.Children)
	for i := range out.Children {
		out.Children[i].History = slices.Clone(out.Children[i].History)
		out.Children[i].Current = slices.Clone(out.Children[i].Current)
	}

	c.used = true

	return out, nil
}

func batchFingerprint(q managed.QuiescenceBatchAcceptanceRequest) (string, error) {
	raw, err := json.Marshal(struct {
		Kind  string                                   `json:"kind"`
		Input managed.QuiescenceBatchAcceptanceRequest `json:"input"`
	}{managed.QuiescenceBatchHistory, q})
	if err != nil {
		return "", err
	}

	if len(raw) > managed.MaxQuiescenceBatchInput {
		return "", managed.ErrCapacity
	}

	return managed.Digest(raw), nil
}
func batchChild(q managed.QuiescenceBatchAcceptanceRequest, i int) managed.QuiescenceAcceptanceRequest {
	if i < 0 || i >= len(q.Entries) || i >= managed.MaxQuiescenceBatchEntries {
		return managed.QuiescenceAcceptanceRequest{}
	}

	r, _ := q.Command.Revision.Uint64()
	e := q.Entries[i]

	return managed.QuiescenceAcceptanceRequest{Command: managed.OriginalCommand{Key: q.Command.Key, ID: e.OperationID, Revision: managed.Decimal(strconv.FormatUint(r+uint64(i), 10)), LeaseEpoch: q.Command.LeaseEpoch, Owner: q.Command.Owner, ReservationID: e.Observation.Reference.ReservationID}, Observation: e.Observation}
}
func batchResult(record quiescenceBatchRecord) managed.QuiescenceBatchReceipt {
	return managed.QuiescenceBatchReceipt{Schema: managed.QuiescenceBatchReceiptSchema, Key: record.Command.Key, BatchID: record.Command.ID, Attempt: record.Attempt, Count: managed.Decimal(strconv.Itoa(len(record.Entries))), Receipt: record.Receipt}
}

func batchOrder(q managed.QuiescenceBatchAcceptanceRequest, v attemptView) error {
	if q.Attempt != v.ref {
		return managed.ErrAuthority
	}

	next := 0
	for _, entry := range q.Entries {
		for next < len(v.descriptor.ReservationIDs) && v.descriptor.ReservationIDs[next] != entry.Observation.Reference.ReservationID {
			next++
		}

		if next == len(v.descriptor.ReservationIDs) {
			return managed.ErrAuthority
		}

		next++
	}

	return nil
}

// batchGraph reads immutable history and its current association without fresh-time checks.
func batchGraph(ctx context.Context, tx ControllerTransaction, t *managed.Target, k managed.QuiescenceBatchKey, v attemptView) (managed.QuiescenceBatchAcceptanceRequest, managed.QuiescenceBatchReceipt, error) {
	var (
		empty  managed.QuiescenceBatchAcceptanceRequest
		result managed.QuiescenceBatchReceipt
	)

	scope, err := managed.QuiescenceBatchScope(k.Key)
	if err != nil {
		return empty, result, err
	}

	record, err := attemptRead[quiescenceBatchRecord](ctx, tx, managed.QuiescenceBatchHistory, scope, k.BatchID.String(), managed.MaxQuiescenceBatchRecord)
	if controllerAbsent(err) {
		receipts, probeErr := tx.ReadImmutableReceipts(ctx, k.Key, []id.ID{k.BatchID})
		if probeErr != nil {
			return empty, result, probeErr
		}

		if len(receipts) != 1 || receipts[0].OperationID != k.BatchID || receipts[0].Found {
			return empty, result, managed.ErrUnavailable
		}
	}

	if err != nil {
		return empty, result, err
	}

	if record.Schema != managed.QuiescenceBatchRecordSchema || record.Command.Key != k.Key || record.Command.ID != k.BatchID || len(record.Entries) < 1 || len(record.Entries) > 64 {
		return empty, result, managed.ErrUnavailable
	}

	q := managed.QuiescenceBatchAcceptanceRequest{Schema: managed.QuiescenceBatchRequestSchema, Command: record.Command, Attempt: record.Attempt, Entries: make([]managed.QuiescenceBatchEntry, 0, len(record.Entries))}

	children := make([]managed.QuiescenceRecord, 0, len(record.Entries))
	for _, member := range record.Entries {
		childKey := managed.QuiescenceKey{Key: k.Key, ReservationID: member.ReservationID, OperationID: member.OperationID}

		child, _, e := readQuiescence(ctx, tx, childKey, t)
		if e != nil {
			return empty, result, batchMissing(e)
		}

		if e = quiescenceReplayCurrent(ctx, tx, childKey, t, child); e != nil {
			return empty, result, batchMissing(e)
		}

		fingerprint, e := quiescenceFingerprint(child.Request)
		if e != nil {
			return empty, result, e
		}

		hash, e := managed.QuiescenceRecordDigest(child)
		if e != nil {
			return empty, result, e
		}

		if fingerprint != member.RequestDigest || hash != member.RecordDigest {
			return empty, result, managed.ErrUnavailable
		}

		q.Entries = append(q.Entries, managed.QuiescenceBatchEntry{OperationID: member.OperationID, Observation: child.Request.Observation})
		children = append(children, child)
	}

	q, err = q.Normalize()
	if err != nil {
		return empty, result, managed.ErrUnavailable
	}

	if err = batchOrder(q, v); err != nil {
		return empty, result, err
	}

	fingerprint, err := batchFingerprint(q)
	if err != nil {
		return empty, result, err
	}

	saved, err := tx.ReadReceipt(ctx, k.Key, k.BatchID, fingerprint)
	if err != nil {
		return empty, result, batchMissing(err)
	}

	base, _ := q.Command.Revision.Uint64()
	if managed.OriginalReceipt(saved) != record.Receipt || saved.Revision != base+uint64(len(q.Entries))+1 {
		return empty, result, managed.ErrUnavailable
	}

	epoch, _ := q.Command.LeaseEpoch.Uint64()
	if saved.LeaseEpoch != epoch || !saved.AcceptedAt.Before(saved.LeaseUntil) {
		return empty, result, managed.ErrUnavailable
	}

	for i, child := range children {
		if !reflect.DeepEqual(child.Request, batchChild(q, i)) || child.Receipt.Revision != managed.Decimal(strconv.FormatUint(base+uint64(i)+1, 10)) || child.Receipt.LeaseEpoch != record.Receipt.LeaseEpoch || child.Receipt.LeaseUntil != record.Receipt.LeaseUntil || child.VerifiedAt != record.Receipt.AcceptedAt {
			return empty, result, managed.ErrUnavailable
		}
	}

	result = batchResult(record)
	if _, err = result.Bytes(); err != nil {
		return empty, managed.QuiescenceBatchReceipt{}, managed.ErrUnavailable
	}

	return q, result, nil
}
func batchMissing(err error) error {
	if controllerAbsent(err) {
		return managed.ErrUnavailable
	}

	return err
}

// AcceptManagedQuiescenceBatch commits the complete batch or returns no successful receipt.
func (r *Repository) AcceptManagedQuiescenceBatch(ctx context.Context, q managed.QuiescenceBatchAcceptanceRequest) (managed.QuiescenceBatchReceipt, error) {
	var out managed.QuiescenceBatchReceipt

	normalized, err := q.Normalize()
	if err != nil {
		return out, err
	}

	scope, err := managed.QuiescenceBatchScope(q.Command.Key)
	if err != nil {
		return out, err
	}

	err = r.controller(ctx, "quiescence-batch:"+scope, []managed.Key{q.Command.Key}, func(tx ControllerTransaction) error {
		capability, ok := tx.(QuiescenceBatchTransaction)
		if !ok {
			return managed.ErrUnsupported
		}

		var e error

		out, e = capability.ApplyQuiescenceObservationBatch(ctx, normalized)

		return e
	})
	if err != nil {
		return managed.QuiescenceBatchReceipt{}, err
	}

	return out, nil
}

// ReadManagedQuiescenceBatch resolves the complete retained aggregate and attempt graph.
func (r *Repository) ReadManagedQuiescenceBatch(ctx context.Context, k managed.QuiescenceBatchKey) (managed.QuiescenceBatchReceipt, error) {
	var out managed.QuiescenceBatchReceipt

	scope, err := managed.QuiescenceBatchScope(k.Key)
	if err != nil {
		return out, err
	}

	if k.BatchID.Prefix() != managed.PrefixOperation || len(k.BatchID.String()) > 64 {
		return out, managed.ErrInvalid
	}

	err = r.controller(ctx, "quiescence-batch:"+scope, []managed.Key{k.Key}, func(tx ControllerTransaction) error {
		t, e := tx.Read(ctx, k.Key)
		if e != nil {
			return e
		}

		v, e := selectedAttempt(ctx, tx, t)
		if e != nil {
			return e
		}

		_, out, e = batchGraph(ctx, tx, t, k, v)

		return e
	})
	if err != nil {
		return managed.QuiescenceBatchReceipt{}, err
	}

	return out, nil
}

// RunHeldQuiescenceObservationBatch performs shared validation with purpose-specific persistence.
func RunHeldQuiescenceObservationBatch(ctx context.Context, tx ControllerTransaction, q managed.QuiescenceBatchAcceptanceRequest, hooks QuiescenceBatchHooks) (managed.QuiescenceBatchReceipt, error) {
	var out managed.QuiescenceBatchReceipt
	if err := ctx.Err(); err != nil {
		return out, err
	}

	q, err := q.Normalize()
	if err != nil {
		return out, err
	}

	t, err := tx.Read(ctx, q.Command.Key)
	if err != nil {
		return out, err
	}

	scope, err := managed.QuiescenceBatchScope(q.Command.Key)
	if err != nil {
		return out, err
	}

	fingerprint, err := batchFingerprint(q)
	if err != nil {
		return out, err
	}

	_, rowErr := tx.ReadRecord(ctx, managed.QuiescenceBatchHistory, scope, q.Command.ID.String())

	_, receiptErr := tx.ReadReceipt(ctx, q.Command.Key, q.Command.ID, fingerprint)
	if rowErr == nil {
		if receiptErr != nil {
			return out, batchMissing(receiptErr)
		}

		v, e := selectedAttempt(ctx, tx, t)
		if e != nil {
			return out, e
		}

		saved, result, e := batchGraph(ctx, tx, t, managed.QuiescenceBatchKey{Key: q.Command.Key, BatchID: q.Command.ID}, v)
		if e != nil {
			return out, e
		}

		if !reflect.DeepEqual(q, saved) {
			return out, managed.ErrConflict
		}

		return result, nil
	}

	if !controllerAbsent(rowErr) {
		return out, rowErr
	}

	if !controllerAbsent(receiptErr) {
		if receiptErr == nil {
			return out, managed.ErrUnavailable
		}

		return out, receiptErr
	}

	first := make([]bool, len(q.Entries))
	for i := range q.Entries {
		child := batchChild(q, i)

		childScope, e := managed.QuiescenceScope(child.Command.Key, child.Command.ReservationID)
		if e != nil {
			return out, e
		}

		_, row := tx.ReadRecord(ctx, managed.QuiescenceHistory, childScope, child.Command.ID.String())

		digest, e := quiescenceFingerprint(child)
		if e != nil {
			return out, e
		}

		_, receipt := tx.ReadReceipt(ctx, child.Command.Key, child.Command.ID, digest)

		if row != nil && !controllerAbsent(row) {
			return out, row
		}

		if receipt != nil && !controllerAbsent(receipt) {
			return out, receipt
		}

		if (row == nil) != (receipt == nil) {
			return out, managed.ErrUnavailable
		}

		if row == nil {
			return out, managed.ErrConflict
		}
	}

	if !fixedAttemptProfile(t) {
		return out, managed.ErrUnsupported
	}

	v, err := selectedAttempt(ctx, tx, t)
	if err != nil {
		return out, err
	}

	if err = batchOrder(q, v); err != nil {
		return out, err
	}

	for i := range q.Entries {
		child := batchChild(q, i)
		if err = quiescenceAssociation(ctx, tx, t, child); err != nil {
			return out, err
		}

		k := managed.QuiescenceKey{Key: child.Command.Key, ReservationID: child.Command.ReservationID, OperationID: child.Command.ID}

		current, _, e := readQuiescenceCurrent(ctx, tx, k, t)
		switch {
		case controllerAbsent(e):
			childScope, se := managed.QuiescenceScope(k.Key, k.ReservationID)
			if se != nil {
				return out, se
			}

			rows, se := tx.ListRecords(ctx, managed.QuiescenceHistory, childScope, "", 1)
			if se != nil {
				return out, se
			}

			if len(rows) != 0 {
				return out, managed.ErrUnavailable
			}

			first[i] = true
		case e != nil:
			return out, e
		case !child.Observation.ObservedAt.After(current.ObservedAt):
			return out, managed.ErrConflict
		}
	}

	now, err := hooks.FreshQuiescenceBatchTime(ctx)
	if err != nil {
		return out, err
	}

	now = now.UTC()
	base, _ := q.Command.Revision.Uint64()

	epoch, _ := q.Command.LeaseEpoch.Uint64()
	if t.Revision != base || t.LeaseEpoch != epoch || t.LeaseOwner != q.Command.Owner || !now.Before(t.LeaseUntil) {
		return out, managed.ErrConflict
	}

	if t.Phase == managed.Deleted {
		return out, managed.ErrBlocked
	}

	for _, entry := range q.Entries {
		if e := originalDrainFreshEligibility(t, entry.Observation.Reference.ReservationID); e != nil {
			return out, e
		}

		binding, e := reservation(t, entry.Observation.Reference.ReservationID)
		if e != nil {
			return out, e
		}

		if binding.Phase != managed.Accepted || binding.Abort != nil || entry.Observation.ObservedAt.After(now) || !now.Before(entry.Observation.ValidUntil) {
			return out, managed.ErrBlocked
		}
	}

	if err = attemptReserve(t, v, uint64(len(q.Entries)+1), false); err != nil {
		return out, err
	}

	previousState := v.state
	// No pre-write view is used after this point; post-check constructs its own.
	candidate := &QuiescenceBatchCandidate{ready: true, key: t.Key, batchID: q.Command.ID, baseRevision: base, finalRevision: base + uint64(len(q.Entries)+1), owner: t.LeaseOwner, epoch: t.LeaseEpoch, leaseUntil: t.LeaseUntil}
	aggregate := managed.Receipt{ID: q.Command.ID, Digest: fingerprint, Revision: candidate.finalRevision, LeaseEpoch: t.LeaseEpoch, AcceptedAt: now, LeaseUntil: t.LeaseUntil}
	record := quiescenceBatchRecord{Schema: managed.QuiescenceBatchRecordSchema, Command: q.Command, Attempt: q.Attempt, Entries: make([]quiescenceBatchMember, 0, len(q.Entries)), Receipt: managed.OriginalReceipt(aggregate)}

	candidate.stage = QuiescenceBatchStage{Scope: scope, Receipt: aggregate, Children: make([]QuiescenceBatchChildStage, 0, len(q.Entries))}
	for i := range q.Entries {
		child := batchChild(q, i)

		digest, e := quiescenceFingerprint(child)
		if e != nil {
			return out, e
		}

		receipt := aggregate
		receipt.ID = child.Command.ID
		receipt.Digest = digest
		receipt.Revision = base + uint64(i) + 1
		history := managed.QuiescenceRecord{Request: child, VerifiedAt: now, Receipt: managed.OriginalReceipt(receipt)}

		raw, e := history.Bytes()
		if e != nil {
			return out, e
		}

		hash, e := managed.QuiescenceRecordDigest(history)
		if e != nil {
			return out, e
		}

		index := managed.QuiescenceIndex{Schema: "ctrlplane.quiescence-current.v1", ReservationID: child.Command.ReservationID, OperationID: child.Command.ID, ObservedAt: child.Observation.ObservedAt, RecordDigest: hash}

		current, e := index.Bytes()
		if e != nil {
			return out, e
		}

		childScope, e := managed.QuiescenceScope(t.Key, child.Command.ReservationID)
		if e != nil {
			return out, e
		}

		receiptBytes, e := json.Marshal(receipt)
		if e != nil {
			return out, e
		}

		if len(receiptBytes) > managed.MaxAttemptReceipt {
			return out, managed.ErrCapacity
		}

		candidate.stage.Children = append(candidate.stage.Children, QuiescenceBatchChildStage{Scope: childScope, OperationID: child.Command.ID, History: raw, Current: current, First: first[i], Receipt: receipt})
		record.Entries = append(record.Entries, quiescenceBatchMember{ReservationID: child.Command.ReservationID, OperationID: child.Command.ID, RequestDigest: digest, RecordDigest: hash})
	}

	candidate.stage.Manifest, err = json.Marshal(record)
	if err != nil {
		return out, err
	}

	if len(candidate.stage.Manifest) > managed.MaxQuiescenceBatchRecord {
		return out, managed.ErrCapacity
	}

	aggregateBytes, err := json.Marshal(aggregate)
	if err != nil {
		return out, err
	}

	if len(aggregateBytes) > managed.MaxAttemptReceipt {
		return out, managed.ErrCapacity
	}

	if err = ctx.Err(); err != nil {
		return out, err
	}

	if err = hooks.StageQuiescenceBatch(ctx, candidate); err != nil {
		return out, err
	}

	current, err := tx.Read(ctx, t.Key)
	if err != nil {
		return out, err
	}

	expected, err := Clone(t)
	if err != nil {
		return out, err
	}

	expected.Revision = candidate.finalRevision
	if !reflect.DeepEqual(current, expected) {
		return out, managed.ErrUnavailable
	}

	post, err := selectedAttempt(ctx, tx, current)
	if err != nil {
		return out, err
	}

	if post.ref != q.Attempt || !reflect.DeepEqual(post.state, previousState) {
		return out, managed.ErrUnavailable
	}

	saved, result, err := batchGraph(ctx, tx, current, managed.QuiescenceBatchKey{Key: t.Key, BatchID: q.Command.ID}, post)
	if err != nil {
		return out, err
	}

	if !reflect.DeepEqual(saved, q) {
		return out, managed.ErrUnavailable
	}

	if err = ctx.Err(); err != nil {
		return out, err
	}

	return result, nil
}

var _ managed.QuiescenceBatchStore = (*Repository)(nil)
