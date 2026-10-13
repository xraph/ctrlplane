package managedstate

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strconv"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

const retryCurrentKind = "step_retry_current"
const retryPreviousKind = "step_retry_previous"

// recoveryReserveAfter contains only obligations remaining after the staged transition.
// Its caller derives these counts from checked full-set facts, never request arguments.
type recoveryReserveAfter struct {
	existing            uint64
	activationMissing   bool
	markerObligations   uint64
	closureObligations  uint64
	closeIntentsMissing uint64
	closeResultsMissing uint64
}

func recoveryRemainingReserve(after recoveryReserveAfter) (uint64, error) {
	if after.markerObligations > managed.MaxRecoverySteps || after.closureObligations > managed.MaxRecoverySteps || after.closeIntentsMissing > managed.MaxRecoveryBindings || after.closeResultsMissing > managed.MaxRecoveryBindings {
		return 0, managed.ErrUnavailable
	}

	additional := after.markerObligations + after.closureObligations + after.closeIntentsMissing + after.closeResultsMissing
	if after.activationMissing {
		additional++
	}

	if after.existing > math.MaxInt64-additional {
		return 0, managed.ErrCapacity
	}

	return after.existing + additional, nil
}

// recoveryCheckHeadroom reserves the staged finite tail against both independent counters.
// Reserved recording must consume its specific obligation before calling this function.
func recoveryCheckHeadroom(parent, attempt, cost uint64, after recoveryReserveAfter) error {
	reserve, err := recoveryRemainingReserve(after)
	if err != nil {
		return err
	}

	if cost > math.MaxInt64 || reserve > math.MaxInt64-cost || parent > math.MaxInt64-cost-reserve || attempt > math.MaxInt64-cost-reserve {
		return managed.ErrCapacity
	}

	return nil
}

func retryStepKey(k managed.AttemptKey, step managed.ProviderStepKey) (string, error) {
	if err := attemptVersion(k); err != nil {
		return "", err
	}

	if step.Key != k.Key || step.ProviderOperationID.Prefix() != managed.PrefixOperation || step.ActionID.Prefix() != managed.PrefixOperation || step.Kind != managed.StepRemoveContainer && step.Kind != managed.StepRemoveNetwork {
		return "", managed.ErrInvalid
	}

	return attemptHash("ctrlplane.managed.step-retry-key.v1", struct {
		Key  managed.AttemptKey      `json:"key"`
		Step managed.ProviderStepKey `json:"step"`
	}{k, step})
}

type retrySelected struct {
	root       managed.RecoveryRecord[managed.ProviderStepRetryRoot]
	current    managed.ProviderStepRetryCurrent
	previous   *managed.ProviderStepRetryPrevious
	permission managed.RecoveryRecord[managed.ProviderInvocationPermission]
	authority  *managed.RecoveryRecord[managed.ProviderStepRetryAuthorization]
	failure    *managed.RecoveryRecord[managed.ProviderInvocationFailure]
	closure    *managed.RecoveryRecord[managed.ProviderInvocationClosure]
	marker     *managed.RecoveryRecord[managed.ProviderInvocationMarker]
	result     *managed.RecoveryRecord[managed.ProviderInvocationResult]
}

// recoveryReadNamedFact reads a key committed by a validated pointer, then checks its receipt.
func recoveryReadNamedFact[T any](ctx context.Context, tx ControllerTransaction, k managed.AttemptKey, kind managed.RecoveryRecordKind, selected string) (managed.RecoveryRecord[T], error) {
	var out managed.RecoveryRecord[T]
	if !digest(selected) {
		return out, managed.ErrUnavailable
	}

	scope, err := attemptScope(k.Key)
	if err != nil {
		return out, err
	}

	raw, err := tx.ReadRecord(ctx, string(kind), scope, selected)
	if err != nil {
		return out, err
	}

	out, err = managed.DecodeRecoveryRecord[T](raw)
	if err != nil {
		return out, err
	}

	reference, err := managed.RecoveryAssociationFor(selected, out)
	if err != nil {
		return out, err
	}

	return recoveryReadFact[T](ctx, tx, k, reference)
}

