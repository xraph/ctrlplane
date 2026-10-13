package managed

import (
	"bytes"
	"encoding/json"
	"math"
	"time"

	"github.com/xraph/ctrlplane/id"
)

// RecoveryRecordKind is a finite retained-record kind, never a caller-defined storage key.
type RecoveryRecordKind string

// Recovery record kinds have independent domain hashes and actual encoded-byte ceilings.
const (
	RecoveryActivationRecord      RecoveryRecordKind = "removal_recovery_profile"
	RecoveryProofRecord           RecoveryRecordKind = "removal_revalidation"
	RecoveryIntentRecord          RecoveryRecordKind = "removal_recovery_intent"
	RecoveryCommitRecord          RecoveryRecordKind = "removal_native_commit"
	RecoveryCloseRecord           RecoveryRecordKind = "removal_native_close"
	RecoveryMarkerRecord          RecoveryRecordKind = "provider_invocation_marker"
	RecoveryClosureRecord         RecoveryRecordKind = "provider_invocation_closure"
	RecoveryCutoffRecord          RecoveryRecordKind = "removal_recovery_cutoff"
	RecoveryRootRecord            RecoveryRecordKind = "step_retry_root"
	RecoveryAuthorizationRecord   RecoveryRecordKind = "step_retry_authorization"
	RecoveryPermissionRecord      RecoveryRecordKind = "step_retry_permission"
	RecoveryPermissionProofRecord RecoveryRecordKind = "removal_permission_proof"
	RecoveryFailureRecord         RecoveryRecordKind = "step_retry_failure"
	RecoveryResultRecord          RecoveryRecordKind = "step_retry_result"
	RecoveryCoverageRecord        RecoveryRecordKind = "removal_effective_coverage"
	RecoveryCoverageBindingRecord RecoveryRecordKind = "removal_effective_binding"
)

// RecoveryRecordLimit returns zero for an unsupported record kind.
func RecoveryRecordLimit(kind RecoveryRecordKind) int {
	switch kind {
	case RecoveryProofRecord, RecoveryCoverageRecord:
		return MaxRemovalRevalidation
	case RecoveryActivationRecord, RecoveryMarkerRecord, RecoveryClosureRecord, RecoveryCutoffRecord:
		return MaxRecoveryFact
	case RecoveryCommitRecord, RecoveryCloseRecord:
		return MaxRecoveryNativeAcceptance
	case RecoveryIntentRecord:
		return MaxRecoveryNativeIntent
	case RecoveryRootRecord:
		return MaxStepRetryRoot
	case RecoveryAuthorizationRecord, RecoveryPermissionRecord, RecoveryFailureRecord, RecoveryResultRecord:
		return MaxStepRetryFact
	case RecoveryPermissionProofRecord, RecoveryCoverageBindingRecord:
		return MaxRecoveryFact
	default:
		return 0
	}
}

func recoveryDomain(kind RecoveryRecordKind) string {
	switch kind {
	case RecoveryActivationRecord:
		return RemovalRecoveryProfile
	case RecoveryProofRecord:
		return "ctrlplane.managed.removal-revalidation.v1"
	case RecoveryIntentRecord:
		return "ctrlplane.managed.removal-native-intent.v1"
	case RecoveryCommitRecord:
		return "ctrlplane.managed.removal-native-commit.v1"
	case RecoveryCloseRecord:
		return "ctrlplane.managed.removal-native-close.v1"
	case RecoveryMarkerRecord:
		return "ctrlplane.managed.provider-invocation.v1"
	case RecoveryClosureRecord:
		return "ctrlplane.managed.invocation-closure.v1"
	case RecoveryCutoffRecord:
		return "ctrlplane.managed.removal-cutoff.v1"
	case RecoveryRootRecord:
		return "ctrlplane.managed.step-retry-root.v1"
	case RecoveryAuthorizationRecord:
		return "ctrlplane.managed.step-retry-authorization.v1"
	case RecoveryPermissionRecord:
		return "ctrlplane.managed.step-retry-permission.v1"
	case RecoveryPermissionProofRecord:
		return "ctrlplane.managed.permission-proof.v1"
	case RecoveryFailureRecord:
		return "ctrlplane.managed.step-retry-failure.v1"
	case RecoveryResultRecord:
		return "ctrlplane.managed.step-retry-result.v1"
	case RecoveryCoverageRecord:
		return "ctrlplane.managed.effective-coverage.v1"
	case RecoveryCoverageBindingRecord:
		return "ctrlplane.managed.effective-coverage-binding.v1"
	default:
		return ""
	}
}

