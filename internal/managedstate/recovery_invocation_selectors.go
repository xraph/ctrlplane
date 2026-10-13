package managedstate

import (
	"context"
	"errors"
	"reflect"
	"strconv"

	"github.com/xraph/ctrlplane/managed"
)

const invocationStatusKind = "recovery_invocation_status"
const invocationCommitCurrentKind = "recovery_invocation_commit_current"
const maxInvocationStatus = 4096

type invocationStatus struct {
	SchemaVersion managed.Decimal               `json:"schema_version"`
	Key           managed.ProviderInvocationKey `json:"key"`
	Permission    managed.RecoveryAssociation   `json:"permission"`
	Marker        *managed.RecoveryAssociation  `json:"marker,omitempty"`
	Failure       *managed.RecoveryAssociation  `json:"failure,omitempty"`
	Closure       *managed.RecoveryAssociation  `json:"closure,omitempty"`
	Result        *managed.RecoveryAssociation  `json:"result,omitempty"`
}

type invocationCommitCurrent struct {
	SchemaVersion managed.Decimal               `json:"schema_version"`
	Invocation    managed.ProviderInvocationKey `json:"invocation"`
	BindingDigest string                        `json:"binding_digest"`
	Permission    managed.RecoveryAssociation   `json:"permission"`
	CurrentProof  managed.RecoveryAssociation   `json:"current_proof"`
	Commit        managed.RecoveryAssociation   `json:"commit"`
}

func invocationCommitCurrentKey(invocation managed.ProviderInvocationKey, binding managed.RemovalRecoveryBinding) (string, error) {
	if invocation.AttemptKey != binding.Key.AttemptKey {
		return "", managed.ErrAuthority
	}

	return attemptHash("ctrlplane.managed.invocation-commit-current.v1", struct {
		Invocation managed.ProviderInvocationKey `json:"invocation"`
		Binding    managed.AttemptBindingKey     `json:"binding"`
	}{invocation, binding.Key})
}

func invocationStatusRead(ctx context.Context, tx ControllerTransaction, key managed.ProviderInvocationKey) (invocationStatus, error) {
	var zero invocationStatus

	scope, err := attemptScope(key.AttemptKey.Key)
	if err != nil {
		return zero, err
	}

	locator, err := invocationLocatorKey(key)
	if err != nil {
		return zero, err
	}

	status, err := attemptRead[invocationStatus](ctx, tx, invocationStatusKind, scope, locator, maxInvocationStatus)
	if err != nil {
		return zero, err
	}

	if status.SchemaVersion != "1" || status.Key != key || status.Permission.Kind != managed.RecoveryPermissionRecord || (status.Failure == nil) != (status.Closure == nil) || status.Result != nil && status.Closure != nil {
		return zero, managed.ErrUnavailable
	}

	for _, reference := range []*managed.RecoveryAssociation{&status.Permission, status.Marker, status.Failure, status.Closure, status.Result} {
		if reference != nil {
			if err = managed.ValidateRecoveryAssociation(*reference); err != nil {
				return zero, err
			}
		}
	}

	return status, nil
}

