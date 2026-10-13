package memory

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

func discoveryResultRequest(t *testing.T, s *Store, phase managed.PhaseCommandGrant, original managed.OriginalLifecycleKey) managed.PhaseOriginalResultRequest {
	t.Helper()
	c := projectionCommand(t, s, phase.Record.Work.Key)
	audit := phase.Record.Request.Audit
	audit.AttemptID = id.New(managed.PrefixOperation)
	audit.OccurredAt = time.Now().UTC().Add(-time.Second)

	return managed.PhaseOriginalResultRequest{Schema: managed.DiscoverySchema, Parent: managed.PhaseParent{Key: c.Key, ID: c.ID, Revision: managed.Decimal(strconv.FormatUint(c.Revision, 10)), LeaseEpoch: managed.Decimal(strconv.FormatUint(c.LeaseEpoch, 10)), Owner: c.Owner}, Work: phase.Record.Work, Original: original, Audit: audit}
}

func discoveryRegistrationResult(t *testing.T, wrongNative bool) (*Store, managed.PhaseOriginalResultRequest, managed.PhaseCommandRequest) {
	t.Helper()
	s, action, original, phase := discoveryRegistration(t)

	grant, err := s.RetainManagedPhaseCommand(t.Context(), phase)
	if err != nil {
		t.Fatal(err)
	}

	original.Command = memoryOriginalCommand(t, s, action.Key, original.Command.ReservationID)
	original.Facts.Native.RequestID = phase.Process.Native.RequestID
	original.Facts.Native.RequestDigest = phase.Process.Native.RequestDigest

	original.Facts.Native.CommandDigest = phase.Process.Native.CommandDigest
	if wrongNative {
		original.Facts.Native.RequestDigest = managed.Digest([]byte("different-native-request"))
	}

	if _, err = s.AcceptManagedOriginalRegistration(t.Context(), original); err != nil {
		t.Fatal(err)
	}

	key := managed.OriginalLifecycleKey{Key: action.Key, Kind: managed.OriginalRegistration, OperationID: original.Command.ID}

	return s, discoveryResultRequest(t, s, grant, key), phase
}

func TestManagedPhaseOriginalResultMemoryExactReceiptAndReplay(t *testing.T) {
	s, q, phase := discoveryRegistrationResult(t, false)
	if phase.Process.Native.RequestDigest == phase.Process.Request.Command.NativeDigest || phase.Process.Native.CommandDigest == phase.Process.Request.Command.OriginalDigest {
		t.Fatal("fixture collapsed digest domains")
	}

	before, err := s.ReadManagedWorkHead(t.Context(), q.Parent.Key)
	if err != nil {
		t.Fatal(err)
	}

	r := managedstate.New(discoveryEarlierClockBackend{managedBackend{store: s}})

	linked, err := r.AttachManagedPhaseOriginalResult(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}

	if linked.AuditReceipt.AcceptedAt != linked.Acceptance.AcceptedAt {
		t.Fatal("result audit used a different acceptance clock")
	}

	read, err := s.ReadManagedPhaseOriginalResult(t.Context(), q.Work)
	if err != nil || !reflect.DeepEqual(linked, read) {
		t.Fatal(read, err)
	}

	replay, err := s.AttachManagedPhaseOriginalResult(t.Context(), q)
	if err != nil || !reflect.DeepEqual(linked, replay) {
		t.Fatal(replay, err)
	}

	after, err := s.ReadManagedWorkHead(t.Context(), q.Parent.Key)
	if err != nil || before != after {
		t.Fatal("accepted result moved discovery", after, err)
	}

	page, err := s.ListManagedUnresolvedWork(t.Context(), managed.WorkPageRequest{Key: q.Parent.Key, Limit: 32})
	if err != nil || len(page.Items) != 2 {
		t.Fatal("native acceptance closed membership", page, err)
	}

	q.Audit.AttemptID = id.New(managed.PrefixOperation)
	if _, err = s.AttachManagedPhaseOriginalResult(t.Context(), q); !errors.Is(err, managed.ErrConflict) {
		t.Fatal("result replacement accepted", err)
	}
}

func TestManagedPhaseOriginalResultMemoryWrongNativeDigestRefuses(t *testing.T) {
	s, q, _ := discoveryRegistrationResult(t, true)

	before := projectionSnapshot(t, s)
	if _, err := s.AttachManagedPhaseOriginalResult(t.Context(), q); !errors.Is(err, managed.ErrAuthority) {
		t.Fatal("different request result attached", err)
	}

	if before != projectionSnapshot(t, s) {
		t.Fatal("refused result mutated authority")
	}
}

func TestManagedPhaseOriginalResultMemoryAtomicAuditAndResultRollback(t *testing.T) {
	for _, kind := range []string{"audit", "phase_original_result"} {
		t.Run(kind, func(t *testing.T) {
			s, q, _ := discoveryRegistrationResult(t, false)
			before := projectionSnapshot(t, s)

			r := managedstate.New(discoveryFailureBackend{managedBackend: managedBackend{store: s}, fail: kind})
			if _, err := r.AttachManagedPhaseOriginalResult(t.Context(), q); err == nil {
				t.Fatal("injected failure ignored")
			}

			if before != projectionSnapshot(t, s) {
				t.Fatal("partial result/audit/local receipt persisted")
			}
		})
	}
}

