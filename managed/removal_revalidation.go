package managed

import (
	"context"
	"time"

	"github.com/xraph/ctrlplane/id"
)

// Recovery profiles leave the original manifest and original attempt facts unchanged.
const (
	RemovalRecoveryProfile      = "ctrlplane.managed.removal-recovery.v1"
	ProviderStepRetryProfile    = "ctrlplane.managed.provider-step-retry.v1"
	MaxRemovalRevalidation      = 32768
	MaxRecoveryCurrent          = 1024
	MaxRecoveryAssociation      = 1024
	MaxRecoveryExport           = 4096
	MaxRecoveryAttestation      = 4096
	MaxRecoveryFact             = 8192
	MaxRecoveryNativeAcceptance = 16384
	MaxRecoveryNativeIntent     = 16384
	MaxRecoveryReceipt          = 2048
	MaxRecoveryLeafSet          = 4 << 20
	MaxRecoveryBindings         = 64
	MaxRecoverySteps            = 33
)

// RecoveryCommand preserves the parent and attempt identity with signed wire counters.
type RecoveryCommand struct {
	Key        Key        `json:"key"`
	ID         id.ID      `json:"id"`
	Revision   Decimal    `json:"revision"`
	LeaseEpoch Decimal    `json:"lease_epoch"`
	Owner      string     `json:"owner"`
	AttemptRef AttemptRef `json:"attempt_ref"`
	Profile    string     `json:"profile"`
}

// RecoveryAssociation names one immutable local fact and its accepted authority receipt.
type RecoveryAssociation struct {
	Kind          RecoveryRecordKind `json:"kind"`
	Key           string             `json:"key"`
	Digest        string             `json:"digest"`
	OperationID   id.ID              `json:"operation_id"`
	ReceiptDigest string             `json:"receipt_digest"`
}

// RecoveryAcceptance links a fact to the immutable local request that accepted it.
type RecoveryAcceptance struct {
	OperationID   id.ID       `json:"operation_id"`
	RequestDigest string      `json:"request_digest"`
	Receipt       WireReceipt `json:"receipt"`
}

// RecoveryRecord retains normalized facts independently of raw native wire schemas.
// Its digest excludes Acceptance and Digest; the retained event binds both to the request.
type RecoveryRecord[T any] struct {
	SchemaVersion Decimal            `json:"schema_version"`
	Kind          RecoveryRecordKind `json:"kind"`
	Facts         T                  `json:"facts"`
	Digest        string             `json:"digest"`
	Acceptance    RecoveryAcceptance `json:"acceptance"`
}

// RemovalRecoveryActivation fixes the adopted original selections without granting send authority.
type RemovalRecoveryActivation struct {
	Key                     AttemptKey `json:"key"`
	AttemptRef              AttemptRef `json:"attempt_ref"`
	Profile                 string     `json:"profile"`
	RetryProfile            string     `json:"retry_profile"`
	ProviderOperationID     id.ID      `json:"provider_operation_id"`
	AdmissionDigest         string     `json:"admission_digest"`
	BindingCount            Decimal    `json:"binding_count"`
	StepCount               Decimal    `json:"step_count"`
	OriginalSelectionDigest string     `json:"original_selection_digest"`
	ActivatedAt             time.Time  `json:"activated_at"`
}

// RemovalRecoveryActivationRequest selects persisted original facts inside the authority transaction.
type RemovalRecoveryActivationRequest struct {
	Command RecoveryCommand `json:"command"`
}

// RemovalRecoveryBinding freezes the original process and native Begin associations.
type RemovalRecoveryBinding struct {
	Key                        AttemptBindingKey `json:"key"`
	DescriptorDigest           string            `json:"descriptor_digest"`
	ProviderOperationID        id.ID             `json:"provider_operation_id"`
	OriginalRegistrationDigest string            `json:"original_registration_digest"`
	OriginalDrainDigest        string            `json:"original_drain_digest"`
	OriginalCommandDigest      string            `json:"original_command_digest"`
	OriginalBeginRequestID     string            `json:"original_begin_request_id"`
	OriginalBeginRequestDigest string            `json:"original_begin_request_digest"`
	NativeFenceDigest          string            `json:"native_fence_digest"`
	OriginalProcessDigest      string            `json:"original_process_digest"`
	AdmissionDigest            string            `json:"admission_digest"`
}

// RecoveryNativeExport holds bounded compact bytes and a separately authenticated attestation.
// Native material and native receipt digests have their own domains and are never raw hashes.
type RecoveryNativeExport struct {
	Raw               []byte  `json:"raw"`
	RawDigest         string  `json:"raw_digest"`
	Attestation       []byte  `json:"attestation"`
	AttestationDigest string  `json:"attestation_digest"`
	ProducerID        string  `json:"producer_id"`
	ProducerProfile   string  `json:"producer_profile"`
	ProducerVersion   Decimal `json:"producer_version"`
	VerifierID        string  `json:"verifier_id"`
}

