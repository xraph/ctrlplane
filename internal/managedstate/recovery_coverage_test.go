package managedstate

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

// These semantic fixtures exercise fixed selections and staged retained rows. They
// do not substitute for the original binding loader or a full public transaction.
func TestRecoveryCoverageFixedSetsAndUncertainty(t *testing.T) {
	for _, size := range [][2]int{{1, 1}, {64, 32}} {
		for _, mode := range []string{"unissued", "unresolved", "uncertain", "positive", "closed", "partial"} {
			t.Run(strconv.Itoa(size[0])+"/"+mode, func(t *testing.T) {
				fixture, target, view, acceptance := recoveryCutoffFixture(t, size[0], size[1], mode != "unissued")

				steps, err := attemptSteps(target, view)
				if err != nil {
					t.Fatal(err)
				}

				now := acceptance.Receipt.AcceptedAt.Add(-time.Millisecond)

				if mode != "unissued" && mode != "unresolved" {
					for index, step := range steps {
						selected, readErr := invocationSelectedHeld(t.Context(), fixture, view, step)
						if readErr != nil {
							t.Fatal(readErr)
						}

						var staged *recoveryActivationRows

						if mode == "closed" || mode == "partial" && index != 0 {
							facts := invocationFailureFixture(t, selected, now)
							accepted := recoveryTestRecord(t, managed.RecoveryFailureRecord, facts, now).Acceptance
							staged, _, readErr = stageInvocationClosure(t.Context(), fixture, selected, facts, accepted, now)
						} else {
							outcome := managed.PortTerminal
							if mode == "uncertain" && index == 0 {
								outcome = managed.PortUnavailable
							}

							facts := invocationResultFixture(t, selected, now, outcome)
							accepted := recoveryTestRecord(t, managed.RecoveryResultRecord, facts, now).Acceptance
							staged, _, readErr = stageInvocationResult(t.Context(), fixture, selected, facts, accepted, now)
						}

						if readErr != nil {
							t.Fatal(readErr)
						}

						updated, readErr := invocationSelectedHeld(t.Context(), staged, view, step)
						if readErr != nil {
							t.Fatal(readErr)
						}

						if readErr = stageInvocationStatus(t.Context(), staged, updated, false); readErr != nil {
							t.Fatal(readErr)
						}

						acceptInvocationStage(t, fixture, staged)
					}
				}

				cutoffRows, checked, _, err := stageRecoveryCutoff(t.Context(), fixture, target, view, acceptance)
				if err != nil {
					t.Fatal(err)
				}

				covered, err := recoveryCoverageStepsHeld(t.Context(), cutoffRows, target, checked, acceptance.Receipt.AcceptedAt)
				if mode == "unresolved" || mode == "uncertain" {
					if !errors.Is(err, managed.ErrBlocked) {
						t.Fatal("unknown effect became covered", err)
					}

					return
				}

				if err != nil || len(covered) != len(steps) {
					t.Fatal("fixed coverage", len(covered), err)
				}

				disposition, err := recoveryCoverageDisposition(covered)
				if mode == "partial" {
					if !errors.Is(err, managed.ErrBlocked) {
						t.Fatal("mixed effects became complete", err)
					}

					return
				}

				if err != nil {
					t.Fatal(err)
				}

				want := managed.SettledWithoutDeletion
				if mode == "positive" {
					want = managed.Removed
				}

				if disposition != want {
					t.Fatal(disposition, want)
				}

				for _, step := range covered {
					if mode == "unissued" && (step.Disposition != managed.EffectiveStepUnissued || step.PermissionDigest != "" || step.TerminalDigest != "") {
						t.Fatal("unissued invented a terminal invocation")
					}
				}
				// Unrelated history rows do not change exact lookup work.
				fixture.reads = 0

				if _, err = recoveryCoverageStepsHeld(t.Context(), cutoffRows, target, checked, acceptance.Receipt.AcceptedAt); err != nil {
					t.Fatal(err)
				}

				reads := fixture.reads
				for i := range 1000 {
					fixture.rows[[3]string{"unrelated_history", checked.scope, strconv.Itoa(i)}] = []byte(`{}`)
				}

				fixture.reads = 0
				if _, err = recoveryCoverageStepsHeld(t.Context(), cutoffRows, target, checked, acceptance.Receipt.AcceptedAt); err != nil || fixture.reads != reads {
					t.Fatal("history length changed lookup bound", reads, fixture.reads, err)
				}
			})
		}
	}
}

