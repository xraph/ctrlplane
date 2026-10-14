package managedstate

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"time"

	"github.com/xraph/ctrlplane/managed"
)

func recoveryNativeCloseFingerprint(request managed.RecoveryNativeCloseRequest) (string, error) {
	return attemptHash("ctrlplane.managed.native-close-acceptance.v1", request)
}

func recoveryNativeCloseSelectionHeld(ctx context.Context, tx ControllerTransaction, view attemptView, binding managed.RemovalRecoveryBinding) (recoveryNativeCloseSelection, error) {
	var zero recoveryNativeCloseSelection

	key, err := recoveryBindingKey(binding)
	if err != nil {
		return zero, err
	}

	selected, err := attemptRead[recoveryNativeCloseSelection](ctx, tx, recoveryNativeCloseSelectionKind, view.scope, key, managed.MaxRecoveryReceipt)
	if err != nil {
		return zero, err
	}

	if selected.SchemaVersion != "1" || selected.BindingDigest != key || selected.Intent.Kind != managed.RecoveryIntentRecord {
		return zero, managed.ErrUnavailable
	}

	if err = managed.ValidateRecoveryAssociation(selected.Intent); err != nil {
		return zero, err
	}

	if selected.Close != nil {
		if selected.Close.Kind != managed.RecoveryCloseRecord {
			return zero, managed.ErrUnavailable
		}

		if err = managed.ValidateRecoveryAssociation(*selected.Close); err != nil {
			return zero, err
		}
	}

	return selected, nil
}

// recoveryNativeCloseInputsHeld is called before producer verification and again
// under the acceptance lock. The native receipt cannot substitute for local custody.
func recoveryNativeCloseInputsHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, facts managed.RecoveryNativeCloseFacts, acceptedAt time.Time) (managed.RecoveryRecord[managed.RecoveryNativeIntent], error) {
	var zero managed.RecoveryRecord[managed.RecoveryNativeIntent]

	intent, err := recoveryNativeIntentExactHeld(ctx, tx, target, view, facts.Intent)
	if err != nil {
		return zero, err
	}

	if err = recoveryIntentMatches(intent, facts.Intent, facts.Binding, "close", facts.NativeRequestID, facts.NativeRequestDigest); err != nil {
		return zero, err
	}

	if intent.Facts.Cutoff == nil || intent.Facts.Coverage == nil || intent.Facts.Proof == nil || *intent.Facts.Cutoff != facts.Cutoff || *intent.Facts.Coverage != facts.Coverage {
		return zero, managed.ErrAuthority
	}

	selected, err := recoveryNativeCloseSelectionHeld(ctx, tx, view, facts.Binding)
	if err != nil {
		return zero, err
	}

	if selected.Intent != facts.Intent {
		return zero, managed.ErrAuthority
	}

	proof, err := recoveryProofExactHeld(ctx, tx, target, view, *intent.Facts.Proof)
	if err != nil {
		return zero, err
	}

	if proof.Facts.GuardID != facts.GuardID || proof.Facts.GuardRootDigest != facts.GuardRootDigest || !token(facts.GuardID) || !digest(facts.GuardRootDigest) || !digest(facts.NativeReceiptDigest) || !attemptTime(facts.NativeAcceptedAt) || facts.NativeAcceptedAt.Before(intent.Acceptance.Receipt.AcceptedAt) || facts.NativeAcceptedAt.After(acceptedAt) {
		return zero, managed.ErrAuthority
	}

	if err = managed.ValidateRecoveryNativeExport(facts.Export); err != nil {
		return zero, err
	}

	return intent, nil
}

func recoveryNativeCloseAcceptedHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, reference managed.RecoveryAssociation) (managed.RecoveryRecord[managed.RecoveryNativeCloseFacts], error) {
	var zero managed.RecoveryRecord[managed.RecoveryNativeCloseFacts]
	if reference.Kind != managed.RecoveryCloseRecord {
		return zero, managed.ErrInvalid
	}

	record, err := recoveryReadFact[managed.RecoveryNativeCloseFacts](ctx, tx, attemptKey(view), reference)
	if err != nil {
		return zero, err
	}

	header, err := attemptRead[recoveryRequestHeader](ctx, tx, recoveryRequestKind, view.scope, reference.Key, managed.MaxRecoveryReceipt)
	if err != nil {
		return zero, err
	}

	if header.SchemaVersion != "1" || header.Kind != managed.RecoveryCloseRecord || header.ExpectedCurrentDigest != "" || header.DerivedCoverage || header.Command.AttemptRef != view.ref || header.Command.Key != target.Key || header.Command.ID != record.Acceptance.OperationID {
		return zero, managed.ErrUnavailable
	}

	if _, err = header.Command.LocalCommand(); err != nil {
		return zero, err
	}

	fingerprint, err := recoveryNativeCloseFingerprint(managed.RecoveryNativeCloseRequest{Command: header.Command, Facts: record.Facts})
	if err != nil {
		return zero, err
	}

	if fingerprint != record.Acceptance.RequestDigest {
		return zero, managed.ErrUnavailable
	}

	if err = recoveryProtocolEventHeld(ctx, tx, view, "recovery_native_close", reference, record.Acceptance); err != nil {
		return zero, err
	}

	return record, nil
}

func recoveryNativeCloseExactHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, reference managed.RecoveryAssociation) (managed.RecoveryRecord[managed.RecoveryNativeCloseFacts], error) {
	var zero managed.RecoveryRecord[managed.RecoveryNativeCloseFacts]

	record, err := recoveryNativeCloseAcceptedHeld(ctx, tx, target, view, reference)
	if err != nil {
		return zero, err
	}

	if _, err = recoveryNativeCloseInputsHeld(ctx, tx, target, view, record.Facts, record.Acceptance.Receipt.AcceptedAt); err != nil {
		return zero, err
	}

	selected, err := recoveryNativeCloseSelectionHeld(ctx, tx, view, record.Facts.Binding)
	if err != nil {
		return zero, err
	}

	if selected.Close == nil || *selected.Close != reference {
		return zero, managed.ErrUnavailable
	}

	return record, nil
}

func recoveryNativeCloseReplayHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, request managed.RecoveryNativeCloseRequest, fingerprint string) (managed.RecoveryRecord[managed.RecoveryNativeCloseFacts], error) {
	var zero managed.RecoveryRecord[managed.RecoveryNativeCloseFacts]

	key, err := recoveryFactKey(attemptKey(view), managed.RecoveryCloseRecord, request.Command.ID, request.Facts)
	if err != nil {
		return zero, err
	}

	record, reference, err := invocationReplayRecord[managed.RecoveryNativeCloseFacts](ctx, tx, attemptKey(view), managed.RecoveryCloseRecord, key, request.Command.ID, fingerprint)
	if err != nil {
		return zero, err
	}

	if !reflect.DeepEqual(record.Facts, request.Facts) {
		return zero, managed.ErrConflict
	}

	return recoveryNativeCloseExactHeld(ctx, tx, target, view, reference)
}

// A present selection releases only its exact retained obligation. Missing facts,
// conflicting receipts and forged selectors refuse instead of reclaiming reserve.
func recoveryNativeCloseReserveHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, after recoveryReserveAfter) (recoveryReserveAfter, error) {
	if len(view.descriptor.ReservationIDs) > managed.MaxRecoveryBindings {
		return after, managed.ErrCapacity
	}

	if _, err := tx.ReadRecord(ctx, recoveryCoverageSelectionKind, view.scope, view.ref.AttemptID.String()); errors.Is(err, managed.ErrNotFound) {
		return after, nil
	} else if err != nil {
		return after, err
	}

	for _, reservationID := range view.descriptor.ReservationIDs {
		retained, err := reservation(target, reservationID)
		if err != nil {
			return after, err
		}

		if retained.Phase == managed.Revoked {
			continue
		}

		binding, err := recoveryBindingInspectionHeld(ctx, tx, target, view, attemptBinding(view, reservationID))
		if err != nil {
			return after, err
		}

		selected, err := recoveryNativeCloseSelectionHeld(ctx, tx, view, binding)
		if errors.Is(err, managed.ErrNotFound) {
			continue
		}

		if err != nil {
			return after, err
		}

		intent, err := recoveryNativeIntentExactHeld(ctx, tx, target, view, selected.Intent)
		if err != nil {
			return after, err
		}

		if intent.Facts.Operation != "close" || intent.Facts.Binding != binding || after.closeIntentsMissing == 0 {
			return after, managed.ErrUnavailable
		}

		after.closeIntentsMissing--

		if selected.Close != nil {
			if _, err = recoveryNativeCloseExactHeld(ctx, tx, target, view, *selected.Close); err != nil {
				return after, err
			}

			if after.closeResultsMissing == 0 {
				return after, managed.ErrUnavailable
			}

			after.closeResultsMissing--
		}
	}

	return after, nil
}