func TestManagedPhaseOriginalResultMemoryLostOriginalRefusesWithoutRepair(t *testing.T) {
	s, q, _ := discoveryRegistrationResult(t, false)
	if _, err := s.AttachManagedPhaseOriginalResult(t.Context(), q); err != nil {
		t.Fatal(err)
	}

	changed := false

	for bucket, rows := range s.managedControllerRecords {
		if strings.HasPrefix(bucket, managed.OriginalRegistration+":") {
			delete(rows, q.Original.OperationID.String())

			changed = true
		}
	}

	if !changed {
		t.Fatal("fixture original missing")
	}

	before := projectionSnapshot(t, s)
	if _, err := s.ReadManagedPhaseOriginalResult(t.Context(), q.Work); err == nil {
		t.Fatal("lost original accepted")
	}

	if _, err := s.AttachManagedPhaseOriginalResult(t.Context(), q); err == nil {
		t.Fatal("lost original backfilled")
	}

	if before != projectionSnapshot(t, s) {
		t.Fatal("historical read repaired source")
	}
}

func TestManagedPhaseOriginalResultMemoryCanceledAttachRefuses(t *testing.T) {
	s, q, _ := discoveryRegistrationResult(t, false)
	before := projectionSnapshot(t, s)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := s.AttachManagedPhaseOriginalResult(ctx, q); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	if before != projectionSnapshot(t, s) {
		t.Fatal("canceled result changed state")
	}
}

func TestManagedPhaseOriginalResultMemoryDrainAcceptanceRemainsUnresolved(t *testing.T) {
	s, registrationResult, registration := discoveryRegistrationResult(t, false)
	if _, err := s.AttachManagedPhaseOriginalResult(t.Context(), registrationResult); err != nil {
		t.Fatal(err)
	}

	operation, err := s.ReadManagedOperation(t.Context(), registrationResult.Work.Operation)
	if err != nil {
		t.Fatal(err)
	}

	pause := discoveryPause(t, s, operation.Submission)
	if _, err = s.SubmitManagedAction(t.Context(), pause); err != nil {
		t.Fatal(err)
	}

	if _, err = s.SealManagedTarget(t.Context(), projectionCommand(t, s, pause.Key)); err != nil {
		t.Fatal(err)
	}

	phase := discoveryNextPhase(t, s, registration, "drain")
	phase.ActionID = pause.ID

	grant, err := s.RetainManagedPhaseCommand(t.Context(), phase)
	if err != nil {
		t.Fatal(err)
	}

	c := memoryOriginalCommand(t, s, pause.Key, phase.Process.Request.Binding.ReservationID)
	stamp := time.Now().UTC().Add(-time.Second)
	native := managed.OriginalNativeAcceptance{Operation: "worker.drain.request", RequestID: phase.Process.Native.RequestID, RequestDigest: phase.Process.Native.RequestDigest, CommandDigest: phase.Process.Native.CommandDigest, ResponseVersion: "1", DeliveryID: "drain-delivery", AcceptedAt: stamp}
	raw := []byte("synthetic-native-drain-export")

	original := managed.OriginalDrainAcceptanceRequest{Command: c, Facts: managed.OriginalDrainFacts{Process: phase.Process.Process, Native: native, OperationID: phase.Process.DrainOperation, Deadline: phase.Process.Deadline, ProcessAccepted: true, AdmissionClosed: true, ObservedAt: stamp}, NativeReceipt: raw, NativeReceiptDigest: managed.Digest(raw)}
	if _, err = s.RecordManagedOriginalDrain(t.Context(), original); err != nil {
		t.Fatal(err)
	}

	q := discoveryResultRequest(t, s, grant, managed.OriginalLifecycleKey{Key: pause.Key, Kind: managed.OriginalDrain, OperationID: c.ID})
	if _, err = s.AttachManagedPhaseOriginalResult(t.Context(), q); err != nil {
		t.Fatal(err)
	}

	if _, err = s.ReadManagedPhaseOriginalResult(t.Context(), q.Work); err != nil {
		t.Fatal(err)
	}

	page, err := s.ListManagedUnresolvedWork(t.Context(), managed.WorkPageRequest{Key: pause.Key, Limit: 32})
	if err != nil || len(page.Items) != 4 {
		t.Fatal("drain acceptance closed physical work", page, err)
	}
}

func TestManagedPhaseOriginalResultMemoryIndependentReceiptRejectsRewrittenLink(t *testing.T) {
	s, q, _ := discoveryRegistrationResult(t, false)
	if _, err := s.AttachManagedPhaseOriginalResult(t.Context(), q); err != nil {
		t.Fatal(err)
	}

	changed := false

	for bucket, rows := range s.managedControllerRecords {
		if !strings.HasPrefix(bucket, "phase_original_result:") {
			continue
		}

		raw, exists := rows[q.Work.ID.String()]
		if !exists {
			continue
		}

		record, err := managed.DecodePhaseOriginalResult(raw)
		if err != nil {
			t.Fatal(err)
		}

		record.Request.Parent.Owner = "rewritten-owner"

		encoded, err := managed.EncodePhaseOriginalResult(record)
		if err != nil {
			t.Fatal(err)
		}

		rows[q.Work.ID.String()] = encoded
		changed = true
	}

	if !changed {
		t.Fatal("fixture link missing")
	}

	if _, err := s.ReadManagedPhaseOriginalResult(t.Context(), q.Work); !errors.Is(err, managed.ErrConflict) {
		t.Fatal("rewritten link escaped original parent receipt", err)
	}
}
