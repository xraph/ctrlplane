package managedstate

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

func TestRecoveryReserveIncludesBothCounters(t *testing.T) {
	after := recoveryReserveAfter{existing: 489, activationMissing: true, markerObligations: 33, closureObligations: 33, closeIntentsMissing: 64, closeResultsMissing: 64}

	reserve, err := recoveryRemainingReserve(after)
	if err != nil || reserve != 684 {
		t.Fatal("incorrect 64-binding/33-step reserve", reserve, err)
	}

	last := uint64(math.MaxInt64 - 1 - 684)
	if err = recoveryCheckHeadroom(last, last, 1, after); err != nil {
		t.Fatal("last valid transition refused", err)
	}

	for _, counters := range [][2]uint64{{last + 1, last}, {last, last + 1}} {
		if err = recoveryCheckHeadroom(counters[0], counters[1], 1, after); !errors.Is(err, managed.ErrCapacity) {
			t.Fatal("independent signed counter reserve bypassed", counters, err)
		}
	}

	// An activation staged by the current transition has already consumed that obligation.
	after.activationMissing = false
	if err = recoveryCheckHeadroom(last+1, last+1, 1, after); err != nil {
		t.Fatal("current activation was charged twice", err)
	}

	if err = recoveryCheckHeadroom(math.MaxInt64, math.MaxInt64, 0, recoveryReserveAfter{}); err != nil {
		t.Fatal("zero-cost terminal receipt recovery refused", err)
	}

	if _, err = recoveryRemainingReserve(recoveryReserveAfter{existing: math.MaxInt64, markerObligations: 1}); !errors.Is(err, managed.ErrCapacity) {
		t.Fatal("reserve arithmetic overflow accepted", err)
	}
}

func retryOriginalFixture(t *testing.T) (*recoveryLeafFixture, managed.AttemptKey, managed.RecoveryAssociation, managed.ProviderStepRetryCurrent, time.Time) {
	t.Helper()

	fixture, key, now := recoveryFixture()
	command := id.New(managed.PrefixOperation)
	step := managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: key.Key, ActionID: id.New(managed.PrefixOperation)}, ProviderOperationID: id.New(managed.PrefixOperation), Kind: managed.StepRemoveContainer, Service: "api"}
	permission := managed.ProviderStepPermission{Step: step, CommandID: command, Digest: strings.Repeat("a", 64), SpecDigest: strings.Repeat("b", 64), CreateProfile: "ctrlplane.managed.create.v1", Physical: managed.Physical{Provider: "docker", Placement: "local", Project: "project", Generation: "generation"}, Seal: 1, FenceDigest: strings.Repeat("c", 64), ResourceID: "container", IssuedAt: now, Revision: 1}
	originalReceipt := managed.Receipt{ID: command, Digest: permission.Digest, Revision: permission.Revision, LeaseEpoch: 1, AcceptedAt: now, LeaseUntil: now.Add(time.Minute)}
	fixture.receipts[attemptReceiptKey{key.Key, command, originalReceipt.Digest}] = originalReceipt
	rootFacts := managed.ProviderStepRetryRoot{AttemptRef: managed.AttemptRef{AttemptID: key.AttemptID, Generation: key.Generation, DescriptorDigest: strings.Repeat("d", 64)}, Step: step, Profile: managed.ProviderStepRetryProfile, SpecDigest: permission.SpecDigest, ResourceID: permission.ResourceID, Incarnation: "incarnation", ProviderCommandDigest: strings.Repeat("e", 64), OriginalPermission: managed.ProviderPermissionWire(permission), OriginalPermissionReceipt: managed.ReceiptWire(originalReceipt)}
	root := recoveryTestRecord(t, managed.RecoveryRootRecord, rootFacts, now)
	rootReference := recoveryTestStore(t, fixture, key, root)
	permissionFacts := managed.ProviderInvocationPermission{Key: managed.ProviderInvocationKey{AttemptKey: key, Step: step, CommandID: command}, RootDigest: root.Digest, Ordinal: "0", Permission: rootFacts.OriginalPermission, ProviderCommandDigest: rootFacts.ProviderCommandDigest}
	accepted := recoveryTestRecord(t, managed.RecoveryPermissionRecord, permissionFacts, now)
	permissionReference := recoveryTestStore(t, fixture, key, accepted)
	current := managed.ProviderStepRetryCurrent{SchemaVersion: "1", RootDigest: root.Digest, CommandID: command, Ordinal: "0", Permission: permissionReference, Authority: rootReference}
	retryWriteTestCurrent(t, fixture, key, rootFacts.Step, &current, true)

	return fixture, key, rootReference, current, now
}

