package managed

import (
	"math"

	"github.com/xraph/ctrlplane/id"
)

// RecoveryRecordAssociation derives the canonical address of a validated record
// belonging to the supplied attempt. It does not authenticate retained acceptance
// or grant delegation; callers must reread the exact association through the store.
func RecoveryRecordAssociation[T any](key AttemptKey, record RecoveryRecord[T]) (RecoveryAssociation, error) {
	if _, err := EncodeRecoveryRecord(record); err != nil {
		return RecoveryAssociation{}, err
	}

	if !recoveryRecordAttemptMatches(key, record.Facts) {
		return RecoveryAssociation{}, ErrAuthority
	}

	selected, err := RecoveryFactKey(key, record.Kind, record.Acceptance.OperationID, record.Facts)
	if err != nil {
		return RecoveryAssociation{}, err
	}

	return RecoveryAssociationFor(selected, record)
}

// RecoveryFactKey derives a canonical lookup address from a typed fact subject.
// Partial subjects are supported for exact store reads. This does not validate a
// complete record or prove acceptance; use RecoveryRecordAssociation for records.
func RecoveryFactKey(k AttemptKey, kind RecoveryRecordKind, operation id.ID, facts any) (string, error) {
	if err := recoveryAssociationAttemptKey(k); err != nil {
		return "", err
	}

	if RecoveryRecordLimit(kind) == 0 || operation.Prefix() != PrefixOperation {
		return "", ErrInvalid
	}

	subject, err := recoveryFactSubject(facts)
	if err != nil {
		return "", err
	}

	return recoveryHash("ctrlplane.managed.recovery-fact-key.v1", struct {
		Key         AttemptKey         `json:"key"`
		Kind        RecoveryRecordKind `json:"kind"`
		OperationID id.ID              `json:"operation_id"`
		Subject     any                `json:"subject"`
	}{k, kind, operation, subject})
}

func recoveryAssociationAttemptKey(key AttemptKey) error {
	generation, err := key.Generation.Uint64()
	if err != nil || generation == 0 || generation > math.MaxInt64 {
		return ErrUnsupported
	}

	if !originalToken(key.TenantID) || key.InstanceID.Prefix() != id.PrefixInstance || key.AttemptID.Prefix() != PrefixOperation {
		return ErrInvalid
	}

	return nil
}

func recoveryRecordAttemptMatches(key AttemptKey, facts any) bool {
	refMatches := func(ref AttemptRef) bool { return ref.AttemptID == key.AttemptID && ref.Generation == key.Generation }
	invocationMatches := func(invocation ProviderInvocationKey) bool {
		return invocation.AttemptKey == key && invocation.Step.Key == key.Key
	}

	switch value := facts.(type) {
	case RemovalRecoveryActivation:
		return value.Key == key && refMatches(value.AttemptRef)
	case RemovalRevalidationFacts:
		return value.Binding.Key.AttemptKey == key
	case RecoveryNativeIntent:
		return value.Binding.Key.AttemptKey == key
	case ProviderNativeCommitFacts:
		return value.Binding.Key.AttemptKey == key && invocationMatches(value.Invocation)
	case RecoveryNativeCloseFacts:
		return value.Binding.Key.AttemptKey == key
	case ProviderStepRetryRoot:
		return refMatches(value.AttemptRef) && value.Step.Key == key.Key
	case ProviderPermissionProof:
		return invocationMatches(value.Invocation)
	case ProviderInvocationPermission:
		return invocationMatches(value.Key)
	case ProviderStepRetryAuthorization:
		return invocationMatches(value.Key)
	case ProviderInvocationMarker:
		return invocationMatches(value.Key)
	case ProviderInvocationClosure:
		return invocationMatches(value.Key)
	case ProviderInvocationFailure:
		return invocationMatches(value.Key)
	case ProviderInvocationResult:
		return invocationMatches(value.Key)
	case RemovalCutoffFacts:
		return value.Key == key && refMatches(value.AttemptRef)
	case EffectiveRemovalCoverage:
		return value.Key == key && refMatches(value.AttemptRef)
	case EffectiveRemovalBinding:
		return value.Binding.Key.AttemptKey == key
	default:
		return false
	}
}

// recoveryFactSubject permits one parent transaction to accept distinct fixed-set leaves.
// A root per step and a proof association per binding may share the same local receipt.
func recoveryFactSubject(facts any) (any, error) {
	switch value := facts.(type) {
	case RemovalRecoveryActivation:
		return value.Key, nil
	case RemovalRevalidationFacts:
		return value.Binding.Key, nil
	case RecoveryNativeIntent:
		return struct {
			Binding   AttemptBindingKey `json:"binding"`
			Operation string            `json:"operation"`
		}{value.Binding.Key, value.Operation}, nil
	case ProviderNativeCommitFacts:
		return struct {
			Binding    AttemptBindingKey     `json:"binding"`
			Invocation ProviderInvocationKey `json:"invocation"`
		}{value.Binding.Key, value.Invocation}, nil
	case RecoveryNativeCloseFacts:
		return value.Binding.Key, nil
	case ProviderStepRetryRoot:
		return value.Step, nil
	case ProviderPermissionProof:
		return struct {
			Invocation    ProviderInvocationKey `json:"invocation"`
			BindingDigest string                `json:"binding_digest"`
		}{value.Invocation, value.BindingDigest}, nil
	case ProviderInvocationPermission:
		return value.Key, nil
	case ProviderStepRetryAuthorization:
		return value.Key, nil
	case ProviderInvocationMarker:
		return value.Key, nil
	case ProviderInvocationClosure:
		return value.Key, nil
	case ProviderInvocationFailure:
		return value.Key, nil
	case ProviderInvocationResult:
		return value.Key, nil
	case RemovalCutoffFacts:
		return value.Key, nil
	case EffectiveRemovalCoverage:
		return value.Key, nil
	case EffectiveRemovalBinding:
		return value.Binding.Key, nil
	default:
		return nil, ErrUnsupported
	}
}
