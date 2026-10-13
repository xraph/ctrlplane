package managed

import (
	"context"
	"time"

	"github.com/xraph/ctrlplane/id"
)

// Retry limits apply to complete encoded records, including their local acceptance.
const (
	MaxStepRetryRoot    = 8192
	MaxStepRetryPointer = 2048
	MaxStepRetryFact    = 16384
)

// ProviderInvocationKey addresses an immutable invocation of one original logical step.
type ProviderInvocationKey struct {
	AttemptKey AttemptKey      `json:"attempt_key"`
	Step       ProviderStepKey `json:"step"`
	CommandID  id.ID           `json:"command_id"`
}

// ProviderStepRetryRoot anchors the original accepted permission and resource identity.
type ProviderStepRetryRoot struct {
	AttemptRef                AttemptRef                 `json:"attempt_ref"`
	Step                      ProviderStepKey            `json:"step"`
	Profile                   string                     `json:"profile"`
	SpecDigest                string                     `json:"spec_digest"`
	ResourceID                string                     `json:"resource_id"`
	Incarnation               string                     `json:"incarnation"`
	ProviderCommandDigest     string                     `json:"provider_command_digest"`
	OriginalPermission        WireProviderStepPermission `json:"original_permission"`
	OriginalPermissionReceipt WireReceipt                `json:"original_permission_receipt"`
}

// ProviderStepRetryCurrent holds one selected invocation, including explicit ordinal zero.
type ProviderStepRetryCurrent struct {
	SchemaVersion  Decimal             `json:"schema_version"`
	RootDigest     string              `json:"root_digest"`
	CommandID      id.ID               `json:"command_id"`
	Ordinal        Decimal             `json:"ordinal"`
	Permission     RecoveryAssociation `json:"permission"`
	Authority      RecoveryAssociation `json:"authority"`
	PreviousDigest string              `json:"previous_digest"`
	MarkerKey      string              `json:"marker_key,omitempty"`
	ClosureKey     string              `json:"closure_key,omitempty"`
	FailureKey     string              `json:"failure_key,omitempty"`
	ResultKey      string              `json:"result_key,omitempty"`
	Digest         string              `json:"digest"`
}

// ProviderStepRetryPrevious is one accepted predecessor association, never a history chain.
type ProviderStepRetryPrevious struct {
	SchemaVersion    Decimal             `json:"schema_version"`
	RootDigest       string              `json:"root_digest"`
	CommandID        id.ID               `json:"command_id"`
	Ordinal          Decimal             `json:"ordinal"`
	PermissionDigest string              `json:"permission_digest"`
	Failure          RecoveryAssociation `json:"failure"`
	Closure          RecoveryAssociation `json:"closure"`
	Authority        RecoveryAssociation `json:"authority"`
	Digest           string              `json:"digest"`
}

// ProviderInvocationPermission retains original command bytes with a new local identity only.
type ProviderInvocationPermission struct {
	Key                      ProviderInvocationKey      `json:"key"`
	RootDigest               string                     `json:"root_digest"`
	Ordinal                  Decimal                    `json:"ordinal"`
	Permission               WireProviderStepPermission `json:"permission"`
	ProviderCommandDigest    string                     `json:"provider_command_digest"`
	PermissionProofSetDigest string                     `json:"permission_proof_set_digest"`
	PermissionProofCount     Decimal                    `json:"permission_proof_count"`
}

// ProviderPermissionProof preserves the exact native proof accepted with this permission.
type ProviderPermissionProof struct {
	Invocation           ProviderInvocationKey `json:"invocation"`
	BindingDigest        string                `json:"binding_digest"`
	PermissionDigest     string                `json:"permission_digest"`
	Proof                RecoveryAssociation   `json:"proof"`
	NativeMaterialDigest string                `json:"native_material_digest"`
}

// ProviderStepRetryAuthorization certifies predecessor checks made in the selecting transaction.
type ProviderStepRetryAuthorization struct {
	Key                   ProviderInvocationKey     `json:"key"`
	RootDigest            string                    `json:"root_digest"`
	Ordinal               Decimal                   `json:"ordinal"`
	Permission            RecoveryAssociation       `json:"permission"`
	Previous              ProviderStepRetryPrevious `json:"previous"`
	ExpectedCurrentDigest string                    `json:"expected_current_digest"`
	ParentRevision        Decimal                   `json:"parent_revision"`
	AttemptRevision       Decimal                   `json:"attempt_revision"`
	AuthorizedAt          time.Time                 `json:"authorized_at"`
}

// ProviderStepRetryRequest selects only the current closed non-dispatched invocation.
type ProviderStepRetryRequest struct {
	Command               RecoveryCommand `json:"command"`
	Step                  ProviderStepKey `json:"step"`
	ExpectedCurrentDigest string          `json:"expected_current_digest"`
	CommandID             id.ID           `json:"command_id"`
}

