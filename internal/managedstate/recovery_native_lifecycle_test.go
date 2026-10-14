package managedstate

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/managed"
)

// These fixtures exercise accepted custody and staged persistence. The full
// public mutation graph remains a required terminal-slice fixture before enablement.
func recoveryNativeCustodyFixture(t *testing.T) (*recoveryLeafFixture, *managed.Target, attemptView, managed.RecoveryNativeIntentRequest, managed.RecoveryRecord[managed.RecoveryNativeIntent], managed.RecoveryAssociation) {
	t.Helper()

	fixture, key, now := recoveryFixture()
	proof := recoveryTestProof(t, key, now)
	facts := managed.RecoveryNativeIntent{Binding: proof.Facts.Binding, Profile: managed.RemovalRecoveryProfile, Operation: "revalidate", RequestID: "request", RequestDigest: strings.Repeat("d", 64), Command: []byte("opaque-native-request"), CommandDigest: managed.Digest([]byte("opaque-native-request"))}
	record := recoveryTestRecord(t, managed.RecoveryIntentRecord, facts, now)
	ref := managed.AttemptRef{AttemptID: key.AttemptID, Generation: key.Generation, DescriptorDigest: strings.Repeat("a", 64)}
	request := managed.RecoveryNativeIntentRequest{Command: managed.RecoveryCommand{Key: key.Key, ID: record.Acceptance.OperationID, Revision: "1", LeaseEpoch: "1", Owner: "owner", AttemptRef: ref, Profile: managed.RemovalRecoveryProfile}, Intent: facts}

	fingerprint, err := recoveryNativeIntentFingerprint(request)
	if err != nil {
		t.Fatal(err)
	}

	record.Acceptance.RequestDigest, record.Acceptance.Receipt.Digest = fingerprint, fingerprint
	reference := recoveryTestStore(t, fixture, key, record)

	scope, err := attemptScope(key.Key)
	if err != nil {
		t.Fatal(err)
	}

	view := attemptView{validated: true, scope: scope, ref: ref, descriptor: managed.AttemptDescriptor{Key: key.Key, AttemptID: key.AttemptID, Generation: key.Generation}}

	header := recoveryRequestHeader{SchemaVersion: "1", Kind: managed.RecoveryIntentRecord, Command: request.Command}
	if err = attemptWrite(t.Context(), fixture, recoveryRequestKind, scope, reference.Key, header, true, managed.MaxRecoveryReceipt); err != nil {
		t.Fatal(err)
	}

	if err = stageRecoveryProtocolEvent(t.Context(), fixture, view, "recovery_native_intent", reference, record.Acceptance); err != nil {
		t.Fatal(err)
	}

	return fixture, &managed.Target{Identity: managed.Identity{Key: key.Key}}, view, request, record, reference
}

