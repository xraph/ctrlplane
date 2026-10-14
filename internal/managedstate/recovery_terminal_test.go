package managedstate

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

// Terminal fact tests cover deterministic retained representation and receipt
// binding. They do not replace the required full public Finish/Abort fixture.
func recoveryTerminalSettlementFixture(t *testing.T, steps int) (attemptView, managed.AttemptSettlementRequest, recoveryTerminalAuthority, time.Time) {
	t.Helper()

	_, key, now := recoveryFixture()
	ref := managed.AttemptRef{AttemptID: key.AttemptID, Generation: key.Generation, DescriptorDigest: strings.Repeat("a", 64)}
	view := attemptView{validated: true, ref: ref, descriptor: managed.AttemptDescriptor{Key: key.Key, AttemptID: key.AttemptID, Generation: key.Generation, ProviderOperationID: id.New(managed.PrefixOperation), VerifierID: "provider", ResourcesDigest: strings.Repeat("b", 64)}, state: managed.AttemptState{AdmissionDigest: strings.Repeat("c", 64), RevocationDigest: strings.Repeat("d", 64)}}

	scope, err := attemptScope(key.Key)
	if err != nil {
		t.Fatal(err)
	}

	view.scope = scope
	q := managed.AttemptSettlementRequest{AttemptRequest: managed.AttemptRequest{Command: managed.Command{Key: key.Key, ID: id.New(managed.PrefixOperation), Revision: 10, LeaseEpoch: 1, Owner: "owner"}, AttemptID: key.AttemptID, Generation: key.Generation}, Disposition: managed.SettledWithoutDeletion, ProducerProfile: managed.LocalNoDispatchProfile, VerifierID: "provider", Evidence: []byte("configured local closure"), SettledAt: now}

	coverage := managed.EffectiveRemovalCoverage{Key: key, AttemptRef: ref, ProviderOperationID: view.descriptor.ProviderOperationID, Disposition: q.Disposition}
	for index := range steps {
		coverage.Steps = append(coverage.Steps, managed.EffectiveProviderStep{Disposition: managed.EffectiveStepUnissued, StepDigest: managed.Digest([]byte(strings.Repeat("s", index+1)))})
	}

	record := recoveryTestRecord(t, managed.RecoveryCoverageRecord, coverage, now)
	reference := managed.RecoveryAssociation{Kind: managed.RecoveryCoverageRecord, Key: strings.Repeat("e", 64), Digest: record.Digest, OperationID: record.Acceptance.OperationID, ReceiptDigest: record.Acceptance.RequestDigest}
	authority := recoveryTerminalAuthority{coverage: record, reference: reference, closesDigest: strings.Repeat("f", 64), acceptedAt: now}

	return view, q, authority, now
}

func TestRecoverySettlementPreservesCodecAndExplicitUnissuedCoverage(t *testing.T) {
	for _, steps := range []int{1, managed.MaxRecoverySteps} {
		view, q, authority, now := recoveryTerminalSettlementFixture(t, steps)

		fact, err := recoverySettlementFact(view, q, authority, now)
		if err != nil {
			t.Fatal(err)
		}

		if fact.SchemaVersion != "1" || fact.LocalOperationID != q.ID || fact.AttemptRef != view.ref || fact.CoverageDigest != authority.coverage.Digest || fact.RevocationDigest != view.state.RevocationDigest || fact.Disposition != managed.SettledWithoutDeletion || len(fact.StepReferences) != steps {
			t.Fatal("settlement rewrote original identity or unissued disposition", fact)
		}

		raw, err := json.Marshal(fact)
		if err != nil {
			t.Fatal(err)
		}

		decoded, err := attemptDecode[managed.AttemptSettlement](raw, managed.MaxAttemptSettlement)
		if err != nil || !reflect.DeepEqual(decoded, fact) {
			t.Fatal("original settlement codec changed", err)
		}

		before, _ := attemptFact("removal_settlement", fact)

		after, _ := attemptFact("removal_settlement", decoded)
		if before != after {
			t.Fatal("original hash domain changed")
		}

		changed := q
		changed.Disposition = managed.Removed

		changed.ProducerProfile = "ctrlplane.managed.remove.v1"
		if _, err = recoverySettlementFact(view, changed, authority, now); err == nil {
			t.Fatal("unissued coverage became deletion")
		}

		changed = q

		changed.SettledAt = now.Add(-time.Nanosecond)
		if _, err = recoverySettlementFact(view, changed, authority, now); err == nil {
			t.Fatal("settlement predates guard closure")
		}
	}
}

