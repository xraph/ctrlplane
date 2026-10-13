package managedstate

import (
	"context"
	"errors"

	"github.com/xraph/ctrlplane/managed"
)

func (r *Repository) requireConfiguredRecovery() error {
	if r == nil || r.backend == nil || r.recoveryHost == nil {
		return managed.ErrUnsupported
	}

	if _, held := r.backend.(ControllerTransaction); held {
		return managed.ErrUnsupported
	}

	_, _, _, err := managed.RequireRemovalRecovery(r) //nolint:dogsled // Only the complete capability check is needed here.

	return err
}

func invocationCommandMatches(command managed.RecoveryCommand, invocation managed.ProviderInvocationKey) error {
	if _, err := command.LocalCommand(); err != nil {
		return err
	}

	key := managed.AttemptKey{Key: command.Key, AttemptID: command.AttemptRef.AttemptID, Generation: command.AttemptRef.Generation}
	if invocation.AttemptKey != key {
		return managed.ErrAuthority
	}

	return nil
}

func (r *Repository) invocationPublicReplay(ctx context.Context, request invocationTransitionRequest) (invocationTransitionOutcome, error) {
	var out invocationTransitionOutcome
	if r == nil || r.backend == nil {
		return out, managed.ErrUnsupported
	}

	if _, err := request.Command.LocalCommand(); err != nil {
		return out, err
	}

	fingerprint, err := attemptHash("ctrlplane.managed.invocation-transition.v1", request)
	if err != nil {
		return out, err
	}

	key := managed.AttemptKey{Key: request.Command.Key, AttemptID: request.Command.AttemptRef.AttemptID, Generation: request.Command.AttemptRef.Generation}

	err = r.controller(ctx, "attempt:"+key.InstanceID.String(), []managed.Key{key.Key}, func(tx ControllerTransaction) error {
		target, readErr := tx.Read(ctx, key.Key)
		if readErr != nil {
			return readErr
		}

		view, _, readErr := readRecoveryAttemptInspection(ctx, tx, target, key)
		if errors.Is(readErr, managed.ErrNotFound) {
			return managed.ErrUnavailable
		}

		if readErr != nil {
			return readErr
		}

		if view.ref != request.Command.AttemptRef || view.recoveryActivation == nil {
			return managed.ErrAuthority
		}

		out, readErr = invocationReplay(ctx, tx, request, fingerprint)
		if readErr != nil {
			return readErr
		}

		_, readErr = invocationStatusSelectionHeld(ctx, tx, view, managed.ProviderInvocationKey{AttemptKey: key, Step: request.Step, CommandID: request.InvocationID})
		if errors.Is(readErr, managed.ErrNotFound) {
			return managed.ErrUnavailable
		}

		return readErr
	})
	if err != nil {
		return invocationTransitionOutcome{}, err
	}

	return out, nil
}

func (r *Repository) invocationPublicTransition(ctx context.Context, request invocationTransitionRequest) (invocationTransitionOutcome, error) {
	out, err := r.invocationPublicReplay(ctx, request)
	if err == nil {
		return out, nil
	}

	if !errors.Is(err, managed.ErrNotFound) {
		return invocationTransitionOutcome{}, err
	}

	if err = r.requireConfiguredRecovery(); err != nil {
		return invocationTransitionOutcome{}, err
	}

	return r.transitionInvocation(ctx, request)
}

func (r *Repository) invocationSnapshot(ctx context.Context, key managed.ProviderInvocationKey, reference managed.AttemptRef) (retrySelected, error) {
	var selected retrySelected
	if r == nil || r.backend == nil {
		return selected, managed.ErrUnsupported
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

		if view.ref != reference {
			return managed.ErrAuthority
		}

		selected, readErr = invocationStatusSelectionHeld(ctx, tx, view, key)

		return readErr
	})

	return selected, err
}

// AuthorizeManagedProviderStepRetry retains one guarded retry without delegating transport.
func (r *Repository) AuthorizeManagedProviderStepRetry(ctx context.Context, request managed.ProviderStepRetryRequest) (managed.RecoveryRecord[managed.ProviderStepRetryAuthorization], error) {
	var zero managed.RecoveryRecord[managed.ProviderStepRetryAuthorization]

	input := invocationTransitionRequest{Kind: "retry", Command: request.Command, Step: request.Step, ExpectedCurrentDigest: request.ExpectedCurrentDigest, InvocationID: request.CommandID}
	if _, err := r.invocationPublicTransition(ctx, input); err != nil {
		return zero, err
	}

	key := managed.AttemptKey{Key: request.Command.Key, AttemptID: request.Command.AttemptRef.AttemptID, Generation: request.Command.AttemptRef.Generation}

	return r.ReadManagedProviderStepRetry(ctx, managed.ProviderInvocationKey{AttemptKey: key, Step: request.Step, CommandID: request.CommandID})
}

