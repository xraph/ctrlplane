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

func TestManagedWriterFloorMemoryProtocolOnePublicMutationAndAudit(t *testing.T) {
	s, action, admin := enforcementMemoryFixture(t)
	if _, err := admin.Activate(t.Context(), memoryFloorRequest()); err != nil {
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

	s.mu.RLock()

	retained := false

	for bucket, rows := range s.managedControllerRecords {
		if len(bucket) >= 7 && bucket[:7] == "access:" {
			if _, ok := rows[audit.AttemptID.String()]; ok {
				retained = true
			}
		}
	}

	s.mu.RUnlock()

	if !retained {
		t.Fatal("protected audit was not retained")
	}
	// The constructor choice is fixed. A newly submitted target uses the same floor.
	if _, err = s.SubmitManagedAction(t.Context(), projectionAction(t)); err != nil {
		t.Fatal("protected action", err)
	}
}

func TestManagedWriterFloorMemoryProtocolOneCommitRollbackAndUpgrade(t *testing.T) {
	s, _, admin := enforcementMemoryFixture(t)

	first, err := admin.Activate(t.Context(), memoryFloorRequest())
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
	activated := make(chan error, 1)

	go func() { _, activateErr := admin.Activate(ctx, memoryFloorNext(first)); activated <- activateErr }()

	<-ctx.Done()
	close(release)

	if err = <-done; err != nil {
		t.Fatal(err)
	}

	if err = <-activated; err == nil {
		t.Fatal("activation ignored expired context behind held mutation")
	}

	cancel()

	if _, err = admin.Activate(t.Context(), memoryFloorNext(first)); err != nil {
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