func TestRecoveryCoverageCannotAdoptIssuedSlotAsUnissued(t *testing.T) {
	fixture, target, view, acceptance := recoveryCutoffFixture(t, 1, 1, true)

	rows, checked, _, err := stageRecoveryCutoff(t.Context(), fixture, target, view, acceptance)
	if err != nil {
		t.Fatal(err)
	}

	steps, err := attemptSteps(target, view)
	if err != nil {
		t.Fatal(err)
	}

	stepKey, err := retryStepKey(attemptKey(view), steps[0])
	if err != nil {
		t.Fatal(err)
	}

	fixed := recoveryFixedSelection{SchemaVersion: "1", AttemptRef: view.ref, Step: steps[0], Disposition: "unissued", Activation: *view.recoveryActivation}
	if err = attemptWrite(t.Context(), fixture, recoveryFixedSelectionKind, view.scope, stepKey, fixed, false, managed.MaxStepRetryPointer); err != nil {
		t.Fatal(err)
	}

	if _, err = recoveryCoverageStepsHeld(t.Context(), rows, target, checked, acceptance.Receipt.AcceptedAt); err == nil {
		t.Fatal("accepted original became unissued")
	}
}

func coveragePlanFixture(t *testing.T, count, steps int) (*recoveryLeafFixture, attemptView, recoveryCoveragePlan, managed.RecoveryNativeIntentRequest, managed.RecoveryAcceptance) {
	t.Helper()

	fixture, key, now := recoveryFixture()
	ref := managed.AttemptRef{AttemptID: key.AttemptID, Generation: key.Generation, DescriptorDigest: strings.Repeat("a", 64)}
	cutoff := recoveryTestRecord(t, managed.RecoveryCutoffRecord, managed.RemovalCutoffFacts{Key: key, AttemptRef: ref}, now)
	cutoffRef := recoveryTestStore(t, fixture, key, cutoff)
	proof := recoveryTestProof(t, key, now)
	proofRef := recoveryTestStore(t, fixture, key, proof)

	plan := recoveryCoveragePlan{facts: managed.EffectiveRemovalCoverage{Key: key, AttemptRef: ref, ProviderOperationID: id.New(managed.PrefixOperation), ResourcesDigest: strings.Repeat("b", 64), AdmissionDigest: strings.Repeat("c", 64), Cutoff: cutoffRef, Disposition: managed.SettledWithoutDeletion, BindingCount: managed.Decimal(strconv.Itoa(count))}}
	for index := range steps {
		plan.facts.Steps = append(plan.facts.Steps, managed.EffectiveProviderStep{Disposition: managed.EffectiveStepUnissued, StepDigest: managed.Digest([]byte(strconv.Itoa(index))), SelectionDigest: strings.Repeat("d", 64)})
	}

	for range count {
		binding := managed.RemovalRecoveryBinding{Key: managed.AttemptBindingKey{AttemptKey: key, ReservationID: id.New(managed.PrefixReservation)}, DescriptorDigest: ref.DescriptorDigest, ProviderOperationID: plan.facts.ProviderOperationID, OriginalProcessDigest: strings.Repeat("e", 64)}
		plan.bindings = append(plan.bindings, managed.EffectiveRemovalBinding{Binding: binding, Proof: proofRef})
	}

	intent := managed.RecoveryNativeIntent{Binding: plan.bindings[0].Binding, Profile: managed.RemovalRecoveryProfile, Operation: "close", RequestID: "close-native-request", RequestDigest: strings.Repeat("f", 64), Command: []byte("exact opaque native command"), CommandDigest: managed.Digest([]byte("exact opaque native command")), Cutoff: &cutoffRef, Proof: &proofRef}
	accepted := recoveryTestRecord(t, managed.RecoveryIntentRecord, intent, now.Add(time.Second)).Acceptance

	scope, err := attemptScope(key.Key)
	if err != nil {
		t.Fatal(err)
	}

	view := attemptView{validated: true, scope: scope, ref: ref, descriptor: managed.AttemptDescriptor{Key: key.Key, AttemptID: key.AttemptID, Generation: key.Generation}}
	request := managed.RecoveryNativeIntentRequest{Command: managed.RecoveryCommand{Key: key.Key, ID: accepted.OperationID, Revision: "1", LeaseEpoch: "1", Owner: "owner", AttemptRef: ref, Profile: managed.RemovalRecoveryProfile}, Intent: intent}

	fingerprint, err := recoveryCoverageAcceptanceDigest(request, plan)
	if err != nil {
		t.Fatal(err)
	}

	accepted.RequestDigest, accepted.Receipt.Digest = fingerprint, fingerprint

	return fixture, view, plan, request, accepted
}

