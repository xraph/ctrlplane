package managedstate

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/xraph/ctrlplane/managed"
)

// mutateRecoveryTerminal only accepts the finite original terminal request types.
// It stages their unchanged codecs with recovery coverage and closed-guard custody.
func mutateRecoveryTerminal(ctx context.Context, r *Repository, q managed.AttemptRequest, kind string, input any, change func(ControllerTransaction, *managed.Target, *attemptView, recoveryTerminalAuthority, string, time.Time) error) (managed.Receipt, bool, attemptView, error) {
	var (
		out      managed.Receipt
		accepted attemptView
	)

	fresh := false

	if r == nil || r.backend == nil {
		return out, false, accepted, managed.ErrUnsupported
	}

	if _, held := r.backend.(ControllerTransaction); held {
		return out, false, accepted, managed.ErrUnsupported
	}

	if !validAttemptCommand(q.Command) || !recoveryTerminalInputKind(kind, input) {
		return out, false, accepted, managed.ErrInvalid
	}

	if err := attemptVersion(attemptLocal(q)); err != nil {
		return out, false, accepted, err
	}

	err := r.controller(ctx, "attempt:"+q.InstanceID.String(), []managed.Key{q.Key}, func(tx ControllerTransaction) error {
		target, readErr := tx.Read(ctx, q.Key)
		if readErr != nil {
			return readErr
		}

		view, active, readErr := readRecoveryAttemptInspection(ctx, tx, target, attemptLocal(q))
		if readErr != nil {
			return readErr
		}

		authority, readErr := recoveryTerminalAuthorityHeld(ctx, tx, target, view)
		if readErr != nil {
			return readErr
		}

		witness, fingerprint, readErr := recoveryTerminalRequestFor(view, kind, input, authority)
		if readErr != nil {
			return readErr
		}

		if _, readErr = tx.ReadRecord(ctx, "removal_attempt_event", view.scope, q.ID.String()); readErr == nil {
			event, replayErr := recoveryTerminalAcceptedRequestHeld(ctx, tx, view, q.ID, kind, input, authority)
			if replayErr != nil {
				return replayErr
			}

			if replayErr = recoveryTerminalFactMatchesHeld(ctx, tx, target, view, authority, event, input); replayErr != nil {
				return replayErr
			}

			out, accepted = event.Receipt, view

			return nil
		} else if !errors.Is(readErr, managed.ErrNotFound) {
			return readErr
		}

		if _, readErr = tx.ReadReceipt(ctx, q.Key, q.ID, ""); !errors.Is(readErr, managed.ErrNotFound) {
			if readErr != nil {
				return readErr
			}

			return managed.ErrUnavailable
		}

		if readErr = r.requireConfiguredRecovery(); readErr != nil {
			return readErr
		}

		if active != view.ref {
			return managed.ErrConflict
		}

		out, readErr = tx.Atomic(ctx, q.Key, q.ID, fingerprint, func(current *managed.Target, now time.Time) (managed.Receipt, error) {
			if current.Revision != q.Revision || current.LeaseEpoch != q.LeaseEpoch || current.LeaseOwner != q.Owner || !now.Before(current.LeaseUntil) {
				return managed.Receipt{}, managed.ErrConflict
			}

			checked, selected, heldErr := readRecoveryAttemptInspection(ctx, tx, current, attemptLocal(q))
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if selected != checked.ref || checked.state.Successor != nil || checked.state.TerminalDigest != "" || current.Phase == managed.Deleted {
				return managed.Receipt{}, managed.ErrBlocked
			}

			heldAuthority, heldErr := recoveryTerminalAuthorityHeld(ctx, tx, current, checked)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			heldWitness, heldFingerprint, heldErr := recoveryTerminalRequestFor(checked, kind, input, heldAuthority)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if heldFingerprint != fingerprint {
				return managed.Receipt{}, managed.ErrConflict
			}

			witness = heldWitness

			if heldErr = removalLocalIDAvailable(ctx, tx, q.ID); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			revision, heldErr := checked.state.Revision.Uint64()
			if heldErr != nil || current.Revision >= 1<<63-1 || revision >= 1<<63-1 {
				return managed.Receipt{}, managed.ErrCapacity
			}

			receipt := managed.Receipt{ID: q.ID, Digest: fingerprint, Revision: current.Revision + 1, LeaseEpoch: current.LeaseEpoch, AcceptedAt: now.UTC(), LeaseUntil: current.LeaseUntil}
			staged := &recoveryActivationRows{ControllerTransaction: tx, key: q.Key, receipt: receipt}

			proposed, heldErr := Clone(current)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			updated := checked
			updated.state.BindingSlots = append([]byte(nil), checked.state.BindingSlots...)

			updated.state.ProviderSlots = append([]byte(nil), checked.state.ProviderSlots...)
			if heldErr = change(staged, proposed, &updated, heldAuthority, fingerprint, now); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if heldErr = attemptMarkMutation(&updated, input); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			updated.state.Revision = managed.Decimal(strconv.FormatUint(revision+1, 10))
			proposed.Revision = receipt.Revision

			if updated.recoveryActivation == nil {
				return managed.Receipt{}, managed.ErrUnsupported
			}

			event, heldErr := stageRecoveryTerminalCustody(ctx, staged, updated, input, witness, kind, receipt)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			after, afterCurrent, heldErr := readRecoveryAttemptInspection(ctx, staged, proposed, attemptLocal(q))
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if afterCurrent != after.ref {
				return managed.Receipt{}, managed.ErrConflict
			}

			remaining, heldErr := invocationRemainingReserve(ctx, staged, proposed, after)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if heldErr = recoveryCheckHeadroom(current.Revision, revision, 1, remaining); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if heldErr = recoveryTerminalFactMatchesHeld(ctx, staged, proposed, after, heldAuthority, event, input); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			checked.validated, updated.validated, after.validated, view.validated = false, false, false, false

			if heldErr = applyRecoveryActivationRows(ctx, tx, staged); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			*current = *proposed
			fresh = true
			accepted = after

			return receipt, nil
		})
		if readErr != nil {
			return readErr
		}

		if !fresh {
			return managed.ErrUnavailable
		}

		return nil
	})
	if err != nil {
		return managed.Receipt{}, false, attemptView{}, err
	}

	return out, fresh, accepted, nil
}

