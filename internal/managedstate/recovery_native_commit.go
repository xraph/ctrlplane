package managedstate

import (
	"context"
	"errors"
	"math"
	"reflect"
	"time"

	"github.com/xraph/ctrlplane/managed"
)

// invocationNativeCommitReceipt preserves both counters for nonissuing native retention.
// Fresh parent CAS and lease checks remain in the authority callback.
func invocationNativeCommitReceipt(current *managed.Target, state managed.AttemptState, command managed.Command, fingerprint string, now time.Time) (managed.Receipt, uint64, error) {
	attemptRevision, err := state.Revision.Uint64()
	if err != nil || current.Revision > math.MaxInt64 || attemptRevision > math.MaxInt64 {
		return managed.Receipt{}, 0, managed.ErrCapacity
	}

	receipt := managed.Receipt{ID: command.ID, Digest: fingerprint, Revision: current.Revision, LeaseEpoch: current.LeaseEpoch, AcceptedAt: now.UTC(), LeaseUntil: current.LeaseUntil}

	return receipt, attemptRevision, nil
}

func invocationNativeCommitHeadroom(parent, attempt uint64, remaining recoveryReserveAfter) error {
	return recoveryCheckHeadroom(parent, attempt, 0, remaining)
}

func invocationNativeCommitMemberHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, selected retrySelected, facts managed.ProviderNativeCommitFacts, now time.Time) (invocationProofMember, error) {
	var zero invocationProofMember
	if selected.permission.Facts.Key != facts.Invocation || selected.current.Ordinal == "0" || selected.marker != nil || selected.result != nil || selected.closure != nil {
		return zero, managed.ErrBlocked
	}

	members, err := invocationLiveProofsHeld(ctx, tx, target, view, now)
	if err != nil {
		return zero, err
	}

	for _, member := range members {
		if member.binding == facts.Binding {
			if member.current.Proof != facts.CurrentProof {
				return zero, managed.ErrConflict
			}

			return member, nil
		}
	}

	return zero, managed.ErrAuthority
}

func stageRecoveryProtocolEvent(ctx context.Context, tx ControllerTransaction, view attemptView, kind string, primary managed.RecoveryAssociation, acceptance managed.RecoveryAcceptance) error {
	raw, err := tx.ReadRecord(ctx, string(primary.Kind), view.scope, primary.Key)
	if err != nil {
		return err
	}

	digest, err := attemptStoredFactDigest(string(primary.Kind), raw)
	if err != nil {
		return err
	}

	receipt, err := acceptance.Receipt.Value()
	if err != nil {
		return err
	}

	event := attemptEvent{SchemaVersion: "1", AttemptRef: view.ref, Kind: kind, RequestDigest: acceptance.RequestDigest, FactKind: string(primary.Kind), FactKey: primary.Key, FactDigest: digest, Receipt: receipt}

	return attemptWrite(ctx, tx, "removal_attempt_event", view.scope, acceptance.OperationID.String(), event, true, managed.MaxAttemptFact)
}

func recoveryProtocolEventHeld(ctx context.Context, tx ControllerTransaction, view attemptView, kind string, primary managed.RecoveryAssociation, acceptance managed.RecoveryAcceptance) error {
	event, err := attemptEventRead(ctx, tx, view, acceptance.OperationID)
	if err != nil {
		return err
	}

	receipt, err := acceptance.Receipt.Value()
	if err != nil {
		return err
	}

	if event.Kind != kind || event.RequestDigest != acceptance.RequestDigest || event.FactKind != string(primary.Kind) || event.FactKey != primary.Key || event.Receipt != receipt {
		return managed.ErrUnavailable
	}

	return nil
}