func recoveryFactKind(value any) RecoveryRecordKind {
	switch value.(type) {
	case RemovalRecoveryActivation:
		return RecoveryActivationRecord
	case RemovalRevalidationFacts:
		return RecoveryProofRecord
	case RecoveryNativeIntent:
		return RecoveryIntentRecord
	case ProviderNativeCommitFacts:
		return RecoveryCommitRecord
	case RecoveryNativeCloseFacts:
		return RecoveryCloseRecord
	case ProviderInvocationMarker:
		return RecoveryMarkerRecord
	case ProviderInvocationClosure:
		return RecoveryClosureRecord
	case RemovalCutoffFacts:
		return RecoveryCutoffRecord
	case ProviderStepRetryRoot:
		return RecoveryRootRecord
	case ProviderStepRetryAuthorization:
		return RecoveryAuthorizationRecord
	case ProviderInvocationPermission:
		return RecoveryPermissionRecord
	case ProviderPermissionProof:
		return RecoveryPermissionProofRecord
	case ProviderInvocationFailure:
		return RecoveryFailureRecord
	case ProviderInvocationResult:
		return RecoveryResultRecord
	case EffectiveRemovalCoverage:
		return RecoveryCoverageRecord
	case EffectiveRemovalBinding:
		return RecoveryCoverageBindingRecord
	default:
		return ""
	}
}

func recoveryHash(domain string, value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}

	return Digest(append([]byte(domain+"\n"), raw...)), nil
}

// RecoveryFactDigest hashes only the normalized typed fact, excluding local acceptance.
// Semantic authority and evidence authenticity must be checked before accepting the fact.
func RecoveryFactDigest[T any](facts T) (string, error) {
	kind := recoveryFactKind(facts)
	if kind == "" {
		return "", ErrUnsupported
	}

	return recoveryHash(recoveryDomain(kind), facts)
}

// EncodeRecoveryRecord checks the fact type, its digest and the complete encoded byte cap.
// This codec validates storage shape, not native authenticity or transition authority.
func EncodeRecoveryRecord[T any](record RecoveryRecord[T]) ([]byte, error) {
	kind := recoveryFactKind(record.Facts)
	if record.SchemaVersion != "1" || kind == "" || record.Kind != kind {
		return nil, ErrUnsupported
	}

	hash, err := RecoveryFactDigest(record.Facts)
	if err != nil {
		return nil, err
	}

	if record.Digest != hash || !hexDigest(record.Acceptance.RequestDigest) || record.Acceptance.OperationID.Prefix() != PrefixOperation {
		return nil, ErrInvalid
	}

	r, err := record.Acceptance.Receipt.Value()
	if err != nil || r.ID != record.Acceptance.OperationID || r.Digest != record.Acceptance.RequestDigest || r.Revision == 0 || r.Revision > math.MaxInt64 || r.LeaseEpoch > math.MaxInt64 || r.AcceptedAt.IsZero() {
		return nil, ErrInvalid
	}

	receiptRaw, err := json.Marshal(record.Acceptance)
	if err != nil {
		return nil, err
	}

	if len(receiptRaw) > MaxRecoveryReceipt {
		return nil, ErrCapacity
	}

	raw, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}

	if len(raw) > RecoveryRecordLimit(kind) {
		return nil, ErrCapacity
	}

	return raw, nil
}

// DecodeRecoveryRecord refuses noncanonical, truncated, unknown-field and oversized JSON.
func DecodeRecoveryRecord[T any](raw []byte) (RecoveryRecord[T], error) {
	var record RecoveryRecord[T]

	limit := RecoveryRecordLimit(recoveryFactKind(record.Facts))
	if limit == 0 {
		return record, ErrUnsupported
	}

	if len(raw) == 0 || len(raw) > limit {
		return record, ErrCapacity
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&record); err != nil {
		return RecoveryRecord[T]{}, ErrInvalid
	}

	canonical, err := EncodeRecoveryRecord(record)
	if err != nil {
		return RecoveryRecord[T]{}, err
	}

	if !bytes.Equal(raw, canonical) {
		return RecoveryRecord[T]{}, ErrInvalid
	}

	return record, nil
}

