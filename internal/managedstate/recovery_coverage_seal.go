package managedstate

import (
	"context"
	"errors"
	"time"

	"github.com/xraph/ctrlplane/managed"
)

var _ managed.RemovalCoverageSealer = (*Repository)(nil)

func recoveryCoverageSealDigest(command managed.RecoveryCommand, plan recoveryCoveragePlan) (string, error) {
	local, err := command.LocalCommand()
	if err != nil {
		return "", err
	}

	if local.Key != plan.facts.Key.Key || command.AttemptRef != plan.facts.AttemptRef {
		return "", managed.ErrAuthority
	}

	facts := plan.facts
	facts.BindingSetDigest = ""

	return attemptHash("ctrlplane.managed.effective-coverage-seal.v1", struct {
		Command  managed.RecoveryCommand           `json:"command"`
		Facts    managed.EffectiveRemovalCoverage  `json:"facts"`
		Bindings []managed.EffectiveRemovalBinding `json:"bindings"`
	}{command, facts, plan.bindings})
}

func recoveryCoverageSealAcceptance(selection recoveryCoverageSelection, coverage managed.RecoveryRecord[managed.EffectiveRemovalCoverage], bindings []managed.EffectiveRemovalBinding) error {
	if selection.SchemaVersion != "2" || selection.Sponsor != (managed.RecoveryAssociation{}) || selection.Command.ID != coverage.Acceptance.OperationID {
		return managed.ErrUnavailable
	}

	fingerprint, err := recoveryCoverageSealDigest(selection.Command, recoveryCoveragePlan{facts: coverage.Facts, bindings: bindings})
	if err != nil {
		return err
	}

	if fingerprint != coverage.Acceptance.RequestDigest {
		return managed.ErrUnavailable
	}

	return nil
}

func stageRecoveryCoverageSeal(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, command managed.RecoveryCommand, now time.Time) (*recoveryActivationRows, managed.RecoveryRecord[managed.EffectiveRemovalCoverage], error) {
	var zero managed.RecoveryRecord[managed.EffectiveRemovalCoverage]
	if err := recoveryNativeFreshCommand(target, view, command, now); err != nil {
		return nil, zero, err
	}

	if _, err := tx.ReadRecord(ctx, recoveryCoverageSelectionKind, view.scope, view.ref.AttemptID.String()); !errors.Is(err, managed.ErrNotFound) {
		if err != nil {
			return nil, zero, err
		}

		return nil, zero, managed.ErrConflict
	}

	if err := removalLocalIDAvailable(ctx, tx, command.ID); err != nil {
		return nil, zero, err
	}

	if _, err := tx.ReadReceipt(ctx, target.Key, command.ID, ""); !errors.Is(err, managed.ErrNotFound) {
		if err != nil {
			return nil, zero, err
		}

		return nil, zero, managed.ErrConflict
	}

	plan, err := prepareRecoveryCoverageHeld(ctx, tx, target, view, now)
	if err != nil {
		return nil, zero, err
	}

	fingerprint, err := recoveryCoverageSealDigest(command, plan)
	if err != nil {
		return nil, zero, err
	}
	// Sealing observes an already complete, permanently cut off set. It neither
	// issues a permission nor discharges any reserved native close obligation.
	receipt := managed.Receipt{ID: command.ID, Digest: fingerprint, Revision: target.Revision, LeaseEpoch: target.LeaseEpoch, AcceptedAt: now.UTC(), LeaseUntil: target.LeaseUntil}
	acceptance := managed.RecoveryAcceptance{OperationID: command.ID, RequestDigest: fingerprint, Receipt: managed.ReceiptWire(receipt)}

	bindings, coverage, err := recoveryCoverageRecords(plan, acceptance)
	if err != nil {
		return nil, zero, err
	}

	rows := &recoveryActivationRows{ControllerTransaction: tx, key: target.Key, receipt: receipt}
	for _, binding := range bindings {
		if _, err = recoveryWriteFact(ctx, rows, attemptKey(view), binding); err != nil {
			return nil, zero, err
		}
	}

	reference, err := recoveryWriteFact(ctx, rows, attemptKey(view), coverage)
	if err != nil {
		return nil, zero, err
	}

	if err = stageRecoveryProtocolEvent(ctx, rows, view, "recovery_coverage_seal", reference, acceptance); err != nil {
		return nil, zero, err
	}

	selection := recoveryCoverageSelection{SchemaVersion: "2", AttemptRef: view.ref, Coverage: reference, Command: command}
	if err = attemptWrite(ctx, rows, recoveryCoverageSelectionKind, view.scope, view.ref.AttemptID.String(), selection, true, managed.MaxRecoveryReceipt); err != nil {
		return nil, zero, err
	}

	return rows, coverage, nil
}