func TestNativeIntentAcceptedCustodyRejectsLostOrRewrittenRequest(t *testing.T) {
	fixture, target, view, request, record, reference := recoveryNativeCustodyFixture(t)
	if _, err := recoveryNativeIntentAcceptedHeld(t.Context(), fixture, target, view, reference); err != nil {
		t.Fatal(err)
	}

	for _, mode := range []string{"missing-header", "owner", "derived-coverage", "missing-event", "event", "receipt"} {
		t.Run(mode, func(t *testing.T) {
			broken := cloneActivationFixture(fixture)
			header := recoveryRequestHeader{SchemaVersion: "1", Kind: managed.RecoveryIntentRecord, Command: request.Command}

			switch mode {
			case "missing-header":
				delete(broken.rows, [3]string{recoveryRequestKind, view.scope, reference.Key})
			case "owner", "derived-coverage":
				if mode == "owner" {
					header.Command.Owner = "different"
				} else {
					header.DerivedCoverage = true
				}

				if err := attemptWrite(t.Context(), broken, recoveryRequestKind, view.scope, reference.Key, header, false, managed.MaxRecoveryReceipt); err != nil {
					t.Fatal(err)
				}
			case "missing-event":
				delete(broken.rows, [3]string{"removal_attempt_event", view.scope, request.Command.ID.String()})
			case "event":
				event, err := attemptRead[attemptEvent](t.Context(), broken, "removal_attempt_event", view.scope, request.Command.ID.String(), managed.MaxAttemptFact)
				if err != nil {
					t.Fatal(err)
				}

				event.Kind = "recovery_close_intent"
				if err = attemptWrite(t.Context(), broken, "removal_attempt_event", view.scope, request.Command.ID.String(), event, false, managed.MaxAttemptFact); err != nil {
					t.Fatal(err)
				}
			case "receipt":
				delete(broken.receipts, attemptReceiptKey{target.Key, record.Acceptance.OperationID, record.Acceptance.RequestDigest})
			}

			if _, err := recoveryNativeIntentAcceptedHeld(t.Context(), broken, target, view, reference); err == nil {
				t.Fatal("lost accepted custody passed", mode)
			}
		})
	}
	// A valid self-hash and a rewritten local event cannot rewrite the independent receipt.
	changed := record
	changed.Facts.Command = []byte("another-native-command")
	changed.Facts.CommandDigest = managed.Digest(changed.Facts.Command)

	var err error

	changed.Digest, err = managed.RecoveryFactDigest(changed.Facts)
	if err != nil {
		t.Fatal(err)
	}

	broken := cloneActivationFixture(fixture)

	changedRef := recoveryTestStoreRewrite(t, broken, attemptKey(view), changed, reference)
	if err = stageRecoveryProtocolEventRewrite(t, broken, view, "recovery_native_intent", changedRef, changed.Acceptance); err != nil {
		t.Fatal(err)
	}

	if _, err = recoveryReadFact[managed.RecoveryNativeIntent](t.Context(), broken, attemptKey(view), changedRef); err != nil {
		t.Fatal("fixture failed before witness check", err)
	}

	if _, err = recoveryNativeIntentAcceptedHeld(t.Context(), broken, target, view, changedRef); err == nil {
		t.Fatal("rewritten command accepted with original receipt")
	}
}

// Rewrite helpers deliberately model corruption beneath the immutable store API.
func recoveryTestStoreRewrite[T any](t *testing.T, fixture *recoveryLeafFixture, key managed.AttemptKey, record managed.RecoveryRecord[T], previous managed.RecoveryAssociation) managed.RecoveryAssociation {
	t.Helper()

	scope, err := attemptScope(key.Key)
	if err != nil {
		t.Fatal(err)
	}

	delete(fixture.rows, [3]string{string(previous.Kind), scope, previous.Key})
	delete(fixture.rows, [3]string{recoveryAcceptanceKind, scope, previous.Key})

	return recoveryTestStore(t, fixture, key, record)
}
func stageRecoveryProtocolEventRewrite(t *testing.T, fixture *recoveryLeafFixture, view attemptView, kind string, reference managed.RecoveryAssociation, acceptance managed.RecoveryAcceptance) error {
	t.Helper()
	delete(fixture.rows, [3]string{"removal_attempt_event", view.scope, acceptance.OperationID.String()})

	return stageRecoveryProtocolEvent(t.Context(), fixture, view, kind, reference, acceptance)
}