func TestCoverageRecordsKeepFiniteBindingCommitmentAndEncodedCaps(t *testing.T) {
	for _, size := range [][2]int{{1, 2}, {64, 33}} {
		_, _, plan, _, acceptance := coveragePlanFixture(t, size[0], size[1])

		bindings, coverage, err := recoveryCoverageRecords(plan, acceptance)
		if err != nil || len(bindings) != size[0] || len(coverage.Facts.Steps) != size[1] {
			t.Fatal(err)
		}

		if _, err = managed.EncodeRecoveryRecord(coverage); err != nil {
			t.Fatal(err)
		}

		changed := plan
		changed.bindings = append([]managed.EffectiveRemovalBinding(nil), plan.bindings...)
		changed.bindings[0].Proof.Digest = strings.Repeat("1", 64)

		_, altered, err := recoveryCoverageRecords(changed, acceptance)
		if err != nil || altered.Facts.BindingSetDigest == coverage.Facts.BindingSetDigest || altered.Digest == coverage.Digest {
			t.Fatal("binding substitution missed by coverage", err)
		}
	}

	_, _, plan, _, acceptance := coveragePlanFixture(t, 64, 33) //nolint:dogsled // Encoded size checks need only the plan and acceptance.
	plan.bindings = append(plan.bindings, plan.bindings[0])

	plan.facts.BindingCount = "65"
	if _, _, err := recoveryCoverageRecords(plan, acceptance); !errors.Is(err, managed.ErrCapacity) {
		t.Fatal("binding cap+1", err)
	}

	plan.bindings = plan.bindings[:64]
	plan.facts.BindingCount = "64"

	plan.facts.Steps = append(plan.facts.Steps, plan.facts.Steps[0])
	if _, _, err := recoveryCoverageRecords(plan, acceptance); err == nil {
		t.Fatal("step cap+1")
	}
}

func TestCoverageSponsorRequiresIntentEventAndIndependentReceipt(t *testing.T) {
	fixture, view, plan, intent, acceptance := coveragePlanFixture(t, 1, 2)

	staged, coverage, err := stageRecoveryCoveragePlan(t.Context(), fixture, view, plan, intent, acceptance)
	if err != nil {
		t.Fatal(err)
	}

	selection, err := attemptRead[recoveryCoverageSelection](t.Context(), staged, recoveryCoverageSelectionKind, view.scope, view.ref.AttemptID.String(), managed.MaxRecoveryReceipt)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = recoveryCoverageSponsorHeld(t.Context(), staged, view, selection, coverage); err != nil {
		t.Fatal(err)
	}

	acceptInvocationStage(t, fixture, staged)

	for _, key := range [][3]string{
		{string(managed.RecoveryIntentRecord), view.scope, selection.Sponsor.Key},
		{recoveryAcceptanceKind, view.scope, selection.Sponsor.Key},
		{"removal_attempt_event", view.scope, acceptance.OperationID.String()},
	} {
		broken := cloneActivationFixture(fixture)
		delete(broken.rows, key)

		if _, err = recoveryCoverageSponsorHeld(t.Context(), broken, view, selection, coverage); err == nil {
			t.Fatal("missing sponsor dependency accepted", key)
		}
	}

	broken := cloneActivationFixture(fixture)

	receipt, err := acceptance.Receipt.Value()
	if err != nil {
		t.Fatal(err)
	}

	delete(broken.receipts, attemptReceiptKey{plan.facts.Key.Key, receipt.ID, receipt.Digest})

	if _, err = recoveryCoverageSponsorHeld(t.Context(), broken, view, selection, coverage); err == nil {
		t.Fatal("isolated coverage without receipt accepted")
	}

	forged := coverage
	forged.Facts.ResourcesDigest = strings.Repeat("9", 64)

	forged.Digest, err = managed.RecoveryFactDigest(forged.Facts)
	if err != nil {
		t.Fatal(err)
	}

	forgedSelection := selection

	forgedSelection.Coverage, err = managed.RecoveryAssociationFor(selection.Coverage.Key, forged)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = recoveryCoverageSponsorHeld(t.Context(), fixture, view, forgedSelection, forged); err == nil {
		t.Fatal("rehashed coverage escaped saved sponsor")
	}
}

