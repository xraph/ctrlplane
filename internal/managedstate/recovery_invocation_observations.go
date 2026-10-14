package managedstate

import (
	"context"
	"reflect"
	"strconv"
	"time"

	"github.com/xraph/ctrlplane/managed"
)

// invocationMarkerMember verifies independently retained commit and permission-time proof
// rows against the freshly selected native guard. A historical commit is not a grant.
func invocationMarkerMember(ctx context.Context, tx ControllerTransaction, selected retrySelected, member invocationProofMember, commitReference managed.RecoveryAssociation, now time.Time) (managed.RecoveryAssociation, error) {
	if commitReference.Kind != managed.RecoveryCommitRecord {
		return managed.RecoveryAssociation{}, managed.ErrAuthority
	}

	commit, err := recoveryReadFact[managed.ProviderNativeCommitFacts](ctx, tx, selected.permission.Facts.Key.AttemptKey, commitReference)
	if err != nil {
		return managed.RecoveryAssociation{}, err
	}

	return invocationCommitFactsHeld(ctx, tx, selected, member, commit.Facts, commit.Acceptance.Receipt.AcceptedAt, now)
}

// invocationCommitFactsHeld checks the complete bounded permission/proof/intent graph.
// Before verification, acceptedAt is the inspection time; no local acceptance is implied.
func invocationCommitFactsHeld(ctx context.Context, tx ControllerTransaction, selected retrySelected, member invocationProofMember, facts managed.ProviderNativeCommitFacts, acceptedAt, now time.Time) (managed.RecoveryAssociation, error) {
	var zero managed.RecoveryAssociation

	key := selected.permission.Facts.Key

	permissionDigest, err := recoveryPermissionDigest(selected.permission.Facts.Permission)
	if err != nil {
		return zero, err
	}

	proofKey, err := invocationPermissionProofKey(key, selected.permission.Acceptance.OperationID, member.bindingDigest)
	if err != nil {
		return zero, err
	}

	permissionProof, err := recoveryReadNamedFact[managed.ProviderPermissionProof](ctx, tx, key.AttemptKey, managed.RecoveryPermissionProofRecord, proofKey)
	if err != nil {
		return zero, err
	}

	proofReference, err := managed.RecoveryAssociationFor(proofKey, permissionProof)
	if err != nil {
		return zero, err
	}

	if permissionProof.Acceptance != selected.permission.Acceptance || permissionProof.Facts.Invocation != key || permissionProof.Facts.BindingDigest != member.bindingDigest || permissionProof.Facts.PermissionDigest != permissionDigest {
		return zero, managed.ErrAuthority
	}

	originalProof, err := recoveryReadFact[managed.RemovalRevalidationFacts](ctx, tx, key.AttemptKey, permissionProof.Facts.Proof)
	if err != nil {
		return zero, err
	}

	if originalProof.Facts.Binding != member.binding || originalProof.Facts.NativeMaterialDigest != permissionProof.Facts.NativeMaterialDigest || originalProof.Facts.GuardID != member.proof.Facts.GuardID || originalProof.Facts.GuardRootDigest != member.proof.Facts.GuardRootDigest || !managed.RecoveryProofFresh(originalProof.Facts, selected.permission.Facts.Permission.IssuedAt) {
		return zero, managed.ErrAuthority
	}

	if facts.Invocation != key || facts.Binding != member.binding || facts.PermissionDigest != permissionDigest || facts.PermissionProof != proofReference || facts.CurrentProof != member.current.Proof || facts.PermissionProofDigest != permissionProof.Digest || facts.CurrentProofDigest != member.proof.Digest || facts.GuardID != member.proof.Facts.GuardID || facts.GuardRootDigest != member.proof.Facts.GuardRootDigest || facts.NativeAcceptedAt.Before(member.proof.Facts.NativeAcceptedAt) || facts.NativeAcceptedAt.Before(selected.permission.Facts.Permission.IssuedAt) || facts.NativeAcceptedAt.After(now) || acceptedAt.Before(facts.NativeAcceptedAt) || acceptedAt.After(now) {
		return zero, managed.ErrAuthority
	}

	intent, err := recoveryReadFact[managed.RecoveryNativeIntent](ctx, tx, key.AttemptKey, facts.Intent)
	if err != nil {
		return zero, err
	}

	if err = recoveryIntentMatches(intent, facts.Intent, member.binding, "commit", facts.NativeRequestID, facts.NativeRequestDigest); err != nil {
		return zero, err
	}

	if intent.Facts.Permission == nil || *intent.Facts.Permission != selected.current.Permission || intent.Facts.Proof == nil || *intent.Facts.Proof != member.current.Proof {
		return zero, managed.ErrAuthority
	}

	return proofReference, nil
}