// CommitManagedProviderInvocation returns delegation only from a newly committed marker.
func (r *Repository) CommitManagedProviderInvocation(ctx context.Context, request managed.ProviderInvocationCommitRequest) (managed.ProviderInvocationGrant, error) {
	var zero managed.ProviderInvocationGrant
	if err := invocationCommandMatches(request.Command, request.Invocation); err != nil {
		return zero, err
	}

	selected, err := r.invocationSnapshot(ctx, request.Invocation, request.Command.AttemptRef)
	if err != nil {
		return zero, err
	}

	input := invocationTransitionRequest{Kind: "marker", Command: request.Command, Step: request.Invocation.Step, ExpectedCurrentDigest: request.ExpectedCurrentDigest, InvocationID: request.Invocation.CommandID}

	err = r.controller(ctx, "attempt:"+request.Invocation.AttemptKey.InstanceID.String(), []managed.Key{request.Invocation.AttemptKey.Key}, func(tx ControllerTransaction) error {
		if selected.marker != nil {
			var readErr error

			input.NativeCommits, readErr = invocationMarkerMembersHeld(ctx, tx, selected)

			return readErr
		}

		if capabilityErr := r.requireConfiguredRecovery(); capabilityErr != nil {
			return capabilityErr
		}

		target, readErr := tx.Read(ctx, request.Invocation.AttemptKey.Key)
		if readErr != nil {
			return readErr
		}

		view, active, readErr := readRecoveryAttemptInspection(ctx, tx, target, request.Invocation.AttemptKey)
		if readErr != nil {
			return readErr
		}

		if view.ref != active || active != request.Command.AttemptRef {
			return managed.ErrConflict
		}

		current, readErr := invocationSelectedHeld(ctx, tx, view, request.Invocation.Step)
		if readErr != nil {
			return readErr
		}

		if current.permission.Facts.Key != request.Invocation {
			return managed.ErrConflict
		}

		members, readErr := invocationLiveProofsHeld(ctx, tx, target, view, tx.Now())
		if readErr != nil {
			return readErr
		}

		input.NativeCommits, readErr = invocationCommitSelectionsHeld(ctx, tx, current, members)

		return readErr
	})
	if err != nil {
		return zero, err
	}

	out, err := r.invocationPublicTransition(ctx, input)
	if err != nil {
		return zero, err
	}

	if out.Marker == nil {
		return zero, managed.ErrUnavailable
	}

	return managed.ProviderInvocationGrant{Marker: *out.Marker, MayDelegate: out.MayDelegate}, nil
}

// CloseManagedProviderInvocation authenticates exact before-delegation custody outside locks.
func (r *Repository) CloseManagedProviderInvocation(ctx context.Context, request managed.ProviderInvocationCloseRequest) (managed.RecoveryRecord[managed.ProviderInvocationClosure], error) {
	var zero managed.RecoveryRecord[managed.ProviderInvocationClosure]

	cloned, err := recoverySnapshot(request)
	if err != nil {
		return zero, err
	}

	if err = invocationCommandMatches(cloned.Command, cloned.Failure.Key); err != nil {
		return zero, err
	}

	selected, err := r.invocationSnapshot(ctx, cloned.Failure.Key, cloned.Command.AttemptRef)
	if err != nil {
		return zero, err
	}

	beforeClosure := selected.current
	beforeClosure.FailureKey, beforeClosure.ClosureKey = "", ""

	expected, err := managed.RetryCurrentDigest(beforeClosure)
	if err != nil {
		return zero, err
	}

	input := invocationTransitionRequest{Kind: "closure", Command: cloned.Command, Step: cloned.Failure.Key.Step, ExpectedCurrentDigest: expected, InvocationID: cloned.Failure.Key.CommandID, Failure: &cloned.Failure}

	out, err := r.invocationPublicReplay(ctx, input)
	if err != nil {
		if !errors.Is(err, managed.ErrNotFound) {
			return zero, err
		}

		if err = r.requireConfiguredRecovery(); err != nil {
			return zero, err
		}

		input.closureVerification, err = r.verifyInvocationClosure(ctx, selected, cloned.Failure)
		if err != nil {
			return zero, err
		}

		out, err = r.transitionInvocation(ctx, input)
		if err != nil {
			return zero, err
		}
	}

	return r.invocationClosureExact(ctx, cloned.Failure.Key.AttemptKey, out.Primary)
}

func (r *Repository) invocationClosureExact(ctx context.Context, key managed.AttemptKey, reference managed.RecoveryAssociation) (managed.RecoveryRecord[managed.ProviderInvocationClosure], error) {
	var out managed.RecoveryRecord[managed.ProviderInvocationClosure]

	err := r.controller(ctx, "attempt:"+key.InstanceID.String(), []managed.Key{key.Key}, func(tx ControllerTransaction) error {
		var readErr error

		out, readErr = recoveryReadFact[managed.ProviderInvocationClosure](ctx, tx, key, reference)

		return readErr
	})

	return out, err
}

// RecordManagedProviderInvocationResult accepts the caller's stable observation operation.
// Replaying that complete request keeps its identity and never allocates a replacement ID.
func (r *Repository) RecordManagedProviderInvocationResult(ctx context.Context, request managed.ProviderInvocationObservationRequest) error {
	cloned, err := recoverySnapshot(request)
	if err != nil {
		return err
	}

	if err = invocationCommandMatches(cloned.Command, cloned.Result.Key); err != nil {
		return err
	}

	input := invocationTransitionRequest{Kind: "result", Command: cloned.Command, Step: cloned.Result.Key.Step, ExpectedCurrentDigest: cloned.ExpectedCurrentDigest, InvocationID: cloned.Result.Key.CommandID, Result: &cloned.Result}
	_, err = r.invocationPublicTransition(ctx, input)

	return err
}