// retryReadSelected verifies the selected certificate and exactly one predecessor association.
// It performs no list/count query and does not recurse into predecessor authority.
func retryReadSelected(ctx context.Context, tx ControllerTransaction, k managed.AttemptKey, rootReference managed.RecoveryAssociation) (retrySelected, error) {
	var selected retrySelected

	root, err := recoveryReadFact[managed.ProviderStepRetryRoot](ctx, tx, k, rootReference)
	if err != nil {
		return selected, err
	}

	if root.Facts.Profile != managed.ProviderStepRetryProfile || root.Facts.AttemptRef.AttemptID != k.AttemptID || root.Facts.AttemptRef.Generation != k.Generation || root.Facts.Step.Key != k.Key || root.Facts.OriginalPermission.Step != root.Facts.Step || root.Facts.OriginalPermission.ResourceID != root.Facts.ResourceID || root.Facts.OriginalPermission.SpecDigest != root.Facts.SpecDigest {
		return selected, managed.ErrUnavailable
	}

	originalReceipt, err := root.Facts.OriginalPermissionReceipt.Value()
	if err != nil {
		return selected, err
	}

	if originalReceipt.ID != root.Facts.OriginalPermission.CommandID || originalReceipt.Digest != root.Facts.OriginalPermission.Digest || managed.Decimal(strconv.FormatUint(originalReceipt.Revision, 10)) != root.Facts.OriginalPermission.Revision || !originalReceipt.AcceptedAt.Equal(root.Facts.OriginalPermission.IssuedAt) {
		return selected, managed.ErrUnavailable
	}

	if _, permissionErr := recoveryPermissionDigest(root.Facts.OriginalPermission); permissionErr != nil {
		return selected, permissionErr
	}

	if err = attemptReceiptCheck(ctx, tx, k.Key, originalReceipt); err != nil {
		return selected, err
	}

	stepKey, err := retryStepKey(k, root.Facts.Step)
	if err != nil {
		return selected, err
	}

	scope, err := attemptScope(k.Key)
	if err != nil {
		return selected, err
	}

	current, err := attemptRead[managed.ProviderStepRetryCurrent](ctx, tx, retryCurrentKind, scope, stepKey, managed.MaxStepRetryPointer)
	if err != nil {
		return selected, err
	}

	currentHash, err := managed.RetryCurrentDigest(current)
	if err != nil {
		return selected, err
	}

	ordinal, ordinalErr := current.Ordinal.Uint64()
	if ordinalErr != nil || ordinal > math.MaxInt64 || current.SchemaVersion != "1" || current.RootDigest != root.Digest || current.Digest != currentHash || current.CommandID.Prefix() != managed.PrefixOperation || current.Permission.Kind != managed.RecoveryPermissionRecord {
		return selected, managed.ErrUnavailable
	}

	permission, err := recoveryReadFact[managed.ProviderInvocationPermission](ctx, tx, k, current.Permission)
	if err != nil {
		return selected, err
	}

	p := permission.Facts

	invocation := managed.ProviderInvocationKey{AttemptKey: k, Step: root.Facts.Step, CommandID: current.CommandID}
	if p.Key != invocation || p.RootDigest != root.Digest || p.Ordinal != current.Ordinal || p.Permission.CommandID != current.CommandID || p.Permission.Step != root.Facts.Step || p.Permission.ResourceID != root.Facts.ResourceID || p.Permission.SpecDigest != root.Facts.SpecDigest || p.Permission.Physical != root.Facts.OriginalPermission.Physical || p.Permission.Seal != root.Facts.OriginalPermission.Seal || p.Permission.FenceDigest != root.Facts.OriginalPermission.FenceDigest || p.ProviderCommandDigest != root.Facts.ProviderCommandDigest {
		return selected, managed.ErrUnavailable
	}

	selected = retrySelected{root: root, current: current, permission: permission}
	if ordinal == 0 {
		if current.CommandID != root.Facts.OriginalPermission.CommandID || !reflect.DeepEqual(p.Permission, root.Facts.OriginalPermission) || current.PreviousDigest != "" || current.Authority != rootReference {
			return retrySelected{}, managed.ErrUnavailable
		}

		if _, previousErr := tx.ReadRecord(ctx, retryPreviousKind, scope, stepKey); !errors.Is(previousErr, managed.ErrNotFound) {
			if previousErr != nil {
				return retrySelected{}, previousErr
			}

			return retrySelected{}, managed.ErrUnavailable
		}
	} else {
		if err = retryReadCertificate(ctx, tx, k, scope, stepKey, &selected); err != nil {
			return retrySelected{}, err
		}
	}

	if err = retryReadSelectedTerminal(ctx, tx, k, &selected); err != nil {
		return retrySelected{}, err
	}

	return selected, nil
}

