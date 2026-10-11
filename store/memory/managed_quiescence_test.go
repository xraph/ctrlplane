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

// quiescencePauseBackend delays the real held Atomic call without replacing its clock.
type quiescencePauseBackend struct {
	managedBackend

	t         *testing.T
	operation id.ID
	expiry    time.Time
	paused    *bool
	skipCalls *int
}

type quiescencePauseTransaction struct {
	managedstate.ControllerTransaction

	pause quiescencePauseBackend
}

func (b quiescencePauseBackend) ControllerAtomic(ctx context.Context, serial string, keys []managed.Key, change func(managedstate.ControllerTransaction) error) error {
	return b.managedBackend.ControllerAtomic(ctx, serial, keys, func(tx managedstate.ControllerTransaction) error {
		if !tx.Now().Before(b.expiry) {
			b.t.Fatal("fixture expired before entering the real held transaction")
		}

		b.t.Logf("held transaction entry=%s expiry=%s", tx.Now().Format(time.RFC3339Nano), b.expiry.Format(time.RFC3339Nano))

		return change(quiescencePauseTransaction{ControllerTransaction: tx, pause: b})
	})
}

func (tx quiescencePauseTransaction) Atomic(ctx context.Context, k managed.Key, operation id.ID, digest string, change func(*managed.Target, time.Time) (managed.Receipt, error)) (managed.Receipt, error) {
	if operation == tx.pause.operation && *tx.pause.skipCalls > 0 {
		*tx.pause.skipCalls--
	} else if operation == tx.pause.operation {
		if !time.Now().Before(tx.pause.expiry) {
			tx.pause.t.Fatal("fixture expired before reaching the intended Atomic pause")
		}

		*tx.pause.paused = true
		time.Sleep(time.Until(tx.pause.expiry) + time.Millisecond)
		tx.pause.t.Logf("real held Atomic resumes=%s", time.Now().UTC().Format(time.RFC3339Nano))
	}

	return tx.ControllerTransaction.Atomic(ctx, k, operation, digest, change)
}

func signMemoryQuiescence(t *testing.T, q *managed.QuiescenceAcceptanceRequest) {
	t.Helper()

	var err error

	q.Observation.ChallengeDigest, err = managed.QuiescenceChallengeDigest(q.Observation.QuiescenceLeaf)
	if err != nil {
		t.Fatal(err)
	}
}

func TestManagedQuiescenceMemoryActualCallbackExpiry(t *testing.T) {
	s, q := memoryQuiescence(t)
	q.Observation.ObservedAt = time.Now().UTC()
	q.Observation.ValidUntil = q.Observation.ObservedAt.Add(300 * time.Millisecond)
	signMemoryQuiescence(t, &q)
	before := projectionSnapshot(t, s)
	paused := false
	skipCalls := 0 // ReadReceipt does not invoke Atomic before the fresh transition.

	repo := managedstate.New(quiescencePauseBackend{managedBackend: managedBackend{store: s}, t: t, operation: q.Command.ID, expiry: q.Observation.ValidUntil, paused: &paused, skipCalls: &skipCalls})
	if _, err := repo.AcceptManagedQuiescence(t.Context(), q); !errors.Is(err, managed.ErrBlocked) {
		t.Fatalf("real Memory callback accepted expired proof: %v", err)
	}

	if !paused {
		t.Fatal("fresh transition did not reach real Atomic pause")
	}

	if after := projectionSnapshot(t, s); after != before {
		t.Fatal("expired acceptance changed parent, receipt, history, index or projection")
	}
}