// ProviderInvocationFailure retains the qualified witness for one exact permission.
type ProviderInvocationFailure struct {
	Key              ProviderInvocationKey `json:"key"`
	RootDigest       string                `json:"root_digest"`
	PermissionDigest string                `json:"permission_digest"`
	Failure          ProviderStepFailure   `json:"failure"`
}

// ProviderInvocationClosure permanently rules out delegation of the exact local invocation.
// The failure and closure are accepted in the same authority transaction.
type ProviderInvocationClosure struct {
	Key              ProviderInvocationKey `json:"key"`
	RootDigest       string                `json:"root_digest"`
	PermissionDigest string                `json:"permission_digest"`
	FailureDigest    string                `json:"failure_digest"`
	ProducerProfile  string                `json:"producer_profile"`
	VerifierID       string                `json:"verifier_id"`
	ClosedAt         time.Time             `json:"closed_at"`
}

// ProviderInvocationCloseRequest accepts an affirmative configured before-delegation witness.
type ProviderInvocationCloseRequest struct {
	Command RecoveryCommand           `json:"command"`
	Failure ProviderInvocationFailure `json:"failure"`
}

// ProviderInvocationResult retains positive or uncertain facts without replacing earlier facts.
type ProviderInvocationResult struct {
	Key              ProviderInvocationKey  `json:"key"`
	RootDigest       string                 `json:"root_digest"`
	PermissionDigest string                 `json:"permission_digest"`
	Result           WireProviderStepResult `json:"result"`
}

// ProviderInvocationObservationRequest gives each observation a stable caller operation ID.
// Reuse the complete request after a lost response; receipt replay never issues transport.
type ProviderInvocationObservationRequest struct {
	Command               RecoveryCommand          `json:"command"`
	ExpectedCurrentDigest string                   `json:"expected_current_digest"`
	Result                ProviderInvocationResult `json:"result"`
}

// ProviderNativeCommitFacts binds exact original permission and independently fresh current proof.
type ProviderNativeCommitFacts struct {
	Binding               RemovalRecoveryBinding `json:"binding"`
	Invocation            ProviderInvocationKey  `json:"invocation"`
	Intent                RecoveryAssociation    `json:"intent"`
	PermissionDigest      string                 `json:"permission_digest"`
	PermissionProof       RecoveryAssociation    `json:"permission_proof"`
	CurrentProof          RecoveryAssociation    `json:"current_proof"`
	PermissionProofDigest string                 `json:"permission_proof_digest"`
	CurrentProofDigest    string                 `json:"current_proof_digest"`
	GuardID               string                 `json:"guard_id"`
	GuardRootDigest       string                 `json:"guard_root_digest"`
	NativeRequestID       string                 `json:"native_request_id"`
	NativeRequestDigest   string                 `json:"native_request_digest"`
	NativeReceiptDigest   string                 `json:"native_receipt_digest"`
	NativeAcceptedAt      time.Time              `json:"native_accepted_at"`
	Export                RecoveryNativeExport   `json:"export"`
}

// ProviderNativeCommitRequest retains authenticated native acceptance without issuing a send.
type ProviderNativeCommitRequest struct {
	Command RecoveryCommand           `json:"command"`
	Facts   ProviderNativeCommitFacts `json:"facts"`
}

// ProviderInvocationMarker commits a finite ordered set of per-binding native acceptances.
// Native commits remain in individual rows; the marker never embeds all 64 exports.
type ProviderInvocationMarker struct {
	Key                   ProviderInvocationKey `json:"key"`
	RootDigest            string                `json:"root_digest"`
	PermissionDigest      string                `json:"permission_digest"`
	SelectionDigest       string                `json:"selection_digest"`
	NativeCommitSetDigest string                `json:"native_commit_set_digest"`
	CurrentProofSetDigest string                `json:"current_proof_set_digest"`
	BindingCount          Decimal               `json:"binding_count"`
	AcceptedAt            time.Time             `json:"accepted_at"`
}

// ProviderInvocationCommitRequest names a permission; the store derives complete binding coverage.
type ProviderInvocationCommitRequest struct {
	Command               RecoveryCommand       `json:"command"`
	Invocation            ProviderInvocationKey `json:"invocation"`
	ExpectedCurrentDigest string                `json:"expected_current_digest"`
}

// ProviderInvocationGrant permits one delegation only when the unique marker was newly accepted.
// Receipt recovery, lookup and historical permission replay always leave MayDelegate false.
type ProviderInvocationGrant struct {
	Marker      RecoveryRecord[ProviderInvocationMarker] `json:"marker"`
	MayDelegate bool                                     `json:"may_delegate"`
}

// EffectiveProviderStep names one selected terminal invocation for one original logical step.
type EffectiveProviderStep struct {
	StepDigest       string `json:"step_digest"`
	SelectionDigest  string `json:"selection_digest"`
	PermissionDigest string `json:"permission_digest"`
	TerminalDigest   string `json:"terminal_digest"`
	ClosureDigest    string `json:"closure_digest,omitempty"`
}

