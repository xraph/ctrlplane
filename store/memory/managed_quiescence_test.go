package memory

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

func memoryQuiescence(t *testing.T) (*Store, managed.QuiescenceAcceptanceRequest) {
	t.Helper()
	s, a := memoryOriginal(t)

	d := memoryOriginalDrain(t, s, a)
	if _, err := s.RecordManagedOriginalDrain(t.Context(), d); err != nil {
		t.Fatal(err)
	}

	r, err := s.ReadManagedOriginalLifecycle(t.Context(), managed.OriginalLifecycleKey{Key: a.Command.Key, Kind: managed.OriginalRegistration, OperationID: a.Command.ID})
	if err != nil {
		t.Fatal(err)
	}

	drain, err := s.ReadManagedOriginalLifecycle(t.Context(), managed.OriginalLifecycleKey{Key: a.Command.Key, Kind: managed.OriginalDrain, OperationID: d.Command.ID})
	if err != nil {
		t.Fatal(err)
	}

	ref, err := managed.NewQuiescenceReference(r, drain)
	if err != nil {
		t.Fatal(err)
	}

	stamp := time.Now().UTC().Add(-time.Millisecond)
	o := managed.QuiescenceObservation{QuiescenceLeaf: managed.QuiescenceLeaf{Reference: ref, ObservedAt: stamp, ValidUntil: stamp.Add(time.Minute), AdmissionClosed: true, Quiescent: true, Complete: true, ActiveClaims: "0", UnknownClaims: "0", ObservationID: "observation", ChallengeID: "challenge", VerifierID: ref.Process.ProducerVerifier}, Attestation: []byte("synthetic")}

	o.ChallengeDigest, err = managed.QuiescenceChallengeDigest(o.QuiescenceLeaf)
	if err != nil {
		t.Fatal(err)
	}

	return s, managed.QuiescenceAcceptanceRequest{Command: memoryOriginalCommand(t, s, a.Command.Key, a.Command.ReservationID), Observation: o}
}

type quiescenceFailureBackend struct {
	managedBackend

	failure string
}
type quiescenceFailureTransaction struct {
	managedstate.ControllerTransaction

	failure string
}

func (b quiescenceFailureBackend) ControllerAtomic(ctx context.Context, serial string, keys []managed.Key, change func(managedstate.ControllerTransaction) error) error {
	return b.managedBackend.ControllerAtomic(ctx, serial, keys, func(tx managedstate.ControllerTransaction) error {
		if err := change(quiescenceFailureTransaction{tx, b.failure}); err != nil {
			return err
		}

		if b.failure == "commit" {
			return errors.New("injected commit failure")
		}

		return nil
	})
}
func (tx quiescenceFailureTransaction) WriteRecord(ctx context.Context, kind, scope, key string, raw []byte, insert bool) error {
	if err := tx.ControllerTransaction.WriteRecord(ctx, kind, scope, key, raw, insert); err != nil {
		return err
	}

	if tx.failure == kind {
		return errors.New("injected after retained write")
	}

	return nil
}
func (tx quiescenceFailureTransaction) Atomic(ctx context.Context, k managed.Key, operation id.ID, digest string, change func(*managed.Target, time.Time) (managed.Receipt, error)) (managed.Receipt, error) {
	receipt, err := tx.ControllerTransaction.Atomic(ctx, k, operation, digest, func(t *managed.Target, now time.Time) (managed.Receipt, error) {
		if tx.failure == "callback-expiry" {
			now = now.Add(time.Minute)
		}

		return change(t, now)
	})
	if err == nil && tx.failure == "receipt" {
		return managed.Receipt{}, errors.New("injected after receipt")
	}

	return receipt, err
}

func TestManagedQuiescenceMemoryRollback(t *testing.T) {
	for _, failure := range []string{managed.QuiescenceHistory, managed.QuiescenceCurrent, "receipt", "commit", "callback-expiry"} {
		t.Run(failure, func(t *testing.T) {
			s, q := memoryQuiescence(t)
			before := projectionSnapshot(t, s)

			repo := managedstate.New(quiescenceFailureBackend{managedBackend: managedBackend{store: s}, failure: failure})
			if _, err := repo.AcceptManagedQuiescence(t.Context(), q); err == nil {
				t.Fatal("injected failure accepted")
			}

			if after := projectionSnapshot(t, s); !reflect.DeepEqual(before, after) {
				t.Fatal("partial write escaped transaction")
			}

			if _, err := s.AcceptManagedQuiescence(t.Context(), q); err != nil {
				t.Fatal("rollback recovery", err)
			}
		})
	}
}

