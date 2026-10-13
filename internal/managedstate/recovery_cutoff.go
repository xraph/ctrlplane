package managedstate

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/xraph/ctrlplane/managed"
)

// recoveryCutoffHeld validates permanent revocation independently of effect coverage.
// Fenced leaves CoverageDigest empty: it grants no settlement or native close authority.
func recoveryCutoffHeld(ctx context.Context, tx ControllerTransaction, view attemptView) (managed.RecoveryRecord[managed.RemovalCutoffFacts], error) {
	var zero managed.RecoveryRecord[managed.RemovalCutoffFacts]
	if !view.validated || view.recoveryActivation == nil || view.state.RevocationDigest == "" {
		return zero, managed.ErrBlocked
	}

	revocation, err := attemptRevocationRead(ctx, tx, view)
	if err != nil {
		return zero, err
	}

	if revocation.SchemaVersion != "1" || revocation.ProviderOperationID != view.descriptor.ProviderOperationID || revocation.Disposition != managed.Fenced || revocation.CoverageDigest != "" || revocation.SettlementDigest != "" {
		return zero, managed.ErrUnavailable
	}

	if err = attemptLocalFact(ctx, tx, view, revocation.LocalOperationID, "removal_revocation", view.ref.AttemptID.String(), revocation); err != nil {
		return zero, err
	}

	key, err := recoveryFactKey(attemptKey(view), managed.RecoveryCutoffRecord, revocation.LocalOperationID, managed.RemovalCutoffFacts{Key: attemptKey(view)})
	if err != nil {
		return zero, err
	}

	cutoff, err := recoveryReadNamedFact[managed.RemovalCutoffFacts](ctx, tx, attemptKey(view), managed.RecoveryCutoffRecord, key)
	if err != nil {
		return zero, err
	}

	facts := cutoff.Facts
	if facts.Key != attemptKey(view) || facts.AttemptRef != view.ref || facts.ProviderOperationID != view.descriptor.ProviderOperationID || facts.RevocationDigest != view.state.RevocationDigest || facts.RevocationReceipt != cutoff.Acceptance.Receipt || cutoff.Acceptance.OperationID != revocation.LocalOperationID || facts.RevocationReceipt.Revision != revocation.ParentRevision || !facts.CutoffAt.Equal(revocation.RevokedAt) || !facts.CutoffAt.Equal(cutoff.Acceptance.Receipt.AcceptedAt) {
		return zero, managed.ErrUnavailable
	}

	event, err := attemptEventRead(ctx, tx, view, revocation.LocalOperationID)
	if err != nil {
		return zero, err
	}

	receipt, err := cutoff.Acceptance.Receipt.Value()
	if err != nil {
		return zero, err
	}

	if event.Kind != "recovery_cutoff" || event.RequestDigest != cutoff.Acceptance.RequestDigest || event.Receipt != receipt {
		return zero, managed.ErrUnavailable
	}

	return cutoff, nil
}