func TestNativeIntentOperationEdgesRemainAcyclic(t *testing.T) {
	fixture, target, view, request, record, fixtureRef := recoveryNativeCustodyFixture(t)
	if fixture == nil || target.Key != request.Command.Key || view.ref != request.Command.AttemptRef || record.Digest != fixtureRef.Digest {
		t.Fatal("invalid native custody fixture")
	}

	if err := recoveryNativeIntentShape(request.Intent); err != nil {
		t.Fatal(err)
	}

	reference := managed.RecoveryAssociation{Kind: managed.RecoveryProofRecord}

	for _, field := range []string{"proof", "permission", "cutoff", "coverage"} {
		facts := request.Intent

		switch field {
		case "proof":
			facts.Proof = &reference
		case "permission":
			facts.Permission = &reference
		case "cutoff":
			facts.Cutoff = &reference
		case "coverage":
			facts.Coverage = &reference
		}

		if err := recoveryNativeIntentShape(facts); !errors.Is(err, managed.ErrInvalid) {
			t.Fatal("revalidate predecessor edge accepted", field, err)
		}
	}

	commit := request.Intent
	commit.Operation = "commit"
	commit.Proof = &reference

	commit.Permission = &reference
	if err := recoveryNativeIntentShape(commit); err != nil {
		t.Fatal(err)
	}

	commit.Cutoff = &reference
	if err := recoveryNativeIntentShape(commit); !errors.Is(err, managed.ErrInvalid) {
		t.Fatal(err)
	}

	closeIntent := request.Intent
	closeIntent.Operation = "close"
	closeIntent.Proof = &reference

	closeIntent.Cutoff = &reference
	if err := recoveryNativeIntentShape(closeIntent); err != nil {
		t.Fatal(err)
	}

	closeIntent.Permission = &reference
	if err := recoveryNativeIntentShape(closeIntent); !errors.Is(err, managed.ErrInvalid) {
		t.Fatal(err)
	}
}

func TestNativeCloseSelectorUsesActualEncodedCap(t *testing.T) {
	fixture, key, _ := recoveryFixture()

	scope, err := attemptScope(key.Key)
	if err != nil {
		t.Fatal(err)
	}

	selected := recoveryNativeCloseSelection{SchemaVersion: "1"}

	raw, err := json.Marshal(selected)
	if err != nil {
		t.Fatal(err)
	}

	selected.BindingDigest = strings.Repeat("x", managed.MaxRecoveryReceipt-len(raw))

	raw, err = json.Marshal(selected)
	if err != nil || len(raw) != managed.MaxRecoveryReceipt {
		t.Fatal(len(raw), err)
	}

	if err = attemptWrite(t.Context(), fixture, recoveryNativeCloseSelectionKind, scope, "cap", selected, true, managed.MaxRecoveryReceipt); err != nil {
		t.Fatal(err)
	}

	selected.BindingDigest += "x"
	if err = attemptWrite(t.Context(), fixture, recoveryNativeCloseSelectionKind, scope, "over", selected, true, managed.MaxRecoveryReceipt); !errors.Is(err, managed.ErrCapacity) {
		t.Fatal(err)
	}

	if _, err = fixture.ReadRecord(t.Context(), recoveryNativeCloseSelectionKind, scope, "over"); !errors.Is(err, managed.ErrNotFound) {
		t.Fatal("oversized row persisted", err)
	}
	// Padding tests encoded bytes only; a non-digest selector never supplies authority.
}

func TestNativeIntentAndCloseRequireConfiguredLifecycle(t *testing.T) {
	repository := New(&recoveryBackendWitness{})
	if err := repository.requireConfiguredRecovery(); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal(err)
	}

	if err := repository.requireConfiguredRecovery(); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestNativeCustodyRowsRollbackAtEveryWriteAndReceipt(t *testing.T) {
	fixture, target, view, request, record, reference := recoveryNativeCustodyFixture(t)
	// Retain a new native request in an isolated batch using the actual row writers.
	empty, _, _ := recoveryFixture()

	receipt, err := record.Acceptance.Receipt.Value()
	if err != nil {
		t.Fatal(err)
	}

	staged := &recoveryActivationRows{ControllerTransaction: empty, key: target.Key, receipt: receipt}
	if _, err = recoveryWriteFact(t.Context(), staged, attemptKey(view), record); err != nil {
		t.Fatal(err)
	}

	header := recoveryRequestHeader{SchemaVersion: "1", Kind: managed.RecoveryIntentRecord, Command: request.Command}
	if err = attemptWrite(t.Context(), staged, recoveryRequestKind, view.scope, reference.Key, header, true, managed.MaxRecoveryReceipt); err != nil {
		t.Fatal(err)
	}

	if err = stageRecoveryProtocolEvent(t.Context(), staged, view, "recovery_native_intent", reference, record.Acceptance); err != nil {
		t.Fatal(err)
	}

	if len(staged.rows) != 4 {
		t.Fatal(len(staged.rows))
	}

	for failure := 1; failure <= len(staged.rows)+1; failure++ {
		backend := &activationTransactionFixture{state: cloneActivationFixture(empty), target: &managed.Target{Identity: managed.Identity{Key: target.Key}, LeaseUntil: receipt.LeaseUntil}, failAt: failure, failReceipt: failure > len(staged.rows)}
		before := cloneActivationFixture(backend.state)

		err = backend.ControllerAtomic(t.Context(), "native-custody", []managed.Key{target.Key}, func(tx ControllerTransaction) error {
			_, atomicErr := tx.Atomic(t.Context(), target.Key, request.Command.ID, receipt.Digest, func(current *managed.Target, _ time.Time) (managed.Receipt, error) {
				if applyErr := applyRecoveryActivationRows(t.Context(), tx, staged); applyErr != nil {
					return managed.Receipt{}, applyErr
				}

				current.Revision = receipt.Revision

				return receipt, nil
			})

			return atomicErr
		})
		if err == nil || !reflect.DeepEqual(before.rows, backend.state.rows) || !reflect.DeepEqual(before.receipts, backend.state.receipts) || backend.target.Revision != 0 {
			t.Fatal("partial custody committed", failure, err)
		}
	}

	if len(fixture.rows) != 4 {
		t.Fatal("fixture unexpectedly changed")
	}
}

