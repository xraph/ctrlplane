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

// invocationTransitionRequest retains the complete operation fingerprint across public replay.
type invocationTransitionRequest struct {
	Kind                  string                             `json:"kind"`
	Command               managed.RecoveryCommand            `json:"command"`
	Step                  managed.ProviderStepKey            `json:"step"`
	ExpectedCurrentDigest string                             `json:"expected_current_digest"`
	InvocationID          id.ID                              `json:"invocation_id"`
	NativeCommits         []managed.RecoveryAssociation      `json:"native_commits,omitempty"`
	Failure               *managed.ProviderInvocationFailure `json:"failure,omitempty"`
	Result                *managed.ProviderInvocationResult  `json:"result,omitempty"`
	closureVerification   *invocationVerifiedClosure
}

type invocationVerifiedClosure struct {
	facts           managed.ProviderInvocationFailure
	selectionDigest string
}

type invocationTransitionOutcome struct {
	Primary     managed.RecoveryAssociation
	Marker      *managed.RecoveryRecord[managed.ProviderInvocationMarker]
	MayDelegate bool
}

// verifyInvocationClosure must be called after the inspection transaction has returned.
// Its token pins the exact current selection so intervening custody changes refuse.
func (r *Repository) verifyInvocationClosure(ctx context.Context, selected retrySelected, facts managed.ProviderInvocationFailure) (*invocationVerifiedClosure, error) {
	if r == nil || r.backend == nil {
		return nil, managed.ErrUnsupported
	}

	if _, held := r.backend.(ControllerTransaction); held {
		return nil, managed.ErrUnsupported
	}

	verified, err := r.verifyRecoveryNoDispatch(ctx, selected.permission, selected.marker, facts)
	if err != nil {
		return nil, err
	}

	if verified.Failure.AttemptRef != selected.root.Facts.AttemptRef || selected.result != nil || selected.closure != nil {
		return nil, managed.ErrAuthority
	}

	return &invocationVerifiedClosure{facts: verified, selectionDigest: selected.current.Digest}, nil
}

func invocationPrimaryKind(kind string) (managed.RecoveryRecordKind, error) {
	switch kind {
	case "retry":
		return managed.RecoveryAuthorizationRecord, nil
	case "marker":
		return managed.RecoveryMarkerRecord, nil
	case "closure":
		return managed.RecoveryClosureRecord, nil
	case "result":
		return managed.RecoveryResultRecord, nil
	default:
		return "", managed.ErrInvalid
	}
}

func invocationReplayRecord[T any](ctx context.Context, tx ControllerTransaction, key managed.AttemptKey, kind managed.RecoveryRecordKind, factKey string, command id.ID, fingerprint string) (managed.RecoveryRecord[T], managed.RecoveryAssociation, error) {
	record, err := recoveryReadNamedFact[T](ctx, tx, key, kind, factKey)
	if err != nil {
		if errors.Is(err, managed.ErrNotFound) {
			// Only a missing primary with no accepted operation is a fresh request.
			// A present primary with lost associations must not reach a verifier.
			scope, scopeErr := attemptScope(key.Key)
			if scopeErr != nil {
				return record, managed.RecoveryAssociation{}, scopeErr
			}

			if _, rowErr := tx.ReadRecord(ctx, string(kind), scope, factKey); !errors.Is(rowErr, managed.ErrNotFound) {
				if rowErr != nil {
					return record, managed.RecoveryAssociation{}, rowErr
				}

				return record, managed.RecoveryAssociation{}, managed.ErrUnavailable
			}

			if _, receiptErr := tx.ReadReceipt(ctx, key.Key, command, ""); !errors.Is(receiptErr, managed.ErrNotFound) {
				if receiptErr != nil {
					return record, managed.RecoveryAssociation{}, receiptErr
				}

				return record, managed.RecoveryAssociation{}, managed.ErrUnavailable
			}
		}

		return record, managed.RecoveryAssociation{}, err
	}

	if record.Acceptance.OperationID != command || record.Acceptance.RequestDigest != fingerprint {
		return record, managed.RecoveryAssociation{}, managed.ErrConflict
	}

	reference, err := managed.RecoveryAssociationFor(factKey, record)

	return record, reference, err
}

