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

// invocationProofMember is descriptor ordered. Each proof remains an exact retained row.
type invocationProofMember struct {
	binding       managed.RemovalRecoveryBinding
	bindingDigest string
	current       managed.RemovalRevalidationCurrent
	proof         managed.RecoveryRecord[managed.RemovalRevalidationFacts]
}

// invocationLiveProofsHeld derives every active binding at the actual callback clock.
// This initial slice requires a live process; positive process discharge remains separate.
func invocationLiveProofsHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, now time.Time) ([]invocationProofMember, error) {
	if !view.validated || view.state.SchemaVersion != "2" || view.recoveryActivation == nil || view.state.AdmissionDigest == "" || view.state.RevocationDigest != "" || view.state.SettlementDigest != "" || view.state.TerminalDigest != "" || view.state.Successor != nil || target.Phase == managed.Deleted {
		return nil, managed.ErrBlocked
	}

	out := make([]invocationProofMember, 0, len(view.descriptor.ReservationIDs))
	for _, reservationID := range view.descriptor.ReservationIDs {
		reservation, err := reservation(target, reservationID)
		if err != nil {
			return nil, err
		}

		if reservation.Phase == managed.Revoked {
			continue
		}

		binding, err := recoveryBindingHeld(ctx, tx, target, view, attemptBinding(view, reservationID))
		if err != nil {
			return nil, err
		}

		current, proof, err := recoveryReadCurrent(ctx, tx, binding)
		if err != nil {
			return nil, err
		}

		if proof.Facts.Binding != binding || proof.Facts.ProcessDisposition != "live" || proof.Facts.EffectComplete != nil || !managed.RecoveryProofFresh(proof.Facts, now) {
			return nil, managed.ErrBlocked
		}

		if err = recoveryProofBindingHeld(ctx, tx, target, view, proof.Facts, now); err != nil {
			return nil, err
		}

		bindingDigest, err := recoveryBindingKey(binding)
		if err != nil {
			return nil, err
		}

		out = append(out, invocationProofMember{binding, bindingDigest, current, proof})
	}

	if len(out) == 0 || len(out) > managed.MaxRecoveryBindings {
		return nil, managed.ErrUnavailable
	}

	return out, nil
}

func invocationPermissionProofKey(key managed.ProviderInvocationKey, operation id.ID, bindingDigest string) (string, error) {
	return recoveryFactKey(key.AttemptKey, managed.RecoveryPermissionProofRecord, operation, managed.ProviderPermissionProof{Invocation: key, BindingDigest: bindingDigest})
}

