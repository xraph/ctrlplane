package managedstate

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

const recoveryFixedSelectionKind = "recovery_original_selection"

// recoveryFixedSelection records every slot at activation, including explicit absence.
// Original association bytes remain in the original slot and its plural commitment.
type recoveryFixedSelection struct {
	SchemaVersion managed.Decimal              `json:"schema_version"`
	AttemptRef    managed.AttemptRef           `json:"attempt_ref"`
	Step          managed.ProviderStepKey      `json:"step"`
	Disposition   string                       `json:"disposition"`
	Activation    managed.RecoveryAssociation  `json:"activation"`
	Root          *managed.RecoveryAssociation `json:"root,omitempty"`
}

type recoveryActivationRow struct {
	kind, scope, key string
	raw              []byte
	insert           bool
}

// recoveryActivationRows stages only bounded rows. It cannot persist anything itself.
// Its overlay supplies the pending receipt solely for validation before Atomic accepts it.
type recoveryActivationRows struct {
	ControllerTransaction

	rows    []recoveryActivationRow
	bytes   int
	key     managed.Key
	receipt managed.Receipt
}

func (staged *recoveryActivationRows) WriteRecord(_ context.Context, kind, scope, key string, raw []byte, insert bool) error {
	for _, row := range staged.rows {
		if row.kind == kind && row.scope == scope && row.key == key {
			return managed.ErrConflict
		}
	}

	if len(staged.rows) >= 300 || len(raw) > managed.MaxRecoveryLeafSet-staged.bytes {
		return managed.ErrCapacity
	}

	staged.rows = append(staged.rows, recoveryActivationRow{kind, scope, key, append([]byte(nil), raw...), insert})
	staged.bytes += len(raw)

	return nil
}

func (staged *recoveryActivationRows) ReadRecord(ctx context.Context, kind, scope, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	for _, row := range staged.rows {
		if row.kind == kind && row.scope == scope && row.key == key {
			return append([]byte(nil), row.raw...), nil
		}
	}

	return staged.ControllerTransaction.ReadRecord(ctx, kind, scope, key)
}

func (staged *recoveryActivationRows) ReadReceipt(ctx context.Context, key managed.Key, operation id.ID, digest string) (managed.Receipt, error) {
	if err := ctx.Err(); err != nil {
		return managed.Receipt{}, err
	}

	if key == staged.key && operation == staged.receipt.ID {
		if digest != staged.receipt.Digest {
			return managed.Receipt{}, managed.ErrConflict
		}

		return staged.receipt, nil
	}

	return staged.ControllerTransaction.ReadReceipt(ctx, key, operation, digest)
}

func activationRecord[T any](kind managed.RecoveryRecordKind, facts T, acceptance managed.RecoveryAcceptance) (managed.RecoveryRecord[T], error) {
	digest, err := managed.RecoveryFactDigest(facts)
	if err != nil {
		return managed.RecoveryRecord[T]{}, err
	}

	return managed.RecoveryRecord[T]{SchemaVersion: "1", Kind: kind, Facts: facts, Digest: digest, Acceptance: acceptance}, nil
}

