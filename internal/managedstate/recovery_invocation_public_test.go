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

// These are exact reader, selector and staging regressions. They do not exercise the
// complete public entry through the original binding reader or a production backend.
func TestInvocationCommitSelectorPersistsExactRefresh(t *testing.T) {
	fixture, selected, now := invocationFixture(t)

	member, reference, commit := invocationCommitMemberFixture(t, fixture, selected, now)
	if err := stageInvocationNativeCommitSelector(t.Context(), fixture, selected, member, commit, reference); err != nil {
		t.Fatal(err)
	}

	refs, err := invocationCommitSelectionsHeld(t.Context(), fixture, selected, []invocationProofMember{member})
	if err != nil || len(refs) != 1 || refs[0] != reference {
		t.Fatal("exact selector lost", err)
	}

	stale := member

	stale.current.Proof.Digest = strings.Repeat("f", 64)
	if _, err = invocationCommitSelectionsHeld(t.Context(), fixture, selected, []invocationProofMember{stale}); err == nil {
		t.Fatal("selector silently followed changed proof")
	}

	for _, mutation := range []func(*managed.ProviderNativeCommitFacts){
		func(facts *managed.ProviderNativeCommitFacts) { facts.GuardID = "another-guard" },
		func(facts *managed.ProviderNativeCommitFacts) { facts.GuardRootDigest = strings.Repeat("e", 64) },
		func(facts *managed.ProviderNativeCommitFacts) { facts.PermissionProof.Digest = strings.Repeat("e", 64) },
		func(facts *managed.ProviderNativeCommitFacts) { facts.NativeAcceptedAt = now.Add(-time.Second) },
	} {
		facts := commit.Facts
		mutation(&facts)
		bad := recoveryTestRecord(t, managed.RecoveryCommitRecord, facts, now)

		badRef := recoveryTestStore(t, fixture, selected.permission.Facts.Key.AttemptKey, bad)
		if err = stageInvocationNativeCommitSelector(t.Context(), fixture, selected, member, bad, badRef); err == nil {
			t.Fatal("incompatible selector refresh accepted")
		}
	}

	facts := commit.Facts
	facts.NativeAcceptedAt = now.Add(time.Second)
	refresh := recoveryTestRecord(t, managed.RecoveryCommitRecord, facts, now.Add(time.Second))

	refreshRef := recoveryTestStore(t, fixture, selected.permission.Facts.Key.AttemptKey, refresh)
	if err = stageInvocationNativeCommitSelector(t.Context(), fixture, selected, member, refresh, reference); err == nil {
		t.Fatal("different saved commit reference accepted")
	}

	if err = stageInvocationNativeCommitSelector(t.Context(), fixture, selected, member, refresh, refreshRef); err != nil {
		t.Fatal(err)
	}

	refs, err = invocationCommitSelectionsHeld(t.Context(), fixture, selected, []invocationProofMember{member})
	if err != nil || refs[0] != refreshRef {
		t.Fatal("refresh not selected", err)
	}

	if _, err = recoveryReadFact[managed.ProviderNativeCommitFacts](t.Context(), fixture, selected.permission.Facts.Key.AttemptKey, reference); err != nil {
		t.Fatal("refresh erased old commit", err)
	}
}

