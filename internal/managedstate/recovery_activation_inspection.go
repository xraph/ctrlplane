package managedstate

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"

	"github.com/xraph/ctrlplane/managed"
)

// readRecoveryCompatibleAttemptState keeps legacy readers on schema 1. Only the private
// recovery inspection session can decode schema 2, whose full associations are checked
// after the original attempt has passed its unchanged slot and lineage validation.
func readRecoveryCompatibleAttemptState(ctx context.Context, reads *attemptCheckedReads, scope, key string) (managed.AttemptState, *managed.RecoveryAssociation, error) {
	var zero managed.AttemptState

	raw, err := reads.ReadRecord(ctx, "removal_attempt_state", scope, key)
	if err != nil {
		return zero, nil, err
	}

	if len(raw) > managed.MaxAttemptFact {
		return zero, nil, managed.ErrCapacity
	}

	var header struct {
		SchemaVersion managed.Decimal `json:"schema_version"`
	}
	if json.Unmarshal(raw, &header) != nil {
		return zero, nil, managed.ErrUnavailable
	}

	if header.SchemaVersion == "1" {
		state, decodeErr := attemptDecode[managed.AttemptState](raw, managed.MaxAttemptFact)

		return state, nil, decodeErr
	}

	if header.SchemaVersion != "2" || !reads.recoveryInspection {
		return zero, nil, managed.ErrUnsupported
	}

	state, err := attemptDecode[recoveryAttemptState](raw, managed.MaxAttemptFact)
	if err != nil {
		return zero, nil, err
	}

	if state.Activation.Kind != managed.RecoveryActivationRecord {
		return zero, nil, managed.ErrUnavailable
	}

	if err = managed.ValidateRecoveryAssociation(state.Activation); err != nil {
		return zero, nil, err
	}

	return state.AttemptState, &state.Activation, nil
}

// validateRecoveryActivationInspectionHeld never calls an attempt reader. It extends an
// already checked original view with exact profile, selection, effect and receipt reads.
func validateRecoveryActivationInspectionHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView) error {
	if !view.validated || view.state.SchemaVersion != "2" || view.recoveryActivation == nil {
		return managed.ErrUnavailable
	}

	profile, err := recoveryReadFact[managed.RemovalRecoveryActivation](ctx, tx, attemptKey(view), *view.recoveryActivation)
	if err != nil {
		return err
	}

	facts := profile.Facts

	receipt, err := profile.Acceptance.Receipt.Value()
	if err != nil {
		return err
	}

	if facts.Key != attemptKey(view) || facts.AttemptRef != view.ref || facts.Profile != managed.RemovalRecoveryProfile || facts.RetryProfile != managed.ProviderStepRetryProfile || facts.ProviderOperationID != view.descriptor.ProviderOperationID || facts.AdmissionDigest == "" || facts.AdmissionDigest != view.state.AdmissionDigest || !facts.ActivatedAt.Equal(receipt.AcceptedAt) || receipt.Revision > target.Revision {
		return managed.ErrUnavailable
	}

	selections, err := recoveryOriginalSelectionsHeld(ctx, tx, target, view)
	if err != nil {
		return err
	}

	digest, err := attemptHash("ctrlplane.managed.recovery-original-selections.v1", selections)
	if err != nil {
		return err
	}

	if facts.OriginalSelectionDigest != digest || facts.StepCount != managed.Decimal(strconv.Itoa(len(selections))) {
		return managed.ErrUnavailable
	}

	bindingCount := 0

	for _, reservationID := range view.descriptor.ReservationIDs {
		binding, readErr := reservation(target, reservationID)
		if readErr != nil {
			return readErr
		}

		if binding.Phase == managed.Revoked {
			continue
		}

		bindingCount++

		if _, readErr = recoveryBindingInspectionHeld(ctx, tx, target, view, attemptBinding(view, reservationID)); readErr != nil {
			return readErr
		}
	}

	if facts.BindingCount != managed.Decimal(strconv.Itoa(bindingCount)) {
		return managed.ErrUnavailable
	}

	if view.state.RevocationDigest != "" {
		if _, err = recoveryCutoffHeld(ctx, tx, view); err != nil {
			return err
		}
	}

	for index, selection := range selections {
		if err = inspectRecoveryFixedSelection(ctx, tx, target, view, profile, selection, index); err != nil {
			return err
		}
	}

	return validateRecoveryTerminalInspectionHeld(ctx, tx, target, view)
}