func stageInvocationStatus(ctx context.Context, tx ControllerTransaction, selected retrySelected, insert bool) error {
	key := selected.permission.Facts.Key

	status := invocationStatus{SchemaVersion: "1", Key: key, Permission: selected.current.Permission}
	if selected.marker != nil {
		ref, err := managed.RecoveryAssociationFor(selected.current.MarkerKey, *selected.marker)
		if err != nil {
			return err
		}

		status.Marker = &ref
	}

	if selected.failure != nil {
		ref, err := managed.RecoveryAssociationFor(selected.current.FailureKey, *selected.failure)
		if err != nil {
			return err
		}

		status.Failure = &ref
	}

	if selected.closure != nil {
		ref, err := managed.RecoveryAssociationFor(selected.current.ClosureKey, *selected.closure)
		if err != nil {
			return err
		}

		status.Closure = &ref
	}

	if selected.result != nil {
		ref, err := managed.RecoveryAssociationFor(selected.current.ResultKey, *selected.result)
		if err != nil {
			return err
		}

		status.Result = &ref
	}

	if !insert {
		previous, err := invocationStatusRead(ctx, tx, key)
		if err != nil {
			return err
		}

		if previous.Permission != status.Permission || previous.Marker != nil && !reflect.DeepEqual(previous.Marker, status.Marker) || previous.Closure != nil && !reflect.DeepEqual(previous.Closure, status.Closure) || previous.Failure != nil && !reflect.DeepEqual(previous.Failure, status.Failure) {
			return managed.ErrConflict
		}

		if previous.Result != nil && !reflect.DeepEqual(previous.Result, status.Result) {
			result, readErr := recoveryReadFact[managed.ProviderInvocationResult](ctx, tx, key.AttemptKey, *previous.Result)
			if readErr != nil {
				return readErr
			}

			if result.Facts.Result.Outcome == managed.PortTerminal || status.Result == nil {
				return managed.ErrConflict
			}
		}
	}

	scope, err := attemptScope(key.AttemptKey.Key)
	if err != nil {
		return err
	}

	locator, err := invocationLocatorKey(key)
	if err != nil {
		return err
	}

	return attemptWrite(ctx, tx, invocationStatusKind, scope, locator, status, insert, maxInvocationStatus)
}

func invocationCommitSelectionsHeld(ctx context.Context, tx ControllerTransaction, selected retrySelected, members []invocationProofMember) ([]managed.RecoveryAssociation, error) {
	if len(members) == 0 || len(members) > managed.MaxRecoveryBindings {
		return nil, managed.ErrUnavailable
	}

	key := selected.permission.Facts.Key

	scope, err := attemptScope(key.AttemptKey.Key)
	if err != nil {
		return nil, err
	}

	references := make([]managed.RecoveryAssociation, 0, len(members))
	for _, member := range members {
		selectorKey, keyErr := invocationCommitCurrentKey(key, member.binding)
		if keyErr != nil {
			return nil, keyErr
		}

		selector, readErr := attemptRead[invocationCommitCurrent](ctx, tx, invocationCommitCurrentKind, scope, selectorKey, maxInvocationStatus)
		if readErr != nil {
			return nil, readErr
		}

		if selector.SchemaVersion != "1" || selector.Invocation != key || selector.BindingDigest != member.bindingDigest || selector.Permission != selected.current.Permission || selector.CurrentProof != member.current.Proof || selector.Commit.Kind != managed.RecoveryCommitRecord {
			return nil, managed.ErrAuthority
		}

		if _, readErr = recoveryReadFact[managed.ProviderNativeCommitFacts](ctx, tx, key.AttemptKey, selector.Commit); readErr != nil {
			return nil, readErr
		}

		references = append(references, selector.Commit)
	}

	return references, nil
}