// invocationReplay reads the exact accepted primary, never the current marker as a grant.
func invocationReplay(ctx context.Context, tx ControllerTransaction, request invocationTransitionRequest, fingerprint string) (invocationTransitionOutcome, error) {
	var out invocationTransitionOutcome

	key := managed.AttemptKey{Key: request.Command.Key, AttemptID: request.Command.AttemptRef.AttemptID, Generation: request.Command.AttemptRef.Generation}
	invocation := managed.ProviderInvocationKey{AttemptKey: key, Step: request.Step, CommandID: request.InvocationID}

	kind, err := invocationPrimaryKind(request.Kind)
	if err != nil {
		return out, err
	}

	var subject any

	switch request.Kind {
	case "retry":
		subject = managed.ProviderStepRetryAuthorization{Key: invocation}
	case "marker":
		subject = managed.ProviderInvocationMarker{Key: invocation}
	case "closure":
		subject = managed.ProviderInvocationClosure{Key: invocation}
	case "result":
		subject = managed.ProviderInvocationResult{Key: invocation}
	}

	factKey, err := recoveryFactKey(key, kind, request.Command.ID, subject)
	if err != nil {
		return out, err
	}

	switch request.Kind {
	case "retry":
		_, out.Primary, err = invocationReplayRecord[managed.ProviderStepRetryAuthorization](ctx, tx, key, kind, factKey, request.Command.ID, fingerprint)
	case "marker":
		var marker managed.RecoveryRecord[managed.ProviderInvocationMarker]

		marker, out.Primary, err = invocationReplayRecord[managed.ProviderInvocationMarker](ctx, tx, key, kind, factKey, request.Command.ID, fingerprint)
		if err == nil {
			out.Marker = &marker
		}
	case "closure":
		_, out.Primary, err = invocationReplayRecord[managed.ProviderInvocationClosure](ctx, tx, key, kind, factKey, request.Command.ID, fingerprint)
	case "result":
		_, out.Primary, err = invocationReplayRecord[managed.ProviderInvocationResult](ctx, tx, key, kind, factKey, request.Command.ID, fingerprint)
	}

	if err == nil {
		_, err = invocationHistoricalSelectionHeld(ctx, tx, invocation, request.Command.AttemptRef, kind, out.Primary)
		if errors.Is(err, managed.ErrNotFound) {
			err = managed.ErrUnavailable
		}
	}

	return out, err
}

func invocationSelectedHeld(ctx context.Context, tx ControllerTransaction, view attemptView, step managed.ProviderStepKey) (retrySelected, error) {
	var zero retrySelected

	stepKey, err := retryStepKey(attemptKey(view), step)
	if err != nil {
		return zero, err
	}

	fixed, err := attemptRead[recoveryFixedSelection](ctx, tx, recoveryFixedSelectionKind, view.scope, stepKey, managed.MaxStepRetryPointer)
	if err != nil {
		return zero, err
	}

	if view.recoveryActivation == nil || fixed.Activation != *view.recoveryActivation || fixed.AttemptRef != view.ref || fixed.Step != step || fixed.Disposition != "original" || fixed.Root == nil {
		return zero, managed.ErrBlocked
	}

	selected, err := retryReadSelected(ctx, tx, attemptKey(view), *fixed.Root)
	if err != nil {
		return zero, err
	}

	if err = invocationRetryEventsHeld(ctx, tx, selected); err != nil {
		return zero, err
	}

	return selected, nil
}