func invocationNativeCommitReplayHeld(ctx context.Context, tx ControllerTransaction, view attemptView, request managed.ProviderNativeCommitRequest, fingerprint string) (managed.RecoveryRecord[managed.ProviderNativeCommitFacts], error) {
	var zero managed.RecoveryRecord[managed.ProviderNativeCommitFacts]

	factKey, err := recoveryFactKey(attemptKey(view), managed.RecoveryCommitRecord, request.Command.ID, request.Facts)
	if err != nil {
		return zero, err
	}

	record, reference, err := invocationReplayRecord[managed.ProviderNativeCommitFacts](ctx, tx, attemptKey(view), managed.RecoveryCommitRecord, factKey, request.Command.ID, fingerprint)
	if err != nil {
		return zero, err
	}

	if !reflect.DeepEqual(record.Facts, request.Facts) {
		return zero, managed.ErrConflict
	}

	if err = recoveryProtocolEventHeld(ctx, tx, view, "provider_native_commit", reference, record.Acceptance); err != nil {
		return zero, managed.ErrUnavailable
	}

	selected, err := invocationStatusSelectionHeld(ctx, tx, view, record.Facts.Invocation)
	if err != nil {
		return zero, managed.ErrUnavailable
	}

	target, err := tx.Read(ctx, view.descriptor.Key)
	if err != nil {
		return zero, err
	}

	if _, err = recoveryNativeIntentExactHeld(ctx, tx, target, view, record.Facts.Intent); err != nil {
		return zero, err
	}

	proof, err := recoveryProofExactHeld(ctx, tx, target, view, record.Facts.CurrentProof)
	if err != nil {
		return zero, managed.ErrUnavailable
	}

	if proof.Facts.Binding != record.Facts.Binding || !managed.RecoveryProofFresh(proof.Facts, record.Acceptance.Receipt.AcceptedAt) {
		return zero, managed.ErrUnavailable
	}

	bindingDigest, err := recoveryBindingKey(record.Facts.Binding)
	if err != nil {
		return zero, err
	}

	member := invocationProofMember{binding: record.Facts.Binding, bindingDigest: bindingDigest, current: managed.RemovalRevalidationCurrent{Proof: record.Facts.CurrentProof}, proof: proof}
	if _, err = invocationMarkerMember(ctx, tx, selected, member, reference, record.Acceptance.Receipt.AcceptedAt); err != nil {
		return zero, err
	}

	return record, nil
}

