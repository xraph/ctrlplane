package managedstate

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/xraph/ctrlplane/managed"
)

func TestRevalidationRequestWitnessBindsFactsAndHeaderToIndependentReceipt(t *testing.T) {
	fixture, key, now := recoveryFixture()
	record := recoveryTestProof(t, key, now)
	ref := managed.AttemptRef{AttemptID: key.AttemptID, Generation: key.Generation, DescriptorDigest: strings.Repeat("a", 64)}
	request := managed.RemovalRevalidationRequest{Command: managed.RecoveryCommand{Key: key.Key, ID: record.Acceptance.OperationID, Revision: "1", LeaseEpoch: "1", Owner: "owner", AttemptRef: ref, Profile: managed.RemovalRecoveryProfile}, Facts: record.Facts}

	fingerprint, err := recoveryProofFingerprint(request)
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

	header := recoveryRequestHeader{SchemaVersion: "1", Kind: managed.RecoveryProofRecord, Command: request.Command}
	if err = attemptWrite(t.Context(), fixture, recoveryRequestKind, scope, reference.Key, header, true, managed.MaxRecoveryReceipt); err != nil {
		t.Fatal(err)
	}

	if _, err = recoveryProofRequestHeld(t.Context(), fixture, view, reference, record); err != nil {
		t.Fatal(err)
	}

	for _, change := range []string{"proof", "command", "expected-current", "missing"} {
		t.Run(change, func(t *testing.T) {
			broken := cloneActivationFixture(fixture)
			saved := record
			changed := header

			switch change {
			case "proof":
				saved.Facts.GuardID = "another-guard"

				saved.Digest, err = managed.RecoveryFactDigest(saved.Facts)
				if err != nil {
					t.Fatal(err)
				}
			case "command":
				changed.Command.Owner = "another-owner"
			case "expected-current":
				changed.ExpectedCurrentDigest = strings.Repeat("9", 64)
			case "missing":
				delete(broken.rows, [3]string{recoveryRequestKind, scope, reference.Key})
			}

			if change != "missing" {
				if err = attemptWrite(t.Context(), broken, recoveryRequestKind, scope, reference.Key, changed, false, managed.MaxRecoveryReceipt); err != nil {
					t.Fatal(err)
				}
			}

			if _, err = recoveryProofRequestHeld(t.Context(), broken, view, reference, saved); err == nil {
				t.Fatal("rewritten request accepted with original receipt")
			}
		})
	}
}

func TestRecoveryRequestHeaderActualEncodedCapAndCapPlusOne(t *testing.T) {
	fixture, key, _ := recoveryFixture()

	scope, err := attemptScope(key.Key)
	if err != nil {
		t.Fatal(err)
	}

	header := recoveryRequestHeader{SchemaVersion: "1", Kind: managed.RecoveryProofRecord}

	raw, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}

	header.Command.Owner = strings.Repeat("p", managed.MaxRecoveryReceipt-len(raw))

	raw, err = json.Marshal(header)
	if err != nil || len(raw) != managed.MaxRecoveryReceipt {
		t.Fatal(len(raw), err)
	}

	if err = attemptWrite(t.Context(), fixture, recoveryRequestKind, scope, "cap", header, true, managed.MaxRecoveryReceipt); err != nil {
		t.Fatal(err)
	}

	header.Command.Owner += "p"
	if err = attemptWrite(t.Context(), fixture, recoveryRequestKind, scope, "over", header, true, managed.MaxRecoveryReceipt); !errors.Is(err, managed.ErrCapacity) {
		t.Fatal("request header cap+1", err)
	}

	if _, err = fixture.ReadRecord(t.Context(), recoveryRequestKind, scope, "over"); !errors.Is(err, managed.ErrNotFound) {
		t.Fatal("oversized request persisted", err)
	}
	// Oversized owner text tests encoding only; it is never a valid authority command.
}

func TestRevalidationIntakeRequiresConfiguredLifecycle(t *testing.T) {
	if err := New(&recoveryBackendWitness{}).requireConfiguredRecovery(); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal("partial lifecycle enabled", err)
	}
}

func TestRetryPreEffectExactCustodyRequiresHistoricalRequestAndEvent(t *testing.T) {
	fixture, key, now := recoveryFixture()
	record := recoveryTestProof(t, key, now)
	record.Facts.ProcessDisposition = "live"

	proofDigest, digestErr := managed.RecoveryFactDigest(record.Facts)
	if digestErr != nil {
		t.Fatal(digestErr)
	}

	record.Digest = proofDigest
	ref := managed.AttemptRef{AttemptID: key.AttemptID, Generation: key.Generation, DescriptorDigest: strings.Repeat("a", 64)}
	request := managed.RemovalRevalidationRequest{Command: managed.RecoveryCommand{Key: key.Key, ID: record.Acceptance.OperationID, Revision: "1", LeaseEpoch: "1", Owner: "owner", AttemptRef: ref, Profile: managed.RemovalRecoveryProfile}, Facts: record.Facts}

	fingerprint, err := recoveryProofFingerprint(request)
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

	header := recoveryRequestHeader{SchemaVersion: "1", Kind: managed.RecoveryProofRecord, Command: request.Command}
	if err = attemptWrite(t.Context(), fixture, recoveryRequestKind, scope, reference.Key, header, true, managed.MaxRecoveryReceipt); err != nil {
		t.Fatal(err)
	}

	if err = stageRecoveryProtocolEvent(t.Context(), fixture, view, "recovery_revalidation", reference, record.Acceptance); err != nil {
		t.Fatal(err)
	}

	accepted, err := recoveryProofAcceptedHeld(t.Context(), fixture, view, reference)
	if err != nil || accepted.Digest != record.Digest {
		t.Fatal("complete historical custody refused", err)
	}

	for _, mode := range []string{"missing-header", "changed-header", "missing-event", "changed-event"} {
		t.Run(mode, func(t *testing.T) {
			broken := cloneActivationFixture(fixture)

			switch mode {
			case "missing-header":
				delete(broken.rows, [3]string{recoveryRequestKind, scope, reference.Key})
			case "changed-header":
				changed := header

				changed.Command.Owner = "replacement-owner"
				if err = attemptWrite(t.Context(), broken, recoveryRequestKind, scope, reference.Key, changed, false, managed.MaxRecoveryReceipt); err != nil {
					t.Fatal(err)
				}
			case "missing-event":
				delete(broken.rows, [3]string{"removal_attempt_event", scope, record.Acceptance.OperationID.String()})
			case "changed-event":
				event, readErr := attemptEventRead(t.Context(), broken, view, record.Acceptance.OperationID)
				if readErr != nil {
					t.Fatal(readErr)
				}

				event.Kind = "unrelated-operation"
				if err = attemptWrite(t.Context(), broken, "removal_attempt_event", scope, record.Acceptance.OperationID.String(), event, false, managed.MaxAttemptFact); err != nil {
					t.Fatal(err)
				}
			}

			if _, err = recoveryProofAcceptedHeld(t.Context(), broken, view, reference); err == nil {
				t.Fatal("historical custody loss accepted")
			}
			// The full exact reader must refuse before it accesses binding authority.
			// This is not a positive full-binding or public invocation fixture.
			if _, err = recoveryProofExactHeld(t.Context(), broken, nil, view, reference); err == nil {
				t.Fatal("exact reader skipped historical custody")
			}
		})
	}
}
