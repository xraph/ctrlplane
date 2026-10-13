package managedstate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

const recoveryAcceptanceKind = "removal_recovery_acceptance"
const recoveryCurrentKind = "removal_revalidation_current"

// recoveryAcceptance binds each leaf to the same-parent receipt that accepted it.
// Several facts may share one receipt, such as a failure and its permanent closure.
type recoveryAcceptance struct {
	SchemaVersion managed.Decimal             `json:"schema_version"`
	AttemptKey    managed.AttemptKey          `json:"attempt_key"`
	Reference     managed.RecoveryAssociation `json:"reference"`
}

func recoveryFactKey(k managed.AttemptKey, kind managed.RecoveryRecordKind, operation id.ID, facts any) (string, error) {
	if err := attemptVersion(k); err != nil {
		return "", err
	}

	if managed.RecoveryRecordLimit(kind) == 0 || operation.Prefix() != managed.PrefixOperation {
		return "", managed.ErrInvalid
	}

	subject, err := recoveryFactSubject(facts)
	if err != nil {
		return "", err
	}

	return attemptHash("ctrlplane.managed.recovery-fact-key.v1", struct {
		Key         managed.AttemptKey         `json:"key"`
		Kind        managed.RecoveryRecordKind `json:"kind"`
		OperationID id.ID                      `json:"operation_id"`
		Subject     any                        `json:"subject"`
	}{k, kind, operation, subject})
}

// recoveryReadFact does not search for a substitute if a named fact or receipt is missing.
func recoveryReadFact[T any](ctx context.Context, tx ControllerTransaction, k managed.AttemptKey, reference managed.RecoveryAssociation) (managed.RecoveryRecord[T], error) {
	var out managed.RecoveryRecord[T]
	if err := managed.ValidateRecoveryAssociation(reference); err != nil {
		return out, err
	}

	selected := reference.Key

	scope, err := attemptScope(k.Key)
	if err != nil {
		return out, err
	}

	raw, err := tx.ReadRecord(ctx, string(reference.Kind), scope, selected)
	if err != nil {
		return out, err
	}

	out, err = managed.DecodeRecoveryRecord[T](raw)
	if err != nil {
		return out, err
	}

	expectedKey, err := recoveryFactKey(k, out.Kind, out.Acceptance.OperationID, out.Facts)
	if err != nil {
		return out, err
	}

	if expectedKey != selected {
		return out, managed.ErrAuthority
	}

	if err = recoveryExportShape(out.Facts); err != nil {
		return out, err
	}

	actual, err := managed.RecoveryAssociationFor(selected, out)
	if err != nil {
		return out, err
	}

	if actual != reference {
		return out, managed.ErrUnavailable
	}

	accepted, err := attemptRead[recoveryAcceptance](ctx, tx, recoveryAcceptanceKind, scope, selected, managed.MaxRecoveryReceipt)
	if err != nil {
		return out, err
	}

	if accepted.SchemaVersion != "1" || accepted.AttemptKey != k || accepted.Reference != reference {
		return out, managed.ErrUnavailable
	}

	receipt, err := out.Acceptance.Receipt.Value()
	if err != nil {
		return out, err
	}

	if err = attemptReceiptCheck(ctx, tx, k.Key, receipt); err != nil {
		return out, err
	}

	return out, nil
}

