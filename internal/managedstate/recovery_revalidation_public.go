package managedstate

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/xraph/ctrlplane/managed"
)

const recoveryRequestKind = "removal_recovery_request"

// recoveryRequestHeader retains only the request fields absent from its fact.
// It supplies a hash preimage, never authority without the independent receipt.
type recoveryRequestHeader struct {
	SchemaVersion         managed.Decimal            `json:"schema_version"`
	Kind                  managed.RecoveryRecordKind `json:"kind"`
	Command               managed.RecoveryCommand    `json:"command"`
	ExpectedCurrentDigest string                     `json:"expected_current_digest,omitempty"`
}

func recoveryProofFingerprint(request managed.RemovalRevalidationRequest) (string, error) {
	return attemptHash("ctrlplane.managed.revalidation-acceptance.v1", request)
}

func recoveryProofRequestHeld(ctx context.Context, tx ControllerTransaction, view attemptView, reference managed.RecoveryAssociation, record managed.RecoveryRecord[managed.RemovalRevalidationFacts]) (managed.RemovalRevalidationRequest, error) {
	var zero managed.RemovalRevalidationRequest

	header, err := attemptRead[recoveryRequestHeader](ctx, tx, recoveryRequestKind, view.scope, reference.Key, managed.MaxRecoveryReceipt)
	if err != nil {
		return zero, err
	}

	if header.SchemaVersion != "1" || header.Kind != managed.RecoveryProofRecord || header.Command.AttemptRef != view.ref || header.Command.Key != view.descriptor.Key || header.Command.ID != record.Acceptance.OperationID {
		return zero, managed.ErrUnavailable
	}

	if _, err = header.Command.LocalCommand(); err != nil {
		return zero, err
	}

	request := managed.RemovalRevalidationRequest{Command: header.Command, ExpectedCurrentDigest: header.ExpectedCurrentDigest, Facts: record.Facts}

	fingerprint, err := recoveryProofFingerprint(request)
	if err != nil {
		return zero, err
	}

	if fingerprint != record.Acceptance.RequestDigest {
		return zero, managed.ErrUnavailable
	}

	return request, nil
}

// recoveryProofAcceptedHeld checks retained custody only. Full local binding and
// effect authority are checked by recoveryProofExactHeld immediately afterward.
func recoveryProofAcceptedHeld(ctx context.Context, tx ControllerTransaction, view attemptView, reference managed.RecoveryAssociation) (managed.RecoveryRecord[managed.RemovalRevalidationFacts], error) {
	var zero managed.RecoveryRecord[managed.RemovalRevalidationFacts]
	if reference.Kind != managed.RecoveryProofRecord {
		return zero, managed.ErrInvalid
	}

	record, err := recoveryReadFact[managed.RemovalRevalidationFacts](ctx, tx, attemptKey(view), reference)
	if err != nil {
		return zero, err
	}

	if _, err = recoveryProofRequestHeld(ctx, tx, view, reference, record); err != nil {
		return zero, err
	}

	if err = recoveryProtocolEventHeld(ctx, tx, view, "recovery_revalidation", reference, record.Acceptance); err != nil {
		return zero, err
	}

	return record, nil
}

func recoveryProofExactHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, reference managed.RecoveryAssociation) (managed.RecoveryRecord[managed.RemovalRevalidationFacts], error) {
	var zero managed.RecoveryRecord[managed.RemovalRevalidationFacts]

	record, err := recoveryProofAcceptedHeld(ctx, tx, view, reference)
	if err != nil {
		return zero, err
	}

	if err = recoveryRevalidationInputsHeld(ctx, tx, target, view, record.Facts, record.Acceptance.Receipt.AcceptedAt); err != nil {
		return zero, err
	}

	return record, nil
}

func recoveryProofReplayHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, request managed.RemovalRevalidationRequest, fingerprint string) (managed.RecoveryRecord[managed.RemovalRevalidationFacts], error) {
	var zero managed.RecoveryRecord[managed.RemovalRevalidationFacts]

	key, err := recoveryFactKey(attemptKey(view), managed.RecoveryProofRecord, request.Command.ID, request.Facts)
	if err != nil {
		return zero, err
	}

	record, reference, err := invocationReplayRecord[managed.RemovalRevalidationFacts](ctx, tx, attemptKey(view), managed.RecoveryProofRecord, key, request.Command.ID, fingerprint)
	if err != nil {
		return zero, err
	}

	if !reflect.DeepEqual(record.Facts, request.Facts) {
		return zero, managed.ErrConflict
	}

	return recoveryProofExactHeld(ctx, tx, target, view, reference)
}

func recoveryProofFreshHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, request managed.RemovalRevalidationRequest, now time.Time) error {
	command, err := request.Command.LocalCommand()
	if err != nil {
		return err
	}

	if target.Key != command.Key || view.ref != request.Command.AttemptRef || request.Facts.Binding.Key.AttemptKey != attemptKey(view) {
		return managed.ErrAuthority
	}

	if target.Revision != command.Revision || target.LeaseEpoch != command.LeaseEpoch || target.LeaseOwner != command.Owner || !now.Before(target.LeaseUntil) {
		return managed.ErrConflict
	}

	if view.state.TerminalDigest != "" || view.state.SettlementDigest != "" || view.state.Successor != nil || target.Phase == managed.Deleted {
		return managed.ErrBlocked
	}

	if _, readErr := tx.ReadRecord(ctx, recoveryCoverageSelectionKind, view.scope, view.ref.AttemptID.String()); !errors.Is(readErr, managed.ErrNotFound) {
		if readErr != nil {
			return readErr
		}

		return managed.ErrBlocked
	}

	if err = recoveryRevalidationInputsHeld(ctx, tx, target, view, request.Facts, now); err != nil {
		return err
	}

	current, _, err := recoveryReadCurrent(ctx, tx, request.Facts.Binding)
	if errors.Is(err, managed.ErrNotFound) {
		if request.ExpectedCurrentDigest != "" {
			return managed.ErrConflict
		}

		bindingKey, keyErr := recoveryBindingKey(request.Facts.Binding)
		if keyErr != nil {
			return keyErr
		}

		if _, readErr := tx.ReadRecord(ctx, recoveryCurrentKind, view.scope, bindingKey); !errors.Is(readErr, managed.ErrNotFound) {
			if readErr != nil {
				return readErr
			}

			return managed.ErrUnavailable
		}

		return nil
	}

	if err != nil {
		return err
	}

	previous, err := recoveryProofExactHeld(ctx, tx, target, view, current.Proof)
	if err != nil {
		return err
	}

	if current.Digest != request.ExpectedCurrentDigest || !request.Facts.SourceObservedAt.After(current.SourceObservedAt) || request.Facts.GuardID != previous.Facts.GuardID || request.Facts.GuardRootDigest != previous.Facts.GuardRootDigest {
		return managed.ErrConflict
	}

	return nil
}

func stageRecoveryRevalidation(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, request managed.RemovalRevalidationRequest, fingerprint string, now time.Time) (*recoveryActivationRows, managed.RecoveryRecord[managed.RemovalRevalidationFacts], error) {
	var zero managed.RecoveryRecord[managed.RemovalRevalidationFacts]

	expected, err := recoveryProofFingerprint(request)
	if err != nil {
		return nil, zero, err
	}

	if expected != fingerprint {
		return nil, zero, managed.ErrAuthority
	}

	if err := recoveryProofFreshHeld(ctx, tx, target, view, request, now); err != nil {
		return nil, zero, err
	}

	if err := removalLocalIDAvailable(ctx, tx, request.Command.ID); err != nil {
		return nil, zero, err
	}

	command, err := request.Command.LocalCommand()
	if err != nil {
		return nil, zero, err
	}

	receipt, _, err := invocationNativeCommitReceipt(target, view.state, command, fingerprint, now)
	if err != nil {
		return nil, zero, err
	}

	acceptance := managed.RecoveryAcceptance{OperationID: command.ID, RequestDigest: fingerprint, Receipt: managed.ReceiptWire(receipt)}

	record, err := activationRecord(managed.RecoveryProofRecord, request.Facts, acceptance)
	if err != nil {
		return nil, zero, err
	}

	staged := &recoveryActivationRows{ControllerTransaction: tx, key: target.Key, receipt: receipt}

	_, changed, err := recoveryAdvanceProof(ctx, staged, request.ExpectedCurrentDigest, record, now)
	if err != nil {
		return nil, zero, err
	}

	if !changed {
		return nil, zero, managed.ErrConflict
	}

	key, err := recoveryFactKey(attemptKey(view), managed.RecoveryProofRecord, command.ID, request.Facts)
	if err != nil {
		return nil, zero, err
	}

	reference, err := managed.RecoveryAssociationFor(key, record)
	if err != nil {
		return nil, zero, err
	}

	header := recoveryRequestHeader{SchemaVersion: "1", Kind: managed.RecoveryProofRecord, Command: request.Command, ExpectedCurrentDigest: request.ExpectedCurrentDigest}
	if err = attemptWrite(ctx, staged, recoveryRequestKind, view.scope, key, header, true, managed.MaxRecoveryReceipt); err != nil {
		return nil, zero, err
	}

	if err = stageRecoveryProtocolEvent(ctx, staged, view, "recovery_revalidation", reference, acceptance); err != nil {
		return nil, zero, err
	}

	if _, err = recoveryProofExactHeld(ctx, staged, target, view, reference); err != nil {
		return nil, zero, err
	}

	remaining, err := recoveryActivationReserve(target, view)
	if view.recoveryActivation != nil {
		remaining, err = invocationRemainingReserve(ctx, staged, target, view)
	}

	if err != nil {
		return nil, zero, err
	}

	revision, err := view.state.Revision.Uint64()
	if err != nil {
		return nil, zero, err
	}

	if err = recoveryCheckHeadroom(target.Revision, revision, 0, remaining); err != nil {
		return nil, zero, err
	}

	return staged, record, nil
}