func recoveryCoverageSealReplay(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, command managed.RecoveryCommand) (managed.RecoveryRecord[managed.EffectiveRemovalCoverage], error) {
	var zero managed.RecoveryRecord[managed.EffectiveRemovalCoverage]

	selection, err := attemptRead[recoveryCoverageSelection](ctx, tx, recoveryCoverageSelectionKind, view.scope, view.ref.AttemptID.String(), managed.MaxRecoveryReceipt)
	if err != nil {
		return zero, err
	}

	if selection.SchemaVersion != "2" || selection.Command != command {
		return zero, managed.ErrConflict
	}

	return recoveryCoverageReadHeld(ctx, tx, target, view, &selection.Coverage)
}

// SealManagedEffectiveRemovalCoverage atomically retains a nonissuing coverage
// observation and its independent receipt. Full close intents remain separate.
func (r *Repository) SealManagedEffectiveRemovalCoverage(ctx context.Context, command managed.RecoveryCommand) (managed.RecoveryRecord[managed.EffectiveRemovalCoverage], error) {
	var out managed.RecoveryRecord[managed.EffectiveRemovalCoverage]
	if r == nil || r.backend == nil {
		return out, managed.ErrUnsupported
	}

	if _, held := r.backend.(ControllerTransaction); held {
		return out, managed.ErrUnsupported
	}

	local, err := command.LocalCommand()
	if err != nil {
		return out, err
	}

	key := managed.AttemptKey{Key: local.Key, AttemptID: command.AttemptRef.AttemptID, Generation: command.AttemptRef.Generation}

	err = r.controller(ctx, "attempt:"+key.InstanceID.String(), []managed.Key{key.Key}, func(tx ControllerTransaction) error {
		target, readErr := tx.Read(ctx, key.Key)
		if readErr != nil {
			return readErr
		}

		view, active, readErr := readRecoveryAttemptInspection(ctx, tx, target, key)
		if readErr != nil {
			return readErr
		}

		if view.ref != command.AttemptRef {
			return managed.ErrAuthority
		}

		out, readErr = recoveryCoverageSealReplay(ctx, tx, target, view, command)
		if readErr == nil {
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

		staged, _, readErr := stageRecoveryCoverageSeal(ctx, tx, target, view, command, tx.Now())
		if readErr != nil {
			return readErr
		}

		fingerprint := staged.receipt.Digest

		_, atomicErr := tx.Atomic(ctx, key.Key, command.ID, fingerprint, func(current *managed.Target, now time.Time) (managed.Receipt, error) {
			checked, currentRef, heldErr := readRecoveryAttemptInspection(ctx, tx, current, key)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if currentRef != command.AttemptRef || checked.ref != currentRef {
				return managed.Receipt{}, managed.ErrConflict
			}

			rows, _, heldErr := stageRecoveryCoverageSeal(ctx, tx, current, checked, command, now)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if rows.receipt.Digest != fingerprint {
				return managed.Receipt{}, managed.ErrConflict
			}

			after, heldErr := recoveryNativeValidateStaged(ctx, rows, current, checked)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if _, heldErr = recoveryCoverageSealReplay(ctx, rows, current, after, command); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			checked.validated, after.validated, view.validated = false, false, false

			if heldErr = applyRecoveryActivationRows(ctx, tx, rows); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			return rows.receipt, nil
		})
		if atomicErr != nil {
			return atomicErr
		}

		target, readErr = tx.Read(ctx, key.Key)
		if readErr != nil {
			return readErr
		}

		view, _, readErr = readRecoveryAttemptInspection(ctx, tx, target, key)
		if readErr != nil {
			return readErr
		}

		out, readErr = recoveryCoverageSealReplay(ctx, tx, target, view, command)

		return readErr
	})
	if err != nil {
		return managed.RecoveryRecord[managed.EffectiveRemovalCoverage]{}, err
	}

	return out, nil
}