func memoryQuiescenceProvider(t *testing.T) (*Store, managed.ActionSubmission, managed.QuiescenceAcceptanceRequest) {
	return memoryQuiescenceProviderSetup(t, true)
}
func memoryQuiescenceProviderSetup(t *testing.T, withAttempt bool) (*Store, managed.ActionSubmission, managed.QuiescenceAcceptanceRequest) {
	t.Helper()
	seed, original := memoryOriginal(t)

	target, err := seed.ReadManagedTarget(t.Context(), original.Command.Key)
	if err != nil {
		t.Fatal(err)
	}

	s := New()
	action := projectionAction(t)
	action.Key = target.Key
	action.NewTarget = &target.Identity
	signProjectionAction(t, &action)

	if _, err = s.SubmitManagedAction(t.Context(), action); err != nil {
		t.Fatal(err)
	}

	projection := projectionLease(t, s, target.Key, action.ID)
	if _, err = s.ProjectManagedRows(t.Context(), projection); err != nil {
		t.Fatal(err)
	}

	if _, err = s.IssueManagedProvision(t.Context(), projectionCommand(t, s, target.Key)); err != nil {
		t.Fatal(err)
	}

	if _, err = s.BindManagedResources(t.Context(), managed.ResourcesRequest{Command: projectionCommand(t, s, target.Key), OperationID: target.ProvisionOperationID, Receipt: []byte("provisioned"), Resources: map[string]string{"network": "network", "api": "api", "sidecar": "sidecar"}}); err != nil {
		t.Fatal(err)
	}

	registration := target.Reservations[0]

	registration.ID = original.Command.ReservationID
	if _, err = s.ReserveManagedRegistration(t.Context(), managed.ReserveRequest{Command: projectionCommand(t, s, target.Key), Registration: registration.Registration}); err != nil {
		t.Fatal(err)
	}

	if _, err = s.IssueManagedRegistration(t.Context(), managed.ReservationRequest{Command: projectionCommand(t, s, target.Key), ReservationID: registration.ID}); err != nil {
		t.Fatal(err)
	}

	original.Command = memoryOriginalCommand(t, s, target.Key, registration.ID)

	drain := memoryOriginalDrain(t, s, original)
	if _, err = s.RecordManagedOriginalDrain(t.Context(), drain); err != nil {
		t.Fatal(err)
	}

	reg, err := s.ReadManagedOriginalLifecycle(t.Context(), managed.OriginalLifecycleKey{Key: target.Key, Kind: managed.OriginalRegistration, OperationID: original.Command.ID})
	if err != nil {
		t.Fatal(err)
	}

	d, err := s.ReadManagedOriginalLifecycle(t.Context(), managed.OriginalLifecycleKey{Key: target.Key, Kind: managed.OriginalDrain, OperationID: drain.Command.ID})
	if err != nil {
		t.Fatal(err)
	}

	ref, err := managed.NewQuiescenceReference(reg, d)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = s.SealManagedTarget(t.Context(), projectionCommand(t, s, target.Key)); err != nil {
		t.Fatal(err)
	}

	if withAttempt {
		memoryAttemptStart(t, s, action, registration.ID)
	}

	q := managed.QuiescenceAcceptanceRequest{Command: memoryOriginalCommand(t, s, target.Key, registration.ID), Observation: managed.QuiescenceObservation{QuiescenceLeaf: managed.QuiescenceLeaf{Reference: ref, AdmissionClosed: true, Quiescent: true, Complete: true, ActiveClaims: "0", UnknownClaims: "0", ObservationID: "observation", ChallengeID: "challenge", VerifierID: ref.Process.ProducerVerifier}, Attestation: []byte("synthetic")}}

	return s, action, q
}