// AcceptManagedRemovalRevalidation verifies configured evidence outside authority
// locks, then repeats exact dependency, freshness and current-proof CAS checks.
func (r *Repository) AcceptManagedRemovalRevalidation(ctx context.Context, request managed.RemovalRevalidationRequest) (managed.RecoveryRecord[managed.RemovalRevalidationFacts], error) {
	var out managed.RecoveryRecord[managed.RemovalRevalidationFacts]
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

	fingerprint, err := recoveryProofFingerprint(cloned)
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

		out, readErr = recoveryProofReplayHeld(ctx, tx, target, view, cloned, fingerprint)
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

		if readErr = recoveryProofFreshHeld(ctx, tx, target, view, cloned, tx.Now()); readErr != nil {
			return readErr
		}

		intent, readErr = recoveryReadFact[managed.RecoveryNativeIntent](ctx, tx, key, cloned.Facts.Intent)

		return readErr
	})
	if err != nil || replayed {
		return out, err
	}

	verified, err := r.verifyRecoveryProof(ctx, intent, cloned.Facts)
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

			if view.ref != active || view.ref != cloned.Command.AttemptRef {
				return managed.Receipt{}, managed.ErrConflict
			}

			staged, _, readErr := stageRecoveryRevalidation(ctx, tx, target, view, cloned, fingerprint, now)
			if readErr != nil {
				return managed.Receipt{}, readErr
			}

			view.validated = false

			if readErr = applyRecoveryActivationRows(ctx, tx, staged); readErr != nil {
				return managed.Receipt{}, readErr
			}

			return staged.receipt, nil
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

		out, readErr = recoveryProofReplayHeld(ctx, tx, target, view, cloned, fingerprint)

		return readErr
	})
	if err != nil {
		return managed.RecoveryRecord[managed.RemovalRevalidationFacts]{}, err
	}

	return out, nil
}

// ReadManagedRemovalRevalidation inspects the exact accepted proof and request witness.
func (r *Repository) ReadManagedRemovalRevalidation(ctx context.Context, request managed.RecoveryExactRequest) (managed.RecoveryRecord[managed.RemovalRevalidationFacts], error) {
	var out managed.RecoveryRecord[managed.RemovalRevalidationFacts]
	if r == nil || r.backend == nil {
		return out, managed.ErrUnsupported
	}

	if err := attemptVersion(request.Key); err != nil {
		return out, err
	}

	err := r.controller(ctx, "attempt:"+request.Key.InstanceID.String(), []managed.Key{request.Key.Key}, func(tx ControllerTransaction) error {
		target, readErr := tx.Read(ctx, request.Key.Key)
		if readErr != nil {
			return readErr
		}

		view, _, readErr := readRecoveryAttemptInspection(ctx, tx, target, request.Key)
		if readErr != nil {
			return readErr
		}

		out, readErr = recoveryProofExactHeld(ctx, tx, target, view, request.Association)

		return readErr
	})
	if err != nil {
		return managed.RecoveryRecord[managed.RemovalRevalidationFacts]{}, err
	}

	return out, nil
}

// ReadManagedRemovalRevalidationSet returns the complete descriptor-ordered set.
func (r *Repository) ReadManagedRemovalRevalidationSet(ctx context.Context, key managed.AttemptKey) (managed.RemovalRevalidationSet, error) {
	var out managed.RemovalRevalidationSet
	if r == nil || r.backend == nil {
		return out, managed.ErrUnsupported
	}

	if err := attemptVersion(key); err != nil {
		return out, err
	}

	err := r.controller(ctx, "attempt:"+key.InstanceID.String(), []managed.Key{key.Key}, func(tx ControllerTransaction) error {
		target, readErr := tx.Read(ctx, key.Key)
		if readErr != nil {
			return readErr
		}

		view, _, readErr := readRecoveryAttemptInspection(ctx, tx, target, key)
		if readErr != nil {
			return readErr
		}

		if len(view.descriptor.ReservationIDs) > managed.MaxRecoveryBindings {
			return managed.ErrCapacity
		}

		proofs := make([]managed.RecoveryRecord[managed.RemovalRevalidationFacts], 0, len(view.descriptor.ReservationIDs))

		pointers := make([]managed.RemovalRevalidationCurrent, 0, len(view.descriptor.ReservationIDs))
		for _, reservationID := range view.descriptor.ReservationIDs {
			retained, bindingErr := reservation(target, reservationID)
			if bindingErr != nil {
				return bindingErr
			}

			if retained.Phase == managed.Revoked {
				continue
			}

			binding, bindingErr := recoveryBindingHeld(ctx, tx, target, view, attemptBinding(view, reservationID))
			if bindingErr != nil {
				return bindingErr
			}

			current, _, bindingErr := recoveryReadCurrent(ctx, tx, binding)
			if bindingErr != nil {
				return bindingErr
			}

			proof, bindingErr := recoveryProofExactHeld(ctx, tx, target, view, current.Proof)
			if bindingErr != nil {
				return bindingErr
			}

			pointers, proofs = append(pointers, current), append(proofs, proof)
		}

		out, readErr = recoveryLeafSet(view.ref, proofs, pointers)

		return readErr
	})
	if err != nil {
		return managed.RemovalRevalidationSet{}, err
	}

	return out, nil
}
