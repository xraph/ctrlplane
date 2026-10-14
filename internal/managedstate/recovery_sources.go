package managedstate

import (
	"context"
	"encoding/json"

	"github.com/xraph/ctrlplane/managed"
)

var _ managed.ProviderInvocationSourcesStore = (*Repository)(nil)
var _ managed.RemovalCutoffReader = (*Repository)(nil)

// ReadManagedProviderInvocationSources returns retained sources without checking
// current lease ownership or issuing callbacks, writes, or delegation authority.
func (r *Repository) ReadManagedProviderInvocationSources(ctx context.Context, key managed.ProviderInvocationKey) (managed.ProviderInvocationSources, error) {
	var out managed.ProviderInvocationSources
	if r == nil || r.backend == nil {
		return out, managed.ErrUnsupported
	}

	if _, err := invocationLocatorKey(key); err != nil {
		return out, err
	}

	err := r.controller(ctx, "attempt:"+key.AttemptKey.InstanceID.String(), []managed.Key{key.AttemptKey.Key}, func(tx ControllerTransaction) error {
		target, readErr := tx.Read(ctx, key.AttemptKey.Key)
		if readErr != nil {
			return readErr
		}

		view, _, readErr := readRecoveryAttemptInspection(ctx, tx, target, key.AttemptKey)
		if readErr != nil {
			return readErr
		}

		if _, readErr = attemptProviderIndex(target, view, key.Step); readErr != nil {
			return readErr
		}

		selected, readErr := invocationStatusSelectionHeld(ctx, tx, view, key)
		if readErr != nil {
			return readErr
		}

		out, readErr = invocationSourcesHeld(ctx, tx, target, view, selected)

		return readErr
	})
	if err != nil {
		return managed.ProviderInvocationSources{}, err
	}

	return out, nil
}

func invocationSourcesHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, selected retrySelected) (managed.ProviderInvocationSources, error) {
	var zero managed.ProviderInvocationSources

	permission := selected.permission

	key := permission.Facts.Key
	if !view.validated || key.AttemptKey != attemptKey(view) || selected.root.Facts.AttemptRef != view.ref || permission.Facts.RootDigest != selected.root.Digest {
		return zero, managed.ErrAuthority
	}

	count, err := permission.Facts.PermissionProofCount.Uint64()
	if err != nil || count > managed.MaxRecoveryBindings || len(view.descriptor.ReservationIDs) > managed.MaxRecoveryBindings {
		return zero, managed.ErrCapacity
	}

	out := managed.ProviderInvocationSources{Root: selected.root, PermissionProofs: make([]managed.RecoveryRecord[managed.ProviderPermissionProof], 0, int(count))}
	if count == 0 {
		if permission.Facts.Ordinal != "0" || permission.Facts.PermissionProofSetDigest != "" {
			return zero, managed.ErrUnavailable
		}

		return out, nil
	}

	permissionDigest, err := recoveryPermissionDigest(permission.Facts.Permission)
	if err != nil {
		return zero, err
	}

	refs := make([]managed.RecoveryAssociation, 0, int(count))

	for _, reservationID := range view.descriptor.ReservationIDs {
		retained, readErr := reservation(target, reservationID)
		if readErr != nil {
			return zero, readErr
		}

		if retained.Phase == managed.Revoked {
			continue
		}

		binding, readErr := recoveryBindingInspectionHeld(ctx, tx, target, view, attemptBinding(view, reservationID))
		if readErr != nil {
			return zero, readErr
		}

		bindingDigest, readErr := recoveryBindingKey(binding)
		if readErr != nil {
			return zero, readErr
		}

		rowKey, readErr := invocationPermissionProofKey(key, permission.Acceptance.OperationID, bindingDigest)
		if readErr != nil {
			return zero, readErr
		}

		proof, readErr := recoveryReadNamedFact[managed.ProviderPermissionProof](ctx, tx, key.AttemptKey, managed.RecoveryPermissionProofRecord, rowKey)
		if readErr != nil {
			return zero, readErr
		}

		if proof.Acceptance != permission.Acceptance || proof.Facts.Invocation != key || proof.Facts.BindingDigest != bindingDigest || proof.Facts.PermissionDigest != permissionDigest {
			return zero, managed.ErrUnavailable
		}

		native, readErr := recoveryProofExactHeld(ctx, tx, target, view, proof.Facts.Proof)
		if readErr != nil {
			return zero, readErr
		}

		if native.Facts.Binding != binding || native.Facts.NativeMaterialDigest != proof.Facts.NativeMaterialDigest || native.Acceptance.Receipt.AcceptedAt.After(permission.Facts.Permission.IssuedAt) || !managed.RecoveryProofFresh(native.Facts, permission.Facts.Permission.IssuedAt) {
			return zero, managed.ErrUnavailable
		}

		reference, readErr := managed.RecoveryAssociationFor(rowKey, proof)
		if readErr != nil {
			return zero, readErr
		}

		refs = append(refs, reference)
		out.PermissionProofs = append(out.PermissionProofs, proof)
	}

	setDigest, err := attemptHash("ctrlplane.managed.permission-proof-set.v1", refs)
	if err != nil {
		return zero, err
	}

	if uint64(len(refs)) != count || setDigest != permission.Facts.PermissionProofSetDigest {
		return zero, managed.ErrUnavailable
	}

	raw, err := json.Marshal(out)
	if err != nil {
		return zero, err
	}

	if len(raw) > managed.MaxRecoveryLeafSet {
		return zero, managed.ErrCapacity
	}

	return out, nil
}

// ReadManagedRemovalCutoffExact validates permanent revocation and its original
// event/receipt before comparing the complete caller-supplied association.
func (r *Repository) ReadManagedRemovalCutoffExact(ctx context.Context, request managed.RecoveryExactRequest) (managed.RecoveryRecord[managed.RemovalCutoffFacts], error) {
	var out managed.RecoveryRecord[managed.RemovalCutoffFacts]
	if r == nil || r.backend == nil {
		return out, managed.ErrUnsupported
	}

	if err := attemptVersion(request.Key); err != nil {
		return out, err
	}

	if request.Association.Kind != managed.RecoveryCutoffRecord {
		return out, managed.ErrInvalid
	}

	if err := managed.ValidateRecoveryAssociation(request.Association); err != nil {
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

		out, readErr = recoveryCutoffHeld(ctx, tx, view)
		if readErr != nil {
			return readErr
		}

		reference, readErr := managed.RecoveryRecordAssociation(request.Key, out)
		if readErr != nil {
			return readErr
		}

		if reference != request.Association {
			return managed.ErrUnavailable
		}

		return nil
	})
	if err != nil {
		return managed.RecoveryRecord[managed.RemovalCutoffFacts]{}, err
	}

	return out, nil
}
