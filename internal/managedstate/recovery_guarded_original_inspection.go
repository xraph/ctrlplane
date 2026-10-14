package managedstate

import (
	"context"
	"errors"
	"reflect"
	"strconv"

	"github.com/xraph/ctrlplane/managed"
)

// recoveryInvocationRootReferenceHeld resolves a fixed original or the additional
// guarded selector. Full attempt inspection authenticates either authority graph.
func recoveryInvocationRootReferenceHeld(ctx context.Context, tx ControllerTransaction, view attemptView, step managed.ProviderStepKey) (managed.RecoveryAssociation, error) {
	var zero managed.RecoveryAssociation

	key, err := retryStepKey(attemptKey(view), step)
	if err != nil {
		return zero, err
	}

	fixed, err := attemptRead[recoveryFixedSelection](ctx, tx, recoveryFixedSelectionKind, view.scope, key, managed.MaxStepRetryPointer)
	if err != nil {
		return zero, err
	}

	if fixed.SchemaVersion != "1" || view.recoveryActivation == nil || fixed.Activation != *view.recoveryActivation || fixed.AttemptRef != view.ref || fixed.Step != step {
		return zero, managed.ErrAuthority
	}

	switch fixed.Disposition {
	case "original":
		if fixed.Root == nil {
			return zero, managed.ErrUnavailable
		}

		return *fixed.Root, nil
	case "unissued":
		if fixed.Root != nil {
			return zero, managed.ErrUnavailable
		}

		selected, readErr := guardedOriginalSelectionHeld(ctx, tx, view, step)
		if readErr != nil {
			return zero, readErr
		}

		if selected.Root == nil {
			return zero, managed.ErrBlocked
		}

		return *selected.Root, nil
	default:
		return zero, managed.ErrUnavailable
	}
}

func inspectGuardedOriginalHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, step managed.ProviderStepKey) error {
	pending, selector, err := guardedOriginalPendingHeld(ctx, tx, target, view, step)
	if err != nil {
		return err
	}

	key, err := retryStepKey(attemptKey(view), step)
	if err != nil {
		return err
	}

	if selector.Root == nil {
		for _, kind := range []string{retryCurrentKind, retryPreviousKind} {
			if _, readErr := tx.ReadRecord(ctx, kind, view.scope, key); !errors.Is(readErr, managed.ErrNotFound) {
				if readErr != nil {
					return readErr
				}

				return managed.ErrUnavailable
			}
		}

		return nil
	}

	root, err := recoveryReadFact[managed.ProviderStepRetryRoot](ctx, tx, attemptKey(view), *selector.Root)
	if err != nil {
		return err
	}

	effect, err := providerEffectReadHeld(ctx, tx, pending.Original)
	if err != nil {
		return err
	}

	expected, err := providerEffectRootHeld(ctx, tx, pending.Original)
	if err != nil {
		return err
	}

	if !reflect.DeepEqual(root.Facts, expected) {
		return managed.ErrUnavailable
	}

	if root.Acceptance.Receipt.AcceptedAt.Before(effect.Acceptance.Receipt.AcceptedAt) || root.Acceptance.Receipt.AcceptedAt.Before(pending.Original.Receipt.AcceptedAt) {
		return managed.ErrUnavailable
	}

	rootRevision, err := root.Acceptance.Receipt.Revision.Uint64()
	if err != nil {
		return err
	}

	effectRevision, err := effect.Acceptance.Receipt.Revision.Uint64()
	if err != nil {
		return err
	}

	pendingRevision, err := pending.Original.Receipt.Revision.Uint64()
	if err != nil {
		return err
	}

	if rootRevision < effectRevision || rootRevision < pendingRevision {
		return managed.ErrUnavailable
	}

	witness, err := attemptRead[guardedOriginalMaterialization](ctx, tx, guardedOriginalMaterializationKind, view.scope, root.Acceptance.OperationID.String(), managed.MaxAttemptFact)
	if err != nil {
		return err
	}

	fingerprint, err := guardedOriginalMaterializationDigest(witness.Request, effect.Digest)
	if err != nil {
		return err
	}

	if witness.SchemaVersion != "1" || witness.Root != *selector.Root || witness.Request.Command.ID != root.Acceptance.OperationID || witness.Request.Command.AttemptRef != view.ref || witness.Request.Step != step || witness.Request.PendingDigest != pending.Digest || witness.EffectDigest != effect.Digest || fingerprint != root.Acceptance.RequestDigest || root.Acceptance.Receipt.Revision != witness.Request.Command.Revision {
		return managed.ErrUnavailable
	}

	if err = recoveryProtocolEventHeld(ctx, tx, view, "recovery_original_materialize", *selector.Root, root.Acceptance); err != nil {
		return err
	}

	invocation := managed.ProviderInvocationKey{AttemptKey: attemptKey(view), Step: step, CommandID: pending.Original.Permission.CommandID}

	permissionKey, err := recoveryFactKey(attemptKey(view), managed.RecoveryPermissionRecord, root.Acceptance.OperationID, managed.ProviderInvocationPermission{Key: invocation})
	if err != nil {
		return err
	}

	permission, err := recoveryReadNamedFact[managed.ProviderInvocationPermission](ctx, tx, attemptKey(view), managed.RecoveryPermissionRecord, permissionKey)
	if err != nil {
		return err
	}

	permissionDigest, err := recoveryPermissionDigest(pending.Original.Permission)
	if err != nil {
		return err
	}

	refs := make([]managed.RecoveryAssociation, 0, len(pending.Proofs))
	for _, reference := range pending.Proofs {
		proof, readErr := recoveryReadFact[managed.RemovalRevalidationFacts](ctx, tx, attemptKey(view), reference)
		if readErr != nil {
			return readErr
		}

		bindingDigest, readErr := recoveryBindingKey(proof.Facts.Binding)
		if readErr != nil {
			return readErr
		}

		proofKey, readErr := invocationPermissionProofKey(invocation, root.Acceptance.OperationID, bindingDigest)
		if readErr != nil {
			return readErr
		}

		normalized, readErr := recoveryReadNamedFact[managed.ProviderPermissionProof](ctx, tx, attemptKey(view), managed.RecoveryPermissionProofRecord, proofKey)
		if readErr != nil {
			return readErr
		}

		expectedProof := managed.ProviderPermissionProof{Invocation: invocation, BindingDigest: bindingDigest, PermissionDigest: permissionDigest, Proof: reference, NativeMaterialDigest: proof.Facts.NativeMaterialDigest}
		if normalized.Acceptance != root.Acceptance || normalized.Facts != expectedProof {
			return managed.ErrUnavailable
		}

		normalizedRef, readErr := managed.RecoveryAssociationFor(proofKey, normalized)
		if readErr != nil {
			return readErr
		}

		refs = append(refs, normalizedRef)
	}

	setDigest, err := attemptHash("ctrlplane.managed.permission-proof-set.v1", refs)
	if err != nil {
		return err
	}

	expectedPermission := managed.ProviderInvocationPermission{Key: invocation, RootDigest: root.Digest, Ordinal: "0", Permission: pending.Original.Permission, ProviderCommandDigest: root.Facts.ProviderCommandDigest, PermissionProofSetDigest: setDigest, PermissionProofCount: managed.Decimal(strconv.Itoa(len(refs)))}
	if permission.Acceptance != root.Acceptance || !reflect.DeepEqual(permission.Facts, expectedPermission) {
		return managed.ErrUnavailable
	}

	selected, err := retryReadSelected(ctx, tx, attemptKey(view), *selector.Root)
	if err != nil {
		return err
	}

	if err = invocationRetryEventsHeld(ctx, tx, selected); err != nil {
		return err
	}

	return invocationStatusMatchesHeld(ctx, tx, view, selected)
}