func stageRecoveryNativeClose(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, request managed.RecoveryNativeCloseRequest, fingerprint string, now time.Time) (*recoveryActivationRows, managed.RecoveryRecord[managed.RecoveryNativeCloseFacts], error) {
	var zero managed.RecoveryRecord[managed.RecoveryNativeCloseFacts]

	expected, err := recoveryNativeCloseFingerprint(request)
	if err != nil {
		return nil, zero, err
	}

	if expected != fingerprint {
		return nil, zero, managed.ErrAuthority
	}

	if err = recoveryNativeFreshCommand(target, view, request.Command, now); err != nil {
		return nil, zero, err
	}

	if _, err = recoveryNativeCloseInputsHeld(ctx, tx, target, view, request.Facts, now); err != nil {
		return nil, zero, err
	}

	selected, err := recoveryNativeCloseSelectionHeld(ctx, tx, view, request.Facts.Binding)
	if err != nil {
		return nil, zero, err
	}

	if selected.Close != nil {
		return nil, zero, managed.ErrConflict
	}

	if err = removalLocalIDAvailable(ctx, tx, request.Command.ID); err != nil {
		return nil, zero, err
	}

	revision, err := view.state.Revision.Uint64()
	if err != nil || target.Revision >= 1<<63-1 || revision >= 1<<63-1 {
		return nil, zero, managed.ErrCapacity
	}

	receipt := managed.Receipt{ID: request.Command.ID, Digest: fingerprint, Revision: target.Revision + 1, LeaseEpoch: target.LeaseEpoch, AcceptedAt: now.UTC(), LeaseUntil: target.LeaseUntil}
	acceptance := managed.RecoveryAcceptance{OperationID: request.Command.ID, RequestDigest: fingerprint, Receipt: managed.ReceiptWire(receipt)}

	record, err := activationRecord(managed.RecoveryCloseRecord, request.Facts, acceptance)
	if err != nil {
		return nil, zero, err
	}

	staged := &recoveryActivationRows{ControllerTransaction: tx, key: target.Key, receipt: receipt}

	reference, err := recoveryWriteFact(ctx, staged, attemptKey(view), record)
	if err != nil {
		return nil, zero, err
	}

	header := recoveryRequestHeader{SchemaVersion: "1", Kind: managed.RecoveryCloseRecord, Command: request.Command}
	if err = attemptWrite(ctx, staged, recoveryRequestKind, view.scope, reference.Key, header, true, managed.MaxRecoveryReceipt); err != nil {
		return nil, zero, err
	}

	if err = stageRecoveryProtocolEvent(ctx, staged, view, "recovery_native_close", reference, acceptance); err != nil {
		return nil, zero, err
	}

	selected.Close = &reference

	bindingKey, err := recoveryBindingKey(request.Facts.Binding)
	if err != nil {
		return nil, zero, err
	}

	if err = attemptWrite(ctx, staged, recoveryNativeCloseSelectionKind, view.scope, bindingKey, selected, false, managed.MaxRecoveryReceipt); err != nil {
		return nil, zero, err
	}

	if view.recoveryActivation == nil {
		return nil, zero, managed.ErrUnsupported
	}

	state := recoveryAttemptState{AttemptState: view.state, Activation: *view.recoveryActivation}

	state.Revision = managed.Decimal(strconv.FormatUint(revision+1, 10))
	if err = attemptWrite(ctx, staged, "removal_attempt_state", view.scope, view.ref.AttemptID.String(), state, false, managed.MaxAttemptFact); err != nil {
		return nil, zero, err
	}

	return staged, record, nil
}