func stageInvocationNativeCommitSelector(ctx context.Context, tx ControllerTransaction, selected retrySelected, member invocationProofMember, commit managed.RecoveryRecord[managed.ProviderNativeCommitFacts], reference managed.RecoveryAssociation) error {
	key := selected.permission.Facts.Key
	if commit.Facts.Invocation != key || commit.Facts.Binding != member.binding || commit.Facts.CurrentProof != member.current.Proof || selected.marker != nil || selected.closure != nil || selected.result != nil {
		return managed.ErrBlocked
	}

	exact, exactErr := recoveryReadFact[managed.ProviderNativeCommitFacts](ctx, tx, key.AttemptKey, reference)
	if exactErr != nil {
		return exactErr
	}

	if !reflect.DeepEqual(exact, commit) {
		return managed.ErrAuthority
	}

	scope, err := attemptScope(key.AttemptKey.Key)
	if err != nil {
		return err
	}

	selectorKey, err := invocationCommitCurrentKey(key, member.binding)
	if err != nil {
		return err
	}

	selector := invocationCommitCurrent{SchemaVersion: "1", Invocation: key, BindingDigest: member.bindingDigest, Permission: selected.current.Permission, CurrentProof: member.current.Proof, Commit: reference}
	previous, err := attemptRead[invocationCommitCurrent](ctx, tx, invocationCommitCurrentKind, scope, selectorKey, maxInvocationStatus)

	insert := errors.Is(err, managed.ErrNotFound)
	if err != nil && !insert {
		return err
	}

	if !insert {
		if previous.SchemaVersion != "1" || previous.Invocation != key || previous.BindingDigest != member.bindingDigest || previous.Permission != selected.current.Permission {
			return managed.ErrAuthority
		}

		accepted, readErr := recoveryReadFact[managed.ProviderNativeCommitFacts](ctx, tx, key.AttemptKey, previous.Commit)
		if readErr != nil {
			return readErr
		}

		if accepted.Facts.Invocation != key || accepted.Facts.Binding != member.binding || accepted.Facts.PermissionProof != commit.Facts.PermissionProof || accepted.Facts.GuardID != commit.Facts.GuardID || accepted.Facts.GuardRootDigest != commit.Facts.GuardRootDigest || commit.Facts.NativeAcceptedAt.Before(accepted.Facts.NativeAcceptedAt) {
			return managed.ErrConflict
		}
	}

	return attemptWrite(ctx, tx, invocationCommitCurrentKind, scope, selectorKey, selector, insert, maxInvocationStatus)
}

const invocationMarkerMembersKind = "recovery_invocation_marker_members"

type invocationMarkerMemberReference struct {
	SchemaVersion managed.Decimal               `json:"schema_version"`
	Key           managed.ProviderInvocationKey `json:"key"`
	Marker        managed.RecoveryAssociation   `json:"marker"`
	Index         managed.Decimal               `json:"index"`
	Commit        managed.RecoveryAssociation   `json:"commit"`
}

func invocationMarkerMemberKey(key managed.ProviderInvocationKey, index int) (string, error) {
	if index < 0 || index >= managed.MaxRecoveryBindings {
		return "", managed.ErrInvalid
	}

	return attemptHash("ctrlplane.managed.invocation-marker-member.v1", struct {
		Invocation managed.ProviderInvocationKey `json:"invocation"`
		Index      int                           `json:"index"`
	}{key, index})
}

func stageInvocationMarkerMembers(ctx context.Context, tx ControllerTransaction, marker managed.RecoveryRecord[managed.ProviderInvocationMarker], reference managed.RecoveryAssociation, commits []managed.RecoveryAssociation) error {
	if len(commits) == 0 || len(commits) > managed.MaxRecoveryBindings || marker.Facts.BindingCount != managed.Decimal(strconv.Itoa(len(commits))) {
		return managed.ErrInvalid
	}

	scope, err := attemptScope(marker.Facts.Key.AttemptKey.Key)
	if err != nil {
		return err
	}

	seen := map[managed.RecoveryAssociation]bool{}
	for index, commit := range commits {
		if commit.Kind != managed.RecoveryCommitRecord || seen[commit] {
			return managed.ErrInvalid
		}

		if err = managed.ValidateRecoveryAssociation(commit); err != nil {
			return err
		}

		seen[commit] = true

		key, keyErr := invocationMarkerMemberKey(marker.Facts.Key, index)
		if keyErr != nil {
			return keyErr
		}

		member := invocationMarkerMemberReference{SchemaVersion: "1", Key: marker.Facts.Key, Marker: reference, Index: managed.Decimal(strconv.Itoa(index)), Commit: commit}
		if err = attemptWrite(ctx, tx, invocationMarkerMembersKind, scope, key, member, true, maxInvocationStatus); err != nil {
			return err
		}
	}

	return nil
}

