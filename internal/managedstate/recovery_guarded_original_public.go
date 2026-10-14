package managedstate

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"time"

	"github.com/xraph/ctrlplane/managed"
)

// PrepareManagedGuardedOriginal returns nonissuing evidence custody. Exact replay
// precedes fresh authority checks and never asks a provider to perform transport.
func (r *Repository) PrepareManagedGuardedOriginal(ctx context.Context, request managed.GuardedOriginalRequest) (managed.GuardedOriginalPermission, error) {
	var out managed.GuardedOriginalPermission

	q, err := recoverySnapshot(request)
	if err != nil {
		return out, err
	}

	local, err := q.Command.LocalCommand()
	if err != nil {
		return out, err
	}

	if r == nil || r.backend == nil {
		return out, managed.ErrUnsupported
	}

	if _, held := r.backend.(ControllerTransaction); held {
		return out, managed.ErrUnsupported
	}

	err = r.controller(ctx, "attempt:"+local.InstanceID.String(), []managed.Key{local.Key}, func(tx ControllerTransaction) error {
		target, readErr := tx.Read(ctx, local.Key)
		if readErr != nil {
			return readErr
		}

		key := managed.AttemptKey{Key: local.Key, AttemptID: q.Command.AttemptRef.AttemptID, Generation: q.Command.AttemptRef.Generation}

		view, active, readErr := readRecoveryAttemptInspection(ctx, tx, target, key)
		if readErr != nil {
			return readErr
		}

		pending, _, readErr := guardedOriginalPendingHeld(ctx, tx, target, view, q.Step)
		if readErr == nil {
			if !reflect.DeepEqual(pending.Request, q) {
				return managed.ErrConflict
			}

			out = managed.GuardedOriginalPermission{Original: pending.Original, PendingDigest: pending.Digest}

			return nil
		}

		if !errors.Is(readErr, managed.ErrNotFound) {
			return readErr
		}

		if _, readErr = tx.ReadReceipt(ctx, local.Key, local.ID, ""); !errors.Is(readErr, managed.ErrNotFound) {
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

		initial, _, readErr := stageGuardedOriginalPending(ctx, tx, target, view, q, tx.Now())
		if readErr != nil {
			return readErr
		}

		fresh := false

		_, readErr = tx.Atomic(ctx, local.Key, local.ID, initial.receipt.Digest, func(current *managed.Target, now time.Time) (managed.Receipt, error) {
			checked, selected, heldErr := readRecoveryAttemptInspection(ctx, tx, current, key)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if selected != checked.ref {
				return managed.Receipt{}, managed.ErrConflict
			}

			if heldErr = removalLocalIDAvailable(ctx, tx, local.ID); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			staged, accepted, heldErr := stageGuardedOriginalPending(ctx, tx, current, checked, q, now)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if staged.receipt.Digest != initial.receipt.Digest {
				return managed.Receipt{}, managed.ErrConflict
			}

			revision, heldErr := checked.state.Revision.Uint64()
			if heldErr != nil || revision >= 1<<63-1 {
				return managed.Receipt{}, managed.ErrCapacity
			}

			state := recoveryAttemptState{AttemptState: checked.state, Activation: *checked.recoveryActivation, PendingOriginals: append([]string(nil), checked.recoveryPendingOriginals...)}

			index, indexErr := guardedOriginalFixedHeld(ctx, tx, current, checked, q.Step)
			if indexErr != nil {
				return managed.Receipt{}, indexErr
			}

			if len(state.PendingOriginals) == 0 {
				state.PendingOriginals = make([]string, managed.MaxRecoverySteps)
			}

			if len(state.PendingOriginals) != managed.MaxRecoverySteps || state.PendingOriginals[index] != "" {
				return managed.Receipt{}, managed.ErrUnavailable
			}

			state.PendingOriginals[index] = accepted.Digest

			state.Revision = managed.Decimal(strconv.FormatUint(revision+1, 10))
			if heldErr = attemptWrite(ctx, staged, "removal_attempt_state", checked.scope, checked.ref.AttemptID.String(), state, false, managed.MaxAttemptFact); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			after, heldErr := recoveryNativeValidateStaged(ctx, staged, current, checked)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			checked.validated, after.validated, view.validated = false, false, false

			if heldErr = applyRecoveryActivationRows(ctx, tx, staged); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			current.Revision = staged.receipt.Revision
			out = managed.GuardedOriginalPermission{Original: accepted.Original, PendingDigest: accepted.Digest}
			fresh = true

			return staged.receipt, nil
		})
		if readErr == nil && !fresh {
			return managed.ErrUnavailable
		}

		return readErr
	})
	if err != nil {
		return managed.GuardedOriginalPermission{}, err
	}

	return out, nil
}

// MaterializeManagedGuardedOriginal retains authenticated effect identity without
// advancing authority counters. Fresh CAS, lease and full remaining reserve apply.
func (r *Repository) MaterializeManagedGuardedOriginal(ctx context.Context, request managed.GuardedOriginalMaterializationRequest) (managed.RecoveryAssociation, error) {
	var out managed.RecoveryAssociation

	q, err := recoverySnapshot(request)
	if err != nil {
		return out, err
	}

	local, err := q.Command.LocalCommand()
	if err != nil {
		return out, err
	}

	if r == nil || r.backend == nil {
		return out, managed.ErrUnsupported
	}

	if _, held := r.backend.(ControllerTransaction); held {
		return out, managed.ErrUnsupported
	}

	err = r.controller(ctx, "attempt:"+local.InstanceID.String(), []managed.Key{local.Key}, func(tx ControllerTransaction) error {
		target, readErr := tx.Read(ctx, local.Key)
		if readErr != nil {
			return readErr
		}

		key := managed.AttemptKey{Key: local.Key, AttemptID: q.Command.AttemptRef.AttemptID, Generation: q.Command.AttemptRef.Generation}

		view, active, readErr := readRecoveryAttemptInspection(ctx, tx, target, key)
		if readErr != nil {
			return readErr
		}

		pending, selector, readErr := guardedOriginalPendingHeld(ctx, tx, target, view, q.Step)
		if readErr != nil {
			return readErr
		}

		if selector.Root != nil {
			root, heldErr := recoveryReadFact[managed.ProviderStepRetryRoot](ctx, tx, key, *selector.Root)
			if heldErr != nil {
				return heldErr
			}

			witness, heldErr := attemptRead[guardedOriginalMaterialization](ctx, tx, guardedOriginalMaterializationKind, view.scope, root.Acceptance.OperationID.String(), managed.MaxAttemptFact)
			if heldErr != nil {
				return heldErr
			}

			if !reflect.DeepEqual(witness.Request, q) {
				return managed.ErrConflict
			}

			out = *selector.Root

			return nil
		}

		if pending.Digest != q.PendingDigest {
			return managed.ErrConflict
		}

		if _, readErr = tx.ReadReceipt(ctx, local.Key, local.ID, ""); !errors.Is(readErr, managed.ErrNotFound) {
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

		initial, _, readErr := stageGuardedOriginalMaterialization(ctx, tx, target, view, q, tx.Now())
		if readErr != nil {
			return readErr
		}

		fresh := false

		_, readErr = tx.Atomic(ctx, local.Key, local.ID, initial.receipt.Digest, func(current *managed.Target, now time.Time) (managed.Receipt, error) {
			checked, selected, heldErr := readRecoveryAttemptInspection(ctx, tx, current, key)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if selected != checked.ref {
				return managed.Receipt{}, managed.ErrConflict
			}

			if heldErr = removalLocalIDAvailable(ctx, tx, local.ID); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			staged, reference, heldErr := stageGuardedOriginalMaterialization(ctx, tx, current, checked, q, now)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if staged.receipt.Digest != initial.receipt.Digest {
				return managed.Receipt{}, managed.ErrConflict
			}

			after, heldErr := recoveryNativeValidateStaged(ctx, staged, current, checked)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			checked.validated, after.validated, view.validated = false, false, false

			if heldErr = applyRecoveryActivationRows(ctx, tx, staged); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			out = reference
			fresh = true

			return staged.receipt, nil
		})
		if readErr == nil && !fresh {
			return managed.ErrUnavailable
		}

		return readErr
	})
	if err != nil {
		return managed.RecoveryAssociation{}, err
	}

	return out, nil
}
