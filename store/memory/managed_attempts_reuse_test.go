package memory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

func TestManagedRemovalContinuationMemoryReadSession(t *testing.T) {
	s, q := memoryContinuationInput(t)

	receipt, err := s.BeginManagedRemovalContinuation(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}

	key := managed.AttemptKey{Key: q.Action.Key, AttemptID: receipt.Successor.AttemptID, Generation: receipt.Successor.Generation}
	before := projectionSnapshot(t, s)
	stop := errors.New("rollback probe")

	err = (managedBackend{store: s}).ControllerAtomic(t.Context(), "read-session", []managed.Key{key.Key}, func(tx managedstate.ControllerTransaction) error {
		repo := managedstate.New(tx)
		held := tx.(managedControllerTransaction)
		check := func(want bool) {
			t.Helper()

			_, e := repo.ReadManagedRemovalAttempt(t.Context(), key)
			if (e == nil) != want {
				t.Fatalf("valid=%v error=%v", want, e)
			}
		}
		check(true)

		for bucket, rows := range held.store.managedControllerRecords {
			kind, _, _ := strings.Cut(bucket, ":")
			switch kind {
			case managed.OriginalRegistration, "removal_attempt_closure", "removal_attempt_current", "removal_attempt_event":
			default:
				continue
			}

			for rowKey, raw := range rows {
				delete(rows, rowKey)
				check(false)

				rows[rowKey] = []byte(`{}`)

				check(false)

				rows[rowKey] = raw

				check(true)
			}
		}

		for receiptKey, saved := range held.store.managedReceipts {
			// Only original acceptance receipts are dependencies of this read.
			needed := false

			for bucket, rows := range held.store.managedControllerRecords {
				if strings.HasPrefix(bucket, managed.OriginalRegistration+":") {
					_, needed = rows[saved.ID.String()]
					if needed {
						break
					}
				}
			}

			if !needed {
				continue
			}

			delete(held.store.managedReceipts, receiptKey)
			check(false)

			corrupt := saved
			corrupt.Digest = managed.Digest([]byte("changed receipt"))
			held.store.managedReceipts[receiptKey] = corrupt

			check(false)

			held.store.managedReceipts[receiptKey] = saved

			check(true)
		}

		return stop
	})
	if !errors.Is(err, stop) || projectionSnapshot(t, s) != before {
		t.Fatal("read-session rollback", err)
	}
}

type originalAfterWriteBackend struct {
	managedBackend

	original managedstate.ControllerRecordKey
}
type originalAfterWriteTransaction struct {
	managedstate.ControllerTransaction

	original managedstate.ControllerRecordKey
}

func (b originalAfterWriteBackend) ControllerAtomic(ctx context.Context, serial string, keys []managed.Key, change func(managedstate.ControllerTransaction) error) error {
	return b.managedBackend.ControllerAtomic(ctx, serial, keys, func(tx managedstate.ControllerTransaction) error {
		return change(originalAfterWriteTransaction{tx, b.original})
	})
}
func (tx originalAfterWriteTransaction) WriteRecord(ctx context.Context, kind, scope, key string, raw []byte, insert bool) error {
	if err := tx.ControllerTransaction.WriteRecord(ctx, kind, scope, key, raw, insert); err != nil {
		return err
	}

	if kind == "removal_attempt_event" {
		return tx.ControllerTransaction.WriteRecord(ctx, tx.original.Kind, tx.original.Scope, tx.original.Key, []byte(`{}`), false)
	}

	return nil
}
func TestManagedRemovalContinuationMemoryPostWriteValidation(t *testing.T) {
	s, q := memoryContinuationInput(t)

	receipt, err := s.BeginManagedRemovalContinuation(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}

	var original managedstate.ControllerRecordKey

	for bucket, rows := range s.managedControllerRecords {
		kind, scope, _ := strings.Cut(bucket, ":")
		if kind == managed.OriginalRegistration {
			for key := range rows {
				original = managedstate.ControllerRecordKey{Kind: kind, Scope: scope, Key: key}

				break
			}

			break
		}
	}

	if original.Key == "" {
		t.Fatal("missing original fixture")
	}

	before := projectionSnapshot(t, s)
	repo := managedstate.New(originalAfterWriteBackend{managedBackend{store: s}, original})
	request := managed.AttemptRequest{Command: projectionCommand(t, s, q.Action.Key), AttemptID: receipt.Successor.AttemptID, Generation: receipt.Successor.Generation}

	request.ID = id.New(managed.PrefixOperation)
	if _, err = repo.RevokeManagedRemovalAttempt(t.Context(), request); err == nil {
		t.Fatal("post-write original change accepted")
	}

	if projectionSnapshot(t, s) != before {
		t.Fatal("failed validation leaked staged writes")
	}

	if _, err = s.RevokeManagedRemovalAttempt(t.Context(), request); err != nil {
		t.Fatal("valid retry", err)
	}
}