// stageInvocationRetry selects only an affirmatively closed exact predecessor. No saved
// permission or absent marker is treated as proof that transport was prevented.
func stageInvocationRetry(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, selected retrySelected, request managed.ProviderStepRetryRequest, acceptance managed.RecoveryAcceptance, now time.Time) (*recoveryActivationRows, managed.RecoveryRecord[managed.ProviderStepRetryAuthorization], error) {
	var zero managed.RecoveryRecord[managed.ProviderStepRetryAuthorization]
	if request.Step != selected.root.Facts.Step || request.Command.AttemptRef != view.ref || selected.permission.Facts.Key.AttemptKey != attemptKey(view) {
		return nil, zero, managed.ErrInvalid
	}

	previous, ordinal, err := retryPredecessor(selected, request.ExpectedCurrentDigest, request.CommandID)
	if err != nil {
		return nil, zero, err
	}

	proofs, err := invocationLiveProofsHeld(ctx, tx, target, view, now)
	if err != nil {
		return nil, zero, err
	}

	if err = removalLocalIDAvailable(ctx, tx, request.CommandID); err != nil {
		return nil, zero, err
	}

	if _, receiptErr := tx.ReadReceipt(ctx, target.Key, request.CommandID, ""); !errors.Is(receiptErr, managed.ErrNotFound) {
		if receiptErr != nil {
			return nil, zero, receiptErr
		}

		return nil, zero, managed.ErrConflict
	}

	receipt, err := acceptance.Receipt.Value()
	if err != nil {
		return nil, zero, err
	}

	staged := &recoveryActivationRows{ControllerTransaction: tx, key: target.Key, receipt: receipt}
	permission := selected.root.Facts.OriginalPermission
	permission.CommandID = request.CommandID
	permission.Digest = acceptance.RequestDigest
	permission.IssuedAt = now.UTC()

	permission.Revision = acceptance.Receipt.Revision
	if _, err = permission.Value(); err != nil {
		return nil, zero, err
	}

	invocation := managed.ProviderInvocationKey{AttemptKey: attemptKey(view), Step: request.Step, CommandID: request.CommandID}

	permissionDigest, err := recoveryPermissionDigest(permission)
	if err != nil {
		return nil, zero, err
	}

	proofRefs := make([]managed.RecoveryAssociation, 0, len(proofs))
	for _, member := range proofs {
		facts := managed.ProviderPermissionProof{Invocation: invocation, BindingDigest: member.bindingDigest, PermissionDigest: permissionDigest, Proof: member.current.Proof, NativeMaterialDigest: member.proof.Facts.NativeMaterialDigest}

		record, recordErr := activationRecord(managed.RecoveryPermissionProofRecord, facts, acceptance)
		if recordErr != nil {
			return nil, zero, recordErr
		}

		reference, recordErr := recoveryWriteFact(ctx, staged, attemptKey(view), record)
		if recordErr != nil {
			return nil, zero, recordErr
		}

		proofRefs = append(proofRefs, reference)
	}

	proofSetDigest, err := attemptHash("ctrlplane.managed.permission-proof-set.v1", proofRefs)
	if err != nil {
		return nil, zero, err
	}

	facts := managed.ProviderInvocationPermission{Key: invocation, RootDigest: selected.root.Digest, Ordinal: ordinal, Permission: permission, ProviderCommandDigest: selected.root.Facts.ProviderCommandDigest, PermissionProofSetDigest: proofSetDigest, PermissionProofCount: managed.Decimal(strconv.Itoa(len(proofRefs)))}

	permissionRecord, err := activationRecord(managed.RecoveryPermissionRecord, facts, acceptance)
	if err != nil {
		return nil, zero, err
	}

	permissionRef, err := recoveryWriteFact(ctx, staged, attemptKey(view), permissionRecord)
	if err != nil {
		return nil, zero, err
	}

	attemptRevision, err := view.state.Revision.Uint64()
	if err != nil || attemptRevision >= 1<<63-1 {
		return nil, zero, managed.ErrCapacity
	}

	authorizationFacts := managed.ProviderStepRetryAuthorization{Key: invocation, RootDigest: selected.root.Digest, Ordinal: ordinal, Permission: permissionRef, Previous: previous, ExpectedCurrentDigest: request.ExpectedCurrentDigest, ParentRevision: acceptance.Receipt.Revision, AttemptRevision: managed.Decimal(strconv.FormatUint(attemptRevision+1, 10)), AuthorizedAt: now.UTC()}

	authorization, err := activationRecord(managed.RecoveryAuthorizationRecord, authorizationFacts, acceptance)
	if err != nil {
		return nil, zero, err
	}

	authority, err := recoveryWriteFact(ctx, staged, attemptKey(view), authorization)
	if err != nil {
		return nil, zero, err
	}

	if err = stageInvocationRetryEvent(ctx, staged, view, selected.root, authorization, authority); err != nil {
		return nil, zero, err
	}

	stepKey, err := retryStepKey(attemptKey(view), request.Step)
	if err != nil {
		return nil, zero, err
	}

	previous.Digest, err = managed.RetryPreviousDigest(previous)
	if err != nil {
		return nil, zero, err
	}

	if err = attemptWrite(ctx, staged, retryPreviousKind, view.scope, stepKey, previous, selected.previous == nil, managed.MaxStepRetryPointer); err != nil {
		return nil, zero, err
	}

	current := managed.ProviderStepRetryCurrent{SchemaVersion: "1", RootDigest: selected.root.Digest, CommandID: request.CommandID, Ordinal: ordinal, Permission: permissionRef, Authority: authority, PreviousDigest: previous.Digest}

	current.Digest, err = managed.RetryCurrentDigest(current)
	if err != nil {
		return nil, zero, err
	}

	if err = attemptWrite(ctx, staged, retryCurrentKind, view.scope, stepKey, current, false, managed.MaxStepRetryPointer); err != nil {
		return nil, zero, err
	}

	identity := attemptIdentity{SchemaVersion: "1", ID: request.CommandID, Role: "invocation", Key: target.Key, AttemptRef: view.ref}
	if err = attemptWrite(ctx, staged, "removal_identity", "", request.CommandID.String(), identity, true, managed.MaxAttemptPointer); err != nil {
		return nil, zero, err
	}

	return staged, authorization, nil
}