func inspectRecoveryFixedSelection(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, profile managed.RecoveryRecord[managed.RemovalRecoveryActivation], selection recoveryOriginalSelection, index int) error {
	stepKey, err := retryStepKey(attemptKey(view), selection.Step)
	if err != nil {
		return err
	}

	fixed, err := attemptRead[recoveryFixedSelection](ctx, tx, recoveryFixedSelectionKind, view.scope, stepKey, managed.MaxStepRetryPointer)
	if err != nil {
		return err
	}

	if fixed.SchemaVersion != "1" || fixed.AttemptRef != view.ref || fixed.Step != selection.Step || fixed.Disposition != selection.Disposition || fixed.Activation != *view.recoveryActivation {
		return managed.ErrUnavailable
	}

	if selection.Disposition == "unissued" {
		if selection.Association != nil || fixed.Root != nil {
			return managed.ErrUnavailable
		}

		for _, kind := range []string{retryCurrentKind, retryPreviousKind} {
			if _, readErr := tx.ReadRecord(ctx, kind, view.scope, stepKey); !errors.Is(readErr, managed.ErrNotFound) {
				if readErr != nil {
					return readErr
				}

				return managed.ErrUnavailable
			}
		}

		rootKey, keyErr := recoveryFactKey(attemptKey(view), managed.RecoveryRootRecord, profile.Acceptance.OperationID, managed.ProviderStepRetryRoot{Step: selection.Step})
		if keyErr != nil {
			return keyErr
		}

		for _, kind := range []string{string(managed.RecoveryRootRecord), recoveryAcceptanceKind} {
			if _, readErr := tx.ReadRecord(ctx, kind, view.scope, rootKey); !errors.Is(readErr, managed.ErrNotFound) {
				if readErr != nil {
					return readErr
				}

				return managed.ErrUnavailable
			}
		}

		return nil
	}

	if selection.Disposition != "original" || selection.Association == nil || fixed.Root == nil || fixed.Root.Kind != managed.RecoveryRootRecord {
		return managed.ErrUnavailable
	}

	record, association, err := attemptProviderRead(ctx, tx, target, view, selection.Step, index)
	if err != nil {
		return err
	}

	original := managed.ProviderEffectOriginal{Key: managed.ProviderEffectBindingKey{AttemptKey: attemptKey(view), Step: selection.Step, OriginalCommandID: record.Permission.CommandID}, AttemptRef: view.ref, Permission: managed.ProviderPermissionWire(record.Permission), Receipt: association.Receipt}

	expected, err := providerEffectRootHeld(ctx, tx, original)
	if err != nil {
		return err
	}

	selected, err := retryReadSelected(ctx, tx, attemptKey(view), *fixed.Root)
	if err != nil {
		return err
	}

	if !reflect.DeepEqual(selected.root.Facts, expected) || selected.root.Acceptance != profile.Acceptance {
		return managed.ErrUnavailable
	}

	if err = invocationRetryEventsHeld(ctx, tx, selected); err != nil {
		return err
	}

	if err = invocationStatusMatchesHeld(ctx, tx, view, selected); err != nil {
		return err
	}

	if selected.current.Ordinal == "0" {
		if selected.permission.Acceptance != profile.Acceptance || selected.permission.Facts.PermissionProofCount != "0" || selected.permission.Facts.PermissionProofSetDigest != "" {
			return managed.ErrUnavailable
		}
	}

	if record.Result != nil {
		wire := managed.ProviderStepWire(record)
		originalInvocation := managed.ProviderInvocationKey{AttemptKey: attemptKey(view), Step: selection.Step, CommandID: record.Permission.CommandID}

		originalKey, keyErr := recoveryFactKey(attemptKey(view), managed.RecoveryResultRecord, profile.Acceptance.OperationID, managed.ProviderInvocationResult{Key: originalInvocation})
		if keyErr != nil {
			return keyErr
		}

		originalResult, readErr := recoveryReadNamedFact[managed.ProviderInvocationResult](ctx, tx, attemptKey(view), managed.RecoveryResultRecord, originalKey)
		if readErr != nil {
			return readErr
		}

		originalDigest, digestErr := recoveryPermissionDigest(original.Permission)
		if digestErr != nil {
			return digestErr
		}

		if originalResult.Acceptance != profile.Acceptance || originalResult.Facts.Key != originalInvocation || originalResult.Facts.RootDigest != selected.root.Digest || originalResult.Facts.PermissionDigest != originalDigest || !reflect.DeepEqual(originalResult.Facts.Result, *wire.Result) {
			return managed.ErrUnavailable
		}
		// An uncertain original may gain later observations for that same invocation.
		// It cannot become a closed predecessor or authorize a retry.
		if selected.current.Ordinal != "0" || selected.result == nil {
			return managed.ErrUnavailable
		}

		if wire.Result.Outcome == managed.PortTerminal && !reflect.DeepEqual(selected.result.Facts.Result, *wire.Result) {
			return managed.ErrUnavailable
		}
	}

	return nil
}