func TestManagedQuiescenceMemoryActualProviderCallbackExpiry(t *testing.T) {
	s, action, q := memoryQuiescenceProvider(t)
	q.Observation.ObservedAt = time.Now().UTC()
	q.Observation.ValidUntil = q.Observation.ObservedAt.Add(2 * time.Second)
	signMemoryQuiescence(t, &q)

	receipt, err := s.AcceptManagedQuiescence(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}

	admission := managed.AttemptAdmissionRequest{AttemptRequest: memoryAttemptRequest(t, s, action), VerifierID: "provider"}
	if _, err = s.AdmitManagedRemovalAttempt(t.Context(), admission); err != nil {
		t.Fatal(err)
	}

	first := managed.ProviderStepAuthorization{Command: projectionCommand(t, s, action.Key), Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: action.Key, ActionID: action.ID}, ProviderOperationID: func() id.ID {
		record, e := s.ReadManagedRemovalAttempt(t.Context(), managed.AttemptKey{Key: action.Key, AttemptID: admission.AttemptID, Generation: "1"})
		if e != nil {
			t.Fatal(e)
		}

		return record.Descriptor.ProviderOperationID
	}(), Kind: managed.StepRemoveContainer, Service: "api"}}

	grant, err := s.AuthorizeManagedProviderStep(t.Context(), first)
	if err != nil || !grant.NewlyIssued {
		t.Fatal("first removal step", err)
	}

	if err = s.RecordManagedProviderStepResult(t.Context(), managed.ProviderStepResult{Permission: grant.Permission, Outcome: managed.PortTerminal, ResourceID: grant.Permission.ResourceID, Evidence: []byte("removed"), ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	second := first
	second.Command = projectionCommand(t, s, action.Key)
	second.Step.Service = "sidecar"
	before := projectionSnapshot(t, s)
	paused := false
	skipCalls := 0

	repo := managedstate.New(quiescencePauseBackend{managedBackend: managedBackend{store: s}, t: t, operation: second.ID, expiry: q.Observation.ValidUntil, paused: &paused, skipCalls: &skipCalls})
	if result, e := repo.AuthorizeManagedProviderStep(t.Context(), second); !errors.Is(e, managed.ErrBlocked) || result.NewlyIssued {
		t.Fatalf("real Memory callback issued fresh permission after expiry: %+v %v", result, e)
	}

	if !paused {
		t.Fatal("later provider step did not reach real Atomic pause")
	}

	if after := projectionSnapshot(t, s); after != before {
		t.Fatal("expired provider step changed parent, receipt, history, index, permission or projection")
	}

	if saved, e := s.AcceptManagedQuiescence(t.Context(), q); e != nil || saved != receipt {
		t.Fatal("expired accepted observation replay", e)
	}

	if replay, e := s.AuthorizeManagedProviderStep(t.Context(), first); e != nil || replay.NewlyIssued || replay.Permission != grant.Permission {
		t.Fatal("expired permission replay authorized another send", e)
	}

	if after := projectionSnapshot(t, s); after != before {
		t.Fatal("saved replay mutated state")
	}
}

func TestManagedQuiescenceMemoryHistoricalReplayCorruption(t *testing.T) {
	for _, failure := range []string{"newer-history", "newer-receipt"} {
		t.Run(failure, func(t *testing.T) {
			s, older := memoryQuiescence(t)
			if _, err := s.AcceptManagedQuiescence(t.Context(), older); err != nil {
				t.Fatal(err)
			}

			newer := older
			newer.Command = memoryOriginalCommand(t, s, older.Command.Key, older.Command.ReservationID)
			newer.Observation.ObservedAt = time.Now().UTC()
			newer.Observation.ValidUntil = newer.Observation.ObservedAt.Add(time.Minute)
			newer.Observation.ObservationID = "newer-observation"
			signMemoryQuiescence(t, &newer)

			if _, err := s.AcceptManagedQuiescence(t.Context(), newer); err != nil {
				t.Fatal(err)
			}

			if _, err := s.AcceptManagedQuiescence(t.Context(), older); err != nil {
				t.Fatal("valid historical replay", err)
			}

			scope, err := managed.QuiescenceScope(older.Command.Key, older.Command.ReservationID)
			if err != nil {
				t.Fatal(err)
			}

			if failure == "newer-history" {
				s.managedControllerRecords[managed.QuiescenceHistory+":"+scope][newer.Command.ID.String()] = []byte("{}")
			} else {
				key := newer.Command.InstanceID.String() + ":" + newer.Command.ID.String()
				r := s.managedReceipts[key]
				r.Revision++
				s.managedReceipts[key] = r
			}

			before := projectionSnapshot(t, s)
			if _, err = s.AcceptManagedQuiescence(t.Context(), older); err == nil {
				t.Fatal("historical replay ignored corrupt newer selection")
			}

			if after := projectionSnapshot(t, s); after != before {
				t.Fatal("historical replay mutated or repaired state")
			}
		})
	}
}