func retryReadCertificate(ctx context.Context, tx ControllerTransaction, k managed.AttemptKey, scope, stepKey string, selected *retrySelected) error {
	current := selected.current

	authority, err := recoveryReadFact[managed.ProviderStepRetryAuthorization](ctx, tx, k, current.Authority)
	if err != nil {
		return err
	}

	previous, err := attemptRead[managed.ProviderStepRetryPrevious](ctx, tx, retryPreviousKind, scope, stepKey, managed.MaxStepRetryPointer)
	if err != nil {
		return err
	}

	previousHash, err := managed.RetryPreviousDigest(previous)
	if err != nil {
		return err
	}

	previousOrdinal, previousErr := previous.Ordinal.Uint64()

	currentOrdinal, currentErr := current.Ordinal.Uint64()
	if previousErr != nil || currentErr != nil || previousOrdinal >= math.MaxInt64 || previousOrdinal+1 != currentOrdinal || previous.SchemaVersion != "1" || previous.Digest != previousHash || previous.Digest != current.PreviousDigest || previous.RootDigest != selected.root.Digest || previous.CommandID == current.CommandID || authority.Facts.Key != selected.permission.Facts.Key || authority.Facts.RootDigest != selected.root.Digest || authority.Facts.Ordinal != current.Ordinal || authority.Facts.Permission != current.Permission || !reflect.DeepEqual(authority.Facts.Previous, previous) {
		return managed.ErrUnavailable
	}

	// Read the named predecessor's certificate and receipt, but never its predecessor.
	if previousOrdinal == 0 {
		predecessor, readErr := recoveryReadFact[managed.ProviderStepRetryRoot](ctx, tx, k, previous.Authority)
		if readErr != nil {
			return readErr
		}

		originalDigest, digestErr := recoveryPermissionDigest(predecessor.Facts.OriginalPermission)
		if digestErr != nil {
			return digestErr
		}

		if predecessor.Digest != selected.root.Digest || previous.CommandID != predecessor.Facts.OriginalPermission.CommandID || previous.PermissionDigest != originalDigest {
			return managed.ErrUnavailable
		}
	} else {
		predecessor, readErr := recoveryReadFact[managed.ProviderStepRetryAuthorization](ctx, tx, k, previous.Authority)
		if readErr != nil {
			return readErr
		}

		if predecessor.Facts.RootDigest != selected.root.Digest || predecessor.Facts.Ordinal != previous.Ordinal || predecessor.Facts.Key.AttemptKey != k || predecessor.Facts.Key.Step != selected.root.Facts.Step || predecessor.Facts.Key.CommandID != previous.CommandID {
			return managed.ErrUnavailable
		}

		permission, permissionErr := recoveryReadFact[managed.ProviderInvocationPermission](ctx, tx, k, predecessor.Facts.Permission)
		if permissionErr != nil {
			return permissionErr
		}

		permissionDigest, digestErr := recoveryPermissionDigest(permission.Facts.Permission)
		if digestErr != nil {
			return digestErr
		}

		if permission.Facts.Key != predecessor.Facts.Key || permission.Facts.RootDigest != selected.root.Digest || permission.Facts.Ordinal != previous.Ordinal || permissionDigest != previous.PermissionDigest {
			return managed.ErrUnavailable
		}
	}

	failure, err := recoveryReadFact[managed.ProviderInvocationFailure](ctx, tx, k, previous.Failure)
	if err != nil {
		return err
	}

	closure, err := recoveryReadFact[managed.ProviderInvocationClosure](ctx, tx, k, previous.Closure)
	if err != nil {
		return err
	}

	predecessorKey := managed.ProviderInvocationKey{AttemptKey: k, Step: selected.root.Facts.Step, CommandID: previous.CommandID}
	if failure.Facts.Key != predecessorKey || closure.Facts.Key != predecessorKey || failure.Facts.RootDigest != selected.root.Digest || closure.Facts.RootDigest != selected.root.Digest || failure.Facts.PermissionDigest != previous.PermissionDigest || closure.Facts.PermissionDigest != previous.PermissionDigest || closure.Facts.FailureDigest != failure.Digest || failure.Acceptance != closure.Acceptance {
		return managed.ErrUnavailable
	}

	if err = retryClosedFailure(failure, closure); err != nil {
		return err
	}

	selected.previous = &previous
	selected.authority = &authority

	return nil
}

