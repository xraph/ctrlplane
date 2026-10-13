package managedstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

func invocationFixture(t *testing.T) (*recoveryLeafFixture, retrySelected, time.Time) {
	t.Helper()
	fixture, key, root, _, now := retryOriginalFixture(t)

	selected, err := retryReadSelected(t.Context(), fixture, key, root)
	if err != nil {
		t.Fatal(err)
	}

	selected.permission.Acceptance = selected.root.Acceptance
	permissionRef := recoveryTestStore(t, fixture, key, selected.permission)
	selected.current.Permission = permissionRef
	retryWriteTestCurrent(t, fixture, key, selected.root.Facts.Step, &selected.current, false)

	scope, err := attemptScope(key.Key)
	if err != nil {
		t.Fatal(err)
	}

	stepKey, err := retryStepKey(key, selected.root.Facts.Step)
	if err != nil {
		t.Fatal(err)
	}

	fixed := recoveryFixedSelection{SchemaVersion: "1", AttemptRef: selected.root.Facts.AttemptRef, Step: selected.root.Facts.Step, Disposition: "original", Root: &root}
	if err = attemptWrite(t.Context(), fixture, recoveryFixedSelectionKind, scope, stepKey, fixed, true, managed.MaxStepRetryPointer); err != nil {
		t.Fatal(err)
	}

	return fixture, selected, now
}

func invocationFailureFixture(t *testing.T, selected retrySelected, now time.Time) managed.ProviderInvocationFailure {
	t.Helper()

	permission, err := selected.permission.Facts.Permission.Value()
	if err != nil {
		t.Fatal(err)
	}

	permissionDigest, err := recoveryPermissionDigest(selected.permission.Facts.Permission)
	if err != nil {
		t.Fatal(err)
	}

	failure := managed.ProviderStepFailure{SchemaVersion: "1", Permission: permission, AttemptRef: selected.root.Facts.AttemptRef, InvocationID: permission.CommandID, ProducerProfile: managed.LocalNoDispatchProfile, VerifierID: "verifier", Class: "local_no_dispatch", Phase: "before_transport_delegate", Reason: "invocation_closed", ObservedAt: now, Witness: []byte("affirmative"), WitnessDigest: managed.Digest([]byte("affirmative"))}

	return managed.ProviderInvocationFailure{Key: selected.permission.Facts.Key, RootDigest: selected.root.Digest, PermissionDigest: permissionDigest, Failure: failure}
}

func invocationResultFixture(t *testing.T, selected retrySelected, now time.Time, outcome managed.PortOutcome) managed.ProviderInvocationResult {
	t.Helper()

	digest, err := recoveryPermissionDigest(selected.permission.Facts.Permission)
	if err != nil {
		t.Fatal(err)
	}

	result := managed.WireProviderStepResult{Permission: selected.permission.Facts.Permission, Outcome: outcome, Evidence: []byte("provider-observation"), ObservedAt: now}
	if outcome == managed.PortTerminal {
		result.ResourceID = result.Permission.ResourceID
	}

	return managed.ProviderInvocationResult{Key: selected.permission.Facts.Key, RootDigest: selected.root.Digest, PermissionDigest: digest, Result: result}
}

func acceptInvocationStage(t *testing.T, fixture *recoveryLeafFixture, staged *recoveryActivationRows) {
	t.Helper()

	if err := applyRecoveryActivationRows(t.Context(), fixture, staged); err != nil {
		t.Fatal(err)
	}

	fixture.receipts[attemptReceiptKey{staged.key, staged.receipt.ID, staged.receipt.Digest}] = staged.receipt
}