// recoveryWriteFact stages immutable bytes inside the caller's controller/parent transaction.
// The caller must accept the matching parent receipt in that transaction and discard checked views.
// This leaf writer grants no permission, profile activation or delegation on its own.
func recoveryWriteFact[T any](ctx context.Context, tx ControllerTransaction, k managed.AttemptKey, record managed.RecoveryRecord[T]) (managed.RecoveryAssociation, error) {
	var reference managed.RecoveryAssociation
	if err := recoveryExportShape(record.Facts); err != nil {
		return reference, err
	}

	raw, err := managed.EncodeRecoveryRecord(record)
	if err != nil {
		return reference, err
	}

	selected, err := recoveryFactKey(k, record.Kind, record.Acceptance.OperationID, record.Facts)
	if err != nil {
		return reference, err
	}

	reference, err = managed.RecoveryAssociationFor(selected, record)
	if err != nil {
		return reference, err
	}

	scope, err := attemptScope(k.Key)
	if err != nil {
		return reference, err
	}

	if err = tx.WriteRecord(ctx, string(record.Kind), scope, selected, raw, true); err != nil {
		return reference, err
	}

	accepted := recoveryAcceptance{SchemaVersion: "1", AttemptKey: k, Reference: reference}
	if err = attemptWrite(ctx, tx, recoveryAcceptanceKind, scope, selected, accepted, true, managed.MaxRecoveryReceipt); err != nil {
		return reference, err
	}

	return reference, nil
}

func recoveryBindingKey(binding managed.RemovalRecoveryBinding) (string, error) {
	return attemptHash("ctrlplane.managed.recovery-binding.v1", binding)
}

func recoveryReadCurrent(ctx context.Context, tx ControllerTransaction, binding managed.RemovalRecoveryBinding) (managed.RemovalRevalidationCurrent, managed.RecoveryRecord[managed.RemovalRevalidationFacts], error) {
	var proof managed.RecoveryRecord[managed.RemovalRevalidationFacts]

	bindingDigest, err := recoveryBindingKey(binding)
	if err != nil {
		return managed.RemovalRevalidationCurrent{}, proof, err
	}

	scope, err := attemptScope(binding.Key.Key)
	if err != nil {
		return managed.RemovalRevalidationCurrent{}, proof, err
	}

	current, err := attemptRead[managed.RemovalRevalidationCurrent](ctx, tx, recoveryCurrentKind, scope, bindingDigest, managed.MaxRecoveryCurrent)
	if err != nil {
		return current, proof, err
	}

	hash, err := managed.RecoveryCurrentDigest(current)
	if err != nil {
		return current, proof, err
	}

	if current.SchemaVersion != "1" || current.BindingDigest != bindingDigest || current.Digest != hash || current.Proof.Kind != managed.RecoveryProofRecord {
		return current, proof, managed.ErrUnavailable
	}

	proof, err = recoveryReadFact[managed.RemovalRevalidationFacts](ctx, tx, binding.Key.AttemptKey, current.Proof)
	if err != nil {
		return current, proof, err
	}

	if proof.Facts.Binding != binding || !proof.Facts.SourceObservedAt.Equal(current.SourceObservedAt) {
		return current, proof, managed.ErrUnavailable
	}

	return current, proof, nil
}

