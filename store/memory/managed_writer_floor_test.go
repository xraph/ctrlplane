package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

func enforcementMemoryFixture(t *testing.T) (*Store, managed.ActionSubmission, *WriterFloorAdmin) {
	t.Helper()

	s, err := NewWithWriterFloorAdmission("floor-test")
	if err != nil {
		t.Fatal(err)
	}

	action := projectionAction(t)
	if _, err = s.SubmitManagedAction(t.Context(), action); err != nil {
		t.Fatal("zero floor action", err)
	}

	projectionLease(t, s, action.Key, action.ID)

	admin, err := NewWriterFloorAdmin(s, "floor-test")
	if err != nil {
		t.Fatal(err)
	}

	return s, action, admin
}

func TestManagedWriterFloorMemoryZeroAndPositivePublicMutations(t *testing.T) {
	s, action, admin := enforcementMemoryFixture(t)
	command := projectionCommand(t, s, action.Key)

	accepted, err := s.IssueManagedProvision(t.Context(), command)
	if err != nil {
		t.Fatal("zero floor provision", err)
	}

	if _, err = admin.Activate(t.Context(), memoryFloorRequestAboveCeiling()); err != nil {
		t.Fatal(err)
	}

	before := projectionSnapshot(t, s)
	if _, err = s.SetManagedDesired(t.Context(), managed.DesiredRequest{Command: projectionCommand(t, s, action.Key), Mode: managed.Paused}); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal("positive floor mutation", err)
	}

	if _, err = s.CreateManagedTarget(t.Context(), *projectionAction(t).NewTarget); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal("positive floor create", err)
	}

	audit := managed.AccessAuditRequest{AttemptID: id.New(managed.PrefixOperation), Installation: action.Installation, Authority: action.Authority, ApplicationID: action.ApplicationID, OrganizationID: action.OrganizationID, Actor: action.Actor, Scope: managed.AuditScope{Kind: "tenant", TenantID: action.TenantID}, Action: "managed.reconcile", ResourceKind: "instance", ResourceID: action.InstanceID.String(), Outcome: "allowed", Reason: "authorized", OccurredAt: time.Now().UTC().Add(-time.Second)}
	if _, err = s.RecordManagedAccessAudit(t.Context(), audit); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal("audit bypassed floor", err)
	}
	// Exact historical receipts and authority reads do not regain mutation permission.
	if replay, replayErr := s.IssueManagedProvision(t.Context(), command); replayErr != nil || replay != accepted {
		t.Fatal(replay, replayErr)
	}

	if _, err = s.ReadManagedOperation(t.Context(), managed.OperationKey{Key: action.Key, ActionID: action.ID}); err != nil {
		t.Fatal("read-only inspection", err)
	}

	if before != projectionSnapshot(t, s) {
		t.Fatal("refusal or replay changed authority")
	}
}

func TestManagedWriterFloorMemoryDirectWritesAndBatchAreGuarded(t *testing.T) {
	s, action, admin := enforcementMemoryFixture(t)
	if _, err := admin.Activate(t.Context(), memoryFloorRequestAboveCeiling()); err != nil {
		t.Fatal(err)
	}

	before := projectionSnapshot(t, s)
	for _, kind := range []string{"audit", "record", "projection", "batch"} {
		err := (managedBackend{store: s}).ControllerAtomic(t.Context(), "floor-direct", []managed.Key{action.Key}, func(tx managedstate.ControllerTransaction) error {
			switch kind {
			case "audit", "record":
				return tx.WriteRecord(t.Context(), kind, "test", "entry", []byte("{}"), true)
			case "projection":
				return tx.ProjectRows(t.Context(), *action.NewTarget, managed.ActionReceipt{}, false)
			default:
				hook := &quiescenceBatchStager{tx: tx.(managedControllerTransaction), key: action.Key, batch: id.New(managed.PrefixOperation)}
				// A blocked stage must refuse before consuming even the candidate handle.
				return hook.StageQuiescenceBatch(t.Context(), nil)
			}
		})
		if !errors.Is(err, managed.ErrUnsupported) {
			t.Fatal(kind, err)
		}
	}

	if before != projectionSnapshot(t, s) {
		t.Fatal("direct path escaped floor")
	}
}

func TestManagedWriterFloorMemoryCorruptionAndAuthorityCannotSelectZero(t *testing.T) {
	for _, damage := range []string{"missing_current", "missing_history", "other_authority"} {
		t.Run(damage, func(t *testing.T) {
			s, action, admin := enforcementMemoryFixture(t)

			first, err := admin.Activate(t.Context(), memoryFloorRequest())
			if err != nil {
				t.Fatal(err)
			}

			switch damage {
			case "missing_current":
				s.writerFloors["floor-test"].current = nil
			case "missing_history":
				delete(s.writerFloors["floor-test"].history, first.ActivationID.String())
			case "other_authority":
				s.writerFloors["another-authority"] = s.writerFloors["floor-test"]
				delete(s.writerFloors, "floor-test")
			}

			before := projectionSnapshot(t, s)
			if _, err = s.IssueManagedProvision(t.Context(), projectionCommand(t, s, action.Key)); err == nil {
				t.Fatal("damaged floor allowed mutation")
			}

			if before != projectionSnapshot(t, s) {
				t.Fatal("refusal mutated authority")
			}
		})
	}
}

func TestManagedWriterFloorMemoryHeldZeroCannotOverlapActivation(t *testing.T) {
	s, _, admin := enforcementMemoryFixture(t)

	entered, release, mutationDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		mutationDone <- (managedBackend{store: s}).ControllerAtomic(t.Context(), "floor-lock", nil, func(tx managedstate.ControllerTransaction) error {
			close(entered)
			<-release

			return tx.WriteRecord(t.Context(), "floor-test", "lock", "before", []byte("{}"), true)
		})
	}()

	<-entered

	activationDone := make(chan error, 1)

	go func() { _, err := admin.Activate(t.Context(), memoryFloorRequestAboveCeiling()); activationDone <- err }()

	close(release)

	if err := <-mutationDone; err != nil {
		t.Fatal(err)
	}

	if err := <-activationDone; err != nil {
		t.Fatal(err)
	}

	err := (managedBackend{store: s}).ControllerAtomic(context.Background(), "floor-lock", nil, func(tx managedstate.ControllerTransaction) error {
		return tx.WriteRecord(t.Context(), "floor-test", "lock", "after", []byte("{}"), true)
	})
	if !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal("later transaction reused zero admission", err)
	}
}

func memoryFloorRequestAboveCeiling() managed.WriterFloorActivation {
	request := memoryFloorRequest()
	request.MinimumProtocol = "2"

	return request
}