func retryWriteTestCurrent(t *testing.T, fixture *recoveryLeafFixture, key managed.AttemptKey, step managed.ProviderStepKey, current *managed.ProviderStepRetryCurrent, insert bool) {
	t.Helper()

	hash, err := managed.RetryCurrentDigest(*current)
	if err != nil {
		t.Fatal(err)
	}

	current.Digest = hash

	scope, err := attemptScope(key.Key)
	if err != nil {
		t.Fatal(err)
	}

	selected, err := retryStepKey(key, step)
	if err != nil {
		t.Fatal(err)
	}

	if err = attemptWrite(t.Context(), fixture, retryCurrentKind, scope, selected, current, insert, managed.MaxStepRetryPointer); err != nil {
		t.Fatal(err)
	}
}

func TestRetryReadsDoNotScanUnselectedHistory(t *testing.T) {
	fixture, key, root, _, _ := retryOriginalFixture(t)

	fixture.reads = 0
	if _, err := retryReadSelected(t.Context(), fixture, key, root); err != nil {
		t.Fatal(err)
	}

	baseline := fixture.reads
	for i := range 10000 {
		fixture.rows[[3]string{string(managed.RecoveryAuthorizationRecord), "unselected", strconv.Itoa(i)}] = []byte("corrupt unselected history")
	}

	fixture.reads = 0
	if _, err := retryReadSelected(t.Context(), fixture, key, root); err != nil {
		t.Fatal(err)
	}

	if fixture.reads != baseline || baseline > 12 {
		t.Fatal("read count depends on history", baseline, fixture.reads)
	}
}

func TestRetryNeedsExplicitCurrentSelection(t *testing.T) {
	fixture, key, root, _, _ := retryOriginalFixture(t)

	selected, err := retryReadSelected(t.Context(), fixture, key, root)
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err = retryPredecessor(selected, selected.current.Digest, id.New(managed.PrefixOperation)); !errors.Is(err, managed.ErrBlocked) {
		t.Fatal("missing marker treated as non-dispatch", err)
	}

	scope, err := attemptScope(key.Key)
	if err != nil {
		t.Fatal(err)
	}

	step, err := retryStepKey(key, selected.root.Facts.Step)
	if err != nil {
		t.Fatal(err)
	}

	delete(fixture.rows, [3]string{retryCurrentKind, scope, step})

	if _, err = retryReadSelected(t.Context(), fixture, key, root); err == nil {
		t.Fatal("original selected from absent current pointer")
	}
}