func invocationMarkerHistoryFixture(t *testing.T, count int) (*recoveryLeafFixture, retrySelected, []managed.RecoveryAssociation) {
	t.Helper()
	fixture, selected, now := invocationFixture(t)
	commits := make([]managed.RecoveryAssociation, 0, count)
	proofs := make([]managed.RecoveryAssociation, 0, count)

	currentProofs := make([]managed.RecoveryAssociation, 0, count)
	for range count {
		_, reference, commit := invocationCommitMemberFixture(t, fixture, selected, now)
		commits = append(commits, reference)
		proofs = append(proofs, commit.Facts.PermissionProof)
		currentProofs = append(currentProofs, commit.Facts.CurrentProof)
	}

	var err error

	selected.permission.Facts.PermissionProofCount = managed.Decimal(strconv.Itoa(count))

	selected.permission.Facts.PermissionProofSetDigest, err = attemptHash("ctrlplane.managed.permission-proof-set.v1", proofs)
	if err != nil {
		t.Fatal(err)
	}

	commitDigest, err := attemptHash("ctrlplane.managed.native-commit-set.v1", commits)
	if err != nil {
		t.Fatal(err)
	}

	currentDigest, err := attemptHash("ctrlplane.managed.current-proof-set.v1", currentProofs)
	if err != nil {
		t.Fatal(err)
	}

	selectionDigest, err := retrySelectionDigest(selected.current)
	if err != nil {
		t.Fatal(err)
	}

	permissionDigest, err := recoveryPermissionDigest(selected.permission.Facts.Permission)
	if err != nil {
		t.Fatal(err)
	}

	marker := recoveryTestRecord(t, managed.RecoveryMarkerRecord, managed.ProviderInvocationMarker{Key: selected.permission.Facts.Key, RootDigest: selected.root.Digest, PermissionDigest: permissionDigest, SelectionDigest: selectionDigest, NativeCommitSetDigest: commitDigest, CurrentProofSetDigest: currentDigest, BindingCount: managed.Decimal(strconv.Itoa(count)), AcceptedAt: now}, now)
	reference := recoveryTestStore(t, fixture, selected.permission.Facts.Key.AttemptKey, marker)

	selected.marker, selected.current.MarkerKey = &marker, reference.Key
	if err = stageInvocationMarkerMembers(t.Context(), fixture, marker, reference, commits); err != nil {
		t.Fatal(err)
	}

	return fixture, selected, commits
}

func TestInvocationMarkerMembersPersistSmallAndMaximum(t *testing.T) {
	for _, count := range []int{1, managed.MaxRecoveryBindings} {
		fixture, selected, commits := invocationMarkerHistoryFixture(t, count)
		before := cloneActivationFixture(fixture)

		actual, err := invocationMarkerMembersHeld(t.Context(), fixture, selected)
		if err != nil || !reflect.DeepEqual(actual, commits) {
			t.Fatal("complete saved member set refused", count, err)
		}

		if !reflect.DeepEqual(before.rows, fixture.rows) {
			t.Fatal("inspection changed authority")
		}

		scope, err := attemptScope(selected.permission.Facts.Key.AttemptKey.Key)
		if err != nil {
			t.Fatal(err)
		}

		last, err := invocationMarkerMemberKey(selected.permission.Facts.Key, count-1)
		if err != nil {
			t.Fatal(err)
		}

		rowKey := [3]string{invocationMarkerMembersKind, scope, last}
		saved := bytes.Clone(fixture.rows[rowKey])
		delete(fixture.rows, rowKey)

		if _, err = invocationMarkerMembersHeld(t.Context(), fixture, selected); err == nil {
			t.Fatal("missing ordinal concealed")
		}

		fixture.rows[rowKey] = saved

		row, err := attemptDecode[invocationMarkerMemberReference](saved, maxInvocationStatus)
		if err != nil {
			t.Fatal(err)
		}

		row.Index = "64"

		changed, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}

		fixture.rows[rowKey] = changed
		if _, err = invocationMarkerMembersHeld(t.Context(), fixture, selected); err == nil {
			t.Fatal("substituted ordinal accepted")
		}

		fixture.rows[rowKey] = saved
		commitKey := [3]string{string(managed.RecoveryCommitRecord), scope, commits[count-1].Key}
		delete(fixture.rows, commitKey)

		if _, err = invocationMarkerMembersHeld(t.Context(), fixture, selected); err == nil {
			t.Fatal("missing independently retained commit accepted")
		}
	}
}

