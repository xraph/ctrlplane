package managedstate

import (
	"context"
	"time"

	"github.com/xraph/ctrlplane/managed"
)

// recoveryEffectCompleteHeld authenticates local associations before a configured
// verifier runs and again under the accepting authority lock. The verifier still
// owns the affirmative resource/incarnation-to-original-process discharge.
func recoveryEffectCompleteHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, facts managed.RemovalRevalidationFacts, now time.Time) error {
	if facts.ProcessDisposition != "effect_complete" || facts.EffectComplete == nil {
		return managed.ErrInvalid
	}

	effect := facts.EffectComplete
	if effect.Invocation.AttemptKey != facts.Binding.Key.AttemptKey || effect.Invocation.AttemptKey != attemptKey(view) || effect.OriginalProcessDigest != facts.Binding.OriginalProcessDigest || !digest(effect.OriginalProcessDigest) || !digest(effect.PreEffectProofDigest) || effect.Result.Kind != managed.RecoveryResultRecord {
		return managed.ErrAuthority
	}

	selected, err := invocationSelectedHeld(ctx, tx, view, effect.Invocation.Step)
	if err != nil {
		return err
	}

	if err = invocationStatusMatchesHeld(ctx, tx, view, selected); err != nil {
		return err
	}

	if selected.permission.Facts.Key != effect.Invocation || selected.result == nil || selected.closure != nil || selected.failure != nil {
		return managed.ErrBlocked
	}

	stepDigest, err := providerStepID(effect.Invocation.Step)
	if err != nil {
		return err
	}

	covered, err := recoverySelectedCoverage(stepDigest, selected)
	if err != nil {
		return err
	}

	if covered.Disposition != managed.EffectiveStepRemoved || effect.PermissionDigest != covered.PermissionDigest || effect.ResourceID != selected.root.Facts.ResourceID || effect.Incarnation != selected.root.Facts.Incarnation || effect.ResourceID == "" || effect.Incarnation == "" {
		return managed.ErrAuthority
	}

	resultKey, err := recoveryFactKey(effect.Invocation.AttemptKey, managed.RecoveryResultRecord, selected.result.Acceptance.OperationID, selected.result.Facts)
	if err != nil {
		return err
	}

	resultRef, err := managed.RecoveryAssociationFor(resultKey, *selected.result)
	if err != nil {
		return err
	}

	if effect.Result != resultRef || selected.result.Facts.Result.ObservedAt.After(facts.SourceObservedAt) || selected.result.Acceptance.Receipt.AcceptedAt.After(now) {
		return managed.ErrAuthority
	}

	binding, err := recoveryBindingHeld(ctx, tx, target, view, facts.Binding.Key)
	if err != nil {
		return err
	}

	if binding != facts.Binding {
		return managed.ErrAuthority
	}

	if selected.current.Ordinal == "0" {
		return recoveryOriginalPreEffectHeld(ctx, tx, target, view, facts, selected)
	}

	if effect.OriginalPreEffect != nil || effect.RetryPreEffect == nil || effect.RetryPreEffect.Kind != managed.RecoveryPermissionProofRecord {
		return managed.ErrAuthority
	}

	permissionProof, err := recoveryReadFact[managed.ProviderPermissionProof](ctx, tx, attemptKey(view), *effect.RetryPreEffect)
	if err != nil {
		return err
	}

	bindingDigest, err := recoveryBindingKey(binding)
	if err != nil {
		return err
	}

	if permissionProof.Acceptance != selected.permission.Acceptance || permissionProof.Facts.Invocation != effect.Invocation || permissionProof.Facts.BindingDigest != bindingDigest || permissionProof.Facts.PermissionDigest != effect.PermissionDigest {
		return managed.ErrAuthority
	}

	proof, err := recoveryReadFact[managed.RemovalRevalidationFacts](ctx, tx, attemptKey(view), permissionProof.Facts.Proof)
	if err != nil {
		return err
	}

	if proof.Digest != effect.PreEffectProofDigest || proof.Facts.Binding != binding || proof.Facts.NativeMaterialDigest != permissionProof.Facts.NativeMaterialDigest || proof.Facts.ProcessDisposition != "live" || proof.Facts.EffectComplete != nil || proof.Facts.GuardID != facts.GuardID || proof.Facts.GuardRootDigest != facts.GuardRootDigest || proof.Acceptance.Receipt.AcceptedAt.After(selected.permission.Facts.Permission.IssuedAt) || !managed.RecoveryProofFresh(proof.Facts, selected.permission.Facts.Permission.IssuedAt) {
		return managed.ErrAuthority
	}
	// The live/nil-effect check above bounds this exact read: it cannot recurse
	// through another effect-complete proof. Missing request/event custody refuses.
	_, err = recoveryProofExactHeld(ctx, tx, target, view, permissionProof.Facts.Proof)

	return err
}

func recoveryOriginalPreEffectHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, facts managed.RemovalRevalidationFacts, selected retrySelected) error {
	effect := facts.EffectComplete
	if effect == nil || effect.OriginalPreEffect == nil || effect.RetryPreEffect != nil || selected.current.Ordinal != "0" {
		return managed.ErrAuthority
	}

	source := effect.OriginalPreEffect
	if source.Key != facts.Binding.Key || source.OperationID.Prefix() != managed.PrefixOperation || !digest(source.FactDigest) {
		return managed.ErrAuthority
	}

	fence, err := attemptFenceRead(ctx, tx, target, view, source.Key)
	if err != nil {
		return err
	}

	slot, err := attemptSlot(source.Key, "binding")
	if err != nil {
		return err
	}

	if err = attemptLocalFact(ctx, tx, view, fence.LocalOperationID, "removal_fence", slot, fence); err != nil {
		return err
	}

	factDigest, err := attemptFact("removal_fence", fence)
	if err != nil {
		return err
	}

	event, err := attemptEventRead(ctx, tx, view, fence.LocalOperationID)
	if err != nil {
		return err
	}

	return recoveryOriginalPreEffectMatches(facts, selected, fence, factDigest, event.Receipt)
}

func recoveryOriginalPreEffectMatches(facts managed.RemovalRevalidationFacts, selected retrySelected, fence managed.AttemptFenceRecord, factDigest string, receipt managed.Receipt) error {
	effect := facts.EffectComplete
	if effect == nil || effect.OriginalPreEffect == nil || effect.RetryPreEffect != nil || selected.current.Ordinal != "0" {
		return managed.ErrAuthority
	}

	source := effect.OriginalPreEffect

	permissionRevision, err := selected.root.Facts.OriginalPermission.Revision.Uint64()
	if err != nil || permissionRevision == 0 || receipt.Revision == 0 || receipt.Revision >= permissionRevision {
		return managed.ErrAuthority
	}

	issuedAt := selected.root.Facts.OriginalPermission.IssuedAt
	if source.Key != facts.Binding.Key || fence.Key != source.Key || source.OperationID != fence.LocalOperationID || receipt.ID != source.OperationID || source.FactDigest != factDigest || effect.PreEffectProofDigest != fence.ProofDigest || fence.NativeFenceDigest != facts.Binding.NativeFenceDigest || fence.NativeAcceptance.AcceptedAt.After(issuedAt) || receipt.AcceptedAt.After(issuedAt) || !issuedAt.Before(fence.Fence.ValidUntil) {
		return managed.ErrAuthority
	}

	return nil
}

func recoveryProofDispositionShape(facts managed.RemovalRevalidationFacts) error {
	switch facts.ProcessDisposition {
	case "live":
		if facts.EffectComplete != nil {
			return managed.ErrInvalid
		}
	case "effect_complete":
		effect := facts.EffectComplete
		if effect == nil || !digest(effect.PermissionDigest) || !digest(effect.PreEffectProofDigest) || !digest(effect.OriginalProcessDigest) || effect.ResourceID == "" || effect.Incarnation == "" || effect.OriginalProcessDigest != facts.Binding.OriginalProcessDigest || effect.Invocation.AttemptKey != facts.Binding.Key.AttemptKey {
			return managed.ErrInvalid
		}

		if effect.Result.Kind != managed.RecoveryResultRecord || managed.ValidateRecoveryAssociation(effect.Result) != nil {
			return managed.ErrInvalid
		}

		if (effect.OriginalPreEffect == nil) == (effect.RetryPreEffect == nil) {
			return managed.ErrInvalid
		}

		if effect.OriginalPreEffect != nil {
			if effect.OriginalPreEffect.Key != facts.Binding.Key || effect.OriginalPreEffect.OperationID.Prefix() != managed.PrefixOperation || !digest(effect.OriginalPreEffect.FactDigest) {
				return managed.ErrInvalid
			}
		} else if effect.RetryPreEffect.Kind != managed.RecoveryPermissionProofRecord || managed.ValidateRecoveryAssociation(*effect.RetryPreEffect) != nil {
			return managed.ErrInvalid
		}
	default:
		return managed.ErrUnsupported
	}

	return nil
}

func recoveryRevalidationInputsHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, facts managed.RemovalRevalidationFacts, now time.Time) error {
	if err := recoveryProofDispositionShape(facts); err != nil {
		return err
	}

	if err := recoveryProofBindingHeld(ctx, tx, target, view, facts, now); err != nil {
		return err
	}

	if facts.ProcessDisposition == "effect_complete" {
		return recoveryEffectCompleteHeld(ctx, tx, target, view, facts, now)
	}

	return nil
}