func TestCoverageCloseIntentHeadroomConsumesOnlyItsReservedSlot(t *testing.T) {
	reserve := recoveryReserveAfter{existing: 489, markerObligations: 33, closureObligations: 33, closeIntentsMissing: 64, closeResultsMissing: 64}

	total, err := recoveryRemainingReserve(reserve)
	if err != nil {
		t.Fatal(err)
	}

	last := uint64(math.MaxInt64) - total
	if err = recoveryCoverageCloseIntentHeadroom(last, last, reserve); err != nil {
		t.Fatal("reserved intent charged twice", err)
	}

	for _, counts := range [][2]uint64{{last + 1, last}, {last, last + 1}} {
		if err = recoveryCoverageCloseIntentHeadroom(counts[0], counts[1], reserve); !errors.Is(err, managed.ErrCapacity) {
			t.Fatal("counter reserve bypass", err)
		}
	}

	reserve.closeIntentsMissing = 0
	if err = recoveryCoverageCloseIntentHeadroom(0, 0, reserve); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("unreserved intent", err)
	}

	if err = New(&recoveryBackendWitness{}).requireConfiguredRecovery(); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal("coverage enabled incomplete lifecycle", err)
	}
}

func TestCoveragePlanRollbackAtEveryRowAndReceipt(t *testing.T) {
	fixture, view, plan, intent, acceptance := coveragePlanFixture(t, 64, 33)

	staged, _, err := stageRecoveryCoveragePlan(t.Context(), fixture, view, plan, intent, acceptance)
	if err != nil {
		t.Fatal(err)
	}

	if len(staged.rows) != 2*64+6 {
		t.Fatal("unexpected finite row count", len(staged.rows))
	}

	before := cloneActivationFixture(fixture)
	target := &managed.Target{Identity: managed.Identity{Key: plan.facts.Key.Key}, Revision: 1, LeaseUntil: acceptance.Receipt.LeaseUntil}
	backend := &activationTransactionFixture{state: fixture, target: target}
	apply := func() error {
		return New(backend).controller(t.Context(), "coverage-model", []managed.Key{target.Key}, func(tx ControllerTransaction) error {
			_, atomicErr := tx.Atomic(t.Context(), target.Key, staged.receipt.ID, staged.receipt.Digest, func(current *managed.Target, _ time.Time) (managed.Receipt, error) {
				if writeErr := applyRecoveryActivationRows(t.Context(), tx, staged); writeErr != nil {
					return managed.Receipt{}, writeErr
				}

				current.Revision = staged.receipt.Revision

				return staged.receipt, nil
			})

			return atomicErr
		})
	}

	for fail := 1; fail <= len(staged.rows)+1; fail++ {
		backend.failAt, backend.failReceipt = fail, fail == len(staged.rows)+1

		if err = apply(); !errors.Is(err, managed.ErrUnavailable) {
			t.Fatal("failure ignored", err)
		}

		if !reflect.DeepEqual(fixture.rows, before.rows) || !reflect.DeepEqual(fixture.receipts, before.receipts) || target.Revision != 1 {
			t.Fatal("partial coverage escaped rollback")
		}
	}

	backend.failAt, backend.failReceipt = 0, false

	if err = apply(); err != nil {
		t.Fatal(err)
	}
}