// recoveryAdvanceProof selects a fresh immutable proof after the caller has validated authority.
// Exact historical replay returns saved bytes before freshness checks and never moves current.
func recoveryAdvanceProof(ctx context.Context, tx ControllerTransaction, expected string, record managed.RecoveryRecord[managed.RemovalRevalidationFacts], now time.Time) (managed.RecoveryRecord[managed.RemovalRevalidationFacts], bool, error) {
	var zero managed.RecoveryRecord[managed.RemovalRevalidationFacts]

	selected, err := recoveryFactKey(record.Facts.Binding.Key.AttemptKey, record.Kind, record.Acceptance.OperationID, record.Facts)
	if err != nil {
		return zero, false, err
	}

	scope, err := attemptScope(record.Facts.Binding.Key.Key)
	if err != nil {
		return zero, false, err
	}

	raw, err := tx.ReadRecord(ctx, string(record.Kind), scope, selected)
	if err == nil {
		saved, decodeErr := managed.DecodeRecoveryRecord[managed.RemovalRevalidationFacts](raw)
		if decodeErr != nil {
			return zero, false, decodeErr
		}

		if record.Digest != saved.Digest || record.Acceptance.OperationID != saved.Acceptance.OperationID || record.Acceptance.RequestDigest != saved.Acceptance.RequestDigest || !reflect.DeepEqual(record.Facts, saved.Facts) {
			return zero, false, managed.ErrConflict
		}

		reference, referenceErr := managed.RecoveryAssociationFor(selected, saved)
		if referenceErr != nil {
			return zero, false, referenceErr
		}

		saved, readErr := recoveryReadFact[managed.RemovalRevalidationFacts](ctx, tx, record.Facts.Binding.Key.AttemptKey, reference)

		return saved, false, readErr
	}

	if !errors.Is(err, managed.ErrNotFound) {
		return zero, false, err
	}

	if !managed.RecoveryProofFresh(record.Facts, now) {
		return zero, false, managed.ErrBlocked
	}

	if err = managed.ValidateRecoveryNativeExport(record.Facts.Export); err != nil {
		return zero, false, err
	}

	current, previous, err := recoveryReadCurrent(ctx, tx, record.Facts.Binding)

	first := errors.Is(err, managed.ErrNotFound)
	if err != nil && (!first || expected != "") {
		return zero, false, err
	}

	// Only the absent pointer permits first selection. A dangling pointer is corruption.
	if first {
		bindingKey, keyErr := recoveryBindingKey(record.Facts.Binding)
		if keyErr != nil {
			return zero, false, keyErr
		}

		if _, pointerErr := tx.ReadRecord(ctx, recoveryCurrentKind, scope, bindingKey); !errors.Is(pointerErr, managed.ErrNotFound) {
			if pointerErr != nil {
				return zero, false, pointerErr
			}

			return zero, false, managed.ErrUnavailable
		}
	} else if current.Digest != expected || !record.Facts.SourceObservedAt.After(current.SourceObservedAt) || record.Facts.GuardID != previous.Facts.GuardID || record.Facts.GuardRootDigest != previous.Facts.GuardRootDigest {
		return zero, false, managed.ErrConflict
	}

	reference, err := recoveryWriteFact(ctx, tx, record.Facts.Binding.Key.AttemptKey, record)
	if err != nil {
		return zero, false, err
	}

	bindingDigest, err := recoveryBindingKey(record.Facts.Binding)
	if err != nil {
		return zero, false, err
	}

	next := managed.RemovalRevalidationCurrent{SchemaVersion: "1", BindingDigest: bindingDigest, Proof: reference, SourceObservedAt: record.Facts.SourceObservedAt}

	next.Digest, err = managed.RecoveryCurrentDigest(next)
	if err != nil {
		return zero, false, err
	}

	if err = attemptWrite(ctx, tx, recoveryCurrentKind, scope, bindingDigest, next, first, managed.MaxRecoveryCurrent); err != nil {
		return zero, false, err
	}

	return record, true, nil
}

// recoveryLeafSet applies a separate complete-response limit after all exact leaf checks.
func recoveryLeafSet(ref managed.AttemptRef, proofs []managed.RecoveryRecord[managed.RemovalRevalidationFacts], current []managed.RemovalRevalidationCurrent) (managed.RemovalRevalidationSet, error) {
	var zero managed.RemovalRevalidationSet
	if len(proofs) == 0 || len(proofs) > managed.MaxRecoveryBindings || len(proofs) != len(current) {
		return zero, managed.ErrInvalid
	}

	seen := make(map[string]bool, len(proofs))
	for i, proof := range proofs {
		raw, err := managed.EncodeRecoveryRecord(proof)
		if err != nil {
			return zero, err
		}

		if len(raw) > managed.MaxRemovalRevalidation || proof.Facts.Binding.Key.AttemptID != ref.AttemptID || proof.Facts.Binding.Key.Generation != ref.Generation || proof.Facts.Binding.DescriptorDigest != ref.DescriptorDigest || proof.Digest != current[i].Proof.Digest || seen[current[i].BindingDigest] {
			return zero, managed.ErrUnavailable
		}

		seen[current[i].BindingDigest] = true
	}

	out := managed.RemovalRevalidationSet{AttemptRef: ref, Proofs: proofs, Current: current}

	raw, err := json.Marshal(out)
	if err != nil {
		return zero, err
	}

	if len(raw) > managed.MaxRecoveryLeafSet {
		return zero, managed.ErrCapacity
	}

	// Return owned copies so consumers cannot mutate the accepted in-transaction view.
	var owned managed.RemovalRevalidationSet
	if err = json.NewDecoder(bytes.NewReader(raw)).Decode(&owned); err != nil {
		return zero, err
	}

	return owned, nil
}