func TestRecoveryTerminalRequestBindsCompleteClosedGuardSet(t *testing.T) {
	view, q, authority, now := recoveryTerminalSettlementFixture(t, 1)
	fixture, _, _ := recoveryFixture()

	witness, fingerprint, err := recoveryTerminalRequestFor(view, "attempt_settle", q, authority)
	if err != nil {
		t.Fatal(err)
	}

	fact, err := recoverySettlementFact(view, q, authority, now)
	if err != nil {
		t.Fatal(err)
	}

	if err = attemptWrite(t.Context(), fixture, "removal_settlement", view.scope, view.ref.AttemptID.String(), fact, true, managed.MaxAttemptSettlement); err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(fact)
	if err != nil {
		t.Fatal(err)
	}

	factDigest, err := attemptStoredFactDigest("removal_settlement", raw)
	if err != nil {
		t.Fatal(err)
	}

	receipt := managed.Receipt{ID: q.ID, Digest: fingerprint, Revision: 11, LeaseEpoch: 1, AcceptedAt: now, LeaseUntil: now.Add(time.Minute)}
	fixture.receipts[attemptReceiptKey{q.Key, q.ID, fingerprint}] = receipt

	event := attemptEvent{SchemaVersion: "1", AttemptRef: view.ref, Kind: "attempt_settle", RequestDigest: fingerprint, FactKind: "removal_settlement", FactKey: view.ref.AttemptID.String(), FactDigest: factDigest, Receipt: receipt}
	if err = attemptWrite(t.Context(), fixture, "removal_attempt_event", view.scope, q.ID.String(), event, true, managed.MaxAttemptFact); err != nil {
		t.Fatal(err)
	}

	if err = attemptWrite(t.Context(), fixture, recoveryTerminalRequestKind, view.scope, q.ID.String(), witness, true, maxRecoveryTerminalRequest); err != nil {
		t.Fatal(err)
	}

	if _, err = recoveryTerminalAcceptedRequestHeld(t.Context(), fixture, view, q.ID, "attempt_settle", q, authority); err != nil {
		t.Fatal(err)
	}

	for _, mode := range []string{"closed-set", "coverage", "request", "missing-witness", "missing-receipt"} {
		broken := cloneActivationFixture(fixture)
		changedAuthority := authority
		changedRequest := q

		switch mode {
		case "closed-set":
			changedAuthority.closesDigest = strings.Repeat("1", 64)
		case "coverage":
			changedAuthority.reference.Digest = strings.Repeat("2", 64)
		case "request":
			changedRequest.Evidence = []byte("different")
		case "missing-witness":
			delete(broken.rows, [3]string{recoveryTerminalRequestKind, view.scope, q.ID.String()})
		case "missing-receipt":
			delete(broken.receipts, attemptReceiptKey{q.Key, q.ID, fingerprint})
		}

		if mode == "closed-set" || mode == "coverage" || mode == "request" {
			changedWitness, _, hashErr := recoveryTerminalRequestFor(view, "attempt_settle", changedRequest, changedAuthority)
			if hashErr != nil {
				t.Fatal(hashErr)
			}

			if err = attemptWrite(t.Context(), broken, recoveryTerminalRequestKind, view.scope, q.ID.String(), changedWitness, false, maxRecoveryTerminalRequest); err != nil {
				t.Fatal(err)
			}
		}

		if _, err = recoveryTerminalAcceptedRequestHeld(t.Context(), broken, view, q.ID, "attempt_settle", changedRequest, changedAuthority); err == nil {
			t.Fatal("changed terminal authority passed original receipt", mode)
		}
	}
}