func TestCoverageActualEncodedRecordCapAndCapPlusOne(t *testing.T) {
	_, _, plan, _, acceptance := coveragePlanFixture(t, 1, 2) //nolint:dogsled // Encoded size checks need only the plan and acceptance.

	bindings, coverage, err := recoveryCoverageRecords(plan, acceptance)
	if err != nil {
		t.Fatal(err)
	}

	binding := bindings[0]
	binding.Facts.Binding.OriginalProcessDigest = ""

	binding.Digest, err = managed.RecoveryFactDigest(binding.Facts)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := managed.EncodeRecoveryRecord(binding)
	if err != nil {
		t.Fatal(err)
	}

	binding.Facts.Binding.OriginalProcessDigest = strings.Repeat("p", managed.MaxRecoveryFact-len(raw))

	binding.Digest, err = managed.RecoveryFactDigest(binding.Facts)
	if err != nil {
		t.Fatal(err)
	}

	raw, err = managed.EncodeRecoveryRecord(binding)
	if err != nil || len(raw) != managed.MaxRecoveryFact {
		t.Fatal("binding actual encoded cap", len(raw), err)
	}

	binding.Facts.Binding.OriginalProcessDigest += "p"

	binding.Digest, err = managed.RecoveryFactDigest(binding.Facts)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = managed.EncodeRecoveryRecord(binding); !errors.Is(err, managed.ErrCapacity) {
		t.Fatal("binding encoded cap+1", err)
	}

	coverage.Facts.ResourcesDigest = ""

	coverage.Digest, err = managed.RecoveryFactDigest(coverage.Facts)
	if err != nil {
		t.Fatal(err)
	}

	raw, err = managed.EncodeRecoveryRecord(coverage)
	if err != nil {
		t.Fatal(err)
	}

	coverage.Facts.ResourcesDigest = strings.Repeat("p", managed.MaxRemovalRevalidation-len(raw))

	coverage.Digest, err = managed.RecoveryFactDigest(coverage.Facts)
	if err != nil {
		t.Fatal(err)
	}

	raw, err = managed.EncodeRecoveryRecord(coverage)
	if err != nil || len(raw) != managed.MaxRemovalRevalidation {
		t.Fatal("coverage actual encoded cap", len(raw), err)
	}

	coverage.Facts.ResourcesDigest += "p"

	coverage.Digest, err = managed.RecoveryFactDigest(coverage.Facts)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = managed.EncodeRecoveryRecord(coverage); !errors.Is(err, managed.ErrCapacity) {
		t.Fatal("coverage encoded cap+1", err)
	}
}

func TestCoverageRehashedSponsorStillRequiresOriginalLifecycleEvent(t *testing.T) {
	fixture, view, plan, intent, acceptance := coveragePlanFixture(t, 1, 2)

	staged, coverage, err := stageRecoveryCoveragePlan(t.Context(), fixture, view, plan, intent, acceptance)
	if err != nil {
		t.Fatal(err)
	}

	acceptInvocationStage(t, fixture, staged)

	selection, err := attemptRead[recoveryCoverageSelection](t.Context(), fixture, recoveryCoverageSelectionKind, view.scope, view.ref.AttemptID.String(), managed.MaxRecoveryReceipt)
	if err != nil {
		t.Fatal(err)
	}

	sponsor, err := recoveryReadFact[managed.RecoveryNativeIntent](t.Context(), fixture, attemptKey(view), selection.Sponsor)
	if err != nil {
		t.Fatal(err)
	}

	coverage.Facts.ResourcesDigest = strings.Repeat("9", 64)

	coverage.Digest, err = managed.RecoveryFactDigest(coverage.Facts)
	if err != nil {
		t.Fatal(err)
	}

	selection.Coverage, err = managed.RecoveryAssociationFor(selection.Coverage.Key, coverage)
	if err != nil {
		t.Fatal(err)
	}

	sponsor.Facts.Coverage = &selection.Coverage

	sponsor.Digest, err = managed.RecoveryFactDigest(sponsor.Facts)
	if err != nil {
		t.Fatal(err)
	}

	delete(fixture.rows, [3]string{string(managed.RecoveryIntentRecord), view.scope, selection.Sponsor.Key})
	delete(fixture.rows, [3]string{recoveryAcceptanceKind, view.scope, selection.Sponsor.Key})
	selection.Sponsor = recoveryTestStore(t, fixture, attemptKey(view), sponsor)
	// Canonical rewritten rows and their self-digests leave the separately retained
	// lifecycle event unchanged. That event must refuse the replacement.
	if _, err = recoveryCoverageSponsorHeld(t.Context(), fixture, view, selection, coverage); err == nil {
		t.Fatal("rewritten sponsor bypassed lifecycle event")
	}

	encoded, err := json.Marshal(selection)
	if err != nil || len(encoded) > managed.MaxRecoveryReceipt {
		t.Fatal("selector size", err)
	}
}