func TestNativeCloseAcceptedCustodyBindsResultAndCommand(t *testing.T) {
	fixture, target, view, intentRequest, intent, intentRef := recoveryNativeCustodyFixture(t)
	proof := recoveryTestProof(t, attemptKey(view), intent.Acceptance.Receipt.AcceptedAt)
	association := intentRef
	association.Kind = managed.RecoveryCutoffRecord
	coverage := association
	coverage.Kind = managed.RecoveryCoverageRecord
	facts := managed.RecoveryNativeCloseFacts{Binding: intent.Facts.Binding, Intent: intentRef, Cutoff: association, Coverage: coverage, GuardID: "guard", GuardRootDigest: strings.Repeat("b", 64), NativeRequestID: "close-request", NativeRequestDigest: strings.Repeat("c", 64), NativeReceiptDigest: strings.Repeat("d", 64), NativeAcceptedAt: intent.Acceptance.Receipt.AcceptedAt, Export: proof.Facts.Export}
	record := recoveryTestRecord(t, managed.RecoveryCloseRecord, facts, facts.NativeAcceptedAt)
	command := intentRequest.Command
	command.ID = record.Acceptance.OperationID
	request := managed.RecoveryNativeCloseRequest{Command: command, Facts: facts}

	fingerprint, err := recoveryNativeCloseFingerprint(request)
	if err != nil {
		t.Fatal(err)
	}

	record.Acceptance.RequestDigest, record.Acceptance.Receipt.Digest = fingerprint, fingerprint
	reference := recoveryTestStore(t, fixture, attemptKey(view), record)

	header := recoveryRequestHeader{SchemaVersion: "1", Kind: managed.RecoveryCloseRecord, Command: command}
	if err = attemptWrite(t.Context(), fixture, recoveryRequestKind, view.scope, reference.Key, header, true, managed.MaxRecoveryReceipt); err != nil {
		t.Fatal(err)
	}

	if err = stageRecoveryProtocolEvent(t.Context(), fixture, view, "recovery_native_close", reference, record.Acceptance); err != nil {
		t.Fatal(err)
	}

	if _, err = recoveryNativeCloseAcceptedHeld(t.Context(), fixture, target, view, reference); err != nil {
		t.Fatal(err)
	}

	for _, mode := range []string{"missing-header", "changed-command", "missing-event", "changed-result"} {
		broken := cloneActivationFixture(fixture)
		selected := reference

		switch mode {
		case "missing-header":
			delete(broken.rows, [3]string{recoveryRequestKind, view.scope, reference.Key})
		case "changed-command":
			changed := header

			changed.Command.Owner = "different"
			if err = attemptWrite(t.Context(), broken, recoveryRequestKind, view.scope, reference.Key, changed, false, managed.MaxRecoveryReceipt); err != nil {
				t.Fatal(err)
			}
		case "missing-event":
			delete(broken.rows, [3]string{"removal_attempt_event", view.scope, command.ID.String()})
		case "changed-result":
			changed := record
			changed.Facts.GuardID = "another-guard"

			changed.Digest, err = managed.RecoveryFactDigest(changed.Facts)
			if err != nil {
				t.Fatal(err)
			}

			selected = recoveryTestStoreRewrite(t, broken, attemptKey(view), changed, reference)
			if err = stageRecoveryProtocolEventRewrite(t, broken, view, "recovery_native_close", selected, changed.Acceptance); err != nil {
				t.Fatal(err)
			}

			if _, err = recoveryReadFact[managed.RecoveryNativeCloseFacts](t.Context(), broken, attemptKey(view), selected); err != nil {
				t.Fatal("fixture failed before independent witness check", err)
			}
		}

		if _, err = recoveryNativeCloseAcceptedHeld(t.Context(), broken, target, view, selected); err == nil {
			t.Fatal("rewritten close custody accepted", mode)
		}
	}
	// The accepted-custody helper supplies no guard authority by itself.
	if _, err = recoveryNativeCloseExactHeld(t.Context(), fixture, target, view, reference); err == nil {
		t.Fatal("absent original binding and coverage became authority")
	}
}