// stageRecoveryActivationMaterialization fixes all selections before any persistent write.
// The caller must have checked the live parent, current attempt and every original binding.
func stageRecoveryActivationMaterialization(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, plan recoveryActivationPlan) (*recoveryActivationRows, error) {
	if !view.validated || view.state.SchemaVersion != "1" || plan.Profile.Facts.AttemptRef != view.ref || plan.State.AttemptRef != view.ref || plan.State.SchemaVersion != "2" {
		return nil, managed.ErrUnavailable
	}

	selections, err := recoveryOriginalSelectionsHeld(ctx, tx, target, view)
	if err != nil {
		return nil, err
	}

	if !reflect.DeepEqual(selections, plan.Selections) {
		return nil, managed.ErrConflict
	}

	receipt, err := plan.Profile.Acceptance.Receipt.Value()
	if err != nil {
		return nil, err
	}

	staged := &recoveryActivationRows{ControllerTransaction: tx, key: target.Key, receipt: receipt}

	activation, err := recoveryWriteFact(ctx, staged, attemptKey(view), plan.Profile)
	if err != nil {
		return nil, err
	}

	if activation != plan.State.Activation {
		return nil, managed.ErrUnavailable
	}

	for index, selection := range selections {
		stepKey, keyErr := retryStepKey(attemptKey(view), selection.Step)
		if keyErr != nil {
			return nil, keyErr
		}

		for _, kind := range []string{recoveryFixedSelectionKind, retryCurrentKind, retryPreviousKind} {
			if _, readErr := tx.ReadRecord(ctx, kind, view.scope, stepKey); !errors.Is(readErr, managed.ErrNotFound) {
				if readErr != nil {
					return nil, readErr
				}

				return nil, managed.ErrConflict
			}
		}

		fixed := recoveryFixedSelection{SchemaVersion: "1", AttemptRef: view.ref, Step: selection.Step, Disposition: selection.Disposition, Activation: activation}
		if selection.Disposition == "original" {
			record, association, readErr := attemptProviderRead(ctx, tx, target, view, selection.Step, index)
			if readErr != nil {
				return nil, readErr
			}

			original := managed.ProviderEffectOriginal{Key: managed.ProviderEffectBindingKey{AttemptKey: attemptKey(view), Step: selection.Step, OriginalCommandID: record.Permission.CommandID}, AttemptRef: view.ref, Permission: managed.ProviderPermissionWire(record.Permission), Receipt: association.Receipt}

			rootFacts, readErr := providerEffectRootHeld(ctx, tx, original)
			if readErr != nil {
				return nil, readErr
			}

			root, readErr := activationRecord(managed.RecoveryRootRecord, rootFacts, plan.Profile.Acceptance)
			if readErr != nil {
				return nil, readErr
			}

			rootRef, readErr := recoveryWriteFact(ctx, staged, attemptKey(view), root)
			if readErr != nil {
				return nil, readErr
			}

			fixed.Root = &rootRef
			invocation := managed.ProviderInvocationKey{AttemptKey: attemptKey(view), Step: selection.Step, CommandID: record.Permission.CommandID}
			permissionFacts := managed.ProviderInvocationPermission{Key: invocation, RootDigest: root.Digest, Ordinal: "0", Permission: original.Permission, ProviderCommandDigest: rootFacts.ProviderCommandDigest, PermissionProofCount: "0"}

			permission, readErr := activationRecord(managed.RecoveryPermissionRecord, permissionFacts, plan.Profile.Acceptance)
			if readErr != nil {
				return nil, readErr
			}

			permissionRef, readErr := recoveryWriteFact(ctx, staged, attemptKey(view), permission)
			if readErr != nil {
				return nil, readErr
			}

			current := managed.ProviderStepRetryCurrent{SchemaVersion: "1", RootDigest: root.Digest, CommandID: invocation.CommandID, Ordinal: "0", Permission: permissionRef, Authority: rootRef}
			if record.Result != nil {
				permissionDigest, digestErr := recoveryPermissionDigest(original.Permission)
				if digestErr != nil {
					return nil, digestErr
				}

				wire := managed.ProviderStepWire(record)
				facts := managed.ProviderInvocationResult{Key: invocation, RootDigest: root.Digest, PermissionDigest: permissionDigest, Result: *wire.Result}

				result, resultErr := activationRecord(managed.RecoveryResultRecord, facts, plan.Profile.Acceptance)
				if resultErr != nil {
					return nil, resultErr
				}

				resultRef, resultErr := recoveryWriteFact(ctx, staged, attemptKey(view), result)
				if resultErr != nil {
					return nil, resultErr
				}

				current.ResultKey = resultRef.Key
			}

			current.Digest, readErr = managed.RetryCurrentDigest(current)
			if readErr != nil {
				return nil, readErr
			}

			if readErr = attemptWrite(ctx, staged, retryCurrentKind, view.scope, stepKey, current, true, managed.MaxStepRetryPointer); readErr != nil {
				return nil, readErr
			}
		} else if selection.Disposition != "unissued" || selection.Association != nil {
			return nil, managed.ErrUnavailable
		}

		if err = attemptWrite(ctx, staged, recoveryFixedSelectionKind, view.scope, stepKey, fixed, true, managed.MaxStepRetryPointer); err != nil {
			return nil, err
		}
	}

	if err = attemptWrite(ctx, staged, "removal_attempt_state", view.scope, view.ref.AttemptID.String(), plan.State, false, managed.MaxAttemptFact); err != nil {
		return nil, err
	}

	return staged, nil
}

// applyRecoveryActivationRows relies on the outer controller transaction's rollback.
// No row is persisted before the complete staged plan has passed exact validation.
func applyRecoveryActivationRows(ctx context.Context, tx ControllerTransaction, staged *recoveryActivationRows) error {
	for _, row := range staged.rows {
		if err := tx.WriteRecord(ctx, row.kind, row.scope, row.key, row.raw, row.insert); err != nil {
			return err
		}
	}

	return nil
}