// EffectiveRemovalCoverage contains the fixed finite step set, without retry history.
type EffectiveRemovalCoverage struct {
	Key                 AttemptKey              `json:"key"`
	AttemptRef          AttemptRef              `json:"attempt_ref"`
	ProviderOperationID id.ID                   `json:"provider_operation_id"`
	ResourcesDigest     string                  `json:"resources_digest"`
	AdmissionDigest     string                  `json:"admission_digest"`
	Cutoff              RecoveryAssociation     `json:"cutoff"`
	Disposition         Disposition             `json:"disposition"`
	Steps               []EffectiveProviderStep `json:"steps"`
}

// ProviderInvocationRecord is inspection only and cannot recreate a lost delegation grant.
type ProviderInvocationRecord struct {
	Permission    RecoveryRecord[ProviderInvocationPermission]    `json:"permission"`
	Authorization *RecoveryRecord[ProviderStepRetryAuthorization] `json:"authorization,omitempty"`
	Marker        *RecoveryRecord[ProviderInvocationMarker]       `json:"marker,omitempty"`
	Closure       *RecoveryRecord[ProviderInvocationClosure]      `json:"closure,omitempty"`
	Failure       *RecoveryRecord[ProviderInvocationFailure]      `json:"failure,omitempty"`
	Result        *RecoveryRecord[ProviderInvocationResult]       `json:"result,omitempty"`
}

// ProviderInvocationStore owns the unique delegation marker and permanent invocation closure.
type ProviderInvocationStore interface {
	RecordManagedProviderNativeCommit(ctx context.Context, request ProviderNativeCommitRequest) (RecoveryRecord[ProviderNativeCommitFacts], error)
	ReadManagedProviderNativeCommit(ctx context.Context, request RecoveryExactRequest) (RecoveryRecord[ProviderNativeCommitFacts], error)
	CommitManagedProviderInvocation(ctx context.Context, request ProviderInvocationCommitRequest) (ProviderInvocationGrant, error)
	CloseManagedProviderInvocation(ctx context.Context, request ProviderInvocationCloseRequest) (RecoveryRecord[ProviderInvocationClosure], error)
	RecordManagedProviderInvocationResult(ctx context.Context, result ProviderInvocationObservationRequest) error
	ReadManagedProviderInvocation(ctx context.Context, key ProviderInvocationKey) (ProviderInvocationRecord, error)
}

// ProviderStepRetryStore selects same-step retry authority without widening ControllerStore.
type ProviderStepRetryStore interface {
	AuthorizeManagedProviderStepRetry(ctx context.Context, request ProviderStepRetryRequest) (RecoveryRecord[ProviderStepRetryAuthorization], error)
	ReadManagedProviderStepRetry(ctx context.Context, key ProviderInvocationKey) (RecoveryRecord[ProviderStepRetryAuthorization], error)
	ReadManagedEffectiveRemovalCoverage(ctx context.Context, key AttemptKey) (RecoveryRecord[EffectiveRemovalCoverage], error)
}

// RemovalCommitProducer binds, checks or recovers the immutable native permission request.
type RemovalCommitProducer interface {
	BindManagedRemovalCommit(ctx context.Context, producer RecoveryProducerContext) (ProviderNativeCommitFacts, error)
	CheckManagedRemovalCommit(ctx context.Context, producer RecoveryProducerContext) (ProviderNativeCommitFacts, error)
	RecoverManagedRemovalCommit(ctx context.Context, producer RecoveryProducerContext) (ProviderNativeCommitFacts, error)
}

// ProviderInvocationClosureProducer supplies affirmative evidence from before its transport boundary.
type ProviderInvocationClosureProducer interface {
	WitnessManagedProviderNoDispatch(ctx context.Context, key ProviderInvocationKey) (ProviderInvocationFailure, error)
}

// RequireRemovalRecovery refuses partial capability before any recovery profile issuance.
// This checks interface support only. Hosts must also configure and authenticate each producer.
func RequireRemovalRecovery(authority any) (RemovalRevalidationStore, ProviderInvocationStore, ProviderStepRetryStore, error) {
	if recoveryCapabilityAbsent(authority) {
		return nil, nil, nil, ErrUnsupported
	}

	revalidation, revalidationOK := authority.(RemovalRevalidationStore)
	invocation, invocationOK := authority.(ProviderInvocationStore)

	retry, retryOK := authority.(ProviderStepRetryStore)

	_, lifecycleOK := authority.(RemovalRecoveryLifecycleStore)
	if !revalidationOK || !invocationOK || !retryOK || !lifecycleOK {
		return nil, nil, nil, ErrUnsupported
	}

	return revalidation, invocation, retry, nil
}