// recoveryFactSubject permits one parent transaction to accept distinct fixed-set leaves.
// A root per step and a proof association per binding may share the same local receipt.
func recoveryFactSubject(facts any) (any, error) {
	switch value := facts.(type) {
	case managed.RemovalRecoveryActivation:
		return value.Key, nil
	case managed.RemovalRevalidationFacts:
		return value.Binding.Key, nil
	case managed.RecoveryNativeIntent:
		return struct {
			Binding   managed.AttemptBindingKey `json:"binding"`
			Operation string                    `json:"operation"`
		}{value.Binding.Key, value.Operation}, nil
	case managed.ProviderNativeCommitFacts:
		return struct {
			Binding    managed.AttemptBindingKey     `json:"binding"`
			Invocation managed.ProviderInvocationKey `json:"invocation"`
		}{value.Binding.Key, value.Invocation}, nil
	case managed.RecoveryNativeCloseFacts:
		return value.Binding.Key, nil
	case managed.ProviderStepRetryRoot:
		return value.Step, nil
	case managed.ProviderPermissionProof:
		return struct {
			Invocation    managed.ProviderInvocationKey `json:"invocation"`
			BindingDigest string                        `json:"binding_digest"`
		}{value.Invocation, value.BindingDigest}, nil
	case managed.ProviderInvocationPermission:
		return value.Key, nil
	case managed.ProviderStepRetryAuthorization:
		return value.Key, nil
	case managed.ProviderInvocationMarker:
		return value.Key, nil
	case managed.ProviderInvocationClosure:
		return value.Key, nil
	case managed.ProviderInvocationFailure:
		return value.Key, nil
	case managed.ProviderInvocationResult:
		return value.Key, nil
	case managed.RemovalCutoffFacts:
		return value.Key, nil
	case managed.EffectiveRemovalCoverage:
		return value.Key, nil
	case managed.EffectiveRemovalBinding:
		return value.Binding.Key, nil
	default:
		return nil, managed.ErrUnsupported
	}
}

// recoveryExportShape applies independent producer-export bounds on retention and inspection.
// Authentication remains the configured producer's responsibility.
func recoveryExportShape(facts any) error {
	switch value := facts.(type) {
	case managed.RemovalRevalidationFacts:
		return managed.ValidateRecoveryNativeExport(value.Export)
	case managed.ProviderNativeCommitFacts:
		return managed.ValidateRecoveryNativeExport(value.Export)
	case managed.RecoveryNativeCloseFacts:
		return managed.ValidateRecoveryNativeExport(value.Export)
	default:
		return nil
	}
}

// ReadManagedRecoveryNativeIntent returns one immutable native request for inspection.
// It supplies no delegation grant and never selects a newer request or repairs an absent leaf.
func (r *Repository) ReadManagedRecoveryNativeIntent(ctx context.Context, q managed.RecoveryExactRequest) (managed.RecoveryRecord[managed.RecoveryNativeIntent], error) {
	return r.readRecoveryNativeIntent(ctx, q)
}

// ReadManagedProviderNativeCommit returns the exact per-binding acceptance and original
// receipt only after its complete retained invocation associations validate.
func (r *Repository) ReadManagedProviderNativeCommit(ctx context.Context, q managed.RecoveryExactRequest) (managed.RecoveryRecord[managed.ProviderNativeCommitFacts], error) {
	if err := attemptVersion(q.Key); err != nil {
		return managed.RecoveryRecord[managed.ProviderNativeCommitFacts]{}, err
	}

	return r.readManagedProviderNativeCommitInspection(ctx, q)
}