func TestNativeCloseReserveDoesNotReleaseAbsentCoverage(t *testing.T) {
	for _, count := range []int{1, managed.MaxRecoveryBindings} {
		fixture, target, view, _ := activationMaterializationFixture(t, count, 1, false)
		reserve := recoveryReserveAfter{existing: 3, closeIntentsMissing: uint64(count), closeResultsMissing: uint64(count)}

		after, err := recoveryNativeCloseReserveHeld(t.Context(), fixture, target, view, reserve)
		if err != nil || after != reserve {
			t.Fatal("absent coverage released custody reserve", count, after, err)
		}
	}
}

func TestNativeIntentCompleteEncodedCapIncludesBase64AndAcceptance(t *testing.T) {
	fixture, target, view, request, record, fixtureRef := recoveryNativeCustodyFixture(t)
	if fixture == nil || target.Key != request.Command.Key || view.ref != request.Command.AttemptRef || record.Digest != fixtureRef.Digest {
		t.Fatal("invalid native custody fixture")
	}

	record.Facts.Command = nil
	record.Facts.CommandDigest = managed.Digest(nil)

	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	// Choose the largest whole base64 block below the limit, then fill its
	// remaining bytes with an ordinary bounded request token.
	available := managed.MaxRecoveryNativeIntent - len(raw)
	record.Facts.Command = []byte(strings.Repeat("x", (available/4)*3-3))
	record.Facts.CommandDigest = managed.Digest(record.Facts.Command)

	raw, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}

	record.Facts.RequestID += strings.Repeat("p", managed.MaxRecoveryNativeIntent-len(raw))

	record.Digest, err = managed.RecoveryFactDigest(record.Facts)
	if err != nil {
		t.Fatal(err)
	}

	request.Intent = record.Facts

	fingerprint, err := recoveryNativeIntentFingerprint(request)
	if err != nil {
		t.Fatal(err)
	}

	record.Acceptance.RequestDigest, record.Acceptance.Receipt.Digest = fingerprint, fingerprint

	raw, err = managed.EncodeRecoveryRecord(record)
	if err != nil || len(raw) != managed.MaxRecoveryNativeIntent {
		t.Fatal("complete cap", len(raw), err)
	}

	decoded, err := managed.DecodeRecoveryRecord[managed.RecoveryNativeIntent](raw)
	if err != nil || !reflect.DeepEqual(decoded, record) {
		t.Fatal("cap record was truncated", err)
	}

	record.Facts.RequestID += "p"

	record.Digest, err = managed.RecoveryFactDigest(record.Facts)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = managed.EncodeRecoveryRecord(record); !errors.Is(err, managed.ErrCapacity) {
		t.Fatal("cap+1 accepted", err)
	}
}