// activateRecoveryHeld implements activation under an existing parent Atomic callback.
// Checked read caches end before staging and are rebuilt for the proposed schema-2 state.
func (r *Repository) activateRecoveryHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, request managed.RemovalRecoveryActivationRequest, now time.Time) (managed.RecoveryRecord[managed.RemovalRecoveryActivation], error) {
	var zero managed.RecoveryRecord[managed.RemovalRecoveryActivation]

	plan, err := r.prepareRecoveryActivationHeld(ctx, tx, target, request, now)
	if err != nil {
		return zero, err
	}

	key := managed.AttemptKey{Key: request.Command.Key, AttemptID: request.Command.AttemptRef.AttemptID, Generation: request.Command.AttemptRef.Generation}

	view, current, err := readAttemptWithCurrent(ctx, tx, target, key)
	if err != nil {
		return zero, err
	}

	if current != view.ref {
		return zero, managed.ErrConflict
	}

	staged, err := stageRecoveryActivationMaterialization(ctx, tx, target, view, plan)
	if err != nil {
		return zero, err
	}

	proposedTarget := *target
	proposedTarget.Revision = staged.receipt.Revision

	checked, selected, err := readRecoveryAttemptInspection(ctx, staged, &proposedTarget, key)
	if err != nil {
		return zero, err
	}

	if selected != checked.ref {
		return zero, managed.ErrConflict
	}

	remaining, err := recoveryActivationReserve(&proposedTarget, checked)
	if err != nil {
		return zero, err
	}

	oldAttemptRevision, err := view.state.Revision.Uint64()
	if err != nil {
		return zero, err
	}

	if checked.state.Revision != managed.Decimal(strconv.FormatUint(oldAttemptRevision+1, 10)) {
		return zero, managed.ErrUnavailable
	}

	if err = recoveryCheckHeadroom(target.Revision, oldAttemptRevision, 1, remaining); err != nil {
		return zero, err
	}
	// No checked view is used after the first write.
	view.validated = false
	checked.validated = false

	if err = applyRecoveryActivationRows(ctx, tx, staged); err != nil {
		return zero, err
	}

	target.Revision = staged.receipt.Revision

	return plan.Profile, nil
}

// ActivateManagedRemovalRecovery preserves exact replay but refuses fresh activation
// until the complete recovery interfaces and configured host are implemented.
func (r *Repository) ActivateManagedRemovalRecovery(ctx context.Context, request managed.RemovalRecoveryActivationRequest) (managed.RecoveryRecord[managed.RemovalRecoveryActivation], error) {
	var out managed.RecoveryRecord[managed.RemovalRecoveryActivation]

	command, err := request.Command.LocalCommand()
	if err != nil {
		return out, err
	}

	key := managed.AttemptKey{Key: command.Key, AttemptID: request.Command.AttemptRef.AttemptID, Generation: request.Command.AttemptRef.Generation}

	fingerprint, err := attemptHash(managed.RemovalRecoveryProfile+".activate", request)
	if err != nil {
		return out, err
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

		if view.recoveryActivation != nil {
			out, readErr = recoveryReadFact[managed.RemovalRecoveryActivation](ctx, tx, key, *view.recoveryActivation)
			if readErr != nil {
				return readErr
			}

			if out.Acceptance.OperationID != command.ID || out.Acceptance.RequestDigest != fingerprint {
				return managed.ErrConflict
			}

			return nil
		}

		if r.recoveryHost == nil {
			return managed.ErrUnsupported
		}

		if _, _, _, capabilityErr := managed.RequireRemovalRecovery(r); capabilityErr != nil {
			return capabilityErr
		}

		fresh := false

		_, atomicErr := tx.Atomic(ctx, key.Key, command.ID, fingerprint, func(current *managed.Target, now time.Time) (managed.Receipt, error) {
			var heldErr error

			out, heldErr = r.activateRecoveryHeld(ctx, tx, current, request, now)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			receipt, heldErr := out.Acceptance.Receipt.Value()
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			fresh = true

			return receipt, nil
		})
		if atomicErr != nil {
			return atomicErr
		}

		if !fresh {
			return managed.ErrUnavailable
		}

		return nil
	})
	if err != nil {
		return managed.RecoveryRecord[managed.RemovalRecoveryActivation]{}, err
	}

	return out, nil
}

// ReadManagedRecoveryActivation validates the full fixed set without restoring authority.
func (r *Repository) ReadManagedRecoveryActivation(ctx context.Context, key managed.AttemptKey) (managed.RecoveryRecord[managed.RemovalRecoveryActivation], error) {
	var out managed.RecoveryRecord[managed.RemovalRecoveryActivation]

	err := r.controller(ctx, "attempt:"+key.InstanceID.String(), []managed.Key{key.Key}, func(tx ControllerTransaction) error {
		target, readErr := tx.Read(ctx, key.Key)
		if readErr != nil {
			return readErr
		}

		view, _, readErr := readRecoveryAttemptInspection(ctx, tx, target, key)
		if readErr != nil {
			return readErr
		}

		if view.recoveryActivation == nil {
			return managed.ErrNotFound
		}

		out, readErr = recoveryReadFact[managed.RemovalRecoveryActivation](ctx, tx, key, *view.recoveryActivation)

		return readErr
	})
	if err != nil {
		return managed.RecoveryRecord[managed.RemovalRecoveryActivation]{}, err
	}

	return out, nil
}