// invocationMarkerMembersHeld reconstructs the accepted permission/proof/commit sets
// at marker acceptance time. Expired historical proof never grants a fresh delegation.
func invocationMarkerMembersHeld(ctx context.Context, tx ControllerTransaction, selected retrySelected) ([]managed.RecoveryAssociation, error) {
	if selected.marker == nil {
		return nil, managed.ErrNotFound
	}

	key := selected.permission.Facts.Key

	scope, err := attemptScope(key.AttemptKey.Key)
	if err != nil {
		return nil, err
	}

	markerRef, err := managed.RecoveryAssociationFor(selected.current.MarkerKey, *selected.marker)
	if err != nil {
		return nil, err
	}

	count, err := selected.marker.Facts.BindingCount.Uint64()
	if err != nil || count == 0 || count > managed.MaxRecoveryBindings {
		return nil, managed.ErrUnavailable
	}

	commits := make([]managed.RecoveryAssociation, 0, int(count))
	for index := range int(count) {
		locator, keyErr := invocationMarkerMemberKey(key, index)
		if keyErr != nil {
			return nil, keyErr
		}

		member, readErr := attemptRead[invocationMarkerMemberReference](ctx, tx, invocationMarkerMembersKind, scope, locator, maxInvocationStatus)
		if readErr != nil {
			return nil, readErr
		}

		if member.SchemaVersion != "1" || member.Key != key || member.Marker != markerRef || member.Index != managed.Decimal(strconv.Itoa(index)) || member.Commit.Kind != managed.RecoveryCommitRecord {
			return nil, managed.ErrUnavailable
		}

		commits = append(commits, member.Commit)
	}

	permissionProofs := make([]managed.RecoveryAssociation, 0, len(commits))
	currentProofs := make([]managed.RecoveryAssociation, 0, len(commits))
	seen := map[string]bool{}

	for _, commitRef := range commits {
		commit, readErr := recoveryReadFact[managed.ProviderNativeCommitFacts](ctx, tx, key.AttemptKey, commitRef)
		if readErr != nil {
			return nil, readErr
		}

		proof, readErr := recoveryReadFact[managed.RemovalRevalidationFacts](ctx, tx, key.AttemptKey, commit.Facts.CurrentProof)
		if readErr != nil {
			return nil, readErr
		}

		bindingDigest, digestErr := recoveryBindingKey(commit.Facts.Binding)
		if digestErr != nil {
			return nil, digestErr
		}

		if seen[bindingDigest] || proof.Facts.Binding != commit.Facts.Binding || !managed.RecoveryProofFresh(proof.Facts, selected.marker.Facts.AcceptedAt) {
			return nil, managed.ErrUnavailable
		}

		seen[bindingDigest] = true
		member := invocationProofMember{binding: commit.Facts.Binding, bindingDigest: bindingDigest, current: managed.RemovalRevalidationCurrent{Proof: commit.Facts.CurrentProof}, proof: proof}

		permissionProof, memberErr := invocationMarkerMember(ctx, tx, selected, member, commitRef, selected.marker.Facts.AcceptedAt)
		if memberErr != nil {
			return nil, memberErr
		}

		permissionProofs = append(permissionProofs, permissionProof)
		currentProofs = append(currentProofs, commit.Facts.CurrentProof)
	}

	permissionDigest, err := attemptHash("ctrlplane.managed.permission-proof-set.v1", permissionProofs)
	if err != nil {
		return nil, err
	}

	commitDigest, err := attemptHash("ctrlplane.managed.native-commit-set.v1", commits)
	if err != nil {
		return nil, err
	}

	currentDigest, err := attemptHash("ctrlplane.managed.current-proof-set.v1", currentProofs)
	if err != nil {
		return nil, err
	}

	permissionCount, err := selected.permission.Facts.PermissionProofCount.Uint64()
	if err != nil {
		return nil, err
	}

	if count != uint64(len(commits)) || permissionCount != count || permissionDigest != selected.permission.Facts.PermissionProofSetDigest || commitDigest != selected.marker.Facts.NativeCommitSetDigest || currentDigest != selected.marker.Facts.CurrentProofSetDigest {
		return nil, managed.ErrUnavailable
	}

	return append([]managed.RecoveryAssociation(nil), commits...), nil
}