// stageRecoveryCutoff retains the original revocation codec and event association.
// Every proposed row is checked before the caller applies the batch atomically.
func stageRecoveryCutoff(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, acceptance managed.RecoveryAcceptance) (*recoveryActivationRows, attemptView, managed.RecoveryRecord[managed.RemovalCutoffFacts], error) {
	var zero managed.RecoveryRecord[managed.RemovalCutoffFacts]
	if !view.validated || view.recoveryActivation == nil || view.state.SchemaVersion != "2" || view.state.RevocationDigest != "" || view.state.SettlementDigest != "" || view.state.TerminalDigest != "" || view.state.Successor != nil {
		return nil, view, zero, managed.ErrBlocked
	}

	receipt, err := acceptance.Receipt.Value()
	if err != nil {
		return nil, view, zero, err
	}

	revision, err := view.state.Revision.Uint64()
	if err != nil || target.Revision >= 1<<63-1 || revision >= 1<<63-1 {
		return nil, view, zero, managed.ErrCapacity
	}

	if receipt.ID != acceptance.OperationID || receipt.Digest != acceptance.RequestDigest || receipt.Revision != target.Revision+1 || receipt.LeaseEpoch != target.LeaseEpoch || !receipt.LeaseUntil.Equal(target.LeaseUntil) {
		return nil, view, zero, managed.ErrAuthority
	}

	staged := &recoveryActivationRows{ControllerTransaction: tx, key: target.Key, receipt: receipt}
	revocation := managed.AttemptRevocation{LocalOperationID: acceptance.OperationID, SchemaVersion: "1", AttemptRef: view.ref, ProviderOperationID: view.descriptor.ProviderOperationID, Disposition: managed.Fenced, RevokedAt: receipt.AcceptedAt, ParentRevision: acceptance.Receipt.Revision}

	digest, err := attemptFact("removal_revocation", revocation)
	if err != nil {
		return nil, view, zero, err
	}

	if err = attemptWrite(ctx, staged, "removal_revocation", view.scope, view.ref.AttemptID.String(), revocation, true, managed.MaxAttemptFact); err != nil {
		return nil, view, zero, err
	}

	event := attemptEvent{SchemaVersion: "1", AttemptRef: view.ref, Kind: "recovery_cutoff", RequestDigest: acceptance.RequestDigest, FactKind: "removal_revocation", FactKey: view.ref.AttemptID.String(), FactDigest: digest, Receipt: receipt}
	if err = attemptWrite(ctx, staged, "removal_attempt_event", view.scope, acceptance.OperationID.String(), event, true, managed.MaxAttemptFact); err != nil {
		return nil, view, zero, err
	}

	facts := managed.RemovalCutoffFacts{Key: attemptKey(view), AttemptRef: view.ref, ProviderOperationID: view.descriptor.ProviderOperationID, RevocationDigest: digest, RevocationReceipt: acceptance.Receipt, CutoffAt: receipt.AcceptedAt}

	cutoff, err := activationRecord(managed.RecoveryCutoffRecord, facts, acceptance)
	if err != nil {
		return nil, view, zero, err
	}

	if _, err = recoveryWriteFact(ctx, staged, attemptKey(view), cutoff); err != nil {
		return nil, view, zero, err
	}

	state := recoveryAttemptState{AttemptState: view.state, Activation: *view.recoveryActivation}
	state.RevocationDigest, state.Phase = digest, "revoked"

	state.Revision = managed.Decimal(strconv.FormatUint(revision+1, 10))
	if err = attemptWrite(ctx, staged, "removal_attempt_state", view.scope, view.ref.AttemptID.String(), state, false, managed.MaxAttemptFact); err != nil {
		return nil, view, zero, err
	}

	updated := view

	updated.state = state.AttemptState
	if _, err = recoveryCutoffHeld(ctx, staged, updated); err != nil {
		return nil, view, zero, err
	}

	return staged, updated, cutoff, nil
}

func recoveryCutoffReplayHeld(ctx context.Context, tx ControllerTransaction, view attemptView, command managed.RecoveryCommand, fingerprint string) (managed.RecoveryRecord[managed.RemovalCutoffFacts], error) {
	key, err := recoveryFactKey(attemptKey(view), managed.RecoveryCutoffRecord, command.ID, managed.RemovalCutoffFacts{Key: attemptKey(view)})
	if err != nil {
		return managed.RecoveryRecord[managed.RemovalCutoffFacts]{}, err
	}

	out, _, err := invocationReplayRecord[managed.RemovalCutoffFacts](ctx, tx, attemptKey(view), managed.RecoveryCutoffRecord, key, command.ID, fingerprint)
	if err != nil {
		return out, err
	}

	checked, err := recoveryCutoffHeld(ctx, tx, view)
	if err != nil {
		return out, managed.ErrUnavailable
	}

	if out.Digest != checked.Digest || out.Acceptance != checked.Acceptance {
		return out, managed.ErrUnavailable
	}

	return out, nil
}