func TestRetryRequiresSameReceiptFailureAndClosure(t *testing.T) {
	fixture, key, rootRef, current, now := retryOriginalFixture(t)

	selected, err := retryReadSelected(t.Context(), fixture, key, rootRef)
	if err != nil {
		t.Fatal(err)
	}

	permission, err := selected.permission.Facts.Permission.Value()
	if err != nil {
		t.Fatal(err)
	}

	permissionDigest, err := recoveryPermissionDigest(selected.permission.Facts.Permission)
	if err != nil {
		t.Fatal(err)
	}

	failure := managed.ProviderStepFailure{SchemaVersion: "1", Permission: permission, AttemptRef: selected.root.Facts.AttemptRef, InvocationID: permission.CommandID, ProducerProfile: managed.LocalNoDispatchProfile, VerifierID: "verifier", Class: "local_no_dispatch", Phase: "before_transport_delegate", Reason: "invocation_closed", ObservedAt: now, Witness: []byte("affirmative"), WitnessDigest: managed.Digest([]byte("affirmative"))}
	failureFacts := managed.ProviderInvocationFailure{Key: selected.permission.Facts.Key, RootDigest: selected.root.Digest, PermissionDigest: permissionDigest, Failure: failure}
	failureRecord := recoveryTestRecord(t, managed.RecoveryFailureRecord, failureFacts, now.Add(time.Second))
	failureRecord.Acceptance.Receipt.Revision = "3"
	failureRef := recoveryTestStore(t, fixture, key, failureRecord)
	closureFacts := managed.ProviderInvocationClosure{Key: failureFacts.Key, RootDigest: failureFacts.RootDigest, PermissionDigest: permissionDigest, FailureDigest: failureRecord.Digest, ProducerProfile: failure.ProducerProfile, VerifierID: failure.VerifierID, ClosedAt: now.Add(time.Second)}
	closure := recoveryTestRecord(t, managed.RecoveryClosureRecord, closureFacts, now.Add(time.Second))
	closure.Acceptance = failureRecord.Acceptance
	closureRef := recoveryTestStore(t, fixture, key, closure)

	selectionDigest, err := retrySelectionDigest(current)
	if err != nil {
		t.Fatal(err)
	}

	markerFacts := managed.ProviderInvocationMarker{Key: failureFacts.Key, RootDigest: failureFacts.RootDigest, PermissionDigest: permissionDigest, SelectionDigest: selectionDigest, NativeCommitSetDigest: strings.Repeat("f", 64), CurrentProofSetDigest: strings.Repeat("a", 64), BindingCount: "1", AcceptedAt: now}
	marker := recoveryTestRecord(t, managed.RecoveryMarkerRecord, markerFacts, now)
	markerRef := recoveryTestStore(t, fixture, key, marker)
	current.MarkerKey = markerRef.Key
	current.FailureKey = failureRef.Key
	current.ClosureKey = closureRef.Key
	retryWriteTestCurrent(t, fixture, key, selected.root.Facts.Step, &current, false)

	selected, err = retryReadSelected(t.Context(), fixture, key, rootRef)
	if err != nil {
		t.Fatal(err)
	}

	previous, ordinal, err := retryPredecessor(selected, current.Digest, id.New(managed.PrefixOperation))
	if err != nil || ordinal != "1" || previous.PermissionDigest != permissionDigest {
		t.Fatal("marked but affirmatively closed original not eligible for new local invocation", err)
	}

	if previous.PermissionDigest == permission.Digest {
		t.Fatal("request digest substituted for raw permission digest")
	}

	changedClosure := closure

	changedClosure.Acceptance.Receipt.Revision = "4"
	if err = retryClosedFailure(failureRecord, changedClosure); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("failure and closure from different receipts accepted", err)
	}

	changedClosure = closure

	changedClosure.Facts.ClosedAt = closure.Facts.ClosedAt.Add(time.Second)
	if err = retryClosedFailure(failureRecord, changedClosure); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("future closure fact accepted", err)
	}

	changedFailure := failureRecord

	changedFailure.Facts.Failure.ObservedAt = closure.Facts.ClosedAt.Add(time.Second)
	if err = retryClosedFailure(changedFailure, closure); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("failure observed after accepted closure", err)
	}

	selected.result = &managed.RecoveryRecord[managed.ProviderInvocationResult]{}
	if _, _, err = retryPredecessor(selected, current.Digest, id.New(managed.PrefixOperation)); !errors.Is(err, managed.ErrBlocked) {
		t.Fatal("uncertain or positive predecessor retried", err)
	}
}

func TestRetryMarkedWithoutQualifiedClosureRefuses(t *testing.T) {
	fixture, key, rootRef, current, now := retryOriginalFixture(t)

	selected, err := retryReadSelected(t.Context(), fixture, key, rootRef)
	if err != nil {
		t.Fatal(err)
	}

	permissionDigest, err := recoveryPermissionDigest(selected.permission.Facts.Permission)
	if err != nil {
		t.Fatal(err)
	}

	selectionDigest, err := retrySelectionDigest(current)
	if err != nil {
		t.Fatal(err)
	}

	markerFacts := managed.ProviderInvocationMarker{Key: selected.permission.Facts.Key, RootDigest: selected.root.Digest, PermissionDigest: permissionDigest, SelectionDigest: selectionDigest, NativeCommitSetDigest: strings.Repeat("f", 64), CurrentProofSetDigest: strings.Repeat("a", 64), BindingCount: "1", AcceptedAt: now}
	marker := recoveryTestRecord(t, managed.RecoveryMarkerRecord, markerFacts, now)
	markerRef := recoveryTestStore(t, fixture, key, marker)
	current.MarkerKey = markerRef.Key
	retryWriteTestCurrent(t, fixture, key, selected.root.Facts.Step, &current, false)

	selected, err = retryReadSelected(t.Context(), fixture, key, rootRef)
	if err != nil || selected.marker == nil {
		t.Fatal("saved marker was not retained", err)
	}

	writes := fixture.writes
	if _, _, err = retryPredecessor(selected, current.Digest, id.New(managed.PrefixOperation)); !errors.Is(err, managed.ErrBlocked) || fixture.writes != writes {
		t.Fatal("marker uncertainty authorized retry", err)
	}
}