// RecoveryNativeIntent preserves the exact opaque native request before a producer call.
type RecoveryNativeIntent struct {
	Binding       RemovalRecoveryBinding `json:"binding"`
	Profile       string                 `json:"profile"`
	Operation     string                 `json:"operation"`
	RequestID     string                 `json:"request_id"`
	RequestDigest string                 `json:"request_digest"`
	Command       []byte                 `json:"command"`
	CommandDigest string                 `json:"command_digest"`
	Permission    *RecoveryAssociation   `json:"permission,omitempty"`
	Proof         *RecoveryAssociation   `json:"proof,omitempty"`
	Cutoff        *RecoveryAssociation   `json:"cutoff,omitempty"`
	Coverage      *RecoveryAssociation   `json:"coverage,omitempty"`
}

// RecoveryEffectComplete discharges one original process using an exact positive deletion.
// A missing process, expired lease or empty lookup cannot populate this fact.
type RecoveryEffectComplete struct {
	Invocation            ProviderInvocationKey `json:"invocation"`
	PermissionDigest      string                `json:"permission_digest"`
	Result                RecoveryAssociation   `json:"result"`
	ResourceID            string                `json:"resource_id"`
	Incarnation           string                `json:"incarnation"`
	OriginalProcessDigest string                `json:"original_process_digest"`
	PreEffectProofDigest  string                `json:"pre_effect_proof_digest"`
}

// RemovalRevalidationFacts describe one accepted observation of the same original native guard.
type RemovalRevalidationFacts struct {
	Binding              RemovalRecoveryBinding  `json:"binding"`
	Intent               RecoveryAssociation     `json:"intent"`
	GuardID              string                  `json:"guard_id"`
	GuardRootDigest      string                  `json:"guard_root_digest"`
	NativeRequestID      string                  `json:"native_request_id"`
	NativeRequestDigest  string                  `json:"native_request_digest"`
	NativeMaterialDigest string                  `json:"native_material_digest"`
	NativeReceiptDigest  string                  `json:"native_receipt_digest"`
	NativeCurrentDigest  string                  `json:"native_current_digest"`
	NativeScopeDigest    string                  `json:"native_scope_digest"`
	CandidateVersion     Decimal                 `json:"candidate_version"`
	RemovalEpoch         Decimal                 `json:"removal_epoch"`
	CandidateDigest      string                  `json:"candidate_digest"`
	BuildDigest          string                  `json:"build_digest"`
	SurvivorDigest       string                  `json:"survivor_digest"`
	ExclusionDigest      string                  `json:"exclusion_digest"`
	CoverageDigest       string                  `json:"coverage_digest"`
	ProcessDisposition   string                  `json:"process_disposition"`
	EffectComplete       *RecoveryEffectComplete `json:"effect_complete,omitempty"`
	SourceObservedAt     time.Time               `json:"source_observed_at"`
	NativeAcceptedAt     time.Time               `json:"native_accepted_at"`
	ValidUntil           time.Time               `json:"valid_until"`
	Export               RecoveryNativeExport    `json:"export"`
}

// RemovalRevalidationCurrent selects an exact immutable observation by CAS.
type RemovalRevalidationCurrent struct {
	SchemaVersion    Decimal             `json:"schema_version"`
	BindingDigest    string              `json:"binding_digest"`
	Proof            RecoveryAssociation `json:"proof"`
	SourceObservedAt time.Time           `json:"source_observed_at"`
	Digest           string              `json:"digest"`
}

// RemovalRevalidationRequest accepts authenticated producer facts with expected-current CAS.
type RemovalRevalidationRequest struct {
	Command               RecoveryCommand          `json:"command"`
	ExpectedCurrentDigest string                   `json:"expected_current_digest"`
	Facts                 RemovalRevalidationFacts `json:"facts"`
}

// RecoveryExactRequest addresses history independently of the current proof or invocation.
type RecoveryExactRequest struct {
	Key         AttemptKey          `json:"key"`
	Association RecoveryAssociation `json:"association"`
}

// RecoveryNativeIntentRequest records native request identity before invoking the configured producer.
type RecoveryNativeIntentRequest struct {
	Command RecoveryCommand      `json:"command"`
	Intent  RecoveryNativeIntent `json:"intent"`
}