func retryClosedFailure(failureRecord managed.RecoveryRecord[managed.ProviderInvocationFailure], closureRecord managed.RecoveryRecord[managed.ProviderInvocationClosure]) error {
	failure := failureRecord.Facts

	closure := closureRecord.Facts
	if failureRecord.Acceptance != closureRecord.Acceptance || closure.ClosedAt.IsZero() || !closure.ClosedAt.Equal(closureRecord.Acceptance.Receipt.AcceptedAt) {
		return managed.ErrUnavailable
	}

	f := failure.Failure

	permissionDigest, err := recoveryPermissionDigest(managed.ProviderPermissionWire(f.Permission))
	if err != nil {
		return err
	}

	if f.SchemaVersion != "1" || f.ObservedAt.Before(f.Permission.IssuedAt) || len(f.Witness) > managed.MaxEvidence || (f.Reason != "authority_refused" && f.Reason != "request_binding_mismatch" && f.Reason != "invocation_closed") || f.Class != "local_no_dispatch" || f.ProducerProfile != managed.LocalNoDispatchProfile || f.Phase != "before_transport_delegate" || f.InvocationID != failure.Key.CommandID || f.Permission.CommandID != failure.Key.CommandID || f.Permission.Step != failure.Key.Step || permissionDigest != failure.PermissionDigest || f.AttemptRef.AttemptID != failure.Key.AttemptKey.AttemptID || f.AttemptRef.Generation != failure.Key.AttemptKey.Generation || !token(f.VerifierID) || len(f.Witness) == 0 || managed.Digest(f.Witness) != f.WitnessDigest || f.ObservedAt.IsZero() || closure.ClosedAt.Before(f.ObservedAt) || closure.ProducerProfile != f.ProducerProfile || closure.VerifierID != f.VerifierID {
		return managed.ErrUnavailable
	}

	return nil
}

// retryMarkerBeforeClosure checks the accepted order within the same parent authority.
// Receipt revisions establish order even when two callback timestamps are equal.
func retryMarkerBeforeClosure(marker managed.RecoveryRecord[managed.ProviderInvocationMarker], closure managed.RecoveryRecord[managed.ProviderInvocationClosure]) error {
	markerReceipt, err := marker.Acceptance.Receipt.Value()
	if err != nil {
		return managed.ErrUnavailable
	}

	closureReceipt, err := closure.Acceptance.Receipt.Value()
	if err != nil {
		return managed.ErrUnavailable
	}

	if marker.Facts.Key != closure.Facts.Key || marker.Facts.RootDigest != closure.Facts.RootDigest || marker.Facts.PermissionDigest != closure.Facts.PermissionDigest || markerReceipt.Revision == 0 || closureReceipt.Revision > math.MaxInt64 || markerReceipt.Revision >= closureReceipt.Revision || markerReceipt.AcceptedAt.After(closureReceipt.AcceptedAt) || !marker.Facts.AcceptedAt.Equal(markerReceipt.AcceptedAt) || !closure.Facts.ClosedAt.Equal(closureReceipt.AcceptedAt) {
		return managed.ErrUnavailable
	}

	return nil
}