// CutoffManagedRemovalRecovery permanently refuses further issuance. Existing effects
// remain unresolved until separately retained complete coverage proves their disposition.
func (r *Repository) CutoffManagedRemovalRecovery(ctx context.Context, request managed.RecoveryCommand) (managed.RecoveryRecord[managed.RemovalCutoffFacts], error) {
	var out managed.RecoveryRecord[managed.RemovalCutoffFacts]
	if r == nil || r.backend == nil {
		return out, managed.ErrUnsupported
	}

	if _, held := r.backend.(ControllerTransaction); held {
		return out, managed.ErrUnsupported
	}

	command, err := request.LocalCommand()
	if err != nil {
		return out, err
	}

	fingerprint, err := attemptHash("ctrlplane.managed.recovery-cutoff.v1", request)
	if err != nil {
		return out, err
	}

	key := managed.AttemptKey{Key: command.Key, AttemptID: request.AttemptRef.AttemptID, Generation: request.AttemptRef.Generation}

	err = r.controller(ctx, "attempt:"+key.InstanceID.String(), []managed.Key{key.Key}, func(tx ControllerTransaction) error {
		target, readErr := tx.Read(ctx, key.Key)
		if readErr != nil {
			return readErr
		}

		view, _, readErr := readRecoveryAttemptInspection(ctx, tx, target, key)
		if readErr != nil {
			return readErr
		}

		if view.ref != request.AttemptRef {
			return managed.ErrAuthority
		}

		out, readErr = recoveryCutoffReplayHeld(ctx, tx, view, request, fingerprint)
		if readErr == nil {
			return nil
		}

		if !errors.Is(readErr, managed.ErrNotFound) {
			return readErr
		}

		if readErr = r.requireConfiguredRecovery(); readErr != nil {
			return readErr
		}

		_, atomicErr := tx.Atomic(ctx, key.Key, command.ID, fingerprint, func(current *managed.Target, now time.Time) (managed.Receipt, error) {
			if current.Revision != command.Revision || current.LeaseEpoch != command.LeaseEpoch || current.LeaseOwner != command.Owner || !now.Before(current.LeaseUntil) {
				return managed.Receipt{}, managed.ErrConflict
			}

			checked, active, heldErr := readRecoveryAttemptInspection(ctx, tx, current, key)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if checked.ref != active || active != request.AttemptRef || current.Phase == managed.Deleted {
				return managed.Receipt{}, managed.ErrBlocked
			}

			if heldErr = removalLocalIDAvailable(ctx, tx, command.ID); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if current.Revision >= 1<<63-1 {
				return managed.Receipt{}, managed.ErrCapacity
			}

			receipt := managed.Receipt{ID: command.ID, Digest: fingerprint, Revision: current.Revision + 1, LeaseEpoch: current.LeaseEpoch, AcceptedAt: now.UTC(), LeaseUntil: current.LeaseUntil}
			acceptance := managed.RecoveryAcceptance{OperationID: command.ID, RequestDigest: fingerprint, Receipt: managed.ReceiptWire(receipt)}

			staged, _, saved, heldErr := stageRecoveryCutoff(ctx, tx, current, checked, acceptance)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			proposed := *current
			proposed.Revision = receipt.Revision

			after, selected, heldErr := readRecoveryAttemptInspection(ctx, staged, &proposed, key)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if after.ref != selected {
				return managed.Receipt{}, managed.ErrConflict
			}

			reserve, heldErr := invocationRemainingReserve(ctx, staged, &proposed, after)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			revision, heldErr := checked.state.Revision.Uint64()
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if heldErr = recoveryCheckHeadroom(current.Revision, revision, 1, reserve); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			view.validated, checked.validated, after.validated = false, false, false

			if heldErr = applyRecoveryActivationRows(ctx, tx, staged); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			current.Revision, out = receipt.Revision, saved

			return receipt, nil
		})
		if atomicErr != nil {
			return atomicErr
		}
		// Atomic may return an existing receipt without invoking its callback.
		latest, readErr := tx.Read(ctx, key.Key)
		if readErr != nil {
			return readErr
		}

		checked, _, readErr := readRecoveryAttemptInspection(ctx, tx, latest, key)
		if readErr != nil {
			return readErr
		}

		out, readErr = recoveryCutoffReplayHeld(ctx, tx, checked, request, fingerprint)

		return readErr
	})
	if err != nil {
		return managed.RecoveryRecord[managed.RemovalCutoffFacts]{}, err
	}

	return out, nil
}

// recoveryInvocationAfterCutoff permits only retained observations and affirmative
// custody closure. Neither path issues a new invocation or a delegation marker.
func recoveryInvocationAfterCutoff(state managed.AttemptState, kind string) bool {
	return state.RevocationDigest == "" || kind == "result" || kind == "closure"
}