func TestInvocationMemberCompleteEncodingCapAndOneOver(t *testing.T) {
	fixture, selected, _ := invocationMarkerHistoryFixture(t, 1)
	key := selected.permission.Facts.Key

	scope, err := attemptScope(key.AttemptKey.Key)
	if err != nil {
		t.Fatal(err)
	}

	locator, err := invocationMarkerMemberKey(key, 0)
	if err != nil {
		t.Fatal(err)
	}

	row, err := attemptRead[invocationMarkerMemberReference](t.Context(), fixture, invocationMarkerMembersKind, scope, locator, maxInvocationStatus)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}

	if len(raw) >= maxInvocationStatus {
		t.Fatal("ordinary member exceeds cap")
	}
	// This exercises the encoder's full wrapper cap separately from semantic validation.
	row.Commit.Key += strings.Repeat("x", maxInvocationStatus-len(raw))

	raw, err = json.Marshal(row)
	if err != nil || len(raw) != maxInvocationStatus {
		t.Fatal("fixture missed complete row cap", len(raw), err)
	}

	if err = attemptWrite(t.Context(), fixture, invocationMarkerMembersKind, scope, locator, row, false, maxInvocationStatus); err != nil {
		t.Fatal("exact cap refused", err)
	}

	before := bytes.Clone(fixture.rows[[3]string{invocationMarkerMembersKind, scope, locator}])

	row.Commit.Key += "x"
	if err = attemptWrite(t.Context(), fixture, invocationMarkerMembersKind, scope, locator, row, false, maxInvocationStatus); !errors.Is(err, managed.ErrCapacity) {
		t.Fatal("cap plus one accepted", err)
	}

	if !bytes.Equal(before, fixture.rows[[3]string{invocationMarkerMembersKind, scope, locator}]) {
		t.Fatal("oversize write truncated or replaced row")
	}
}

func invocationCheckedStatusView(t *testing.T, fixture *recoveryLeafFixture, selected retrySelected) attemptView {
	t.Helper()

	key := selected.permission.Facts.Key.AttemptKey

	scope, err := attemptScope(key.Key)
	if err != nil {
		t.Fatal(err)
	}

	stepKey, err := retryStepKey(key, selected.root.Facts.Step)
	if err != nil {
		t.Fatal(err)
	}

	fixed, err := attemptRead[recoveryFixedSelection](t.Context(), fixture, recoveryFixedSelectionKind, scope, stepKey, managed.MaxStepRetryPointer)
	if err != nil {
		t.Fatal(err)
	}

	activation := managed.RecoveryAssociation{Kind: managed.RecoveryActivationRecord, Key: strings.Repeat("a", 64), Digest: strings.Repeat("b", 64), OperationID: selected.root.Acceptance.OperationID, ReceiptDigest: selected.root.Acceptance.RequestDigest}

	fixed.Activation = activation
	if err = attemptWrite(t.Context(), fixture, recoveryFixedSelectionKind, scope, stepKey, fixed, false, managed.MaxStepRetryPointer); err != nil {
		t.Fatal(err)
	}

	return attemptView{validated: true, scope: scope, ref: selected.root.Facts.AttemptRef, descriptor: managed.AttemptDescriptor{Key: key.Key}, recoveryActivation: &activation}
}

func TestInvocationHistoricalStatusValidatesOldClosureAndPermission(t *testing.T) {
	fixture, original, now := invocationFixture(t)
	view := invocationCheckedStatusView(t, fixture, original)
	key := original.permission.Facts.Key.AttemptKey
	root := original.current.Authority
	first := retryAdvanceFixture(t, fixture, key, root, now.Add(time.Second))
	invocationRetainRetryAssociations(t, fixture, first)

	if err := stageInvocationStatus(t.Context(), fixture, first, true); err != nil {
		t.Fatal(err)
	}

	second := retryAdvanceFixture(t, fixture, key, root, now.Add(2*time.Second))
	invocationRetainRetryAssociations(t, fixture, second)

	closed, err := invocationHistoricalSelectionHeld(t.Context(), fixture, first.permission.Facts.Key, view.ref, managed.RecoveryClosureRecord, second.authority.Facts.Previous.Closure)
	if err != nil {
		t.Fatal(err)
	}

	if err = stageInvocationStatus(t.Context(), fixture, closed, false); err != nil {
		t.Fatal(err)
	}

	third := retryAdvanceFixture(t, fixture, key, root, now.Add(3*time.Second))
	invocationRetainRetryAssociations(t, fixture, third)
	before := cloneActivationFixture(fixture)

	inspected, err := invocationStatusSelectionHeld(t.Context(), fixture, view, first.permission.Facts.Key)
	if err != nil || inspected.closure == nil || inspected.failure == nil || inspected.permission.Digest != first.permission.Digest {
		t.Fatal("old complete status refused", err)
	}

	for _, missing := range [][3]string{
		{string(managed.RecoveryPermissionRecord), view.scope, first.current.Permission.Key},
		{string(managed.RecoveryFailureRecord), view.scope, closed.current.FailureKey},
		{recoveryAcceptanceKind, view.scope, closed.current.ClosureKey},
		{"removal_attempt_event", view.scope, first.authority.Acceptance.OperationID.String()},
	} {
		raw := fixture.rows[missing]
		delete(fixture.rows, missing)

		if _, err = invocationStatusSelectionHeld(t.Context(), fixture, view, first.permission.Facts.Key); err == nil {
			t.Fatal("missing historical association accepted", missing)
		}

		fixture.rows[missing] = raw
	}

	if !reflect.DeepEqual(before.rows, fixture.rows) {
		t.Fatal("historical reader changed current selection")
	}
}