func TestRecoveryTerminalRequestActualCapAndCapPlusOne(t *testing.T) {
	view, q, authority, _ := recoveryTerminalSettlementFixture(t, 1)

	witness, _, err := recoveryTerminalRequestFor(view, "attempt_settle", q, authority)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(witness)
	if err != nil {
		t.Fatal(err)
	}
	// This padding checks storage representation only, not settlement evidence validity.
	q.Owner += strings.Repeat("x", maxRecoveryTerminalRequest-len(raw))
	// Owner tokens have their own authority bound, so marshal the witness directly.
	witness.Request, err = json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}

	raw, err = json.Marshal(witness)
	if err != nil || len(raw) != maxRecoveryTerminalRequest {
		t.Fatal(len(raw), err)
	}

	fixture, key, _ := recoveryFixture()

	scope, err := attemptScope(key.Key)
	if err != nil {
		t.Fatal(err)
	}

	if err = attemptWrite(t.Context(), fixture, recoveryTerminalRequestKind, scope, "cap", witness, true, maxRecoveryTerminalRequest); err != nil {
		t.Fatal(err)
	}

	q.Owner += "x"

	witness.Request, err = json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}

	if err = attemptWrite(t.Context(), fixture, recoveryTerminalRequestKind, scope, "over", witness, true, maxRecoveryTerminalRequest); !errors.Is(err, managed.ErrCapacity) {
		t.Fatal(err)
	}
}

func TestTerminalMethodsKeepGuardedOriginalRequirementDisabled(t *testing.T) {
	if _, _, _, err := managed.RequireRemovalRecovery(New(&recoveryBackendWitness{})); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal("incomplete guarded-original lifecycle enabled", err)
	}
}

func TestRecoveryTerminalCustodyRollbackAtEveryWriteAndReceipt(t *testing.T) {
	view, q, authority, now := recoveryTerminalSettlementFixture(t, 1)
	fixture, _, _ := recoveryFixture()

	witness, fingerprint, err := recoveryTerminalRequestFor(view, "attempt_settle", q, authority)
	if err != nil {
		t.Fatal(err)
	}

	fact, err := recoverySettlementFact(view, q, authority, now)
	if err != nil {
		t.Fatal(err)
	}

	receipt := managed.Receipt{ID: q.ID, Digest: fingerprint, Revision: 11, LeaseEpoch: 1, AcceptedAt: now, LeaseUntil: now.Add(time.Minute)}
	activation := managed.RecoveryAssociation{Kind: managed.RecoveryActivationRecord, Key: strings.Repeat("a", 64), Digest: strings.Repeat("b", 64), OperationID: id.New(managed.PrefixOperation), ReceiptDigest: strings.Repeat("c", 64)}
	view.recoveryActivation = &activation
	view.state.SchemaVersion = "2"
	view.state.Revision = "4"
	staged := &recoveryActivationRows{ControllerTransaction: fixture, key: q.Key, receipt: receipt}

	view.state.SettlementDigest, err = attemptSetFact(t.Context(), staged, &view, "removal_settlement", view.ref.AttemptID.String(), fact, managed.MaxAttemptSettlement)
	if err != nil {
		t.Fatal(err)
	}

	event, err := stageRecoveryTerminalCustody(t.Context(), staged, view, q, witness, "attempt_settle", receipt)
	if err != nil {
		t.Fatal(err)
	}

	if event.FactDigest != view.state.SettlementDigest || len(staged.rows) != 4 || len(fixture.rows) != 0 {
		t.Fatal("terminal custody escaped staging")
	}

	for failure := 1; failure <= len(staged.rows)+1; failure++ {
		backend := &activationTransactionFixture{state: cloneActivationFixture(fixture), target: &managed.Target{Identity: managed.Identity{Key: q.Key}, LeaseUntil: receipt.LeaseUntil}, failAt: failure, failReceipt: failure > len(staged.rows)}
		before := cloneActivationFixture(backend.state)

		err = backend.ControllerAtomic(t.Context(), "terminal-custody", []managed.Key{q.Key}, func(tx ControllerTransaction) error {
			_, atomicErr := tx.Atomic(t.Context(), q.Key, q.ID, fingerprint, func(current *managed.Target, _ time.Time) (managed.Receipt, error) {
				if applyErr := applyRecoveryActivationRows(t.Context(), tx, staged); applyErr != nil {
					return managed.Receipt{}, applyErr
				}

				current.Revision = receipt.Revision

				return receipt, nil
			})

			return atomicErr
		})
		if err == nil || !reflect.DeepEqual(before.rows, backend.state.rows) || !reflect.DeepEqual(before.receipts, backend.state.receipts) || backend.target.Revision != 0 {
			t.Fatal("partial terminal custody committed", failure, err)
		}
	}
}
