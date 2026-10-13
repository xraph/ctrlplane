package managedstate

import (
	"context"
	"encoding/json"
	"math"
	"reflect"

	"github.com/xraph/ctrlplane/managed"
)

// WithRemovalRecovery returns a host-configured view over the same persistence authority.
// It copies configuration and retains no separate lifecycle state or caller proof resolver.
func (r *Repository) WithRemovalRecovery(host managed.RemovalRecoveryHost) (*Repository, error) {
	if r == nil || r.backend == nil {
		return nil, managed.ErrUnsupported
	}

	// A transaction view keeps its caller's authority lock held. Configured host
	// callbacks must run through a root backend that closes the read transaction first.
	if _, held := r.backend.(ControllerTransaction); held {
		return nil, managed.ErrUnsupported
	}

	for _, capability := range []any{host.Revalidation, host.Commit, host.Close, host.Closure, host.Verifier} {
		if capability == nil {
			return nil, managed.ErrUnsupported
		}

		value := reflect.ValueOf(capability)
		switch value.Kind() {
		case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
			if value.IsNil() {
				return nil, managed.ErrUnsupported
			}
		}
	}

	for _, identity := range []managed.RecoveryProducerIdentity{host.RevalidationIdentity, host.CommitIdentity, host.CloseIdentity} {
		version, err := identity.Version.Uint64()
		if err != nil || version == 0 || version > math.MaxInt64 || !token(identity.ProducerID) || !token(identity.Profile) || !token(identity.VerifierID) {
			return nil, managed.ErrInvalid
		}
	}

	if !token(host.ClosureVerifierID) {
		return nil, managed.ErrInvalid
	}

	return &Repository{backend: r.backend, recoveryHost: &host}, nil
}

func recoverySnapshot[T any](value T) (T, error) {
	var snapshot T

	raw, err := json.Marshal(value)
	if err != nil {
		return snapshot, err
	}

	if len(raw) > managed.MaxRecoveryLeafSet {
		return snapshot, managed.ErrCapacity
	}

	if err = json.Unmarshal(raw, &snapshot); err != nil {
		return snapshot, err
	}

	return snapshot, nil
}

func recoveryProducerMatches(export managed.RecoveryNativeExport, identity managed.RecoveryProducerIdentity) error {
	if err := managed.ValidateRecoveryNativeExport(export); err != nil {
		return err
	}

	if export.ProducerID != identity.ProducerID || export.ProducerProfile != identity.Profile || export.ProducerVersion != identity.Version || export.VerifierID != identity.VerifierID {
		return managed.ErrAuthority
	}

	return nil
}

// recoveryIntentMatches checks an already retained intent before calling host code.
// The authority transaction must reload the exact association after verification.
func recoveryIntentMatches(intent managed.RecoveryRecord[managed.RecoveryNativeIntent], reference managed.RecoveryAssociation, binding managed.RemovalRecoveryBinding, operation, requestID, requestDigest string) error {
	if _, err := managed.EncodeRecoveryRecord(intent); err != nil {
		return err
	}

	key, err := recoveryFactKey(binding.Key.AttemptKey, managed.RecoveryIntentRecord, intent.Acceptance.OperationID, intent.Facts)
	if err != nil {
		return err
	}

	association, err := managed.RecoveryAssociationFor(key, intent)
	if err != nil {
		return err
	}

	facts := intent.Facts
	if association != reference || facts.Binding != binding || facts.Profile != managed.RemovalRecoveryProfile || facts.Operation != operation || !token(requestID) || facts.RequestID != requestID || !digest(requestDigest) || facts.RequestDigest != requestDigest || len(facts.Command) == 0 || facts.CommandDigest != managed.Digest(facts.Command) {
		return managed.ErrAuthority
	}

	return nil
}

