package managedstate

import (
	"context"
	"encoding/json"

	"github.com/xraph/ctrlplane/managed"
)

const invocationLocatorKind = "recovery_invocation_locator"
const maxInvocationLocator = 4096

// invocationLocator preserves the issuing operation when its ID differs from the
// invocation ID. Accepted permission and authorization receipts remain authoritative.
type invocationLocator struct {
	SchemaVersion managed.Decimal               `json:"schema_version"`
	Key           managed.ProviderInvocationKey `json:"key"`
	Root          managed.RecoveryAssociation   `json:"root"`
	Permission    managed.RecoveryAssociation   `json:"permission"`
	Authority     managed.RecoveryAssociation   `json:"authority"`
}

func invocationLocatorKey(key managed.ProviderInvocationKey) (string, error) {
	if _, err := retryStepKey(key.AttemptKey, key.Step); err != nil {
		return "", err
	}

	if key.CommandID.Prefix() != managed.PrefixOperation {
		return "", managed.ErrInvalid
	}

	return attemptHash("ctrlplane.managed.invocation-locator.v1", key)
}

func stageInvocationRetryEvent(ctx context.Context, tx ControllerTransaction, view attemptView, root managed.RecoveryRecord[managed.ProviderStepRetryRoot], authority managed.RecoveryRecord[managed.ProviderStepRetryAuthorization], reference managed.RecoveryAssociation) error {
	rootKey, err := recoveryFactKey(attemptKey(view), managed.RecoveryRootRecord, root.Acceptance.OperationID, root.Facts)
	if err != nil {
		return err
	}

	rootRef, err := managed.RecoveryAssociationFor(rootKey, root)
	if err != nil {
		return err
	}

	locatorKey, err := invocationLocatorKey(authority.Facts.Key)
	if err != nil {
		return err
	}

	locator := invocationLocator{SchemaVersion: "1", Key: authority.Facts.Key, Root: rootRef, Permission: authority.Facts.Permission, Authority: reference}
	if err = attemptWrite(ctx, tx, invocationLocatorKind, view.scope, locatorKey, locator, true, maxInvocationLocator); err != nil {
		return err
	}

	raw, err := tx.ReadRecord(ctx, string(reference.Kind), view.scope, reference.Key)
	if err != nil {
		return err
	}

	factDigest, err := attemptStoredFactDigest(string(reference.Kind), raw)
	if err != nil {
		return err
	}

	receipt, err := authority.Acceptance.Receipt.Value()
	if err != nil {
		return err
	}

	event := attemptEvent{SchemaVersion: "1", AttemptRef: view.ref, Kind: "provider_step_retry", RequestDigest: authority.Acceptance.RequestDigest, FactKind: string(reference.Kind), FactKey: reference.Key, FactDigest: factDigest, Receipt: receipt}

	return attemptWrite(ctx, tx, "removal_attempt_event", view.scope, authority.Acceptance.OperationID.String(), event, true, managed.MaxAttemptFact)
}

func invocationRetryEventHeld(ctx context.Context, tx ControllerTransaction, key managed.AttemptKey, reference managed.AttemptRef, authority managed.RecoveryRecord[managed.ProviderStepRetryAuthorization]) error {
	scope, err := attemptScope(key.Key)
	if err != nil {
		return err
	}

	view := attemptView{scope: scope, ref: reference, descriptor: managed.AttemptDescriptor{Key: key.Key}}

	event, err := attemptEventRead(ctx, tx, view, authority.Acceptance.OperationID)
	if err != nil {
		return err
	}

	factKey, err := recoveryFactKey(key, managed.RecoveryAuthorizationRecord, authority.Acceptance.OperationID, authority.Facts)
	if err != nil {
		return err
	}

	receipt, err := authority.Acceptance.Receipt.Value()
	if err != nil {
		return err
	}

	if event.Kind != "provider_step_retry" || event.RequestDigest != authority.Acceptance.RequestDigest || event.FactKind != string(managed.RecoveryAuthorizationRecord) || event.FactKey != factKey || event.Receipt != receipt {
		return managed.ErrUnavailable
	}

	return nil
}

