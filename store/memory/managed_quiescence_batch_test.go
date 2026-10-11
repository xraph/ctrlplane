package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

func memoryBatchFixture(t *testing.T) (*Store, managed.QuiescenceBatchAcceptanceRequest) {
	t.Helper()
	s, action, original := memoryQuiescenceProvider(t)

	return s, memoryBatchRequest(t, s, action, original)
}
func memoryBatchRequest(t *testing.T, s *Store, action managed.ActionSubmission, original managed.QuiescenceAcceptanceRequest) managed.QuiescenceBatchAcceptanceRequest {
	t.Helper()
	request := memoryAttemptRequest(t, s, action)

	record, err := s.ReadManagedRemovalAttempt(t.Context(), managed.AttemptKey{Key: action.Key, AttemptID: request.AttemptID, Generation: "1"})
	if err != nil {
		t.Fatal(err)
	}

	original.Observation.ObservedAt = time.Now().UTC()
	original.Observation.ValidUntil = original.Observation.ObservedAt.Add(time.Minute)
	signMemoryQuiescence(t, &original)

	return managed.QuiescenceBatchAcceptanceRequest{Schema: managed.QuiescenceBatchRequestSchema, Command: managed.QuiescenceBatchCommand{Key: action.Key, ID: id.New(managed.PrefixOperation), Revision: managed.Decimal(strconv.FormatUint(request.Revision, 10)), LeaseEpoch: managed.Decimal(strconv.FormatUint(request.LeaseEpoch, 10)), Owner: request.Owner}, Attempt: record.State.AttemptRef, Entries: []managed.QuiescenceBatchEntry{{OperationID: id.New(managed.PrefixOperation), Observation: original.Observation}}}
}

type batchFaultBackend struct {
	managedBackend

	mode   string
	cancel context.CancelFunc
}

func (b batchFaultBackend) ControllerAtomic(ctx context.Context, serial string, keys []managed.Key, fn func(managedstate.ControllerTransaction) error) error {
	err := b.managedBackend.ControllerAtomic(ctx, serial, keys, func(tx managedstate.ControllerTransaction) error {
		if err := fn(batchFaultTransaction{tx.(managedControllerTransaction), b.mode, b.cancel}); err != nil {
			return err
		}

		if b.mode == "commit" {
			return errors.New("injected commit failure")
		}

		return nil
	})
	if err == nil && b.mode == "ambiguous" {
		return errors.New("lost commit response")
	}

	return err
}

type batchFaultTransaction struct {
	managedControllerTransaction

	mode   string
	cancel context.CancelFunc
}

func (tx batchFaultTransaction) ApplyQuiescenceObservationBatch(ctx context.Context, q managed.QuiescenceBatchAcceptanceRequest) (managed.QuiescenceBatchReceipt, error) {
	r, _ := q.Command.Revision.Uint64()
	hook := &batchFaultHook{quiescenceBatchStager: &quiescenceBatchStager{tx: tx.managedControllerTransaction, key: q.Command.Key, batch: q.Command.ID, revision: r}, mode: tx.mode, cancel: tx.cancel, q: q}

	return managedstate.RunHeldQuiescenceObservationBatch(ctx, tx, q, hook)
}

type batchFaultHook struct {
	*quiescenceBatchStager

	mode   string
	cancel context.CancelFunc
	q      managed.QuiescenceBatchAcceptanceRequest
}