// ValidateRecoveryNativeExport checks independently bounded raw exports and attestation hashes.
func ValidateRecoveryNativeExport(value RecoveryNativeExport) error {
	if len(value.Raw) == 0 || len(value.Raw) > MaxRecoveryExport || len(value.Attestation) == 0 || len(value.Attestation) > MaxRecoveryAttestation {
		return ErrCapacity
	}

	version, err := value.ProducerVersion.Uint64()
	if err != nil || version == 0 || version > math.MaxInt64 || !originalToken(value.ProducerID) || !originalToken(value.ProducerProfile) || !originalToken(value.VerifierID) || value.RawDigest != Digest(value.Raw) || value.AttestationDigest != Digest(value.Attestation) {
		return ErrInvalid
	}

	return nil
}

// LocalCommand validates recovery's signed counters without changing legacy command codecs.
// Counter headroom depends on staged remaining obligations and is checked by the transition.
func (command RecoveryCommand) LocalCommand() (Command, error) {
	revision, revisionErr := command.Revision.Uint64()
	epoch, epochErr := command.LeaseEpoch.Uint64()

	generation, generationErr := command.AttemptRef.Generation.Uint64()
	if command.Profile != RemovalRecoveryProfile {
		return Command{}, ErrUnsupported
	}

	if revisionErr != nil || epochErr != nil || generationErr != nil || revision == 0 || revision > math.MaxInt64 || epoch == 0 || epoch > math.MaxInt64 || generation == 0 || generation > math.MaxInt64 || !originalToken(command.Key.TenantID) || !originalID(command.Key.InstanceID, id.PrefixInstance) || !originalID(command.ID, PrefixOperation) || !originalID(command.AttemptRef.AttemptID, PrefixOperation) || !hexDigest(command.AttemptRef.DescriptorDigest) || !originalToken(command.Owner) {
		return Command{}, ErrInvalid
	}

	return Command{Key: command.Key, ID: command.ID, Revision: revision, LeaseEpoch: epoch, Owner: command.Owner}, nil
}

// RecoveryCurrentDigest hashes a current proof pointer without its self digest.
func RecoveryCurrentDigest(current RemovalRevalidationCurrent) (string, error) {
	current.Digest = ""

	return recoveryHash("ctrlplane.managed.removal-revalidation-current.v1", current)
}

// RetryCurrentDigest hashes one selected invocation without its self digest.
func RetryCurrentDigest(current ProviderStepRetryCurrent) (string, error) {
	current.Digest = ""

	return recoveryHash("ctrlplane.managed.step-retry-current.v1", current)
}

// RetryPreviousDigest hashes the fixed predecessor certificate without its self digest.
func RetryPreviousDigest(previous ProviderStepRetryPrevious) (string, error) {
	previous.Digest = ""

	return recoveryHash("ctrlplane.managed.step-retry-previous.v1", previous)
}

// RecoveryAssociationFor derives an exact reference from a validated retained record.
func RecoveryAssociationFor[T any](key string, record RecoveryRecord[T]) (RecoveryAssociation, error) {
	if !hexDigest(key) {
		return RecoveryAssociation{}, ErrInvalid
	}

	if _, err := EncodeRecoveryRecord(record); err != nil {
		return RecoveryAssociation{}, err
	}

	return RecoveryAssociation{Kind: record.Kind, Key: key, Digest: record.Digest, OperationID: record.Acceptance.OperationID, ReceiptDigest: record.Acceptance.RequestDigest}, nil
}

// ValidateRecoveryAssociation checks the bounded exact address; it never proves acceptance.
func ValidateRecoveryAssociation(reference RecoveryAssociation) error {
	if RecoveryRecordLimit(reference.Kind) == 0 || !hexDigest(reference.Key) || !hexDigest(reference.Digest) || reference.OperationID.Prefix() != PrefixOperation || !hexDigest(reference.ReceiptDigest) {
		return ErrInvalid
	}

	raw, err := json.Marshal(reference)
	if err != nil {
		return err
	}

	if len(raw) > MaxRecoveryAssociation {
		return ErrCapacity
	}

	return nil
}

// RecoveryProofFresh checks actual callback time independently of native acceptance time.
func RecoveryProofFresh(facts RemovalRevalidationFacts, now time.Time) bool {
	return !now.IsZero() && !facts.SourceObservedAt.IsZero() && !facts.NativeAcceptedAt.IsZero() && !facts.SourceObservedAt.After(facts.NativeAcceptedAt) && !facts.NativeAcceptedAt.After(now) && now.Before(facts.ValidUntil)
}
