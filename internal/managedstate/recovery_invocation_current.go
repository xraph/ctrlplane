package managedstate

import (
	"context"

	"github.com/xraph/ctrlplane/managed"
)

var _ managed.ProviderInvocationCurrentStore = (*Repository)(nil)

// ReadManagedProviderInvocationCurrent binds the requested invocation to its
// complete current pointer, receipts and status within one authority snapshot.
func (r *Repository) ReadManagedProviderInvocationCurrent(ctx context.Context, key managed.ProviderInvocationKey) (managed.ProviderStepRetryCurrent, error) {
	var out managed.ProviderStepRetryCurrent
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

		selected, readErr := invocationSelectedHeld(ctx, tx, view, key.Step)
		if readErr != nil {
			return readErr
		}

		if selected.permission.Facts.Key != key {
			return managed.ErrConflict
		}

		if readErr = invocationStatusMatchesHeld(ctx, tx, view, selected); readErr != nil {
			return readErr
		}

		if readErr = ctx.Err(); readErr != nil {
			return readErr
		}

		out = selected.current

		return nil
	})
	if err != nil {
		return managed.ProviderStepRetryCurrent{}, err
	}

	return out, nil
}