func TestCoverageIndependentReceiptRejectsCoordinatedCanonicalRewrite(t *testing.T) {
	for _, mutation := range []string{"request", "coverage", "binding", "command"} {
		t.Run(mutation, func(t *testing.T) {
			fixture, view, plan, request, acceptance := coveragePlanFixture(t, 1, 2)

			staged, coverage, err := stageRecoveryCoveragePlan(t.Context(), fixture, view, plan, request, acceptance)
			if err != nil {
				t.Fatal(err)
			}

			acceptInvocationStage(t, fixture, staged)

			selection, err := attemptRead[recoveryCoverageSelection](t.Context(), fixture, recoveryCoverageSelectionKind, view.scope, view.ref.AttemptID.String(), managed.MaxRecoveryReceipt)
			if err != nil {
				t.Fatal(err)
			}

			sponsor, err := recoveryCoverageSponsorHeld(t.Context(), fixture, view, selection, coverage)
			if err != nil {
				t.Fatal(err)
			}

			if err = recoveryCoverageAcceptanceMatches(selection, sponsor, coverage, plan.bindings); err != nil {
				t.Fatal(err)
			}

			switch mutation {
			case "request":
				sponsor.Facts.Command = []byte("changed native command")
				sponsor.Facts.CommandDigest = managed.Digest(sponsor.Facts.Command)
			case "coverage":
				plan.facts.ResourcesDigest = strings.Repeat("9", 64)
			case "binding":
				plan.bindings[0].Proof.Digest = strings.Repeat("9", 64)
			case "command":
				selection.Command.Owner = "replacement-owner"
			}

			_, coverage, err = recoveryCoverageRecords(plan, acceptance)
			if err != nil {
				t.Fatal(err)
			}

			selection.Coverage, err = managed.RecoveryAssociationFor(selection.Coverage.Key, coverage)
			if err != nil {
				t.Fatal(err)
			}

			sponsor.Facts.Coverage = &selection.Coverage

			sponsor.Digest, err = managed.RecoveryFactDigest(sponsor.Facts)
			if err != nil {
				t.Fatal(err)
			}

			delete(fixture.rows, [3]string{string(managed.RecoveryIntentRecord), view.scope, selection.Sponsor.Key})
			delete(fixture.rows, [3]string{recoveryAcceptanceKind, view.scope, selection.Sponsor.Key})
			selection.Sponsor = recoveryTestStore(t, fixture, attemptKey(view), sponsor)
			delete(fixture.rows, [3]string{"removal_attempt_event", view.scope, acceptance.OperationID.String()})

			if err = stageRecoveryProtocolEvent(t.Context(), fixture, view, "recovery_close_intent", selection.Sponsor, acceptance); err != nil {
				t.Fatal(err)
			}
			// Even a rewritten event can be internally canonical. The unchanged
			// independently retained receipt must still reject the replacement.
			if _, err = recoveryCoverageSponsorHeld(t.Context(), fixture, view, selection, coverage); err != nil {
				t.Fatal("test did not reach receipt binding", err)
			}

			if err = recoveryCoverageAcceptanceMatches(selection, sponsor, coverage, plan.bindings); err == nil {
				t.Fatal("canonical rewritten authority escaped independent receipt")
			}
		})
	}
}