func invocationRetryEventsHeld(ctx context.Context, tx ControllerTransaction, selected retrySelected) error {
	key := selected.permission.Facts.Key.AttemptKey
	if selected.authority != nil {
		if selected.authority.Acceptance != selected.permission.Acceptance {
			return managed.ErrUnavailable
		}

		if err := invocationRetryEventHeld(ctx, tx, key, selected.root.Facts.AttemptRef, *selected.authority); err != nil {
			return err
		}
	}

	if selected.previous != nil && selected.previous.Ordinal != "0" {
		previous, err := recoveryReadFact[managed.ProviderStepRetryAuthorization](ctx, tx, key, selected.previous.Authority)
		if err != nil {
			return err
		}

		if err = invocationRetryEventHeld(ctx, tx, key, selected.root.Facts.AttemptRef, previous); err != nil {
			return err
		}
	}

	return nil
}

// invocationHistoricalCertificateReads supplies only a named invocation's fixed pointer
// and its saved immediate predecessor to the existing exact certificate reader.
type invocationHistoricalCertificateReads struct {
	ControllerTransaction

	scope, stepKey string
	current        managed.ProviderStepRetryCurrent
	previous       *managed.ProviderStepRetryPrevious
}

func (reads *invocationHistoricalCertificateReads) ReadRecord(ctx context.Context, kind, scope, key string) ([]byte, error) {
	if scope == reads.scope && key == reads.stepKey {
		switch kind {
		case retryCurrentKind:
			return json.Marshal(reads.current)
		case retryPreviousKind:
			if reads.previous == nil {
				return nil, managed.ErrNotFound
			}

			return json.Marshal(*reads.previous)
		}
	}

	return reads.ControllerTransaction.ReadRecord(ctx, kind, scope, key)
}