// stageInvocationClosure accepts failure and permanent custody closure together. A prior
// marker remains historical; its accepted revision must precede the closure revision.
func stageInvocationClosure(ctx context.Context, tx ControllerTransaction, selected retrySelected, facts managed.ProviderInvocationFailure, acceptance managed.RecoveryAcceptance, now time.Time) (*recoveryActivationRows, managed.RecoveryRecord[managed.ProviderInvocationClosure], error) {
	var zero managed.RecoveryRecord[managed.ProviderInvocationClosure]
	if selected.result != nil || selected.closure != nil || selected.failure != nil || facts.Key != selected.permission.Facts.Key || facts.RootDigest != selected.root.Digest {
		return nil, zero, managed.ErrBlocked
	}

	permissionDigest, err := recoveryPermissionDigest(selected.permission.Facts.Permission)
	if err != nil {
		return nil, zero, err
	}

	if facts.PermissionDigest != permissionDigest || !reflect.DeepEqual(managed.ProviderPermissionWire(facts.Failure.Permission), selected.permission.Facts.Permission) || facts.Failure.AttemptRef != selected.root.Facts.AttemptRef || facts.Failure.ObservedAt.After(now) {
		return nil, zero, managed.ErrAuthority
	}

	receipt, err := acceptance.Receipt.Value()
	if err != nil {
		return nil, zero, err
	}

	staged := &recoveryActivationRows{ControllerTransaction: tx, key: facts.Key.AttemptKey.Key, receipt: receipt}

	failure, err := activationRecord(managed.RecoveryFailureRecord, facts, acceptance)
	if err != nil {
		return nil, zero, err
	}

	closureFacts := managed.ProviderInvocationClosure{Key: facts.Key, RootDigest: facts.RootDigest, PermissionDigest: permissionDigest, FailureDigest: failure.Digest, ProducerProfile: facts.Failure.ProducerProfile, VerifierID: facts.Failure.VerifierID, ClosedAt: now.UTC()}

	closure, err := activationRecord(managed.RecoveryClosureRecord, closureFacts, acceptance)
	if err != nil {
		return nil, zero, err
	}

	if err = retryClosedFailure(failure, closure); err != nil {
		return nil, zero, err
	}

	if selected.marker != nil {
		if err = retryMarkerBeforeClosure(*selected.marker, closure); err != nil {
			return nil, zero, err
		}
	}

	failureRef, err := recoveryWriteFact(ctx, staged, facts.Key.AttemptKey, failure)
	if err != nil {
		return nil, zero, err
	}

	closureRef, err := recoveryWriteFact(ctx, staged, facts.Key.AttemptKey, closure)
	if err != nil {
		return nil, zero, err
	}

	current := selected.current
	current.FailureKey = failureRef.Key
	current.ClosureKey = closureRef.Key

	current.Digest, err = managed.RetryCurrentDigest(current)
	if err != nil {
		return nil, zero, err
	}

	scope, err := attemptScope(facts.Key.AttemptKey.Key)
	if err != nil {
		return nil, zero, err
	}

	stepKey, err := retryStepKey(facts.Key.AttemptKey, facts.Key.Step)
	if err != nil {
		return nil, zero, err
	}

	if err = attemptWrite(ctx, staged, retryCurrentKind, scope, stepKey, current, false, managed.MaxStepRetryPointer); err != nil {
		return nil, zero, err
	}

	return staged, closure, nil
}