func retryReadSelectedTerminal(ctx context.Context, tx ControllerTransaction, k managed.AttemptKey, selected *retrySelected) error {
	current := selected.current
	invocation := selected.permission.Facts.Key

	permissionDigest, digestErr := recoveryPermissionDigest(selected.permission.Facts.Permission)
	if digestErr != nil {
		return digestErr
	}

	if current.MarkerKey != "" {
		marker, err := recoveryReadNamedFact[managed.ProviderInvocationMarker](ctx, tx, k, managed.RecoveryMarkerRecord, current.MarkerKey)
		if err != nil {
			return err
		}

		selectionDigest, selectionErr := retrySelectionDigest(current)
		if selectionErr != nil {
			return selectionErr
		}

		if marker.Facts.Key != invocation || marker.Facts.RootDigest != selected.root.Digest || marker.Facts.PermissionDigest != permissionDigest || marker.Facts.SelectionDigest != selectionDigest {
			return managed.ErrUnavailable
		}

		selected.marker = &marker
	}

	if current.ResultKey != "" {
		result, err := recoveryReadNamedFact[managed.ProviderInvocationResult](ctx, tx, k, managed.RecoveryResultRecord, current.ResultKey)
		if err != nil {
			return err
		}

		if result.Facts.Key != invocation || result.Facts.RootDigest != selected.root.Digest || result.Facts.PermissionDigest != permissionDigest || !reflect.DeepEqual(result.Facts.Result.Permission, selected.permission.Facts.Permission) {
			return managed.ErrUnavailable
		}

		selected.result = &result
	}

	if (current.ClosureKey == "") != (current.FailureKey == "") {
		return managed.ErrUnavailable
	}

	if current.ClosureKey != "" {
		failure, err := recoveryReadNamedFact[managed.ProviderInvocationFailure](ctx, tx, k, managed.RecoveryFailureRecord, current.FailureKey)
		if err != nil {
			return err
		}

		closure, err := recoveryReadNamedFact[managed.ProviderInvocationClosure](ctx, tx, k, managed.RecoveryClosureRecord, current.ClosureKey)
		if err != nil {
			return err
		}

		if selected.result != nil || failure.Facts.Key != invocation || closure.Facts.Key != invocation || failure.Facts.RootDigest != selected.root.Digest || closure.Facts.RootDigest != selected.root.Digest || failure.Facts.PermissionDigest != permissionDigest || closure.Facts.PermissionDigest != permissionDigest || closure.Facts.FailureDigest != failure.Digest || failure.Acceptance != closure.Acceptance {
			return managed.ErrUnavailable
		}

		if err = retryClosedFailure(failure, closure); err != nil {
			return err
		}

		if selected.marker != nil {
			if err = retryMarkerBeforeClosure(*selected.marker, closure); err != nil {
				return err
			}
		}

		selected.failure = &failure
		selected.closure = &closure
	}

	return nil
}

// retryPredecessor constructs the next fixed certificate only from an affirmatively closed selection.
// Parent CAS, live lease, native protection, cutoff and staged reserve remain transition checks.
func retryPredecessor(selected retrySelected, expected string, command id.ID) (managed.ProviderStepRetryPrevious, managed.Decimal, error) {
	var previous managed.ProviderStepRetryPrevious
	if selected.current.Digest != expected || selected.failure == nil || selected.closure == nil || selected.result != nil || command.Prefix() != managed.PrefixOperation || command == selected.current.CommandID || command == selected.root.Facts.OriginalPermission.CommandID {
		return previous, "", managed.ErrBlocked
	}

	ordinal, err := selected.current.Ordinal.Uint64()
	if err != nil || ordinal >= math.MaxInt64 {
		return previous, "", managed.ErrCapacity
	}

	failureRef, err := managed.RecoveryAssociationFor(selected.current.FailureKey, *selected.failure)
	if err != nil {
		return previous, "", err
	}

	closureRef, err := managed.RecoveryAssociationFor(selected.current.ClosureKey, *selected.closure)
	if err != nil {
		return previous, "", err
	}

	permissionDigest, err := recoveryPermissionDigest(selected.permission.Facts.Permission)
	if err != nil {
		return previous, "", err
	}

	previous = managed.ProviderStepRetryPrevious{SchemaVersion: "1", RootDigest: selected.root.Digest, CommandID: selected.current.CommandID, Ordinal: selected.current.Ordinal, PermissionDigest: permissionDigest, Failure: failureRef, Closure: closureRef, Authority: selected.current.Authority}

	previous.Digest, err = managed.RetryPreviousDigest(previous)
	if err != nil {
		return managed.ProviderStepRetryPrevious{}, "", err
	}

	return previous, managed.Decimal(strconv.FormatUint(ordinal+1, 10)), nil
}

// recoveryPermissionDigest preserves the original raw local permission hash domain.
// Permission.Digest itself names the accepting request and must not be substituted here.
func recoveryPermissionDigest(wire managed.WireProviderStepPermission) (string, error) {
	permission, err := wire.Value()
	if err != nil {
		return "", err
	}

	raw, err := json.Marshal(permission)
	if err != nil {
		return "", err
	}

	return managed.Digest(raw), nil
}

// retrySelectionDigest excludes terminal indexes that arrive after selection.
// A saved marker remains bound to the same permission when a later closure resolves it.
func retrySelectionDigest(current managed.ProviderStepRetryCurrent) (string, error) {
	current.MarkerKey = ""
	current.ClosureKey = ""
	current.FailureKey = ""
	current.ResultKey = ""

	return managed.RetryCurrentDigest(current)
}