// RecordManagedRemovalGuardClose authenticates outside the controller lock, then
// atomically retains the exact result, selector, event, state and parent receipt.
func (r *Repository) RecordManagedRemovalGuardClose(ctx context.Context, request managed.RecoveryNativeCloseRequest) (managed.RecoveryRecord[managed.RecoveryNativeCloseFacts], error) {
	var out managed.RecoveryRecord[managed.RecoveryNativeCloseFacts]
	if r == nil || r.backend == nil {
		return out, managed.ErrUnsupported
	}

	if _, held := r.backend.(ControllerTransaction); held {
		return out, managed.ErrUnsupported
	}

	cloned, err := recoverySnapshot(request)
	if err != nil {
		return out, err
	}

	command, err := cloned.Command.LocalCommand()
	if err != nil {
		return out, err
	}

	fingerprint, err := recoveryNativeCloseFingerprint(cloned)
	if err != nil {
		return out, err
	}

	key := managed.AttemptKey{Key: command.Key, AttemptID: cloned.Command.AttemptRef.AttemptID, Generation: cloned.Command.AttemptRef.Generation}

	var intent managed.RecoveryRecord[managed.RecoveryNativeIntent]

	replayed := false

	err = r.controller(ctx, "attempt:"+key.InstanceID.String(), []managed.Key{key.Key}, func(tx ControllerTransaction) error {
		target, readErr := tx.Read(ctx, key.Key)
		if readErr != nil {
			return readErr
		}

		view, active, readErr := readRecoveryAttemptInspection(ctx, tx, target, key)
		if readErr != nil {
			return readErr
		}

		if view.ref != cloned.Command.AttemptRef {
			return managed.ErrAuthority
		}

		out, readErr = recoveryNativeCloseReplayHeld(ctx, tx, target, view, cloned, fingerprint)
		if readErr == nil {
			replayed = true

			return nil
		}

		if !errors.Is(readErr, managed.ErrNotFound) {
			return readErr
		}

		if readErr = r.requireConfiguredRecovery(); readErr != nil {
			return readErr
		}

		if active != view.ref {
			return managed.ErrConflict
		}

		if readErr = recoveryNativeFreshCommand(target, view, cloned.Command, tx.Now()); readErr != nil {
			return readErr
		}

		intent, readErr = recoveryNativeCloseInputsHeld(ctx, tx, target, view, cloned.Facts, tx.Now())
		if readErr != nil {
			return readErr
		}

		selected, readErr := recoveryNativeCloseSelectionHeld(ctx, tx, view, cloned.Facts.Binding)
		if readErr != nil {
			return readErr
		}

		if selected.Close != nil {
			return managed.ErrConflict
		}

		return nil
	})
	if err != nil || replayed {
		return out, err
	}

	verified, err := r.verifyRecoveryClose(ctx, intent, cloned.Facts)
	if err != nil {
		return out, err
	}

	if !reflect.DeepEqual(verified, cloned.Facts) {
		return out, managed.ErrAuthority
	}

	err = r.controller(ctx, "attempt:"+key.InstanceID.String(), []managed.Key{key.Key}, func(tx ControllerTransaction) error {
		_, atomicErr := tx.Atomic(ctx, key.Key, command.ID, fingerprint, func(target *managed.Target, now time.Time) (managed.Receipt, error) {
			view, active, readErr := readRecoveryAttemptInspection(ctx, tx, target, key)
			if readErr != nil {
				return managed.Receipt{}, readErr
			}

			if view.ref != active || active != cloned.Command.AttemptRef {
				return managed.Receipt{}, managed.ErrConflict
			}

			rows, _, readErr := stageRecoveryNativeClose(ctx, tx, target, view, cloned, fingerprint, now)
			if readErr != nil {
				return managed.Receipt{}, readErr
			}

			after, readErr := recoveryNativeValidateStaged(ctx, rows, target, view)
			if readErr != nil {
				return managed.Receipt{}, readErr
			}

			proposed := *target

			proposed.Revision = rows.receipt.Revision
			if _, readErr = recoveryNativeCloseReplayHeld(ctx, rows, &proposed, after, cloned, fingerprint); readErr != nil {
				return managed.Receipt{}, readErr
			}

			view.validated, after.validated = false, false

			if readErr = applyRecoveryActivationRows(ctx, tx, rows); readErr != nil {
				return managed.Receipt{}, readErr
			}

			target.Revision = rows.receipt.Revision

			return rows.receipt, nil
		})
		if atomicErr != nil {
			return atomicErr
		}

		target, readErr := tx.Read(ctx, key.Key)
		if readErr != nil {
			return readErr
		}

		view, _, readErr := readRecoveryAttemptInspection(ctx, tx, target, key)
		if readErr != nil {
			return readErr
		}

		out, readErr = recoveryNativeCloseReplayHeld(ctx, tx, target, view, cloned, fingerprint)

		return readErr
	})
	if err != nil {
		return managed.RecoveryRecord[managed.RecoveryNativeCloseFacts]{}, err
	}

	return out, nil
}

// ReadManagedRemovalGuardClose requires the original accepted request, local
// dependencies and selected native result. It performs no external verification.
func (r *Repository) ReadManagedRemovalGuardClose(ctx context.Context, request managed.RecoveryExactRequest) (managed.RecoveryRecord[managed.RecoveryNativeCloseFacts], error) {
	var out managed.RecoveryRecord[managed.RecoveryNativeCloseFacts]
	if r == nil || r.backend == nil {
		return out, managed.ErrUnsupported
	}

	if err := attemptVersion(request.Key); err != nil {
		return out, err
	}

	err := r.controller(ctx, "attempt:"+request.Key.InstanceID.String(), []managed.Key{request.Key.Key}, func(tx ControllerTransaction) error {
		target, err := tx.Read(ctx, request.Key.Key)
		if err != nil {
			return err
		}

		view, _, err := readRecoveryAttemptInspection(ctx, tx, target, request.Key)
		if err != nil {
			return err
		}

		out, err = recoveryNativeCloseExactHeld(ctx, tx, target, view, request.Association)

		return err
	})
	if err != nil {
		return managed.RecoveryRecord[managed.RecoveryNativeCloseFacts]{}, err
	}

	return out, nil
}