// invocationRemainingReserve retains the original Finish/Abort and close obligations.
// Only an accepted current terminal or marker consumes its matching invocation tail.
func invocationRemainingReserve(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView) (recoveryReserveAfter, error) {
	after, err := recoveryActivationReserve(target, view)
	if err != nil {
		return after, err
	}

	after.markerObligations, after.closureObligations = 0, 0

	steps, err := attemptSteps(target, view)
	if err != nil {
		return after, err
	}

	for index, step := range steps {
		if view.state.ProviderSlots[index]&attemptPermissionBit == 0 {
			after.markerObligations++
			after.closureObligations++

			continue
		}

		selected, readErr := invocationSelectedHeld(ctx, tx, view, step)
		if readErr != nil {
			return after, readErr
		}

		if selected.result != nil || selected.closure != nil {
			continue
		}

		if selected.marker == nil {
			after.markerObligations++
		}

		after.closureObligations++
	}

	return recoveryNativeCloseReserveHeld(ctx, tx, target, view, after)
}

func stageInvocationTransition(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, selected retrySelected, request invocationTransitionRequest, acceptance managed.RecoveryAcceptance, now time.Time) (*recoveryActivationRows, error) {
	switch request.Kind {
	case "retry":
		if request.Failure != nil || request.Result != nil || len(request.NativeCommits) != 0 {
			return nil, managed.ErrInvalid
		}

		staged, _, err := stageInvocationRetry(ctx, tx, target, view, selected, managed.ProviderStepRetryRequest{Command: request.Command, Step: request.Step, ExpectedCurrentDigest: request.ExpectedCurrentDigest, CommandID: request.InvocationID}, acceptance, now)

		return staged, err
	case "marker":
		if request.Failure != nil || request.Result != nil || request.InvocationID != selected.permission.Facts.Key.CommandID {
			return nil, managed.ErrInvalid
		}

		staged, _, err := stageInvocationMarker(ctx, tx, target, view, selected, request.ExpectedCurrentDigest, request.NativeCommits, acceptance, now)

		return staged, err
	case "closure":
		if request.Failure == nil || request.Result != nil || len(request.NativeCommits) != 0 || request.closureVerification == nil || request.InvocationID != request.Failure.Key.CommandID || request.closureVerification.selectionDigest != selected.current.Digest || !reflect.DeepEqual(request.closureVerification.facts, *request.Failure) {
			return nil, managed.ErrAuthority
		}

		staged, _, err := stageInvocationClosure(ctx, tx, selected, *request.Failure, acceptance, now)

		return staged, err
	case "result":
		if request.Result == nil || request.Failure != nil || len(request.NativeCommits) != 0 || request.InvocationID != request.Result.Key.CommandID {
			return nil, managed.ErrInvalid
		}

		staged, _, err := stageInvocationResult(ctx, tx, selected, *request.Result, acceptance, now)

		return staged, err
	default:
		return nil, managed.ErrInvalid
	}
}