// RecordManagedProviderNativeCommit verifies configured native acceptance outside the
// authority transaction, then reloads exact intent, proof and permission before retention.
func (r *Repository) RecordManagedProviderNativeCommit(ctx context.Context, request managed.ProviderNativeCommitRequest) (managed.RecoveryRecord[managed.ProviderNativeCommitFacts], error) {
	var out managed.RecoveryRecord[managed.ProviderNativeCommitFacts]
	if r == nil || r.backend == nil {
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

	key := managed.AttemptKey{Key: command.Key, AttemptID: cloned.Command.AttemptRef.AttemptID, Generation: cloned.Command.AttemptRef.Generation}
	if cloned.Facts.Invocation.AttemptKey != key || cloned.Facts.Binding.Key.AttemptKey != key {
		return out, managed.ErrAuthority
	}

	fingerprint, err := attemptHash(managed.RemovalRecoveryProfile+".native-commit", cloned)
	if err != nil {
		return out, err
	}

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

		out, readErr = invocationNativeCommitReplayHeld(ctx, tx, view, cloned, fingerprint)
		if readErr == nil {
			replayed = true

			return nil
		}

		if !errors.Is(readErr, managed.ErrNotFound) {
			return readErr
		}

		if _, receiptErr := tx.ReadReceipt(ctx, key.Key, command.ID, ""); !errors.Is(receiptErr, managed.ErrNotFound) {
			if receiptErr != nil {
				return receiptErr
			}

			return managed.ErrUnavailable
		}

		if capabilityErr := r.requireConfiguredRecovery(); capabilityErr != nil {
			return capabilityErr
		}

		if active != view.ref {
			return managed.ErrConflict
		}

		selected, readErr := invocationSelectedHeld(ctx, tx, view, cloned.Facts.Invocation.Step)
		if readErr != nil {
			return readErr
		}

		member, readErr := invocationNativeCommitMemberHeld(ctx, tx, target, view, selected, cloned.Facts, tx.Now())
		if readErr != nil {
			return readErr
		}

		if _, readErr = invocationCommitFactsHeld(ctx, tx, selected, member, cloned.Facts, tx.Now(), tx.Now()); readErr != nil {
			return readErr
		}

		intent, readErr = recoveryNativeIntentExactHeld(ctx, tx, target, view, cloned.Facts.Intent)

		return readErr
	})
	if err != nil || replayed {
		return out, err
	}

	verified, err := r.verifyRecoveryCommit(ctx, intent, cloned.Facts)
	if err != nil {
		return managed.RecoveryRecord[managed.ProviderNativeCommitFacts]{}, err
	}

	err = r.controller(ctx, "attempt:"+key.InstanceID.String(), []managed.Key{key.Key}, func(tx ControllerTransaction) error {
		target, readErr := tx.Read(ctx, key.Key)
		if readErr != nil {
			return readErr
		}

		view, _, readErr := readRecoveryAttemptInspection(ctx, tx, target, key)
		if readErr != nil {
			return readErr
		}

		out, readErr = invocationNativeCommitReplayHeld(ctx, tx, view, cloned, fingerprint)
		if readErr == nil {
			return nil
		}

		if !errors.Is(readErr, managed.ErrNotFound) {
			return readErr
		}

		fresh := false

		_, atomicErr := tx.Atomic(ctx, key.Key, command.ID, fingerprint, func(current *managed.Target, now time.Time) (managed.Receipt, error) {
			if current.Revision != command.Revision || current.LeaseEpoch != command.LeaseEpoch || current.LeaseOwner != command.Owner || !now.Before(current.LeaseUntil) {
				return managed.Receipt{}, managed.ErrConflict
			}

			checked, active, heldErr := readRecoveryAttemptInspection(ctx, tx, current, key)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if checked.ref != active || active != cloned.Command.AttemptRef || checked.state.RevocationDigest != "" || checked.state.SettlementDigest != "" || checked.state.TerminalDigest != "" || checked.state.Successor != nil || current.Phase == managed.Deleted {
				return managed.Receipt{}, managed.ErrBlocked
			}

			if heldErr = removalLocalIDAvailable(ctx, tx, command.ID); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			selected, heldErr := invocationSelectedHeld(ctx, tx, checked, verified.Invocation.Step)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			member, heldErr := invocationNativeCommitMemberHeld(ctx, tx, current, checked, selected, verified, now)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			savedIntent, heldErr := recoveryNativeIntentExactHeld(ctx, tx, current, checked, verified.Intent)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if !reflect.DeepEqual(savedIntent, intent) {
				return managed.Receipt{}, managed.ErrConflict
			}

			receipt, attemptRevision, heldErr := invocationNativeCommitReceipt(current, checked.state, command, fingerprint, now)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			acceptance := managed.RecoveryAcceptance{OperationID: command.ID, RequestDigest: fingerprint, Receipt: managed.ReceiptWire(receipt)}
			staged := &recoveryActivationRows{ControllerTransaction: tx, key: key.Key, receipt: receipt}

			out, heldErr = activationRecord(managed.RecoveryCommitRecord, verified, acceptance)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			reference, heldErr := recoveryWriteFact(ctx, staged, key, out)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if _, heldErr = invocationMarkerMember(ctx, staged, selected, member, reference, now); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if heldErr = stageInvocationNativeCommitSelector(ctx, staged, selected, member, out, reference); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if heldErr = stageRecoveryProtocolEvent(ctx, staged, checked, "provider_native_commit", reference, acceptance); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			state := recoveryAttemptState{AttemptState: checked.state, Activation: *checked.recoveryActivation}
			if heldErr = attemptWrite(ctx, staged, "removal_attempt_state", checked.scope, checked.ref.AttemptID.String(), state, false, managed.MaxAttemptFact); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			proposed := *current
			proposed.Revision = receipt.Revision

			after, _, heldErr := readRecoveryAttemptInspection(ctx, staged, &proposed, key)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			reserve, heldErr := invocationRemainingReserve(ctx, staged, &proposed, after)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if heldErr = invocationNativeCommitHeadroom(current.Revision, attemptRevision, reserve); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			view.validated, checked.validated, after.validated = false, false, false

			if heldErr = applyRecoveryActivationRows(ctx, tx, staged); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			current.Revision = receipt.Revision
			fresh = true

			return receipt, nil
		})
		if atomicErr != nil {
			return atomicErr
		}

		if !fresh {
			out, atomicErr = invocationNativeCommitReplayHeld(ctx, tx, view, cloned, fingerprint)
		}

		return atomicErr
	})
	if err != nil {
		return managed.RecoveryRecord[managed.ProviderNativeCommitFacts]{}, err
	}

	return out, nil
}

// readManagedProviderNativeCommitInspection validates the complete accepted invocation.
func (r *Repository) readManagedProviderNativeCommitInspection(ctx context.Context, request managed.RecoveryExactRequest) (managed.RecoveryRecord[managed.ProviderNativeCommitFacts], error) {
	var out managed.RecoveryRecord[managed.ProviderNativeCommitFacts]
	if r == nil || r.backend == nil {
		return out, managed.ErrUnsupported
	}

	if request.Association.Kind != managed.RecoveryCommitRecord {
		return out, managed.ErrInvalid
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

		record, readErr := recoveryReadFact[managed.ProviderNativeCommitFacts](ctx, tx, request.Key, request.Association)
		if readErr != nil {
			return readErr
		}

		cloned := managed.ProviderNativeCommitRequest{Command: managed.RecoveryCommand{ID: record.Acceptance.OperationID}, Facts: record.Facts}
		out, readErr = invocationNativeCommitReplayHeld(ctx, tx, view, cloned, record.Acceptance.RequestDigest)

		return readErr
	})

	return out, err
}