// RemovalRevalidationSet is a complete descriptor-ordered response, never a page.
type RemovalRevalidationSet struct {
	AttemptRef AttemptRef                                 `json:"attempt_ref"`
	Proofs     []RecoveryRecord[RemovalRevalidationFacts] `json:"proofs"`
	Current    []RemovalRevalidationCurrent               `json:"current"`
}

// RemovalCutoffFacts binds permanent attempt revocation before native protection closes.
type RemovalCutoffFacts struct {
	Key                 AttemptKey  `json:"key"`
	AttemptRef          AttemptRef  `json:"attempt_ref"`
	ProviderOperationID id.ID       `json:"provider_operation_id"`
	RevocationDigest    string      `json:"revocation_digest"`
	RevocationReceipt   WireReceipt `json:"revocation_receipt"`
	CutoffAt            time.Time   `json:"cutoff_at"`
}

// RecoveryNativeCloseFacts retains exact native closure after permanent cutoff and full coverage.
type RecoveryNativeCloseFacts struct {
	Binding             RemovalRecoveryBinding `json:"binding"`
	Intent              RecoveryAssociation    `json:"intent"`
	Cutoff              RecoveryAssociation    `json:"cutoff"`
	Coverage            RecoveryAssociation    `json:"coverage"`
	GuardID             string                 `json:"guard_id"`
	GuardRootDigest     string                 `json:"guard_root_digest"`
	NativeRequestID     string                 `json:"native_request_id"`
	NativeRequestDigest string                 `json:"native_request_digest"`
	NativeReceiptDigest string                 `json:"native_receipt_digest"`
	NativeAcceptedAt    time.Time              `json:"native_accepted_at"`
	Export              RecoveryNativeExport   `json:"export"`
}

// RecoveryNativeCloseRequest records the configured producer's authenticated compact acceptance.
type RecoveryNativeCloseRequest struct {
	Command RecoveryCommand          `json:"command"`
	Facts   RecoveryNativeCloseFacts `json:"facts"`
}

// RemovalRevalidationStore is an optional complete local recovery capability.
// Evidence verification belongs to configured host producers; store calls never perform transport.
type RemovalRevalidationStore interface {
	ActivateManagedRemovalRecovery(ctx context.Context, request RemovalRecoveryActivationRequest) (RecoveryRecord[RemovalRecoveryActivation], error)
	RecordManagedRecoveryNativeIntent(ctx context.Context, request RecoveryNativeIntentRequest) (RecoveryRecord[RecoveryNativeIntent], error)
	ReadManagedRecoveryNativeIntent(ctx context.Context, request RecoveryExactRequest) (RecoveryRecord[RecoveryNativeIntent], error)
	AcceptManagedRemovalRevalidation(ctx context.Context, request RemovalRevalidationRequest) (RecoveryRecord[RemovalRevalidationFacts], error)
	ReadManagedRemovalRevalidation(ctx context.Context, request RecoveryExactRequest) (RecoveryRecord[RemovalRevalidationFacts], error)
	ReadManagedRemovalRevalidationSet(ctx context.Context, key AttemptKey) (RemovalRevalidationSet, error)
	CutoffManagedRemovalRecovery(ctx context.Context, command RecoveryCommand) (RecoveryRecord[RemovalCutoffFacts], error)
	RecordManagedRemovalGuardClose(ctx context.Context, request RecoveryNativeCloseRequest) (RecoveryRecord[RecoveryNativeCloseFacts], error)
	ReadManagedRemovalGuardClose(ctx context.Context, request RecoveryExactRequest) (RecoveryRecord[RecoveryNativeCloseFacts], error)
}

// RecoveryProducerContext carries only accepted local identities to a configured producer.
// Hosts supply authenticated machine context through ctx; callers cannot inject a proof resolver.
type RecoveryProducerContext struct {
	Profile string                               `json:"profile"`
	Key     AttemptKey                           `json:"key"`
	Intent  RecoveryRecord[RecoveryNativeIntent] `json:"intent"`
}

// RemovalRevalidationProducer revalidates or recovers the retained same-original native request.
type RemovalRevalidationProducer interface {
	RevalidateManagedRemoval(ctx context.Context, producer RecoveryProducerContext) (RemovalRevalidationFacts, error)
	RecoverManagedRemovalRevalidation(ctx context.Context, producer RecoveryProducerContext) (RemovalRevalidationFacts, error)
}

// RemovalGuardCloseProducer closes or recovers a retained native close request.
type RemovalGuardCloseProducer interface {
	CloseManagedRemovalGuard(ctx context.Context, producer RecoveryProducerContext) (RecoveryNativeCloseFacts, error)
	RecoverManagedRemovalGuardClose(ctx context.Context, producer RecoveryProducerContext) (RecoveryNativeCloseFacts, error)
}