// invocationHistoricalSelectionHeld never reads or rotates the live current pointer.
// Its locator is only an address: the named permission, authority, immediate predecessor,
// event and independent receipts must all validate before returning inspection facts.
func invocationHistoricalSelectionHeld(ctx context.Context, tx ControllerTransaction, key managed.ProviderInvocationKey, reference managed.AttemptRef, kind managed.RecoveryRecordKind, primary managed.RecoveryAssociation) (retrySelected, error) {
	var zero retrySelected

	scope, err := attemptScope(key.AttemptKey.Key)
	if err != nil {
		return zero, err
	}

	stepKey, err := retryStepKey(key.AttemptKey, key.Step)
	if err != nil {
		return zero, err
	}

	fixed, err := attemptRead[recoveryFixedSelection](ctx, tx, recoveryFixedSelectionKind, scope, stepKey, managed.MaxStepRetryPointer)
	if err != nil {
		return zero, err
	}

	if fixed.Disposition == "unissued" && fixed.Root == nil {
		selected, readErr := attemptRead[guardedOriginalSelection](ctx, tx, guardedOriginalSelectionKind, scope, stepKey, managed.MaxStepRetryPointer)
		if readErr != nil {
			return zero, readErr
		}

		if selected.SchemaVersion != "1" || selected.AttemptRef != reference || selected.Step != key.Step || selected.Root == nil {
			return zero, managed.ErrUnavailable
		}

		fixed.Root = selected.Root
	} else if fixed.Disposition != "original" {
		return zero, managed.ErrUnavailable
	}

	if fixed.SchemaVersion != "1" || fixed.AttemptRef != reference || fixed.Step != key.Step || fixed.Root == nil {
		return zero, managed.ErrUnavailable
	}

	root, err := recoveryReadFact[managed.ProviderStepRetryRoot](ctx, tx, key.AttemptKey, *fixed.Root)
	if err != nil {
		return zero, err
	}

	if root.Facts.AttemptRef != reference {
		return zero, managed.ErrAuthority
	}

	current := managed.ProviderStepRetryCurrent{SchemaVersion: "1", RootDigest: root.Digest, CommandID: key.CommandID}

	var previous *managed.ProviderStepRetryPrevious

	if key.CommandID == root.Facts.OriginalPermission.CommandID {
		permissionKey, keyErr := recoveryFactKey(key.AttemptKey, managed.RecoveryPermissionRecord, root.Acceptance.OperationID, managed.ProviderInvocationPermission{Key: key})
		if keyErr != nil {
			return zero, keyErr
		}

		permission, readErr := recoveryReadNamedFact[managed.ProviderInvocationPermission](ctx, tx, key.AttemptKey, managed.RecoveryPermissionRecord, permissionKey)
		if readErr != nil {
			return zero, readErr
		}

		current.Permission, err = managed.RecoveryAssociationFor(permissionKey, permission)
		if err != nil {
			return zero, err
		}

		if permission.Acceptance != root.Acceptance {
			return zero, managed.ErrUnavailable
		}

		current.Ordinal, current.Authority = "0", *fixed.Root
	} else {
		locatorKey, keyErr := invocationLocatorKey(key)
		if keyErr != nil {
			return zero, keyErr
		}

		locator, readErr := attemptRead[invocationLocator](ctx, tx, invocationLocatorKind, scope, locatorKey, maxInvocationLocator)
		if readErr != nil {
			return zero, readErr
		}

		if locator.SchemaVersion != "1" || locator.Key != key || locator.Root != *fixed.Root {
			return zero, managed.ErrUnavailable
		}

		authority, readErr := recoveryReadFact[managed.ProviderStepRetryAuthorization](ctx, tx, key.AttemptKey, locator.Authority)
		if readErr != nil {
			return zero, readErr
		}

		if authority.Facts.Key != key || authority.Facts.Permission != locator.Permission || authority.Facts.RootDigest != root.Digest {
			return zero, managed.ErrUnavailable
		}

		current.Permission, current.Authority, current.Ordinal = locator.Permission, locator.Authority, authority.Facts.Ordinal
		previous = &authority.Facts.Previous
		current.PreviousDigest = previous.Digest

		if err = invocationRetryEventHeld(ctx, tx, key.AttemptKey, reference, authority); err != nil {
			return zero, err
		}
	}

	switch kind {
	case managed.RecoveryAuthorizationRecord:
		if current.Authority != primary {
			return zero, managed.ErrAuthority
		}
	case managed.RecoveryMarkerRecord:
		current.MarkerKey = primary.Key
	case managed.RecoveryResultRecord:
		current.ResultKey = primary.Key
	case managed.RecoveryClosureRecord:
		closure, readErr := recoveryReadFact[managed.ProviderInvocationClosure](ctx, tx, key.AttemptKey, primary)
		if readErr != nil {
			return zero, readErr
		}

		current.ClosureKey = primary.Key

		current.FailureKey, err = recoveryFactKey(key.AttemptKey, managed.RecoveryFailureRecord, closure.Acceptance.OperationID, managed.ProviderInvocationFailure{Key: key})
		if err != nil {
			return zero, err
		}
	default:
		return zero, managed.ErrInvalid
	}

	current.Digest, err = managed.RetryCurrentDigest(current)
	if err != nil {
		return zero, err
	}

	reads := &invocationHistoricalCertificateReads{ControllerTransaction: tx, scope: scope, stepKey: stepKey, current: current, previous: previous}

	selected, err := retryReadSelected(ctx, reads, key.AttemptKey, *fixed.Root)
	if err != nil {
		return zero, err
	}

	if err = invocationRetryEventsHeld(ctx, tx, selected); err != nil {
		return zero, err
	}

	return selected, nil
}