func TestInvocationClosureStagesBothFactsAndRequiresAffirmativeWitness(t *testing.T) {
	fixture, selected, now := invocationFixture(t)
	facts := invocationFailureFixture(t, selected, now)
	acceptance := recoveryTestRecord(t, managed.RecoveryFailureRecord, facts, now.Add(time.Second)).Acceptance
	before := cloneActivationFixture(fixture)

	staged, closure, err := stageInvocationClosure(t.Context(), fixture, selected, facts, acceptance, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}

	if len(staged.rows) != 5 || !reflect.DeepEqual(before.rows, fixture.rows) {
		t.Fatal("closure persisted during staging")
	}

	if _, err = retryReadSelected(t.Context(), staged, facts.Key.AttemptKey, selected.current.Authority); err != nil {
		t.Fatal(err)
	}

	if closure.Facts.FailureDigest == "" || closure.Acceptance != acceptance {
		t.Fatal("closure omitted shared acceptance")
	}

	for _, mutation := range []func(*managed.ProviderInvocationFailure){
		func(value *managed.ProviderInvocationFailure) { value.Failure.Witness = nil },
		func(value *managed.ProviderInvocationFailure) { value.Failure.WitnessDigest = strings.Repeat("e", 64) },
		func(value *managed.ProviderInvocationFailure) {
			value.Failure.AttemptRef.DescriptorDigest = strings.Repeat("e", 64)
		},
		func(value *managed.ProviderInvocationFailure) { value.Failure.Phase = "after_transport_delegate" },
		func(value *managed.ProviderInvocationFailure) { value.Failure.ObservedAt = now.Add(time.Hour) },
	} {
		bad, cloneErr := recoverySnapshot(facts)
		if cloneErr != nil {
			t.Fatal(cloneErr)
		}

		mutation(&bad)

		if _, _, err = stageInvocationClosure(t.Context(), fixture, selected, bad, acceptance, now.Add(time.Second)); err == nil {
			t.Fatal("invalid closure accepted")
		}
	}

	request := invocationTransitionRequest{Kind: "closure", InvocationID: facts.Key.CommandID, Failure: &facts}
	if _, err = stageInvocationTransition(t.Context(), fixture, nil, attemptView{}, selected, request, acceptance, now.Add(time.Second)); !errors.Is(err, managed.ErrAuthority) {
		t.Fatal("raw caller facts bypassed configured verification", err)
	}

	if !reflect.DeepEqual(before.rows, fixture.rows) {
		t.Fatal("negative closure staging changed persistence")
	}
}

func TestInvocationClosurePreservesMarkedPossibleEffectAndReceiptOrder(t *testing.T) {
	fixture, selected, now := invocationFixture(t)
	facts := invocationFailureFixture(t, selected, now)

	selectionDigest, err := retrySelectionDigest(selected.current)
	if err != nil {
		t.Fatal(err)
	}

	marker := recoveryTestRecord(t, managed.RecoveryMarkerRecord, managed.ProviderInvocationMarker{Key: facts.Key, RootDigest: facts.RootDigest, PermissionDigest: facts.PermissionDigest, SelectionDigest: selectionDigest, NativeCommitSetDigest: strings.Repeat("b", 64), CurrentProofSetDigest: strings.Repeat("c", 64), BindingCount: "1", AcceptedAt: now}, now)
	markerRef := recoveryTestStore(t, fixture, facts.Key.AttemptKey, marker)
	selected.marker = &marker
	selected.current.MarkerKey = markerRef.Key
	retryWriteTestCurrent(t, fixture, facts.Key.AttemptKey, facts.Key.Step, &selected.current, false)

	acceptance := recoveryTestRecord(t, managed.RecoveryFailureRecord, facts, now.Add(time.Second)).Acceptance
	if _, _, err = stageInvocationClosure(t.Context(), fixture, selected, facts, acceptance, now.Add(time.Second)); err == nil {
		t.Fatal("same revision closure accepted after marker")
	}

	acceptance.Receipt.Revision = "3"

	staged, _, err := stageInvocationClosure(t.Context(), fixture, selected, facts, acceptance, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}

	after, err := retryReadSelected(t.Context(), staged, facts.Key.AttemptKey, selected.current.Authority)
	if err != nil {
		t.Fatal(err)
	}

	if after.marker == nil || after.marker.Digest != marker.Digest || after.closure == nil || after.failure == nil {
		t.Fatal("closure erased accepted marker history")
	}

	selected.result = &managed.RecoveryRecord[managed.ProviderInvocationResult]{}
	if _, _, err = stageInvocationClosure(t.Context(), fixture, selected, facts, acceptance, now.Add(time.Second)); !errors.Is(err, managed.ErrBlocked) {
		t.Fatal("observed invocation closed", err)
	}
}