func TestRetryClosureReceiptOrder(t *testing.T) {
	now := time.Date(2026, 10, 13, 20, 0, 0, 0, time.UTC)
	marker := managed.RecoveryRecord[managed.ProviderInvocationMarker]{Facts: managed.ProviderInvocationMarker{AcceptedAt: now}, Acceptance: managed.RecoveryAcceptance{Receipt: managed.WireReceipt{Revision: "2", LeaseEpoch: "1", AcceptedAt: now}}}

	closure := managed.RecoveryRecord[managed.ProviderInvocationClosure]{Facts: managed.ProviderInvocationClosure{ClosedAt: now.Add(time.Second)}, Acceptance: managed.RecoveryAcceptance{Receipt: managed.WireReceipt{Revision: "3", LeaseEpoch: "1", AcceptedAt: now.Add(time.Second)}}}
	if err := retryMarkerBeforeClosure(marker, closure); err != nil {
		t.Fatal("ordered marker and closure refused", err)
	}

	for _, tc := range []struct {
		name       string
		revision   managed.Decimal
		acceptedAt time.Time
		closedAt   time.Time
	}{
		{"equal_revision", "2", now.Add(time.Second), now.Add(time.Second)},
		{"reversed_revision", "1", now.Add(time.Second), now.Add(time.Second)},
		{"reversed_acceptance", "3", now.Add(-time.Second), now.Add(-time.Second)},
		{"future_closure_fact", "3", now.Add(time.Second), now.Add(2 * time.Second)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := closure
			changed.Acceptance.Receipt.Revision = tc.revision
			changed.Acceptance.Receipt.AcceptedAt = tc.acceptedAt

			changed.Facts.ClosedAt = tc.closedAt
			if err := retryMarkerBeforeClosure(marker, changed); !errors.Is(err, managed.ErrUnavailable) {
				t.Fatal("invalid closure order accepted", err)
			}
		})
	}

	closure.Acceptance.Receipt.AcceptedAt = now

	closure.Facts.ClosedAt = now
	if err := retryMarkerBeforeClosure(marker, closure); err != nil {
		t.Fatal("equal clock values hid valid advancing receipt order", err)
	}
}

// retryAdvanceFixture builds retained history, not an issuing authority transaction.
func retryAdvanceFixture(t *testing.T, fixture *recoveryLeafFixture, key managed.AttemptKey, root managed.RecoveryAssociation, now time.Time) retrySelected {
	t.Helper()

	selected, err := retryReadSelected(t.Context(), fixture, key, root)
	if err != nil {
		t.Fatal(err)
	}

	permission, err := selected.permission.Facts.Permission.Value()
	if err != nil {
		t.Fatal(err)
	}

	permissionDigest, err := recoveryPermissionDigest(selected.permission.Facts.Permission)
	if err != nil {
		t.Fatal(err)
	}

	failure := managed.ProviderStepFailure{SchemaVersion: "1", Permission: permission, AttemptRef: selected.root.Facts.AttemptRef, InvocationID: permission.CommandID, ProducerProfile: managed.LocalNoDispatchProfile, VerifierID: "verifier", Class: "local_no_dispatch", Phase: "before_transport_delegate", Reason: "invocation_closed", ObservedAt: now, Witness: []byte("closed"), WitnessDigest: managed.Digest([]byte("closed"))}
	failureFacts := managed.ProviderInvocationFailure{Key: selected.permission.Facts.Key, RootDigest: selected.root.Digest, PermissionDigest: permissionDigest, Failure: failure}
	failureRecord := recoveryTestRecord(t, managed.RecoveryFailureRecord, failureFacts, now)
	failureReference := recoveryTestStore(t, fixture, key, failureRecord)
	closureFacts := managed.ProviderInvocationClosure{Key: failureFacts.Key, RootDigest: failureFacts.RootDigest, PermissionDigest: permissionDigest, FailureDigest: failureRecord.Digest, ProducerProfile: failure.ProducerProfile, VerifierID: failure.VerifierID, ClosedAt: now}
	closureRecord := recoveryTestRecord(t, managed.RecoveryClosureRecord, closureFacts, now)
	closureRecord.Acceptance = failureRecord.Acceptance
	closureReference := recoveryTestStore(t, fixture, key, closureRecord)
	current := selected.current
	current.FailureKey, current.ClosureKey = failureReference.Key, closureReference.Key
	retryWriteTestCurrent(t, fixture, key, selected.root.Facts.Step, &current, false)

	selected, err = retryReadSelected(t.Context(), fixture, key, root)
	if err != nil {
		t.Fatal(err)
	}

	command := id.New(managed.PrefixOperation)

	previous, ordinal, err := retryPredecessor(selected, current.Digest, command)
	if err != nil {
		t.Fatal(err)
	}

	nextFacts := selected.permission.Facts
	nextFacts.Key.CommandID, nextFacts.Ordinal = command, ordinal
	nextFacts.Permission.CommandID = command
	nextFacts.Permission.Digest = managed.Digest([]byte(command.String()))
	nextFacts.Permission.IssuedAt = now
	nextPermission := recoveryTestRecord(t, managed.RecoveryPermissionRecord, nextFacts, now)
	nextReference := recoveryTestStore(t, fixture, key, nextPermission)
	authorityFacts := managed.ProviderStepRetryAuthorization{Key: nextFacts.Key, RootDigest: selected.root.Digest, Ordinal: ordinal, Permission: nextReference, Previous: previous, ExpectedCurrentDigest: current.Digest, ParentRevision: "2", AttemptRevision: "2", AuthorizedAt: now}
	authority := recoveryTestRecord(t, managed.RecoveryAuthorizationRecord, authorityFacts, now)
	authority.Acceptance = nextPermission.Acceptance
	authorityReference := recoveryTestStore(t, fixture, key, authority)

	scope, err := attemptScope(key.Key)
	if err != nil {
		t.Fatal(err)
	}

	stepKey, err := retryStepKey(key, selected.root.Facts.Step)
	if err != nil {
		t.Fatal(err)
	}

	if err = attemptWrite(t.Context(), fixture, retryPreviousKind, scope, stepKey, previous, selected.current.Ordinal == "0", managed.MaxStepRetryPointer); err != nil {
		t.Fatal(err)
	}

	next := managed.ProviderStepRetryCurrent{SchemaVersion: "1", RootDigest: selected.root.Digest, CommandID: command, Ordinal: ordinal, Permission: nextReference, Authority: authorityReference, PreviousDigest: previous.Digest}
	retryWriteTestCurrent(t, fixture, key, selected.root.Facts.Step, &next, false)

	selected, err = retryReadSelected(t.Context(), fixture, key, root)
	if err != nil {
		t.Fatal(err)
	}

	return selected
}