func (h *batchFaultHook) FreshQuiescenceBatchTime(ctx context.Context) (time.Time, error) {
	if h.mode == "expiry" {
		time.Sleep(time.Until(h.q.Entries[0].Observation.ValidUntil) + time.Millisecond)
	}

	return h.quiescenceBatchStager.FreshQuiescenceBatchTime(ctx)
}
func (h *batchFaultHook) StageQuiescenceBatch(ctx context.Context, c *managedstate.QuiescenceBatchCandidate) error {
	if err := h.quiescenceBatchStager.StageQuiescenceBatch(ctx, c); err != nil {
		return err
	}

	if h.mode == "cancel" {
		h.cancel()

		return nil
	}

	scope, _ := managed.QuiescenceBatchScope(h.q.Command.Key)

	childScope, _ := managed.QuiescenceScope(h.q.Command.Key, h.q.Entries[0].Observation.Reference.ReservationID)
	switch h.mode {
	case "post-aggregate":
		delete(h.tx.store.managedControllerRecords[managed.QuiescenceBatchHistory+":"+scope], h.q.Command.ID.String())
	case "post-child":
		delete(h.tx.store.managedControllerRecords[managed.QuiescenceHistory+":"+childScope], h.q.Entries[0].OperationID.String())
	case "post-receipt":
		delete(h.tx.store.managedReceipts, h.q.Command.Key.InstanceID.String()+":"+h.q.Entries[0].OperationID.String())
	case "post-current":
		delete(h.tx.store.managedControllerRecords[managed.QuiescenceCurrent+":"+childScope], "current")
	case "stage":
		return errors.New("injected stage failure")
	}

	return nil
}
func TestManagedQuiescenceBatchMemoryRollback(t *testing.T) {
	for _, mode := range []string{"stage", "post-aggregate", "post-child", "post-receipt", "post-current", "cancel", "commit", "expiry", "ambiguous"} {
		t.Run(mode, func(t *testing.T) {
			s, q := memoryBatchFixture(t)
			if mode == "expiry" {
				q.Entries[0].Observation.ValidUntil = q.Entries[0].Observation.ObservedAt.Add(30 * time.Millisecond)
				q.Entries[0].Observation.ChallengeDigest, _ = managed.QuiescenceChallengeDigest(q.Entries[0].Observation.QuiescenceLeaf)
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			before := projectionSnapshot(t, s)
			repo := managedstate.New(batchFaultBackend{managedBackend{store: s}, mode, cancel})

			result, err := repo.AcceptManagedQuiescenceBatch(ctx, q)
			if err == nil || result != (managed.QuiescenceBatchReceipt{}) {
				t.Fatal("failure leaked success", err)
			}

			if mode == "ambiguous" {
				replay, e := s.AcceptManagedQuiescenceBatch(t.Context(), q)
				if e != nil || replay.BatchID != q.Command.ID {
					t.Fatal("ambiguous exact recovery", e)
				}
			} else if after := projectionSnapshot(t, s); after != before {
				t.Fatal("failure leaked staged state")
			}
		})
	}
}

func TestManagedQuiescenceBatchMemoryCorruption(t *testing.T) {
	for _, kind := range []string{"aggregate-row", "aggregate-receipt", "child-history", "child-receipt", "current"} {
		t.Run(kind, func(t *testing.T) {
			s, q := memoryBatchFixture(t)
			if _, err := s.AcceptManagedQuiescenceBatch(t.Context(), q); err != nil {
				t.Fatal(err)
			}

			scope, _ := managed.QuiescenceBatchScope(q.Command.Key)
			childScope, _ := managed.QuiescenceScope(q.Command.Key, q.Entries[0].Observation.Reference.ReservationID)

			s.mu.Lock()
			switch kind {
			case "aggregate-row":
				delete(s.managedControllerRecords[managed.QuiescenceBatchHistory+":"+scope], q.Command.ID.String())
			case "aggregate-receipt":
				delete(s.managedReceipts, q.Command.Key.InstanceID.String()+":"+q.Command.ID.String())
			case "child-history":
				delete(s.managedControllerRecords[managed.QuiescenceHistory+":"+childScope], q.Entries[0].OperationID.String())
			case "child-receipt":
				delete(s.managedReceipts, q.Command.Key.InstanceID.String()+":"+q.Entries[0].OperationID.String())
			case "current":
				delete(s.managedControllerRecords[managed.QuiescenceCurrent+":"+childScope], "current")
			}
			s.mu.Unlock()

			before := projectionSnapshot(t, s)
			if _, err := s.AcceptManagedQuiescenceBatch(t.Context(), q); err == nil {
				t.Fatal("broken replay")
			}

			if _, err := s.ReadManagedQuiescenceBatch(t.Context(), managed.QuiescenceBatchKey{Key: q.Command.Key, BatchID: q.Command.ID}); err == nil {
				t.Fatal("broken read")
			}

			if after := projectionSnapshot(t, s); after != before {
				t.Fatal("corruption repaired")
			}
		})
	}
}

func TestManagedQuiescenceBatchMemoryLockExpiry(t *testing.T) {
	s, q := memoryBatchFixture(t)
	q.Entries[0].Observation.ValidUntil = q.Entries[0].Observation.ObservedAt.Add(100 * time.Millisecond)
	q.Entries[0].Observation.ChallengeDigest, _ = managed.QuiescenceChallengeDigest(q.Entries[0].Observation.QuiescenceLeaf)
	before := projectionSnapshot(t, s)
	s.mu.Lock()
	done := make(chan error, 1)

	go func() { _, err := s.AcceptManagedQuiescenceBatch(t.Context(), q); done <- err }()

	time.Sleep(time.Until(q.Entries[0].Observation.ValidUntil) + time.Millisecond)
	s.mu.Unlock()

	if err := <-done; !errors.Is(err, managed.ErrBlocked) {
		t.Fatal("actual held time", err)
	}

	if after := projectionSnapshot(t, s); after != before {
		t.Fatal("expired locked batch wrote")
	}
}

func memoryRefreshBatch(t *testing.T, s *Store, action managed.ActionSubmission, key managed.AttemptKey, observations []managed.QuiescenceAcceptanceRequest, history *[]managed.QuiescenceBatchAcceptanceRequest) []managed.QuiescenceAcceptanceRequest {
	t.Helper()

	before, err := s.ReadManagedTarget(t.Context(), key.Key)
	if err != nil {
		t.Fatal(err)
	}

	record, err := s.ReadManagedRemovalAttempt(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}

	journalKey := managed.OperationKey{Key: key.Key, ActionID: action.ID}

	journal, err := s.ReadManagedOperation(t.Context(), journalKey)
	if err != nil {
		t.Fatal(err)
	}

	q := managed.QuiescenceBatchAcceptanceRequest{Schema: managed.QuiescenceBatchRequestSchema, Command: managed.QuiescenceBatchCommand{Key: key.Key, ID: id.New(managed.PrefixOperation), Revision: managed.Decimal(strconv.FormatUint(before.Revision, 10)), LeaseEpoch: managed.Decimal(strconv.FormatUint(before.LeaseEpoch, 10)), Owner: before.LeaseOwner}, Attempt: record.State.AttemptRef}
	for _, rid := range record.Descriptor.ReservationIDs {
		for _, old := range observations {
			if old.Command.ReservationID != rid {
				continue
			}

			o := old.Observation
			o.ObservedAt = time.Now().UTC()
			o.ValidUntil = o.ObservedAt.Add(time.Minute)
			o.Attestation = bytes.Repeat([]byte{255}, managed.MaxQuiescenceAttestation)

			o.ChallengeDigest, err = managed.QuiescenceChallengeDigest(o.QuiescenceLeaf)
			if err != nil {
				t.Fatal(err)
			}

			q.Entries = append(q.Entries, managed.QuiescenceBatchEntry{OperationID: id.New(managed.PrefixOperation), Observation: o})
		}
	}

	if len(q.Entries) != 64 {
		t.Fatal("incomplete maximum batch")
	}

	base := before.Revision
	started := time.Now()

	receipt, err := s.AcceptManagedQuiescenceBatch(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}

	after, err := s.ReadManagedTarget(t.Context(), key.Key)
	if err != nil {
		t.Fatal(err)
	}

	before.Revision += 65
	if !reflect.DeepEqual(before, after) {
		t.Fatal("batch parent delta")
	}

	later, err := s.ReadManagedRemovalAttempt(t.Context(), key)
	if err != nil || !reflect.DeepEqual(record, later) {
		t.Fatal("attempt delta", err)
	}

	laterJournal, err := s.ReadManagedOperation(t.Context(), journalKey)
	if err != nil || !reflect.DeepEqual(journal, laterJournal) {
		t.Fatal("journal delta", err)
	}

	*history = append(*history, q)
	out := make([]managed.QuiescenceAcceptanceRequest, 0, 64)
	margin := time.Minute

	for i, entry := range q.Entries {
		out = append(out, managed.QuiescenceAcceptanceRequest{Command: managed.OriginalCommand{Key: key.Key, ID: entry.OperationID, Revision: managed.Decimal(strconv.FormatUint(base+uint64(i), 10)), LeaseEpoch: q.Command.LeaseEpoch, Owner: q.Command.Owner, ReservationID: entry.Observation.Reference.ReservationID}, Observation: entry.Observation})
		if left := entry.Observation.ValidUntil.Sub(receipt.Receipt.AcceptedAt); left < margin {
			margin = left
		}
	}

	t.Logf("memory batch64 actual callback margin=%s duration=%s parent_delta=65", margin, time.Since(started))

	if margin <= 0 {
		t.Fatal("expired actual batch authority")
	}

	return out
}
func TestManagedQuiescenceBatchMemoryFullFiniteSetNearLimit(t *testing.T) {
	requireMemoryMaximum(t, "batch")
	testMemoryFiniteSetNearLimit(t, true)
}

func TestManagedQuiescenceBatchMemoryPostAbortCorruption(t *testing.T) {
	for _, kind := range []string{"registration-missing", "registration-malformed", "registration-receipt", "drain-missing", "drain-malformed", "drain-receipt", "drain-projection"} {
		t.Run(kind, func(t *testing.T) {
			s, action, original := memoryQuiescenceProvider(t)

			q := memoryBatchRequest(t, s, action, original)
			if _, err := s.AcceptManagedQuiescenceBatch(t.Context(), q); err != nil {
				t.Fatal(err)
			}

			rid := q.Entries[0].Observation.Reference.ReservationID

			if _, err := s.RevokeManagedRemovalAttempt(t.Context(), memoryAttemptRequest(t, s, action)); err != nil {
				t.Fatal(err)
			}

			key := managed.AttemptKey{Key: action.Key, AttemptID: q.Attempt.AttemptID, Generation: "1"}

			binding, err := s.ReadManagedAttemptBinding(t.Context(), managed.AttemptBindingKey{AttemptKey: key, ReservationID: rid})
			if err != nil {
				t.Fatal(err)
			}

			raw, err := json.Marshal(struct {
				Kind  string `json:"kind"`
				Value any    `json:"value"`
			}{"removal_fence", *binding.Fence})
			if err != nil {
				t.Fatal(err)
			}

			digest := managed.Digest(append([]byte("ctrlplane.managed.attempt-fact.v1\n"), raw...))
			if _, err = s.RecordManagedAttemptBindingSettlement(t.Context(), managed.AttemptBindingSettlementRequest{AttemptRequest: memoryAttemptRequest(t, s, action), ReservationID: rid, FenceFactDigest: digest}); err != nil {
				t.Fatal(err)
			}

			grant := memoryAttemptCommand(t, s, action, rid, managed.AttemptAbort)
			if _, err = s.RecordManagedAttemptAbort(t.Context(), managed.AttemptAbortRequest{AttemptRequest: memoryAttemptRequest(t, s, action), ReservationID: rid, Acceptance: memoryAttemptAcceptance(grant.Command), ProofVerifiedAt: time.Now().UTC(), ProofValidUntil: time.Now().UTC().Add(time.Minute)}); err != nil {
				t.Fatal(err)
			}

			ref := q.Entries[0].Observation.Reference

			selected := managed.OriginalLifecycleKey{Key: action.Key, Kind: managed.OriginalDrain, OperationID: ref.DrainOperationID}
			if kind[:5] == "regis" {
				selected.Kind = managed.OriginalRegistration
				selected.OperationID = ref.RegistrationOperationID
			}

			raw, err = json.Marshal(action.Key)
			if err != nil {
				t.Fatal(err)
			}

			scope := managed.Digest(raw)

			s.mu.Lock()
			switch kind {
			case "registration-missing", "drain-missing":
				delete(s.managedControllerRecords[selected.Kind+":"+scope], selected.OperationID.String())
			case "registration-malformed", "drain-malformed":
				s.managedControllerRecords[selected.Kind+":"+scope][selected.OperationID.String()] = []byte("{}")
			case "registration-receipt", "drain-receipt":
				delete(s.managedReceipts, action.InstanceID.String()+":"+selected.OperationID.String())
			case "drain-projection":
				for i := range s.managedTargets[action.InstanceID.String()].Reservations {
					r := &s.managedTargets[action.InstanceID.String()].Reservations[i]
					if r.ID == rid {
						r.Drain = nil
					}
				}
			}
			s.mu.Unlock()

			before := projectionSnapshot(t, s)
			if _, err = s.ReadManagedOriginalLifecycle(t.Context(), selected); err == nil {
				t.Fatal("corrupt original accepted")
			}

			if _, err = s.ReadManagedQuiescence(t.Context(), managed.QuiescenceKey{Key: action.Key, ReservationID: rid, OperationID: q.Entries[0].OperationID}); err == nil {
				t.Fatal("corrupt child accepted")
			}

			if _, err = s.AcceptManagedQuiescenceBatch(t.Context(), q); err == nil {
				t.Fatal("corrupt replay accepted")
			}

			if _, err = s.ReadManagedQuiescenceBatch(t.Context(), managed.QuiescenceBatchKey{Key: action.Key, BatchID: q.Command.ID}); err == nil {
				t.Fatal("corrupt aggregate accepted")
			}

			if after := projectionSnapshot(t, s); before != after {
				t.Fatal("corrupt history repaired")
			}
		})
	}
}

func TestManagedQuiescenceBatchMemoryReserve(t *testing.T) {
	s, q := memoryBatchFixture(t)

	const (
		reserve = 16
		cost    = 2
	)

	target := s.managedTargets[q.Command.Key.InstanceID.String()]
	target.Revision = math.MaxInt64 - reserve - cost + 1
	q.Command.Revision = managed.Decimal(strconv.FormatUint(target.Revision, 10))

	before := projectionSnapshot(t, s)
	if _, err := s.AcceptManagedQuiescenceBatch(t.Context(), q); !errors.Is(err, managed.ErrCapacity) {
		t.Fatal("batch consumed remaining terminal reserve", err)
	}

	if after := projectionSnapshot(t, s); before != after {
		t.Fatal("capacity refusal wrote")
	}

	target = s.managedTargets[q.Command.Key.InstanceID.String()]
	target.Revision--
	q.Command.Revision = managed.Decimal(strconv.FormatUint(target.Revision, 10))

	receipt, err := s.AcceptManagedQuiescenceBatch(t.Context(), q)
	if err != nil {
		t.Fatal("exact batch allowance", err)
	}

	revision, _ := receipt.Receipt.Revision.Uint64()
	if revision != math.MaxInt64-reserve {
		t.Fatal("batch N+1 counter mismatch")
	}
}