func TestManagedQuiescenceMemoryCorruption(t *testing.T) {
	for _, failure := range []string{"missing-history", "corrupt-history", "missing-current", "corrupt-current", "wrong-current-target", "wrong-current-reservation", "current-time", "missing-receipt", "corrupt-receipt", "missing-original", "corrupt-original"} {
		t.Run(failure, func(t *testing.T) {
			s, q := memoryQuiescence(t)
			if _, err := s.AcceptManagedQuiescence(t.Context(), q); err != nil {
				t.Fatal(err)
			}

			scope, err := managed.QuiescenceScope(q.Command.Key, q.Command.ReservationID)
			if err != nil {
				t.Fatal(err)
			}

			history := s.managedControllerRecords[managed.QuiescenceHistory+":"+scope]
			current := s.managedControllerRecords[managed.QuiescenceCurrent+":"+scope]

			switch failure {
			case "missing-history":
				delete(history, q.Command.ID.String())
			case "corrupt-history":
				history[q.Command.ID.String()] = []byte("{}")
			case "missing-current":
				delete(current, "current")
			case "corrupt-current":
				current["current"] = []byte("{}")
			case "wrong-current-target", "wrong-current-reservation", "current-time":
				index, e := managed.ParseQuiescenceIndex(current["current"])
				if e != nil {
					t.Fatal(e)
				}

				if failure == "wrong-current-target" {
					index.OperationID = id.New(managed.PrefixOperation)
				}

				if failure == "wrong-current-reservation" {
					index.ReservationID = id.New(managed.PrefixReservation)
				}

				if failure == "current-time" {
					index.ObservedAt = index.ObservedAt.Add(time.Nanosecond)
				}

				current["current"], err = index.Bytes()
				if err != nil {
					t.Fatal(err)
				}
			case "missing-receipt", "corrupt-receipt":
				receiptKey := q.Command.InstanceID.String() + ":" + q.Command.ID.String()
				if failure == "missing-receipt" {
					delete(s.managedReceipts, receiptKey)
				} else {
					receipt := s.managedReceipts[receiptKey]
					receipt.Revision++
					s.managedReceipts[receiptKey] = receipt
				}
			case "missing-original", "corrupt-original":
				raw, e := json.Marshal(q.Command.Key)
				if e != nil {
					t.Fatal(e)
				}

				originals := s.managedControllerRecords[managed.OriginalDrain+":"+managed.Digest(raw)]
				if failure == "missing-original" {
					delete(originals, q.Observation.Reference.DrainOperationID.String())
				} else {
					originals[q.Observation.Reference.DrainOperationID.String()] = []byte("{}")
				}
			}

			k := managed.QuiescenceKey{Key: q.Command.Key, ReservationID: q.Command.ReservationID, OperationID: q.Command.ID}
			if _, err = s.ReadManagedQuiescence(t.Context(), k); err == nil {
				t.Fatal("corrupt read accepted")
			}

			before := projectionSnapshot(t, s)
			if _, err = s.AcceptManagedQuiescence(t.Context(), q); err == nil {
				t.Fatal("corrupt replay repaired")
			}

			newer := q
			newer.Command = memoryOriginalCommand(t, s, q.Command.Key, q.Command.ReservationID)
			newer.Observation.ObservedAt = time.Now().UTC()
			newer.Observation.ValidUntil = newer.Observation.ObservedAt.Add(time.Minute)

			newer.Observation.ChallengeDigest, err = managed.QuiescenceChallengeDigest(newer.Observation.QuiescenceLeaf)
			if err != nil {
				t.Fatal(err)
			}

			if _, err = s.AcceptManagedQuiescence(t.Context(), newer); err == nil {
				t.Fatal("fresh acceptance repaired corrupt history")
			}

			if after := projectionSnapshot(t, s); !reflect.DeepEqual(before, after) {
				t.Fatal("refused request changed history")
			}
		})
	}
}
