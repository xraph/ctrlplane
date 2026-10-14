package managedstate

import (
	"context"
	"reflect"

	"github.com/xraph/ctrlplane/managed"
)

// invocationStatusSelectionHeld reads the complete named invocation using its fixed
// locator and status. Only its immediate predecessor certificate is inspected.
func invocationStatusSelectionHeld(ctx context.Context, tx ControllerTransaction, view attemptView, key managed.ProviderInvocationKey) (retrySelected, error) {
	var zero retrySelected
	if !view.validated || attemptKey(view) != key.AttemptKey || view.recoveryActivation == nil {
		return zero, managed.ErrAuthority
	}

	status, err := invocationStatusRead(ctx, tx, key)
	if err != nil {
		return zero, err
	}

	stepKey, err := retryStepKey(key.AttemptKey, key.Step)
	if err != nil {
		return zero, err
	}

	rootReference, err := recoveryInvocationRootReferenceHeld(ctx, tx, view, key.Step)
	if err != nil {
		return zero, err
	}

	root, err := recoveryReadFact[managed.ProviderStepRetryRoot](ctx, tx, key.AttemptKey, rootReference)
	if err != nil {
		return zero, err
	}

	permission, err := recoveryReadFact[managed.ProviderInvocationPermission](ctx, tx, key.AttemptKey, status.Permission)
	if err != nil {
		return zero, err
	}

	if permission.Facts.Key != key || root.Facts.AttemptRef != view.ref {
		return zero, managed.ErrUnavailable
	}

	current := managed.ProviderStepRetryCurrent{SchemaVersion: "1", RootDigest: root.Digest, CommandID: key.CommandID, Ordinal: permission.Facts.Ordinal, Permission: status.Permission}

	var previous *managed.ProviderStepRetryPrevious

	if current.Ordinal == "0" {
		current.Authority = rootReference

		if permission.Acceptance != root.Acceptance {
			return zero, managed.ErrUnavailable
		}
	} else {
		locatorKey, keyErr := invocationLocatorKey(key)
		if keyErr != nil {
			return zero, keyErr
		}

		locator, readErr := attemptRead[invocationLocator](ctx, tx, invocationLocatorKind, view.scope, locatorKey, maxInvocationLocator)
		if readErr != nil {
			return zero, readErr
		}

		if locator.SchemaVersion != "1" || locator.Key != key || locator.Root != rootReference || locator.Permission != status.Permission {
			return zero, managed.ErrUnavailable
		}

		authority, readErr := recoveryReadFact[managed.ProviderStepRetryAuthorization](ctx, tx, key.AttemptKey, locator.Authority)
		if readErr != nil {
			return zero, readErr
		}

		current.Authority = locator.Authority
		previous = &authority.Facts.Previous
		current.PreviousDigest = previous.Digest
	}

	if status.Marker != nil {
		if status.Marker.Kind != managed.RecoveryMarkerRecord {
			return zero, managed.ErrUnavailable
		}

		if _, err = recoveryReadFact[managed.ProviderInvocationMarker](ctx, tx, key.AttemptKey, *status.Marker); err != nil {
			return zero, err
		}

		current.MarkerKey = status.Marker.Key
	}

	if status.Result != nil {
		if status.Result.Kind != managed.RecoveryResultRecord {
			return zero, managed.ErrUnavailable
		}

		if _, err = recoveryReadFact[managed.ProviderInvocationResult](ctx, tx, key.AttemptKey, *status.Result); err != nil {
			return zero, err
		}

		current.ResultKey = status.Result.Key
	}

	if status.Closure != nil {
		if status.Closure.Kind != managed.RecoveryClosureRecord || status.Failure.Kind != managed.RecoveryFailureRecord {
			return zero, managed.ErrUnavailable
		}

		if _, err = recoveryReadFact[managed.ProviderInvocationClosure](ctx, tx, key.AttemptKey, *status.Closure); err != nil {
			return zero, err
		}

		if _, err = recoveryReadFact[managed.ProviderInvocationFailure](ctx, tx, key.AttemptKey, *status.Failure); err != nil {
			return zero, err
		}

		current.ClosureKey, current.FailureKey = status.Closure.Key, status.Failure.Key
	}

	current.Digest, err = managed.RetryCurrentDigest(current)
	if err != nil {
		return zero, err
	}

	reads := &invocationHistoricalCertificateReads{ControllerTransaction: tx, scope: view.scope, stepKey: stepKey, current: current, previous: previous}

	selected, err := retryReadSelected(ctx, reads, key.AttemptKey, rootReference)
	if err != nil {
		return zero, err
	}

	if err = invocationRetryEventsHeld(ctx, tx, selected); err != nil {
		return zero, err
	}

	if selected.marker != nil {
		if _, err = invocationMarkerMembersHeld(ctx, tx, selected); err != nil {
			return zero, err
		}
	}

	return selected, nil
}

func invocationStatusMatchesHeld(ctx context.Context, tx ControllerTransaction, view attemptView, selected retrySelected) error {
	historical, err := invocationStatusSelectionHeld(ctx, tx, view, selected.permission.Facts.Key)
	if err != nil {
		return err
	}

	if !reflect.DeepEqual(historical.current, selected.current) {
		return managed.ErrUnavailable
	}

	return nil
}

// ReadManagedProviderInvocation inspects accepted history and cannot restore a grant.
func (r *Repository) ReadManagedProviderInvocation(ctx context.Context, key managed.ProviderInvocationKey) (managed.ProviderInvocationRecord, error) {
	var out managed.ProviderInvocationRecord
	if r == nil || r.backend == nil {
		return out, managed.ErrUnsupported
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

		out = managed.ProviderInvocationRecord{Permission: selected.permission, Authorization: selected.authority, Marker: selected.marker, Closure: selected.closure, Failure: selected.failure, Result: selected.result}

		return nil
	})
	if err != nil {
		return managed.ProviderInvocationRecord{}, err
	}

	return out, nil
}

// ReadManagedProviderStepRetry returns one exact accepted certificate without selecting it.
func (r *Repository) ReadManagedProviderStepRetry(ctx context.Context, key managed.ProviderInvocationKey) (managed.RecoveryRecord[managed.ProviderStepRetryAuthorization], error) {
	record, err := r.ReadManagedProviderInvocation(ctx, key)
	if err != nil {
		return managed.RecoveryRecord[managed.ProviderStepRetryAuthorization]{}, err
	}

	if record.Authorization == nil {
		return managed.RecoveryRecord[managed.ProviderStepRetryAuthorization]{}, managed.ErrNotFound
	}

	return *record.Authorization, nil
}