// stageInvocationMarker requires one exact commit per descriptor-ordered live binding.
// The caller derives commit references from accepted native intent custody. No public
// resolver or raw caller mapping is exposed by this private staging function.
func stageInvocationMarker(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, selected retrySelected, expected string, commits []managed.RecoveryAssociation, acceptance managed.RecoveryAcceptance, now time.Time) (*recoveryActivationRows, managed.RecoveryRecord[managed.ProviderInvocationMarker], error) {
	var zero managed.RecoveryRecord[managed.ProviderInvocationMarker]
	if selected.current.Ordinal == "0" && !invocationGuardedOriginal(selected) || selected.current.Digest != expected || selected.marker != nil || selected.result != nil || selected.failure != nil || selected.closure != nil {
		return nil, zero, managed.ErrBlocked
	}

	members, err := invocationLiveProofsHeld(ctx, tx, target, view, now)
	if err != nil {
		return nil, zero, err
	}

	if len(commits) != len(members) || selected.permission.Facts.PermissionProofCount != managed.Decimal(strconv.Itoa(len(members))) {
		return nil, zero, managed.ErrAuthority
	}

	permissionProofs := make([]managed.RecoveryAssociation, 0, len(members))

	currentProofs := make([]managed.RecoveryAssociation, 0, len(members))
	for index, member := range members {
		proof, readErr := invocationMarkerMember(ctx, tx, selected, member, commits[index], now)
		if readErr != nil {
			return nil, zero, readErr
		}

		permissionProofs = append(permissionProofs, proof)
		currentProofs = append(currentProofs, member.current.Proof)
	}

	proofSet, err := attemptHash("ctrlplane.managed.permission-proof-set.v1", permissionProofs)
	if err != nil {
		return nil, zero, err
	}

	if proofSet != selected.permission.Facts.PermissionProofSetDigest {
		return nil, zero, managed.ErrAuthority
	}

	commitSet, err := attemptHash("ctrlplane.managed.native-commit-set.v1", commits)
	if err != nil {
		return nil, zero, err
	}

	currentSet, err := attemptHash("ctrlplane.managed.current-proof-set.v1", currentProofs)
	if err != nil {
		return nil, zero, err
	}

	selectionDigest, err := retrySelectionDigest(selected.current)
	if err != nil {
		return nil, zero, err
	}

	permissionDigest, err := recoveryPermissionDigest(selected.permission.Facts.Permission)
	if err != nil {
		return nil, zero, err
	}

	facts := managed.ProviderInvocationMarker{Key: selected.permission.Facts.Key, RootDigest: selected.root.Digest, PermissionDigest: permissionDigest, SelectionDigest: selectionDigest, NativeCommitSetDigest: commitSet, CurrentProofSetDigest: currentSet, BindingCount: managed.Decimal(strconv.Itoa(len(members))), AcceptedAt: now.UTC()}

	marker, err := activationRecord(managed.RecoveryMarkerRecord, facts, acceptance)
	if err != nil {
		return nil, zero, err
	}

	receipt, err := acceptance.Receipt.Value()
	if err != nil {
		return nil, zero, err
	}

	staged := &recoveryActivationRows{ControllerTransaction: tx, key: target.Key, receipt: receipt}

	reference, err := recoveryWriteFact(ctx, staged, attemptKey(view), marker)
	if err != nil {
		return nil, zero, err
	}

	if err = stageInvocationMarkerMembers(ctx, staged, marker, reference, commits); err != nil {
		return nil, zero, err
	}

	current := selected.current

	current.MarkerKey = reference.Key
	if err = stageInvocationCurrent(ctx, staged, facts.Key, current); err != nil {
		return nil, zero, err
	}

	return staged, marker, nil
}