func TestInvocationResultRetainsUncertaintyAndPositiveSeparately(t *testing.T) {
	fixture, selected, now := invocationFixture(t)
	uncertain := invocationResultFixture(t, selected, now.Add(time.Second), managed.PortUnavailable)
	acceptance := recoveryTestRecord(t, managed.RecoveryResultRecord, uncertain, now.Add(time.Second)).Acceptance

	staged, uncertainty, err := stageInvocationResult(t.Context(), fixture, selected, uncertain, acceptance, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}

	acceptInvocationStage(t, fixture, staged)

	selected, err = retryReadSelected(t.Context(), fixture, uncertain.Key.AttemptKey, selected.current.Authority)
	if err != nil {
		t.Fatal(err)
	}

	originalKey := selected.current.ResultKey
	originalRaw := bytes.Clone(fixture.rows[[3]string{string(managed.RecoveryResultRecord), staged.rows[0].scope, originalKey}])
	positive := invocationResultFixture(t, selected, now.Add(2*time.Second), managed.PortTerminal)
	acceptance = recoveryTestRecord(t, managed.RecoveryResultRecord, positive, now.Add(2*time.Second)).Acceptance

	staged, _, err = stageInvocationResult(t.Context(), fixture, selected, positive, acceptance, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}

	acceptInvocationStage(t, fixture, staged)

	after, err := retryReadSelected(t.Context(), fixture, positive.Key.AttemptKey, selected.current.Authority)
	if err != nil {
		t.Fatal(err)
	}

	old, err := recoveryReadNamedFact[managed.ProviderInvocationResult](t.Context(), fixture, uncertain.Key.AttemptKey, managed.RecoveryResultRecord, originalKey)
	if err != nil || old.Digest != uncertainty.Digest {
		t.Fatal("lost exact uncertainty", err)
	}

	if !bytes.Equal(originalRaw, fixture.rows[[3]string{string(managed.RecoveryResultRecord), staged.rows[0].scope, originalKey}]) {
		t.Fatal("rewrote old observation bytes")
	}

	if after.result == nil || after.result.Facts.Result.Outcome != managed.PortTerminal {
		t.Fatal("positive did not become selected")
	}

	if _, _, err = stageInvocationResult(t.Context(), fixture, after, uncertain, acceptance, now.Add(3*time.Second)); !errors.Is(err, managed.ErrConflict) {
		t.Fatal("positive was replaced by uncertainty", err)
	}

	if _, _, err = retryPredecessor(after, after.current.Digest, id.New(managed.PrefixOperation)); err == nil {
		t.Fatal("result became retry authorization")
	}
}

func TestInvocationResultNegativeAssociationsAndRetryMarker(t *testing.T) {
	fixture, selected, now := invocationFixture(t)
	facts := invocationResultFixture(t, selected, now, managed.PortTerminal)

	acceptance := recoveryTestRecord(t, managed.RecoveryResultRecord, facts, now).Acceptance
	for _, mutation := range []func(*managed.ProviderInvocationResult){
		func(value *managed.ProviderInvocationResult) { value.Key.CommandID = id.New(managed.PrefixOperation) },
		func(value *managed.ProviderInvocationResult) { value.PermissionDigest = strings.Repeat("f", 64) },
		func(value *managed.ProviderInvocationResult) {
			value.Result.Permission.CommandID = id.New(managed.PrefixOperation)
		},
		func(value *managed.ProviderInvocationResult) { value.Result.ResourceID = "different-resource" },
		func(value *managed.ProviderInvocationResult) { value.Result.Evidence = nil },
		func(value *managed.ProviderInvocationResult) { value.Result.ObservedAt = now.Add(time.Hour) },
	} {
		bad, err := recoverySnapshot(facts)
		if err != nil {
			t.Fatal(err)
		}

		mutation(&bad)

		if _, _, err = stageInvocationResult(t.Context(), fixture, selected, bad, acceptance, now); err == nil {
			t.Fatal("invalid result accepted")
		}
	}

	selected.current.Ordinal = "1"
	if _, _, err := stageInvocationResult(t.Context(), fixture, selected, facts, acceptance, now); !errors.Is(err, managed.ErrAuthority) {
		t.Fatal("retry result lacked accepted marker", err)
	}
}

