package memory

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

func memoryFloorRequest() managed.WriterFloorActivation {
	return managed.WriterFloorActivation{Schema: managed.WriterFloorSchema, AuthorityID: "floor-test", MinimumProtocol: "1", Epoch: "1", ActivationID: id.New(managed.PrefixOperation)}
}

func memoryFloorFixture(t *testing.T) (*Store, *WriterFloorAdmin, *WriterFloorReader, managed.WriterFloor) {
	t.Helper()

	s := New()

	a, err := NewWriterFloorAdmin(s, "floor-test")
	if err != nil {
		t.Fatal(err)
	}

	r, err := NewWriterFloorReader(s, "floor-test")
	if err != nil {
		t.Fatal(err)
	}

	first, err := a.Activate(t.Context(), memoryFloorRequest())
	if err != nil {
		t.Fatal(err)
	}

	return s, a, r, first
}

func memoryFloorNext(first managed.WriterFloor) managed.WriterFloorActivation {
	q := first.Activation()
	q.ActivationID = id.New(managed.PrefixOperation)
	q.Epoch = "2"
	q.MinimumProtocol = "2"
	q.ExpectedDigest = first.Digest

	return q
}

func TestWriterFloorMemoryConcurrentCASAndHistoricalReplay(t *testing.T) {
	_, a, r, first := memoryFloorFixture(t)
	start := make(chan struct{})
	errorsOut := make(chan error, 2)

	var group sync.WaitGroup

	for range 2 {
		q := memoryFloorNext(first)

		group.Go(func() { <-start; _, err := a.Activate(t.Context(), q); errorsOut <- err })
	}

	close(start)
	group.Wait()
	close(errorsOut)

	success, conflict := 0, 0

	for err := range errorsOut {
		switch {
		case err == nil:
			success++
		case errors.Is(err, managed.ErrConflict):
			conflict++
		default:
			t.Fatal(err)
		}
	}

	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}

	current, err := r.ReadManagedWriterFloor(t.Context())
	if err != nil || current.Epoch != "2" {
		t.Fatal(current, err)
	}

	replayed, err := a.Activate(t.Context(), first.Activation())
	if err != nil || replayed != first {
		t.Fatal(replayed, err)
	}

	after, err := r.ReadManagedWriterFloor(t.Context())
	if err != nil || after != current {
		t.Fatal("historical replay moved current", err)
	}
}

func TestWriterFloorMemoryAtomicRollbackAndCancellation(t *testing.T) {
	for _, installed := range []bool{false, true} {
		for _, cancelAfterWrite := range []bool{false, true} {
			s := New()

			a, err := NewWriterFloorAdmin(s, "floor-test")
			if err != nil {
				t.Fatal(err)
			}

			q := memoryFloorRequest()

			var before []byte

			if installed {
				first, activateErr := a.Activate(t.Context(), q)
				if activateErr != nil {
					t.Fatal(activateErr)
				}

				before = bytes.Clone(s.writerFloors["floor-test"].current)
				q = memoryFloorNext(first)
			}

			floor, err := managed.NewWriterFloor(q, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}

			raw, err := managed.EncodeWriterFloor(floor)
			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithCancel(t.Context())
			err = a.backend.WriterFloorAdminAtomic(ctx, func(tx managedstate.WriterFloorAdminTransaction) error {
				var writeErr error
				if installed {
					writeErr = tx.AdvanceWriterFloor(ctx, q.ExpectedDigest, raw)
				} else {
					writeErr = tx.InstallInitialWriterFloor(ctx, raw)
				}

				if writeErr != nil {
					return writeErr
				}

				if cancelAfterWrite {
					cancel()

					return nil
				}

				return managed.ErrUnavailable
			})

			cancel()

			want := managed.ErrUnavailable
			if cancelAfterWrite {
				want = context.Canceled
			}

			if !errors.Is(err, want) {
				t.Fatal(err)
			}

			state := s.writerFloors["floor-test"]
			if !installed && state != nil {
				t.Fatal("failed initial install published state")
			}

			if installed && (!bytes.Equal(state.current, before) || len(state.history) != 1 || len(state.epochs) != 1) {
				t.Fatal("failed advance published state")
			}
		}
	}
}