func stageInvocationCurrent(ctx context.Context, staged *recoveryActivationRows, key managed.ProviderInvocationKey, current managed.ProviderStepRetryCurrent) error {
	var err error

	current.Digest, err = managed.RetryCurrentDigest(current)
	if err != nil {
		return err
	}

	scope, err := attemptScope(key.AttemptKey.Key)
	if err != nil {
		return err
	}

	stepKey, err := retryStepKey(key.AttemptKey, key.Step)
	if err != nil {
		return err
	}

	return attemptWrite(ctx, staged, retryCurrentKind, scope, stepKey, current, false, managed.MaxStepRetryPointer)
}

// stageInvocationResult retains each accepted observation under its own receipt. It changes
// only the exact selected invocation. Older uncertain observations stay addressable.
func stageInvocationResult(ctx context.Context, tx ControllerTransaction, selected retrySelected, facts managed.ProviderInvocationResult, acceptance managed.RecoveryAcceptance, now time.Time) (*recoveryActivationRows, managed.RecoveryRecord[managed.ProviderInvocationResult], error) {
	var zero managed.RecoveryRecord[managed.ProviderInvocationResult]
	if selected.failure != nil || selected.closure != nil || facts.Key != selected.permission.Facts.Key || facts.RootDigest != selected.root.Digest {
		return nil, zero, managed.ErrConflict
	}

	permissionDigest, err := recoveryPermissionDigest(selected.permission.Facts.Permission)
	if err != nil {
		return nil, zero, err
	}

	if facts.PermissionDigest != permissionDigest || !reflect.DeepEqual(facts.Result.Permission, selected.permission.Facts.Permission) {
		return nil, zero, managed.ErrAuthority
	}

	wire := managed.WireProviderStepRecord{Permission: selected.permission.Facts.Permission, Result: &facts.Result}

	decoded, err := wire.Value()
	if err != nil {
		return nil, zero, err
	}

	result := decoded.Result
	if result == nil || !evidence(result.Evidence) || result.ObservedAt.Before(result.Permission.IssuedAt) || result.ObservedAt.After(now) || result.Outcome != managed.PortTerminal && result.Outcome != managed.PortUnavailable {
		return nil, zero, managed.ErrInvalid
	}

	if result.Outcome == managed.PortTerminal && (result.ResourceID == "" || result.ResourceID != result.Permission.ResourceID) {
		return nil, zero, managed.ErrAuthority
	}

	if selected.result != nil && selected.result.Facts.Result.Outcome == managed.PortTerminal {
		return nil, zero, managed.ErrConflict
	}

	if (selected.current.Ordinal != "0" || invocationGuardedOriginal(selected)) && selected.marker == nil {
		return nil, zero, managed.ErrAuthority
	}

	record, err := activationRecord(managed.RecoveryResultRecord, facts, acceptance)
	if err != nil {
		return nil, zero, err
	}

	receipt, err := acceptance.Receipt.Value()
	if err != nil {
		return nil, zero, err
	}

	staged := &recoveryActivationRows{ControllerTransaction: tx, key: facts.Key.AttemptKey.Key, receipt: receipt}

	reference, err := recoveryWriteFact(ctx, staged, facts.Key.AttemptKey, record)
	if err != nil {
		return nil, zero, err
	}
	// Late uncertainty is retained but cannot displace a later observation.
	if selected.result == nil || result.Outcome == managed.PortTerminal || !result.ObservedAt.Before(selected.result.Facts.Result.ObservedAt) {
		current := selected.current

		current.ResultKey = reference.Key
		if err = stageInvocationCurrent(ctx, staged, facts.Key, current); err != nil {
			return nil, zero, err
		}
	}

	return staged, record, nil
}