func TestRetrySelectedCertificatesStayBounded(t *testing.T) {
	fixture, key, root, _, now := retryOriginalFixture(t)

	var (
		selected    retrySelected
		secondReads int
	)

	for n := 1; n <= 32; n++ {
		selected = retryAdvanceFixture(t, fixture, key, root, now.Add(time.Duration(n)*time.Second))

		fixture.reads = 0
		if _, err := retryReadSelected(t.Context(), fixture, key, root); err != nil {
			t.Fatal(err)
		}

		if n == 2 {
			secondReads = fixture.reads
		}

		if n > 2 && fixture.reads != secondReads {
			t.Fatal("selected history changed exact read bound", n, secondReads, fixture.reads)
		}
	}

	if selected.current.Ordinal != "32" || selected.previous == nil || selected.previous.Ordinal != "31" {
		t.Fatal("fixture never selected later retry")
	}

	scope, err := attemptScope(key.Key)
	if err != nil {
		t.Fatal(err)
	}

	refs := []managed.RecoveryAssociation{root, selected.current.Permission, selected.current.Authority, selected.previous.Authority, selected.previous.Failure, selected.previous.Closure}

	predecessor, err := recoveryReadFact[managed.ProviderStepRetryAuthorization](t.Context(), fixture, key, selected.previous.Authority)
	if err != nil {
		t.Fatal(err)
	}

	refs = append(refs, predecessor.Facts.Permission)
	for _, ref := range refs {
		rowKey := [3]string{string(ref.Kind), scope, ref.Key}
		raw := fixture.rows[rowKey]
		delete(fixture.rows, rowKey)

		if _, err = retryReadSelected(t.Context(), fixture, key, root); err == nil {
			t.Fatal("missing required selected fact accepted", ref.Kind)
		}

		fixture.rows[rowKey] = []byte("corrupt")
		if _, err = retryReadSelected(t.Context(), fixture, key, root); err == nil {
			t.Fatal("corrupt required selected fact accepted", ref.Kind)
		}

		fixture.rows[rowKey] = raw
	}

	receiptKeys := []attemptReceiptKey{{key.Key, selected.root.Facts.OriginalPermissionReceipt.ID, selected.root.Facts.OriginalPermissionReceipt.Digest}}
	for _, ref := range refs {
		receiptKeys = append(receiptKeys, attemptReceiptKey{key.Key, ref.OperationID, ref.ReceiptDigest})
	}

	for _, receiptKey := range receiptKeys {
		receipt, exists := fixture.receipts[receiptKey]
		if !exists {
			t.Fatal("required fixture receipt absent")
		}

		delete(fixture.receipts, receiptKey)
		_, readErr := retryReadSelected(t.Context(), fixture, key, root)
		fixture.receipts[receiptKey] = receipt

		if readErr == nil {
			t.Fatal("missing required selected receipt accepted")
		}
	}
}