func TestWriterFloorMemoryCorruptionAndPristineInstall(t *testing.T) {
	for _, damage := range []string{"missing_current", "bad_current", "missing_history", "bad_history", "epoch_index", "authority"} {
		t.Run(damage, func(t *testing.T) {
			s, a, r, first := memoryFloorFixture(t)
			state := s.writerFloors["floor-test"]

			switch damage {
			case "missing_current":
				state.current = nil
			case "bad_current":
				state.current = []byte("{}")
			case "missing_history":
				delete(state.history, first.ActivationID.String())
			case "bad_history":
				state.history[first.ActivationID.String()] = []byte("{}")
			case "epoch_index":
				state.epochs[first.Epoch] = id.New(managed.PrefixOperation).String()
			case "authority":
				q := first.Activation()
				q.AuthorityID = "another-authority"

				other, err := managed.NewWriterFloor(q, first.ActivatedAt)
				if err != nil {
					t.Fatal(err)
				}

				raw, err := managed.EncodeWriterFloor(other)
				if err != nil {
					t.Fatal(err)
				}

				state.current = raw
				state.history[first.ActivationID.String()] = raw
			}

			if _, err := r.ReadManagedWriterFloor(t.Context()); err == nil {
				t.Fatal("corrupt current accepted")
			}

			if _, err := a.Activate(t.Context(), memoryFloorRequest()); err == nil {
				t.Fatal("retained state treated as pristine")
			}
		})
	}
}

func TestWriterFloorMemoryReadOwnershipAndDisabledHeldCheck(t *testing.T) {
	s, a, r, first := memoryFloorFixture(t)
	if err := a.backend.WriterFloorSnapshot(t.Context(), func(snapshot managedstate.WriterFloorSnapshot) error {
		raw, err := snapshot.ReadCurrentWriterFloor(t.Context())
		if err != nil {
			return err
		}

		raw[0] = '!'

		raw, err = snapshot.ReadWriterFloorActivation(t.Context(), first.ActivationID)
		if err != nil {
			return err
		}

		raw[0] = '!'

		return nil
	}); err != nil {
		t.Fatal(err)
	}

	got, err := r.ReadManagedWriterFloor(t.Context())
	if err != nil || got != first {
		t.Fatal("read aliases retained bytes", err)
	}

	if _, err = a.Activate(t.Context(), memoryFloorNext(first)); err != nil {
		t.Fatal(err)
	}

	b := managedBackend{store: s}

	err = b.ControllerAtomic(t.Context(), "", nil, func(tx managedstate.ControllerTransaction) error {
		held := tx.(managedControllerTransaction)

		return held.checkWriterFloorMutationHeld(t.Context(), "floor-test")
	})
	if !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal("above-ceiling floor enabled mutation", err)
	}

	if _, err = NewWriterFloorAdmin(nil, "floor-test"); !errors.Is(err, managed.ErrInvalid) {
		t.Fatal(err)
	}

	if _, err = NewWriterFloorReader(s, ""); !errors.Is(err, managed.ErrInvalid) {
		t.Fatal(err)
	}
}

func TestWriterFloorMemoryCurrentRollbackCannotReuseEpoch(t *testing.T) {
	s, a, _, first := memoryFloorFixture(t)

	second, err := a.Activate(t.Context(), memoryFloorNext(first))
	if err != nil {
		t.Fatal(err)
	}

	state := s.writerFloors["floor-test"]
	state.current = bytes.Clone(state.history[first.ActivationID.String()])

	if _, err = a.Activate(t.Context(), second.Activation()); !errors.Is(err, managed.ErrWriterFloorCorrupt) {
		t.Fatal(err)
	}

	if _, err = a.Activate(t.Context(), memoryFloorNext(first)); !errors.Is(err, managed.ErrConflict) {
		t.Fatal(err)
	}
}