func TestInvocationReplayMissingDependenciesNeverBecomeFresh(t *testing.T) {
	fixture, selected, now := invocationFixture(t)
	facts := invocationResultFixture(t, selected, now, managed.PortUnavailable)
	record := recoveryTestRecord(t, managed.RecoveryResultRecord, facts, now)
	reference := recoveryTestStore(t, fixture, facts.Key.AttemptKey, record)

	scope, err := attemptScope(facts.Key.AttemptKey.Key)
	if err != nil {
		t.Fatal(err)
	}

	delete(fixture.rows, [3]string{recoveryAcceptanceKind, scope, reference.Key})

	if _, _, err = invocationReplayRecord[managed.ProviderInvocationResult](t.Context(), fixture, facts.Key.AttemptKey, managed.RecoveryResultRecord, reference.Key, record.Acceptance.OperationID, record.Acceptance.RequestDigest); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("present primary became fresh after acceptance loss", err)
	}

	delete(fixture.rows, [3]string{string(managed.RecoveryResultRecord), scope, reference.Key})

	receipt, err := record.Acceptance.Receipt.Value()
	if err != nil {
		t.Fatal(err)
	}

	fixture.receipts[attemptReceiptKey{facts.Key.AttemptKey.Key, receipt.ID, ""}] = receipt
	if _, _, err = invocationReplayRecord[managed.ProviderInvocationResult](t.Context(), fixture, facts.Key.AttemptKey, managed.RecoveryResultRecord, reference.Key, record.Acceptance.OperationID, record.Acceptance.RequestDigest); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("accepted operation became fresh after primary loss", err)
	}
}

func TestInvocationObservationIdentityAndScope(t *testing.T) {
	_, selected, now := invocationFixture(t)
	key := selected.permission.Facts.Key

	command := managed.RecoveryCommand{Key: key.AttemptKey.Key, ID: id.New(managed.PrefixOperation), Revision: "2", LeaseEpoch: "1", Owner: "owner", AttemptRef: selected.root.Facts.AttemptRef, Profile: managed.RemovalRecoveryProfile}
	if err := invocationCommandMatches(command, key); err != nil {
		t.Fatal(err)
	}

	other := key

	other.AttemptKey.AttemptID = id.New(managed.PrefixOperation)
	if err := invocationCommandMatches(command, other); !errors.Is(err, managed.ErrAuthority) {
		t.Fatal("cross-attempt observation accepted", err)
	}

	request := managed.ProviderInvocationObservationRequest{Command: command, ExpectedCurrentDigest: selected.current.Digest, Result: invocationResultFixture(t, selected, now, managed.PortUnavailable)}

	first, err := attemptHash("ctrlplane.managed.observation-request.test", request)
	if err != nil {
		t.Fatal(err)
	}

	replay, err := recoverySnapshot(request)
	if err != nil {
		t.Fatal(err)
	}

	replayed, err := attemptHash("ctrlplane.managed.observation-request.test", replay)
	if err != nil || replayed != first {
		t.Fatal("stable request lost observation identity", err)
	}

	request.Command.ID = id.New(managed.PrefixOperation)

	changed, err := attemptHash("ctrlplane.managed.observation-request.test", request)
	if err != nil || changed == first {
		t.Fatal("caller operation not part of identity", err)
	}

	var store managed.ProviderInvocationStore = (*Repository)(nil)

	_ = store
}

