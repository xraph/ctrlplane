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

// The existing disposable owner fixture tests transactional correctness. It does
// not certify runtime grants, protected deployment admission or restore custody.
func TestManagedWriterFloorPostgresProtocolOnePublicMutationAndAudit(t *testing.T) {
	s, admin, action := enforcementPGFixture(t)
	if _, err := admin.Activate(t.Context(), pgFloorRequest()); err != nil {
		t.Fatal(err)
	}

	before, err := s.ReadManagedTarget(t.Context(), action.Key)
	if err != nil {
		t.Fatal(err)
	}

	request := managed.DesiredRequest{Command: projectionCommand(t, s, action.Key), Mode: managed.Paused}

	receipt, err := s.SetManagedDesired(t.Context(), request)
	if err != nil {
		t.Fatal("protocol-one mutation", err)
	}

	after, err := s.ReadManagedTarget(t.Context(), action.Key)
	if err != nil || after.Revision != before.Revision+1 || after.Desired != managed.Paused {
		t.Fatal("mutation not retained", err)
	}

	if replay, replayErr := s.SetManagedDesired(t.Context(), request); replayErr != nil || replay != receipt {
		t.Fatal("receipt replay", replayErr)
	}

	audit := managed.AccessAuditRequest{AttemptID: id.New(managed.PrefixOperation), Installation: action.Installation, Authority: action.Authority, ApplicationID: action.ApplicationID, OrganizationID: action.OrganizationID, Actor: action.Actor, Scope: managed.AuditScope{Kind: "tenant", TenantID: action.TenantID}, Action: "managed.reconcile", ResourceKind: "instance", ResourceID: action.InstanceID.String(), Outcome: "allowed", Reason: "authorized", OccurredAt: time.Now().UTC().Add(-time.Second)}
	if _, err = s.RecordManagedAccessAudit(t.Context(), audit); err != nil {
		t.Fatal("protected audit", err)
	}

	var count int
	if err = s.pg.NewRaw("SELECT count(*) FROM cp_managed_controller_records WHERE kind='access' AND key=$1", audit.AttemptID.String()).Scan(t.Context(), &count); err != nil || count != 1 {
		t.Fatal("protected audit was not retained", count, err)
	}

	if _, err = s.SubmitManagedAction(t.Context(), projectionAction(t)); err != nil {
		t.Fatal("protected action", err)
	}
}

func TestManagedWriterFloorPostgresProtocolOneCommitRollbackAndUpgrade(t *testing.T) {
	s, admin, _ := enforcementPGFixture(t)

	first, err := admin.Activate(t.Context(), pgFloorRequest())
	if err != nil {
		t.Fatal(err)
	}

	backend := managedBackend{store: s}
	if err = backend.ControllerAtomic(t.Context(), "floor-one", nil, func(tx managedstate.ControllerTransaction) error {
		if writeErr := tx.WriteRecord(t.Context(), "floor-test", "one", "rolled-back", []byte("{}"), true); writeErr != nil {
			return writeErr
		}

		return managed.ErrUnavailable
	}); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("rollback injection", err)
	}

	if err = backend.ControllerAtomic(t.Context(), "floor-one", nil, func(tx managedstate.ControllerTransaction) error {
		if _, readErr := tx.ReadRecord(t.Context(), "floor-test", "one", "rolled-back"); !errors.Is(readErr, managed.ErrNotFound) {
			return errors.New("rollback row survived")
		}

		return tx.WriteRecord(t.Context(), "floor-test", "one", "committed", []byte("{}"), true)
	}); err != nil {
		t.Fatal("positive transaction", err)
	}

	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- backend.ControllerAtomic(t.Context(), "floor-one-lock", nil, func(tx managedstate.ControllerTransaction) error {
			close(entered)

			if writeErr := tx.WriteRecord(t.Context(), "floor-test", "one", "held", []byte("{}"), true); writeErr != nil {
				return writeErr
			}

			<-release

			return nil
		})
	}()

	<-entered

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	_, err = admin.Activate(ctx, pgFloorNext(first))

	cancel()
	close(release)

	if mutationErr := <-done; mutationErr != nil {
		t.Fatal(mutationErr)
	}

	if err == nil {
		t.Fatal("activation passed held protocol-one transaction")
	}

	reader, err := NewWriterFloorReader(s.DB(), "floor-test")
	if err != nil {
		t.Fatal(err)
	}

	if current, readErr := reader.ReadManagedWriterFloor(t.Context()); readErr != nil || current != first {
		t.Fatal("failed upgrade changed floor", readErr)
	}

	if _, err = admin.Activate(t.Context(), pgFloorNext(first)); err != nil {
		t.Fatal(err)
	}

	if err = backend.ControllerAtomic(t.Context(), "floor-one", nil, func(tx managedstate.ControllerTransaction) error {
		if _, readErr := tx.ReadRecord(t.Context(), "floor-test", "one", "committed"); readErr != nil {
			return readErr
		}

		return tx.WriteRecord(t.Context(), "floor-test", "one", "after-upgrade", []byte("{}"), true)
	}); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal("cached protocol-one allow", err)
	}
}