// transitionInvocation executes no provider callbacks. All writes and the parent receipt
// share the backend transaction. The returned one-shot grant is set only after commit.
func (r *Repository) transitionInvocation(ctx context.Context, request invocationTransitionRequest) (invocationTransitionOutcome, error) {
	var out invocationTransitionOutcome

	command, err := request.Command.LocalCommand()
	if err != nil {
		return out, err
	}

	if r == nil || r.backend == nil {
		return out, managed.ErrUnsupported
	}

	if _, held := r.backend.(ControllerTransaction); held {
		return out, managed.ErrUnsupported
	}

	if _, err = invocationPrimaryKind(request.Kind); err != nil {
		return out, err
	}

	fingerprint, err := attemptHash("ctrlplane.managed.invocation-transition.v1", request)
	if err != nil {
		return out, err
	}

	key := managed.AttemptKey{Key: command.Key, AttemptID: request.Command.AttemptRef.AttemptID, Generation: request.Command.AttemptRef.Generation}
	fresh := false

	err = r.controller(ctx, "attempt:"+key.InstanceID.String(), []managed.Key{key.Key}, func(tx ControllerTransaction) error {
		target, readErr := tx.Read(ctx, key.Key)
		if readErr != nil {
			return readErr
		}

		view, _, readErr := readRecoveryAttemptInspection(ctx, tx, target, key)
		if readErr != nil {
			return readErr
		}

		if view.recoveryActivation == nil || view.ref != request.Command.AttemptRef {
			return managed.ErrAuthority
		}

		out, readErr = invocationReplay(ctx, tx, request, fingerprint)
		if readErr == nil {
			return nil
		}

		if !errors.Is(readErr, managed.ErrNotFound) {
			return readErr
		}

		if _, receiptErr := tx.ReadReceipt(ctx, key.Key, command.ID, ""); !errors.Is(receiptErr, managed.ErrNotFound) {
			if receiptErr != nil {
				return receiptErr
			}

			return managed.ErrUnavailable
		}

		_, atomicErr := tx.Atomic(ctx, key.Key, command.ID, fingerprint, func(current *managed.Target, now time.Time) (managed.Receipt, error) {
			if current.Revision != command.Revision || current.LeaseEpoch != command.LeaseEpoch || current.LeaseOwner != command.Owner || !now.Before(current.LeaseUntil) {
				return managed.Receipt{}, managed.ErrConflict
			}

			checked, active, heldErr := readRecoveryAttemptInspection(ctx, tx, current, key)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if checked.ref != active || active != request.Command.AttemptRef || !recoveryInvocationAfterCutoff(checked.state, request.Kind) || checked.state.SettlementDigest != "" || checked.state.TerminalDigest != "" || checked.state.Successor != nil || current.Phase == managed.Deleted {
				return managed.Receipt{}, managed.ErrBlocked
			}

			if heldErr = removalLocalIDAvailable(ctx, tx, command.ID); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if _, heldErr = attemptProviderIndex(current, checked, request.Step); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			selected, heldErr := invocationSelectedHeld(ctx, tx, checked, request.Step)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if selected.current.Digest != request.ExpectedCurrentDigest {
				return managed.Receipt{}, managed.ErrConflict
			}

			cost := uint64(1)
			if request.Kind == "result" {
				cost = 0
			}

			attemptRevision, heldErr := checked.state.Revision.Uint64()
			if heldErr != nil || current.Revision > 1<<63-1-cost || attemptRevision > 1<<63-1-cost {
				return managed.Receipt{}, managed.ErrCapacity
			}

			receipt := managed.Receipt{ID: command.ID, Digest: fingerprint, Revision: current.Revision + cost, LeaseEpoch: current.LeaseEpoch, AcceptedAt: now.UTC(), LeaseUntil: current.LeaseUntil}
			acceptance := managed.RecoveryAcceptance{OperationID: command.ID, RequestDigest: fingerprint, Receipt: managed.ReceiptWire(receipt)}

			staged, heldErr := stageInvocationTransition(ctx, tx, current, checked, selected, request, acceptance, now)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			updated, heldErr := invocationSelectedHeld(ctx, staged, checked, request.Step)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if heldErr = stageInvocationStatus(ctx, staged, updated, request.Kind == "retry"); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			state := recoveryAttemptState{AttemptState: checked.state, Activation: *checked.recoveryActivation}

			state.Revision = managed.Decimal(strconv.FormatUint(attemptRevision+cost, 10))
			if heldErr = attemptWrite(ctx, staged, "removal_attempt_state", checked.scope, checked.ref.AttemptID.String(), state, false, managed.MaxAttemptFact); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			proposed := *current
			proposed.Revision = receipt.Revision

			after, active, heldErr := readRecoveryAttemptInspection(ctx, staged, &proposed, key)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if after.ref != active {
				return managed.Receipt{}, managed.ErrConflict
			}

			reserve, heldErr := invocationRemainingReserve(ctx, staged, &proposed, after)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if heldErr = recoveryCheckHeadroom(current.Revision, attemptRevision, cost, reserve); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			out, heldErr = invocationReplay(ctx, staged, request, fingerprint)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			checked.validated, after.validated, view.validated = false, false, false

			if heldErr = applyRecoveryActivationRows(ctx, tx, staged); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			current.Revision = receipt.Revision
			fresh = true

			return receipt, nil
		})
		if atomicErr != nil {
			return atomicErr
		}

		if !fresh {
			out, atomicErr = invocationReplay(ctx, tx, request, fingerprint)
		}

		return atomicErr
	})
	if err != nil {
		return invocationTransitionOutcome{}, err
	}

	out.MayDelegate = fresh && request.Kind == "marker"

	return out, nil
}