func TestInvocationMarkerMemberBatchRollbackSmallAndMaximum(t *testing.T) {
	for _, count := range []int{1, managed.MaxRecoveryBindings} {
		fixture, selected, commits := invocationMarkerHistoryFixture(t, count)
		key := selected.permission.Facts.Key

		scope, err := attemptScope(key.AttemptKey.Key)
		if err != nil {
			t.Fatal(err)
		}

		receipt, err := selected.marker.Acceptance.Receipt.Value()
		if err != nil {
			t.Fatal(err)
		}

		delete(fixture.rows, [3]string{string(managed.RecoveryMarkerRecord), scope, selected.current.MarkerKey})
		delete(fixture.rows, [3]string{recoveryAcceptanceKind, scope, selected.current.MarkerKey})
		delete(fixture.receipts, attemptReceiptKey{key.AttemptKey.Key, receipt.ID, receipt.Digest})

		for index := range count {
			locator, keyErr := invocationMarkerMemberKey(key, index)
			if keyErr != nil {
				t.Fatal(keyErr)
			}

			delete(fixture.rows, [3]string{invocationMarkerMembersKind, scope, locator})
		}

		staged := &recoveryActivationRows{ControllerTransaction: fixture, key: key.AttemptKey.Key, receipt: receipt}

		reference, err := recoveryWriteFact(t.Context(), staged, key.AttemptKey, *selected.marker)
		if err != nil {
			t.Fatal(err)
		}

		if err = stageInvocationMarkerMembers(t.Context(), staged, *selected.marker, reference, commits); err != nil {
			t.Fatal(err)
		}

		if err = stageInvocationCurrent(t.Context(), staged, key, selected.current); err != nil {
			t.Fatal(err)
		}

		if err = stageInvocationStatus(t.Context(), staged, selected, true); err != nil {
			t.Fatal(err)
		}

		before := cloneActivationFixture(fixture)
		target := &managed.Target{Identity: managed.Identity{Key: key.AttemptKey.Key}, Revision: 1, LeaseUntil: receipt.LeaseUntil}
		backend := &activationTransactionFixture{state: fixture, target: target}
		apply := func() error {
			return New(backend).controller(t.Context(), "marker-member-fixture", []managed.Key{target.Key}, func(tx ControllerTransaction) error {
				_, atomicErr := tx.Atomic(t.Context(), target.Key, receipt.ID, receipt.Digest, func(current *managed.Target, _ time.Time) (managed.Receipt, error) {
					if writeErr := applyRecoveryActivationRows(t.Context(), tx, staged); writeErr != nil {
						return managed.Receipt{}, writeErr
					}

					current.Revision = receipt.Revision

					return receipt, nil
				})

				return atomicErr
			})
		}

		for failAt := 1; failAt <= len(staged.rows)+1; failAt++ {
			backend.failAt, backend.failReceipt = failAt, failAt == len(staged.rows)+1
			if err = apply(); !errors.Is(err, managed.ErrUnavailable) {
				t.Fatal("injected batch failure ignored", count, failAt, err)
			}

			if !reflect.DeepEqual(before.rows, fixture.rows) || !reflect.DeepEqual(before.receipts, fixture.receipts) || target.Revision != 1 {
				t.Fatal("partial marker custody persisted", count, failAt)
			}
		}

		backend.failAt, backend.failReceipt = 0, false

		if err = apply(); err != nil {
			t.Fatal(err)
		}

		if _, err = invocationMarkerMembersHeld(t.Context(), fixture, selected); err != nil {
			t.Fatal("committed member set incomplete", count, err)
		}
	}
}
