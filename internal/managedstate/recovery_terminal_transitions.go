package managedstate

import (
	"context"
	"math"
	"strconv"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

func recoverySettlementFact(view attemptView, request managed.AttemptSettlementRequest, authority recoveryTerminalAuthority, acceptedAt time.Time) (managed.AttemptSettlement, error) {
	var zero managed.AttemptSettlement
	if !evidence(request.Evidence) || !attemptTime(request.SettledAt) || request.SettledAt.After(acceptedAt) || request.SettledAt.Before(authority.acceptedAt) || request.VerifierID != view.descriptor.VerifierID || request.Disposition != authority.coverage.Facts.Disposition {
		return zero, managed.ErrAuthority
	}

	switch request.Disposition {
	case managed.Removed:
		if request.ProducerProfile != "ctrlplane.managed.remove.v1" {
			return zero, managed.ErrUnsupported
		}
	case managed.SettledWithoutDeletion:
		if request.ProducerProfile != managed.LocalNoDispatchProfile {
			return zero, managed.ErrUnsupported
		}
	default:
		return zero, managed.ErrUnsupported
	}

	references, err := recoveryEffectiveStepReferences(authority.coverage.Facts)
	if err != nil {
		return zero, err
	}

	return managed.AttemptSettlement{LocalOperationID: request.ID, SchemaVersion: "1", AttemptRef: view.ref, ProviderOperationID: view.descriptor.ProviderOperationID, ResourcesDigest: view.descriptor.ResourcesDigest, AdmissionDigest: view.state.AdmissionDigest, RevocationDigest: view.state.RevocationDigest, Disposition: request.Disposition, StepReferences: references, CoverageDigest: authority.coverage.Digest, ProducerProfile: request.ProducerProfile, VerifierID: request.VerifierID, Evidence: request.Evidence, EvidenceDigest: managed.Digest(request.Evidence), SettledAt: request.SettledAt}, nil
}

// SettleManagedRemovalRecovery preserves the original settlement codec and binds
// it to complete effective coverage plus every accepted native guard close.
func (r *Repository) SettleManagedRemovalRecovery(ctx context.Context, request managed.AttemptSettlementRequest) (managed.Receipt, error) {
	q, err := recoverySnapshot(request)
	if err != nil {
		return managed.Receipt{}, err
	}

	out, _, _, err := mutateRecoveryTerminal(ctx, r, q.AttemptRequest, "attempt_settle", q, func(tx ControllerTransaction, target *managed.Target, view *attemptView, authority recoveryTerminalAuthority, _ string, now time.Time) error {
		if view.state.SettlementDigest != "" {
			return managed.ErrConflict
		}

		fact, err := recoverySettlementFact(*view, q, authority, now)
		if err != nil {
			return err
		}

		hash, err := attemptSetFact(ctx, tx, view, "removal_settlement", view.ref.AttemptID.String(), fact, managed.MaxAttemptSettlement)
		if err != nil {
			return err
		}

		view.state.SettlementDigest = hash

		view.state.Phase = "settled"
		if view.descriptor.Generation == "1" && target.Deletion != nil {
			target.Deletion.Settlement = &managed.Settlement{OperationID: view.descriptor.ProviderOperationID, FenceDigest: target.Deletion.FenceDigest, Disposition: q.Disposition, Verifier: q.VerifierID, Evidence: q.Evidence, SettledAt: q.SettledAt}
		}

		return nil
	})

	return out, err
}

func recoveryBindingSettlementFact(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, q managed.AttemptBindingSettlementRequest, authority recoveryTerminalAuthority, receipt managed.Receipt) (managed.AttemptBindingSettlement, error) {
	var zero managed.AttemptBindingSettlement
	if view.state.SettlementDigest == "" || authority.coverage.Facts.Disposition != managed.SettledWithoutDeletion {
		return zero, managed.ErrBlocked
	}

	key := attemptBinding(view, q.ReservationID)

	fence, err := attemptFenceRead(ctx, tx, target, view, key)
	if err != nil {
		return zero, err
	}

	hash, err := attemptFact("removal_fence", fence)
	if err != nil {
		return zero, err
	}

	if hash != q.FenceFactDigest || receipt.AcceptedAt.Before(fence.NativeAcceptance.AcceptedAt) || receipt.AcceptedAt.Before(authority.acceptedAt) {
		return zero, managed.ErrAuthority
	}

	return managed.AttemptBindingSettlement{LocalOperationID: q.ID, SchemaVersion: "1", Key: key, DescriptorDigest: view.ref.DescriptorDigest, FenceFactDigest: hash, NativeFenceDigest: fence.NativeFenceDigest, RevocationDigest: view.state.RevocationDigest, SettlementDigest: view.state.SettlementDigest, CoverageDigest: authority.coverage.Digest, Disposition: managed.SettledWithoutDeletion, WitnessedAt: receipt.AcceptedAt, ParentRevision: managed.Decimal(strconv.FormatUint(receipt.Revision, 10))}, nil
}

// RecordManagedRemovalRecoveryBindingSettlement retains a fresh Abort witness
// after independently closed custody and accepted nondeletion settlement.
func (r *Repository) RecordManagedRemovalRecoveryBindingSettlement(ctx context.Context, request managed.AttemptBindingSettlementRequest) (managed.Receipt, error) {
	q, err := recoverySnapshot(request)
	if err != nil {
		return managed.Receipt{}, err
	}

	out, _, _, err := mutateRecoveryTerminal(ctx, r, q.AttemptRequest, "attempt_witness", q, func(tx ControllerTransaction, target *managed.Target, view *attemptView, authority recoveryTerminalAuthority, _ string, now time.Time) error {
		if _, err := recoveryBindingHeld(ctx, tx, target, *view, attemptBinding(*view, q.ReservationID)); err != nil {
			return err
		}

		fact, err := recoveryBindingSettlementFact(ctx, tx, target, *view, q, authority, managed.Receipt{Revision: target.Revision + 1, AcceptedAt: now.UTC()})
		if err != nil {
			return err
		}

		slot, err := attemptSlot(fact.Key, "binding")
		if err != nil {
			return err
		}

		return attemptWrite(ctx, tx, "removal_binding_settlement", view.scope, slot, fact, true, managed.MaxAttemptFact)
	})

	return out, err
}

// RecordManagedRemovalRecoveryCommand retains an original Finish or Abort command.
// The complete closed-guard set precedes either native terminal request.
func (r *Repository) RecordManagedRemovalRecoveryCommand(ctx context.Context, request managed.AttemptCommandRequest) (managed.AttemptCommandGrant, error) {
	var out managed.AttemptCommandGrant

	q, err := recoverySnapshot(request)
	if err != nil {
		return out, err
	}

	if q.NativeCommand.Validate() != nil || q.CurrentBinding != nil || (q.Kind != managed.AttemptAbort && q.Kind != managed.AttemptFinish) {
		return out, managed.ErrInvalid
	}

	version, err := q.ExpectedNativeVersion.Uint64()
	if err != nil || version == 0 || version > math.MaxInt64 {
		return out, managed.ErrInvalid
	}

	receipt, fresh, accepted, err := mutateRecoveryTerminal(ctx, r, q.AttemptRequest, "attempt_command", q, func(tx ControllerTransaction, target *managed.Target, view *attemptView, authority recoveryTerminalAuthority, fingerprint string, now time.Time) error {
		if err := removalLocalIDAvailable(ctx, tx, q.NativeCommand.OperationID); err != nil {
			return err
		}

		if _, err := recoveryBindingHeld(ctx, tx, target, *view, attemptBinding(*view, q.ReservationID)); err != nil {
			return err
		}

		original, err := attemptOriginal(ctx, tx, target, q.ReservationID)
		if err != nil {
			return err
		}

		settlement, err := attemptSettlementRead(ctx, tx, *view)
		if err != nil {
			return err
		}

		if settlement.Disposition != authority.coverage.Facts.Disposition {
			return managed.ErrAuthority
		}

		if q.Kind == managed.AttemptFinish && settlement.Disposition != managed.Removed || q.Kind == managed.AttemptAbort && settlement.Disposition != managed.SettledWithoutDeletion {
			return managed.ErrBlocked
		}

		if q.Kind == managed.AttemptAbort {
			if _, err = attemptWitnessRead(ctx, tx, target, *view, attemptBinding(*view, q.ReservationID)); err != nil {
				return err
			}
		}

		fence, err := attemptFenceRead(ctx, tx, target, *view, attemptBinding(*view, q.ReservationID))
		if err != nil {
			return err
		}

		if q.ExpectedNativeVersion != fence.NativeAcceptance.BindingVersion {
			return managed.ErrConflict
		}

		key := managed.AttemptCommandKey{AttemptBindingKey: attemptBinding(*view, q.ReservationID), Kind: q.Kind}

		slot, err := attemptSlot(key.AttemptBindingKey, q.Kind)
		if err != nil {
			return err
		}

		fact := managed.AttemptCommandRecord{SchemaVersion: "1", Key: key, DescriptorDigest: view.ref.DescriptorDigest, BindingReference: original, ExpectedNativeVersion: q.ExpectedNativeVersion, NativeCommand: q.NativeCommand, AuthorizationID: q.ID, AuthorizationDigest: fingerprint, IssuedAt: now.UTC()}
		if err = attemptWrite(ctx, tx, "removal_command", view.scope, slot, fact, true, managed.MaxAttemptCommand); err != nil {
			return err
		}

		view.commands[key] = fact

		return nil
	})
	if err != nil {
		return out, err
	}

	key := managed.AttemptCommandKey{AttemptBindingKey: managed.AttemptBindingKey{AttemptKey: attemptLocal(q.AttemptRequest), ReservationID: q.ReservationID}, Kind: q.Kind}

	command, ok := accepted.commands[key]
	if !ok {
		return out, managed.ErrUnavailable
	}

	return managed.AttemptCommandGrant{Receipt: attemptGrantReceipt(accepted, receipt), Command: command, NewlyIssued: fresh}, nil
}

func recordRecoveryTerminal(ctx context.Context, r *Repository, q managed.AttemptRequest, reservationID id.ID, acceptance managed.AttemptNativeAcceptance, verified, until time.Time, kind string, input any) (managed.Receipt, error) {
	out, _, _, err := mutateRecoveryTerminal(ctx, r, q, "attempt_"+kind, input, func(tx ControllerTransaction, target *managed.Target, view *attemptView, authority recoveryTerminalAuthority, _ string, now time.Time) error {
		key := attemptBinding(*view, reservationID)

		settlement, err := attemptSettlementRead(ctx, tx, *view)
		if err != nil {
			return err
		}

		if settlement.Disposition != authority.coverage.Facts.Disposition || settlement.CoverageDigest != authority.coverage.Digest {
			return managed.ErrAuthority
		}

		if _, err := recoveryBindingHeld(ctx, tx, target, *view, key); err != nil {
			return err
		}

		command, err := attemptCommandRead(ctx, tx, target, *view, managed.AttemptCommandKey{AttemptBindingKey: key, Kind: kind})
		if err != nil {
			return err
		}

		if err = attemptNativeMatch(command, acceptance, now); err != nil {
			return err
		}

		fence, err := attemptFenceRead(ctx, tx, target, *view, key)
		if err != nil {
			return err
		}

		if fence.NativeFenceDigest != acceptance.NativeFenceDigest {
			return managed.ErrAuthority
		}

		fenceHash, err := attemptFact("removal_fence", fence)
		if err != nil {
			return err
		}

		commandHash, err := attemptHash(attemptCommandDomain, command)
		if err != nil {
			return err
		}

		witnessDigest := ""

		if kind == managed.AttemptAbort {
			if authority.coverage.Facts.Disposition != managed.SettledWithoutDeletion {
				return managed.ErrBlocked
			}

			witness, err := attemptWitnessRead(ctx, tx, target, *view, key)
			if err != nil {
				return err
			}

			if verified.Before(witness.WitnessedAt) || verified.After(now) || !until.After(now) || !until.After(verified) {
				return managed.ErrBlocked
			}

			witnessDigest, err = attemptFact("removal_binding_settlement", witness)
			if err != nil {
				return err
			}
		} else if authority.coverage.Facts.Disposition != managed.Removed {
			return managed.ErrBlocked
		}

		slot, err := attemptSlot(key, "binding")
		if err != nil {
			return err
		}

		opposite := "removal_abort"
		if kind == managed.AttemptAbort {
			opposite = "removal_finish"
		}

		if _, err = tx.ReadRecord(ctx, opposite, view.scope, slot); !controllerAbsent(err) {
			if err != nil {
				return err
			}

			return managed.ErrConflict
		}

		fact := managed.AttemptTerminalRecord{LocalOperationID: q.ID, SchemaVersion: "1", Key: key, DescriptorDigest: view.ref.DescriptorDigest, CommandDigest: commandHash, FenceFactDigest: fenceHash, AttemptNativeAcceptance: acceptance, ProofVerifiedAt: verified, ProofValidUntil: until, SettlementWitnessDigest: witnessDigest}
		if err = attemptWrite(ctx, tx, "removal_"+kind, view.scope, slot, fact, true, managed.MaxAttemptCommand); err != nil {
			return err
		}

		binding, err := reservation(target, reservationID)
		if err != nil {
			return err
		}

		if view.descriptor.Generation == "1" && binding.Fence != nil && target.Deletion != nil && target.Deletion.Settlement != nil {
			if kind == managed.AttemptFinish {
				binding.Finished = acceptance.Receipt
			} else {
				binding.Abort = &managed.AbortProof{FenceDigest: binding.Fence.Digest, VerifiedAt: verified, ValidUntil: until, Evidence: acceptance.OriginalBindingProof, Receipt: acceptance.Receipt}
				target.Phase = managed.Sealed
			}
		}

		return nil
	})

	return out, err
}

// RecordManagedRemovalRecoveryFinish retains the original Finish after closed custody.
func (r *Repository) RecordManagedRemovalRecoveryFinish(ctx context.Context, request managed.AttemptFinishRequest) (managed.Receipt, error) {
	q, err := recoverySnapshot(request)
	if err != nil {
		return managed.Receipt{}, err
	}

	return recordRecoveryTerminal(ctx, r, q.AttemptRequest, q.ReservationID, q.Acceptance, time.Time{}, time.Time{}, managed.AttemptFinish, q)
}

// RecordManagedRemovalRecoveryAbort retains the original Abort and fresh binding proof.
func (r *Repository) RecordManagedRemovalRecoveryAbort(ctx context.Context, request managed.AttemptAbortRequest) (managed.Receipt, error) {
	q, err := recoverySnapshot(request)
	if err != nil {
		return managed.Receipt{}, err
	}

	return recordRecoveryTerminal(ctx, r, q.AttemptRequest, q.ReservationID, q.Acceptance, q.ProofVerifiedAt, q.ProofValidUntil, managed.AttemptAbort, q)
}

// TombstoneManagedRemovalRecovery completes an exactly settled removal after
// every original binding has accepted Finish. Replays retain the same receipt.
func (r *Repository) TombstoneManagedRemovalRecovery(ctx context.Context, q managed.AttemptRequest) (managed.Receipt, error) {
	out, _, _, err := mutateRecoveryTerminal(ctx, r, q, "attempt_tombstone", q, func(tx ControllerTransaction, target *managed.Target, view *attemptView, authority recoveryTerminalAuthority, fingerprint string, _ time.Time) error {
		if authority.coverage.Facts.Disposition != managed.Removed {
			return managed.ErrBlocked
		}

		settlement, err := attemptSettlementRead(ctx, tx, *view)
		if err != nil {
			return err
		}

		if settlement.Disposition != managed.Removed || settlement.CoverageDigest != authority.coverage.Digest {
			return managed.ErrAuthority
		}

		for _, reservationID := range view.descriptor.ReservationIDs {
			retained, err := reservation(target, reservationID)
			if err != nil {
				return err
			}

			if retained.Phase == managed.Revoked {
				continue
			}

			record, err := attemptBindingRead(ctx, tx, target, *view, attemptBinding(*view, reservationID))
			if err != nil {
				return err
			}

			if record.Finish == nil || record.Abort != nil {
				return managed.ErrBlocked
			}
		}

		target.Phase = managed.Deleted
		target.TerminalDisposition = managed.Removed
		view.state.Phase = "removed"
		view.state.TerminalDigest = view.state.SettlementDigest
		selected := recoveryTerminalSelection{SchemaVersion: "1", AttemptRef: view.ref, OperationID: q.ID, RequestDigest: fingerprint}

		return attemptWrite(ctx, tx, recoveryTerminalSelectionKind, view.scope, view.ref.AttemptID.String(), selected, true, managed.MaxRecoveryReceipt)
	})

	return out, err
}