func TestInvocationStagedRollbackAtEveryWriteAndReceipt(t *testing.T) {
	for _, kind := range []string{"closure", "result"} {
		fixture, selected, now := invocationFixture(t)

		var (
			staged *recoveryActivationRows
			err    error
		)

		if kind == "closure" {
			facts := invocationFailureFixture(t, selected, now)
			acceptance := recoveryTestRecord(t, managed.RecoveryFailureRecord, facts, now).Acceptance
			staged, _, err = stageInvocationClosure(t.Context(), fixture, selected, facts, acceptance, now)
		} else {
			facts := invocationResultFixture(t, selected, now, managed.PortUnavailable)
			acceptance := recoveryTestRecord(t, managed.RecoveryResultRecord, facts, now).Acceptance
			staged, _, err = stageInvocationResult(t.Context(), fixture, selected, facts, acceptance, now)
		}

		if err != nil {
			t.Fatal(err)
		}

		before := cloneActivationFixture(fixture)
		target := &managed.Target{Identity: managed.Identity{Key: selected.permission.Facts.Key.AttemptKey.Key}, Revision: 1, LeaseUntil: now.Add(time.Minute)}
		backend := &activationTransactionFixture{state: fixture, target: target}
		apply := func() error {
			return New(backend).controller(t.Context(), "invocation-fixture", []managed.Key{target.Key}, func(tx ControllerTransaction) error {
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

		for failAt := 1; failAt <= len(staged.rows)+1; failAt++ {
			backend.failAt, backend.failReceipt = failAt, failAt == len(staged.rows)+1
			if err = apply(); !errors.Is(err, managed.ErrUnavailable) {
				t.Fatal(kind, failAt, err)
			}

			if !reflect.DeepEqual(fixture.rows, before.rows) || !reflect.DeepEqual(fixture.receipts, before.receipts) || target.Revision != 1 {
				t.Fatal("partial transition escaped rollback")
			}
		}

		backend.failAt, backend.failReceipt = 0, false

		if err = apply(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInvocationMarkerReplayNeverDelegates(t *testing.T) {
	fixture, selected, now := invocationFixture(t)
	request := invocationTransitionRequest{Kind: "marker", Command: managed.RecoveryCommand{Key: selected.permission.Facts.Key.AttemptKey.Key, ID: id.New(managed.PrefixOperation), AttemptRef: selected.root.Facts.AttemptRef}, Step: selected.root.Facts.Step, InvocationID: selected.current.CommandID, ExpectedCurrentDigest: selected.current.Digest}

	fingerprint, err := attemptHash("ctrlplane.managed.invocation-transition.v1", request)
	if err != nil {
		t.Fatal(err)
	}

	permissionDigest, err := recoveryPermissionDigest(selected.permission.Facts.Permission)
	if err != nil {
		t.Fatal(err)
	}

	selectionDigest, err := retrySelectionDigest(selected.current)
	if err != nil {
		t.Fatal(err)
	}

	marker := recoveryTestRecord(t, managed.RecoveryMarkerRecord, managed.ProviderInvocationMarker{Key: selected.permission.Facts.Key, RootDigest: selected.root.Digest, PermissionDigest: permissionDigest, SelectionDigest: selectionDigest, NativeCommitSetDigest: strings.Repeat("a", 64), CurrentProofSetDigest: strings.Repeat("b", 64), BindingCount: "1", AcceptedAt: now}, now)
	marker.Acceptance.OperationID, marker.Acceptance.RequestDigest = request.Command.ID, fingerprint
	marker.Acceptance.Receipt.ID, marker.Acceptance.Receipt.Digest = request.Command.ID, fingerprint
	recoveryTestStore(t, fixture, selected.permission.Facts.Key.AttemptKey, marker)

	out, err := invocationReplay(t.Context(), fixture, request, fingerprint)
	if err != nil || out.MayDelegate || out.Marker == nil {
		t.Fatal("receipt replay issued delegation", err)
	}

	if _, err = invocationReplay(t.Context(), fixture, request, strings.Repeat("f", 64)); !errors.Is(err, managed.ErrConflict) {
		t.Fatal("changed request replayed", err)
	}

	if _, _, err = stageInvocationMarker(t.Context(), fixture, nil, attemptView{}, selected, selected.current.Digest, nil, marker.Acceptance, now); !errors.Is(err, managed.ErrBlocked) {
		t.Fatal("original unguarded permission acquired marker", err)
	}
}

// These rollback tests cover the staging batch through the model backend. They do not
// exercise transitionInvocation's complete original-binding reader or a native store.

func invocationCommitMemberFixture(t *testing.T, fixture *recoveryLeafFixture, selected retrySelected, now time.Time) (invocationProofMember, managed.RecoveryAssociation, managed.RecoveryRecord[managed.ProviderNativeCommitFacts]) {
	t.Helper()

	key := selected.permission.Facts.Key
	proof := recoveryTestProof(t, key.AttemptKey, now)
	proof.Facts.NativeMaterialDigest = strings.Repeat("a", 64)
	proof.Facts.ProcessDisposition = "live"

	var err error

	proof.Digest, err = managed.RecoveryFactDigest(proof.Facts)
	if err != nil {
		t.Fatal(err)
	}

	proofRef := recoveryTestStore(t, fixture, key.AttemptKey, proof)

	bindingDigest, err := recoveryBindingKey(proof.Facts.Binding)
	if err != nil {
		t.Fatal(err)
	}

	permissionDigest, err := recoveryPermissionDigest(selected.permission.Facts.Permission)
	if err != nil {
		t.Fatal(err)
	}

	permissionProof, err := activationRecord(managed.RecoveryPermissionProofRecord, managed.ProviderPermissionProof{Invocation: key, BindingDigest: bindingDigest, PermissionDigest: permissionDigest, Proof: proofRef, NativeMaterialDigest: proof.Facts.NativeMaterialDigest}, selected.permission.Acceptance)
	if err != nil {
		t.Fatal(err)
	}

	permissionProofRef := recoveryTestStore(t, fixture, key.AttemptKey, permissionProof)
	intent := recoveryTestRecord(t, managed.RecoveryIntentRecord, managed.RecoveryNativeIntent{Binding: proof.Facts.Binding, Profile: managed.RemovalRecoveryProfile, Operation: "commit", RequestID: "native-request", RequestDigest: strings.Repeat("c", 64), Command: []byte("bounded-command"), CommandDigest: managed.Digest([]byte("bounded-command")), Permission: &selected.current.Permission, Proof: &proofRef}, now)
	intentRef := recoveryTestStore(t, fixture, key.AttemptKey, intent)
	commit := recoveryTestRecord(t, managed.RecoveryCommitRecord, managed.ProviderNativeCommitFacts{Binding: proof.Facts.Binding, Invocation: key, Intent: intentRef, PermissionDigest: permissionDigest, PermissionProof: permissionProofRef, CurrentProof: proofRef, PermissionProofDigest: permissionProof.Digest, CurrentProofDigest: proof.Digest, GuardID: proof.Facts.GuardID, GuardRootDigest: proof.Facts.GuardRootDigest, NativeRequestID: intent.Facts.RequestID, NativeRequestDigest: intent.Facts.RequestDigest, NativeReceiptDigest: strings.Repeat("d", 64), NativeAcceptedAt: now, Export: proof.Facts.Export}, now)
	commitRef := recoveryTestStore(t, fixture, key.AttemptKey, commit)
	member := invocationProofMember{binding: proof.Facts.Binding, bindingDigest: bindingDigest, current: managed.RemovalRevalidationCurrent{Proof: proofRef}, proof: proof}

	return member, commitRef, commit
}

// These fixtures exercise each exact member association at small and maximum cardinality.
// They do not replace the required complete held binding fixture for marker issuance.
func TestInvocationMarkerExactMembersSmallAndMaximum(t *testing.T) {
	for _, count := range []int{1, managed.MaxRecoveryBindings} {
		fixture, selected, now := invocationFixture(t)
		for index := range count {
			member, reference, commit := invocationCommitMemberFixture(t, fixture, selected, now)
			if _, err := invocationMarkerMember(t.Context(), fixture, selected, member, reference, now); err != nil {
				t.Fatal(count, index, err)
			}

			for _, mutation := range []func(*managed.ProviderNativeCommitFacts){
				func(value *managed.ProviderNativeCommitFacts) {
					value.Binding.Key.ReservationID = id.New(managed.PrefixReservation)
				},
				func(value *managed.ProviderNativeCommitFacts) {
					value.Invocation.CommandID = id.New(managed.PrefixOperation)
				},
				func(value *managed.ProviderNativeCommitFacts) { value.PermissionDigest = strings.Repeat("f", 64) },
				func(value *managed.ProviderNativeCommitFacts) { value.PermissionProofDigest = strings.Repeat("f", 64) },
				func(value *managed.ProviderNativeCommitFacts) { value.CurrentProofDigest = strings.Repeat("f", 64) },
				func(value *managed.ProviderNativeCommitFacts) { value.GuardID = "another-guard" },
				func(value *managed.ProviderNativeCommitFacts) { value.GuardRootDigest = strings.Repeat("f", 64) },
				func(value *managed.ProviderNativeCommitFacts) { value.NativeAcceptedAt = now.Add(time.Second) },
			} {
				facts, err := recoverySnapshot(commit.Facts)
				if err != nil {
					t.Fatal(err)
				}

				mutation(&facts)
				bad := recoveryTestRecord(t, managed.RecoveryCommitRecord, facts, now)

				badRef := recoveryTestStore(t, fixture, selected.permission.Facts.Key.AttemptKey, bad)
				if _, err = invocationMarkerMember(t.Context(), fixture, selected, member, badRef, now); err == nil {
					t.Fatal("changed exact commit accepted", count, index)
				}
			}
		}
	}
}

func TestInvocationInspectionRetainsOriginalUncertaintyAfterPositive(t *testing.T) {
	fixture, target, view, plan := activationMaterializationFixture(t, 1, 1, true)
	step := plan.Selections[0].Step

	original, association, err := attemptProviderRead(t.Context(), fixture, target, view, step, 0)
	if err != nil {
		t.Fatal(err)
	}

	uncertain := managed.ProviderStepResult{Permission: original.Permission, Outcome: managed.PortUnavailable, Evidence: []byte("original uncertainty"), ObservedAt: original.Permission.IssuedAt}
	original.Result = &uncertain

	uncertainRaw, err := json.Marshal(uncertain)
	if err != nil {
		t.Fatal(err)
	}

	hash, err := controllerFingerprint(uncertain)
	if err != nil {
		t.Fatal(err)
	}

	association.LastUncertaintyDigest = hash
	view.state.ProviderSlots[0] |= attemptUncertaintyBit
	fixture.rows[[3]string{"step_history", target.TenantID, hash}] = uncertainRaw

	stepID, err := providerStepID(step)
	if err != nil {
		t.Fatal(err)
	}

	originalRaw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}

	fixture.rows[[3]string{"step", target.TenantID, stepID}] = originalRaw

	associationRaw, err := json.Marshal(association)
	if err != nil {
		t.Fatal(err)
	}

	fixture.rows[[3]string{"removal_provider_slot", view.scope, stepID}] = associationRaw
	request := managed.RemovalRecoveryActivationRequest{Command: managed.RecoveryCommand{Key: target.Key, ID: plan.Profile.Acceptance.OperationID, Revision: managed.Decimal(strconv.FormatUint(target.Revision, 10)), LeaseEpoch: managed.Decimal(strconv.FormatUint(target.LeaseEpoch, 10)), Owner: target.LeaseOwner, AttemptRef: view.ref, Profile: managed.RemovalRecoveryProfile}}

	plan, err = prepareRecoveryActivationPlanHeld(t.Context(), fixture, target, view, request, plan.Profile.Facts.ActivatedAt, 1)
	if err != nil {
		t.Fatal(err)
	}

	activation, err := stageRecoveryActivationMaterialization(t.Context(), fixture, target, view, plan)
	if err != nil {
		t.Fatal(err)
	}

	acceptInvocationStage(t, fixture, activation)

	view.state, view.recoveryActivation = plan.State.AttemptState, &plan.State.Activation

	selected, err := invocationSelectedHeld(t.Context(), fixture, view, step)
	if err != nil {
		t.Fatal(err)
	}

	originalWrapperKey := selected.current.ResultKey
	positive := invocationResultFixture(t, selected, plan.Profile.Facts.ActivatedAt.Add(time.Second), managed.PortTerminal)
	acceptance := recoveryTestRecord(t, managed.RecoveryResultRecord, positive, positive.Result.ObservedAt).Acceptance

	staged, _, err := stageInvocationResult(t.Context(), fixture, selected, positive, acceptance, positive.Result.ObservedAt)
	if err != nil {
		t.Fatal(err)
	}

	if err = inspectRecoveryFixedSelection(t.Context(), staged, target, view, plan.Profile, plan.Selections[0], 0); err != nil {
		t.Fatal("later observation broke original inspection", err)
	}

	if !bytes.Equal(originalRaw, fixture.rows[[3]string{"step", target.TenantID, stepID}]) || !bytes.Equal(associationRaw, fixture.rows[[3]string{"removal_provider_slot", view.scope, stepID}]) {
		t.Fatal("later observation changed original bytes")
	}

	delete(fixture.rows, [3]string{string(managed.RecoveryResultRecord), view.scope, originalWrapperKey})

	if err = inspectRecoveryFixedSelection(t.Context(), staged, target, view, plan.Profile, plan.Selections[0], 0); err == nil {
		t.Fatal("selected positive concealed missing original wrapper")
	}
}

func invocationRetainRetryAssociations(t *testing.T, fixture *recoveryLeafFixture, selected retrySelected) {
	t.Helper()

	if selected.authority == nil {
		t.Fatal("fixture requires retry authority")
	}

	key := selected.permission.Facts.Key.AttemptKey

	scope, err := attemptScope(key.Key)
	if err != nil {
		t.Fatal(err)
	}

	view := attemptView{scope: scope, ref: selected.root.Facts.AttemptRef, descriptor: managed.AttemptDescriptor{Key: key.Key}}
	if err = stageInvocationRetryEvent(t.Context(), fixture, view, selected.root, *selected.authority, selected.current.Authority); err != nil {
		t.Fatal(err)
	}
}

func TestInvocationHistoricalReplayBeyondImmediatePredecessor(t *testing.T) {
	fixture, original, now := invocationFixture(t)
	key := original.permission.Facts.Key.AttemptKey
	root := original.current.Authority
	first := retryAdvanceFixture(t, fixture, key, root, now.Add(time.Second))
	invocationRetainRetryAssociations(t, fixture, first)
	second := retryAdvanceFixture(t, fixture, key, root, now.Add(2*time.Second))
	invocationRetainRetryAssociations(t, fixture, second)
	third := retryAdvanceFixture(t, fixture, key, root, now.Add(3*time.Second))
	invocationRetainRetryAssociations(t, fixture, third)

	request := invocationTransitionRequest{Kind: "retry", Command: managed.RecoveryCommand{Key: key.Key, ID: first.authority.Acceptance.OperationID, AttemptRef: first.root.Facts.AttemptRef}, Step: first.root.Facts.Step, InvocationID: first.current.CommandID}

	scope, err := attemptScope(key.Key)
	if err != nil {
		t.Fatal(err)
	}

	stepKey, err := retryStepKey(key, first.root.Facts.Step)
	if err != nil {
		t.Fatal(err)
	}

	liveRaw := bytes.Clone(fixture.rows[[3]string{retryCurrentKind, scope, stepKey}])
	if out, replayErr := invocationReplay(t.Context(), fixture, request, first.authority.Acceptance.RequestDigest); replayErr != nil || out.MayDelegate {
		t.Fatal("historical retry did not inspect", replayErr)
	}

	for _, rowKey := range [][3]string{
		{string(managed.RecoveryPermissionRecord), scope, first.current.Permission.Key},
		{recoveryAcceptanceKind, scope, first.current.Permission.Key},
		{"removal_attempt_event", scope, first.authority.Acceptance.OperationID.String()},
	} {
		raw := fixture.rows[rowKey]
		delete(fixture.rows, rowKey)

		if _, replayErr := invocationReplay(t.Context(), fixture, request, first.authority.Acceptance.RequestDigest); replayErr == nil {
			t.Fatal("missing old association accepted", rowKey)
		}

		fixture.rows[rowKey] = raw
	}

	eventKey := [3]string{"removal_attempt_event", scope, first.authority.Acceptance.OperationID.String()}
	savedEvent := fixture.rows[eventKey]
	otherEvent := fixture.rows[[3]string{"removal_attempt_event", scope, second.authority.Acceptance.OperationID.String()}]

	fixture.rows[eventKey] = otherEvent
	if _, replayErr := invocationReplay(t.Context(), fixture, request, first.authority.Acceptance.RequestDigest); replayErr == nil {
		t.Fatal("another retry event substituted")
	}

	fixture.rows[eventKey] = savedEvent
	// The second authorization records the first invocation's exact closure reference.
	closureRef := second.authority.Facts.Previous.Closure

	closure, err := recoveryReadFact[managed.ProviderInvocationClosure](t.Context(), fixture, key, closureRef)
	if err != nil {
		t.Fatal(err)
	}

	request.Kind, request.Command.ID = "closure", closure.Acceptance.OperationID
	if _, err = invocationReplay(t.Context(), fixture, request, closure.Acceptance.RequestDigest); err != nil {
		t.Fatal("historical closure did not inspect", err)
	}

	failureKey := [3]string{string(managed.RecoveryFailureRecord), scope, second.authority.Facts.Previous.Failure.Key}
	savedFailure := fixture.rows[failureKey]
	delete(fixture.rows, failureKey)

	if _, err = invocationReplay(t.Context(), fixture, request, closure.Acceptance.RequestDigest); err == nil {
		t.Fatal("historical closure concealed missing paired failure")
	}

	fixture.rows[failureKey] = savedFailure
	if !bytes.Equal(liveRaw, fixture.rows[[3]string{retryCurrentKind, scope, stepKey}]) {
		t.Fatal("historical inspection rotated selection")
	}
}

func TestInvocationRetryEventWriteRollsBackWithLocator(t *testing.T) {
	fixture, original, now := invocationFixture(t)
	key := original.permission.Facts.Key.AttemptKey
	selected := retryAdvanceFixture(t, fixture, key, original.current.Authority, now.Add(time.Second))

	scope, err := attemptScope(key.Key)
	if err != nil {
		t.Fatal(err)
	}

	view := attemptView{scope: scope, ref: selected.root.Facts.AttemptRef, descriptor: managed.AttemptDescriptor{Key: key.Key}}

	receipt, err := selected.authority.Acceptance.Receipt.Value()
	if err != nil {
		t.Fatal(err)
	}

	staged := &recoveryActivationRows{ControllerTransaction: fixture, key: key.Key, receipt: receipt}
	if err = stageInvocationRetryEvent(t.Context(), staged, view, selected.root, *selected.authority, selected.current.Authority); err != nil {
		t.Fatal(err)
	}

	if len(staged.rows) != 2 || staged.rows[1].kind != "removal_attempt_event" {
		t.Fatal("retry did not stage locator and event")
	}

	before := cloneActivationFixture(fixture)
	target := &managed.Target{Identity: managed.Identity{Key: key.Key}, Revision: 2, LeaseUntil: now.Add(time.Minute)}
	backend := &activationTransactionFixture{state: fixture, target: target, failAt: 2}

	err = New(backend).controller(t.Context(), "retry-event-fixture", []managed.Key{key.Key}, func(tx ControllerTransaction) error {
		_, atomicErr := tx.Atomic(t.Context(), key.Key, receipt.ID, receipt.Digest, func(_ *managed.Target, _ time.Time) (managed.Receipt, error) {
			if writeErr := applyRecoveryActivationRows(t.Context(), tx, staged); writeErr != nil {
				return managed.Receipt{}, writeErr
			}

			return receipt, nil
		})

		return atomicErr
	})
	if !errors.Is(err, managed.ErrUnavailable) || !reflect.DeepEqual(before.rows, fixture.rows) {
		t.Fatal("event failure leaked locator", err)
	}

	acceptInvocationStage(t, fixture, staged)

	if err = invocationRetryEventHeld(t.Context(), fixture, key, view.ref, *selected.authority); err != nil {
		t.Fatal(err)
	}

	rowKey := [3]string{"removal_attempt_event", scope, receipt.ID.String()}

	event, err := attemptRead[attemptEvent](t.Context(), fixture, rowKey[0], rowKey[1], rowKey[2], managed.MaxAttemptFact)
	if err != nil {
		t.Fatal(err)
	}

	event.FactDigest = strings.Repeat("f", 64)

	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}

	fixture.rows[rowKey] = raw
	if err = invocationRetryEventHeld(t.Context(), fixture, key, view.ref, *selected.authority); err == nil {
		t.Fatal("corrupt event digest accepted")
	}
}
