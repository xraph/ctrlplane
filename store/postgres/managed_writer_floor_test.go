package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

func enforcementPGFixture(t *testing.T) (*Store, *WriterFloorAdmin, managed.ActionSubmission) {
	t.Helper()
	legacy, admin, _ := pgFloorFixture(t)

	s, err := NewWithWriterFloorAdmission(legacy.DB(), "floor-test")
	if err != nil {
		t.Fatal(err)
	}

	action := projectionAction(t)
	if _, err = s.SubmitManagedAction(t.Context(), action); err != nil {
		t.Fatal("zero floor action", err)
	}

	projectionLease(t, s, action.Key, action.ID)

	return s, admin, action
}

func TestManagedWriterFloorPostgresExplicitModeAndMissingSchema(t *testing.T) {
	legacy, _ := newProjectionPG(t)

	protected, err := NewWithWriterFloorAdmission(legacy.DB(), "floor-test")
	if err != nil {
		t.Fatal(err)
	}

	action := projectionAction(t)
	if _, err = protected.SubmitManagedAction(t.Context(), action); err == nil {
		t.Fatal("missing protected schema treated as zero")
	}
	// Legacy construction does not silently opt every existing consumer into a new schema.
	if _, err = legacy.SubmitManagedAction(t.Context(), action); err != nil {
		t.Fatal("legacy behavior changed", err)
	}
}

func TestManagedWriterFloorPostgresPublicMutationAndReceiptReplay(t *testing.T) {
	s, admin, action := enforcementPGFixture(t)
	command := projectionCommand(t, s, action.Key)

	accepted, err := s.IssueManagedProvision(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = admin.Activate(t.Context(), pgFloorRequestAboveCeiling()); err != nil {
		t.Fatal(err)
	}

	target, err := s.ReadManagedTarget(t.Context(), action.Key)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = s.SetManagedDesired(t.Context(), managed.DesiredRequest{Command: projectionCommand(t, s, action.Key), Mode: managed.Paused}); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal(err)
	}

	audit := managed.AccessAuditRequest{AttemptID: id.New(managed.PrefixOperation), Installation: action.Installation, Authority: action.Authority, ApplicationID: action.ApplicationID, OrganizationID: action.OrganizationID, Actor: action.Actor, Scope: managed.AuditScope{Kind: "tenant", TenantID: action.TenantID}, Action: "managed.reconcile", ResourceKind: "instance", ResourceID: action.InstanceID.String(), Outcome: "allowed", Reason: "authorized", OccurredAt: time.Now().UTC().Add(-time.Second)}
	if _, err = s.RecordManagedAccessAudit(t.Context(), audit); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal("audit escaped floor", err)
	}

	if replay, replayErr := s.IssueManagedProvision(t.Context(), command); replayErr != nil || replay != accepted {
		t.Fatal(replay, replayErr)
	}

	after, err := s.ReadManagedTarget(t.Context(), action.Key)
	if err != nil || after.Revision != target.Revision || after.Desired != target.Desired {
		t.Fatal("refusal changed target", after, err)
	}

	var count int
	if err = s.pg.NewRaw("SELECT count(*) FROM cp_managed_controller_records WHERE kind='access' AND key=$1", audit.AttemptID.String()).Scan(t.Context(), &count); err != nil || count != 0 {
		t.Fatal("refused audit persisted", count, err)
	}
}

func TestManagedWriterFloorPostgresDirectBatchGuardAndMissingCurrent(t *testing.T) {
	s, admin, action := enforcementPGFixture(t)
	if _, err := admin.Activate(t.Context(), pgFloorRequestAboveCeiling()); err != nil {
		t.Fatal(err)
	}

	err := (managedBackend{store: s}).ControllerAtomic(t.Context(), "floor-batch", []managed.Key{action.Key}, func(tx managedstate.ControllerTransaction) error {
		hook := &quiescenceBatchStager{tx: tx.(managedControllerTransaction), key: action.Key, batch: id.New(managed.PrefixOperation)}

		return hook.StageQuiescenceBatch(t.Context(), nil)
	})
	if !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal(err)
	}

	pgFloorExec(t, s, "DELETE FROM public.cp_writer_floor_current")

	if _, err = s.IssueManagedProvision(t.Context(), projectionCommand(t, s, action.Key)); !errors.Is(err, managed.ErrWriterFloorCorrupt) {
		t.Fatal("history treated as pristine", err)
	}
}

func TestManagedWriterFloorPostgresActivationWaitsForHeldZeroTransaction(t *testing.T) {
	s, admin, _ := enforcementPGFixture(t)

	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- (managedBackend{store: s}).ControllerAtomic(t.Context(), "floor-lock", nil, func(tx managedstate.ControllerTransaction) error {
			close(entered)
			<-release

			return tx.WriteRecord(t.Context(), "floor-test", "lock", "before", []byte("{}"), true)
		})
	}()

	<-entered

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	_, err := admin.Activate(ctx, pgFloorRequestAboveCeiling())

	cancel()
	close(release)

	if mutationErr := <-done; mutationErr != nil {
		t.Fatal(mutationErr)
	}

	if err == nil {
		t.Fatal("activation passed held zero-floor transaction")
	}

	current, history := pgFloorCounts(t, s)
	if current != 0 || history != 0 {
		t.Fatal("failed activation leaked rows", current, history)
	}

	if _, err = admin.Activate(t.Context(), pgFloorRequestAboveCeiling()); err != nil {
		t.Fatal(err)
	}

	err = (managedBackend{store: s}).ControllerAtomic(t.Context(), "floor-lock", nil, func(tx managedstate.ControllerTransaction) error {
		return tx.WriteRecord(t.Context(), "floor-test", "lock", "after", []byte("{}"), true)
	})
	if !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal("later transaction reused zero admission", err)
	}
}

func pgFloorRequestAboveCeiling() managed.WriterFloorActivation {
	request := pgFloorRequest()
	request.MinimumProtocol = "2"

	return request
}