// verifyRecoveryProof authenticates a snapshot outside authority locks. It grants no write.
func (r *Repository) verifyRecoveryProof(ctx context.Context, intent managed.RecoveryRecord[managed.RecoveryNativeIntent], facts managed.RemovalRevalidationFacts) (managed.RemovalRevalidationFacts, error) {
	var zero managed.RemovalRevalidationFacts
	if r == nil || r.recoveryHost == nil {
		return zero, managed.ErrUnsupported
	}

	input, err := recoverySnapshot(facts)
	if err != nil {
		return zero, err
	}

	saved, err := recoverySnapshot(intent)
	if err != nil {
		return zero, err
	}

	if err = recoveryIntentMatches(saved, input.Intent, input.Binding, "revalidate", input.NativeRequestID, input.NativeRequestDigest); err != nil {
		return zero, err
	}

	if err = recoveryProducerMatches(input.Export, r.recoveryHost.RevalidationIdentity); err != nil {
		return zero, err
	}

	for _, hash := range []string{input.GuardRootDigest, input.NativeMaterialDigest, input.NativeReceiptDigest, input.NativeCurrentDigest, input.NativeScopeDigest, input.CandidateDigest, input.BuildDigest, input.SurvivorDigest, input.ExclusionDigest, input.CoverageDigest} {
		if !digest(hash) {
			return zero, managed.ErrInvalid
		}
	}

	for _, counter := range []managed.Decimal{input.CandidateVersion, input.RemovalEpoch} {
		value, counterErr := counter.Uint64()
		if counterErr != nil || value == 0 || value > math.MaxInt64 {
			return zero, managed.ErrInvalid
		}
	}

	if !token(input.GuardID) || input.SourceObservedAt.IsZero() || input.NativeAcceptedAt.Before(input.SourceObservedAt) || !input.ValidUntil.After(input.NativeAcceptedAt) {
		return zero, managed.ErrInvalid
	}

	// Positive resource-to-process discharge needs the selected-result reader.
	// Until that reader exists, this slice accepts only the live disposition.
	if input.ProcessDisposition != "live" || input.EffectComplete != nil {
		return zero, managed.ErrUnsupported
	}

	verificationInput, err := recoverySnapshot(input)
	if err != nil {
		return zero, err
	}

	if err = r.recoveryHost.Verifier.VerifyRemovalRevalidation(ctx, saved, verificationInput); err != nil {
		return zero, err
	}

	return input, nil
}

func (r *Repository) verifyRecoveryCommit(ctx context.Context, intent managed.RecoveryRecord[managed.RecoveryNativeIntent], facts managed.ProviderNativeCommitFacts) (managed.ProviderNativeCommitFacts, error) {
	var zero managed.ProviderNativeCommitFacts
	if r == nil || r.recoveryHost == nil {
		return zero, managed.ErrUnsupported
	}

	input, err := recoverySnapshot(facts)
	if err != nil {
		return zero, err
	}

	saved, err := recoverySnapshot(intent)
	if err != nil {
		return zero, err
	}

	if err = recoveryIntentMatches(saved, input.Intent, input.Binding, "commit", input.NativeRequestID, input.NativeRequestDigest); err != nil {
		return zero, err
	}

	if err = recoveryProducerMatches(input.Export, r.recoveryHost.CommitIdentity); err != nil {
		return zero, err
	}

	if input.Invocation.AttemptKey != input.Binding.Key.AttemptKey || input.Invocation.Step.Key != input.Binding.Key.Key || input.Invocation.Step.ProviderOperationID != input.Binding.ProviderOperationID || saved.Facts.Permission == nil || saved.Facts.Proof == nil || *saved.Facts.Proof != input.CurrentProof || !digest(input.PermissionDigest) || !digest(input.PermissionProofDigest) || !digest(input.CurrentProofDigest) || !digest(input.GuardRootDigest) || !digest(input.NativeReceiptDigest) || !token(input.GuardID) || input.NativeAcceptedAt.IsZero() {
		return zero, managed.ErrAuthority
	}

	for _, association := range []managed.RecoveryAssociation{*saved.Facts.Permission, input.PermissionProof, input.CurrentProof} {
		if err = managed.ValidateRecoveryAssociation(association); err != nil {
			return zero, err
		}
	}

	verificationInput, err := recoverySnapshot(input)
	if err != nil {
		return zero, err
	}

	if err = r.recoveryHost.Verifier.VerifyRemovalCommit(ctx, saved, verificationInput); err != nil {
		return zero, err
	}

	return input, nil
}