// stageRecoveryTerminalCustody retains the original fact association, schema 2
// state and independent request witness in the caller's isolated transaction batch.
func stageRecoveryTerminalCustody(ctx context.Context, staged *recoveryActivationRows, view attemptView, input any, witness recoveryTerminalRequest, kind string, receipt managed.Receipt) (attemptEvent, error) {
	var zero attemptEvent
	if view.recoveryActivation == nil {
		return zero, managed.ErrUnsupported
	}

	state := recoveryAttemptState{AttemptState: view.state, Activation: *view.recoveryActivation, PendingOriginals: append([]string(nil), view.recoveryPendingOriginals...)}
	if err := attemptWrite(ctx, staged, "removal_attempt_state", view.scope, view.ref.AttemptID.String(), state, false, managed.MaxAttemptFact); err != nil {
		return zero, err
	}

	factKind, factKey, err := attemptMutationFact(view, input)
	if err != nil {
		return zero, err
	}

	factDigest := receipt.Digest

	if factKind != "" {
		raw, rowErr := staged.ReadRecord(ctx, factKind, view.scope, factKey)
		if rowErr != nil {
			return zero, rowErr
		}

		factDigest, err = attemptStoredFactDigest(factKind, raw)
		if err != nil {
			return zero, err
		}
	}

	event := attemptEvent{SchemaVersion: "1", AttemptRef: view.ref, Kind: kind, RequestDigest: receipt.Digest, FactKind: factKind, FactKey: factKey, FactDigest: factDigest, Receipt: receipt}
	if err = attemptWrite(ctx, staged, "removal_attempt_event", view.scope, receipt.ID.String(), event, true, managed.MaxAttemptFact); err != nil {
		return zero, err
	}

	if err = attemptWrite(ctx, staged, recoveryTerminalRequestKind, view.scope, receipt.ID.String(), witness, true, maxRecoveryTerminalRequest); err != nil {
		return zero, err
	}

	return event, nil
}