func (r *Repository) verifyRecoveryClose(ctx context.Context, intent managed.RecoveryRecord[managed.RecoveryNativeIntent], facts managed.RecoveryNativeCloseFacts) (managed.RecoveryNativeCloseFacts, error) {
	var zero managed.RecoveryNativeCloseFacts
	if r == nil || r.recoveryHost == nil {
		return zero, managed.ErrUnsupported
	}

	input, err := recoverySnapshot(facts)
	if err != nil {
		return zero, err
	}

	saved, err := recoverySnapshot(intent)
	if err != nil {
		return zero, err
	}

	if err = recoveryIntentMatches(saved, input.Intent, input.Binding, "close", input.NativeRequestID, input.NativeRequestDigest); err != nil {
		return zero, err
	}

	if err = recoveryProducerMatches(input.Export, r.recoveryHost.CloseIdentity); err != nil {
		return zero, err
	}

	if saved.Facts.Cutoff == nil || saved.Facts.Coverage == nil || *saved.Facts.Cutoff != input.Cutoff || *saved.Facts.Coverage != input.Coverage || input.Cutoff.Kind != managed.RecoveryCutoffRecord || input.Coverage.Kind != managed.RecoveryCoverageRecord || !token(input.GuardID) || !digest(input.GuardRootDigest) || !digest(input.NativeReceiptDigest) || input.NativeAcceptedAt.IsZero() {
		return zero, managed.ErrAuthority
	}

	for _, association := range []managed.RecoveryAssociation{input.Cutoff, input.Coverage} {
		if err = managed.ValidateRecoveryAssociation(association); err != nil {
			return zero, err
		}
	}

	verificationInput, err := recoverySnapshot(input)
	if err != nil {
		return zero, err
	}

	if err = r.recoveryHost.Verifier.VerifyRemovalClose(ctx, saved, verificationInput); err != nil {
		return zero, err
	}

	return input, nil
}

// verifyRecoveryNoDispatch authenticates affirmative evidence, including marked calls.
// Acceptance still needs an atomic permanent closure, current selection, and no result.
func (r *Repository) verifyRecoveryNoDispatch(ctx context.Context, permission managed.RecoveryRecord[managed.ProviderInvocationPermission], marker *managed.RecoveryRecord[managed.ProviderInvocationMarker], facts managed.ProviderInvocationFailure) (managed.ProviderInvocationFailure, error) {
	var zero managed.ProviderInvocationFailure
	if r == nil || r.recoveryHost == nil {
		return zero, managed.ErrUnsupported
	}

	input, err := recoverySnapshot(facts)
	if err != nil {
		return zero, err
	}

	saved, err := recoverySnapshot(permission)
	if err != nil {
		return zero, err
	}

	if _, err = managed.EncodeRecoveryRecord(saved); err != nil {
		return zero, err
	}

	permissionDigest, err := recoveryPermissionDigest(saved.Facts.Permission)
	if err != nil {
		return zero, err
	}

	failure := input.Failure
	if input.Key != saved.Facts.Key || input.RootDigest != saved.Facts.RootDigest || input.PermissionDigest != permissionDigest || !reflect.DeepEqual(managed.ProviderPermissionWire(failure.Permission), saved.Facts.Permission) || failure.AttemptRef.AttemptID != input.Key.AttemptKey.AttemptID || failure.AttemptRef.Generation != input.Key.AttemptKey.Generation || !digest(failure.AttemptRef.DescriptorDigest) || failure.InvocationID != input.Key.CommandID || failure.SchemaVersion != "1" || failure.ProducerProfile != managed.LocalNoDispatchProfile || failure.VerifierID != r.recoveryHost.ClosureVerifierID || failure.Class != "local_no_dispatch" || failure.Phase != "before_transport_delegate" || !attemptTime(failure.ObservedAt) || failure.ObservedAt.Before(failure.Permission.IssuedAt) || !evidence(failure.Witness) || failure.WitnessDigest != managed.Digest(failure.Witness) {
		return zero, managed.ErrAuthority
	}

	switch failure.Reason {
	case "authority_refused", "request_binding_mismatch", "invocation_closed":
	default:
		return zero, managed.ErrInvalid
	}

	retainedMarker, err := recoverySnapshot(marker)
	if err != nil {
		return zero, err
	}

	if retainedMarker != nil {
		if _, err = managed.EncodeRecoveryRecord(*retainedMarker); err != nil {
			return zero, err
		}

		if retainedMarker.Facts.Key != input.Key || retainedMarker.Facts.RootDigest != input.RootDigest || retainedMarker.Facts.PermissionDigest != input.PermissionDigest {
			return zero, managed.ErrAuthority
		}
	}

	verificationInput, err := recoverySnapshot(input)
	if err != nil {
		return zero, err
	}

	if err = r.recoveryHost.Verifier.VerifyProviderNoDispatch(ctx, saved, retainedMarker, verificationInput); err != nil {
		return zero, err
	}

	return input, nil
}
