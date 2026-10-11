package managed

import (
	"context"
	"time"

	"github.com/xraph/ctrlplane/id"
)

// OperationKey selects one retained accepted action in its original tenant.
type OperationKey struct {
	Key

	ActionID id.ID `json:"action_id"`
}

// OperationState retains original commands and bounded progress without issue authority.
type OperationState struct {
	Key         OperationKey         `json:"key"`
	Submission  ActionSubmission     `json:"submission"`
	Receipt     ActionReceipt        `json:"receipt"`
	Revision    uint64               `json:"revision"`
	Observation OperationObservation `json:"observation"`
}

// OperationObservation reports a finite progress fact and the original remote command.
// Neither phase nor a successful observation grants permission for a provider call.
type OperationObservation struct {
	Kind       string       `json:"kind"`
	Command    *PortCommand `json:"command,omitempty"`
	Evidence   []byte       `json:"evidence,omitempty"`
	RetryAt    time.Time    `json:"retry_at"`
	Diagnostic string       `json:"diagnostic,omitempty"`
	ObservedAt time.Time    `json:"observed_at"`
}

// OperationAdvance applies a parent-fenced journal CAS and retains the exact retry result.
type OperationAdvance struct {
	Command

	ActionID                  id.ID                `json:"action_id"`
	ExpectedOperationRevision uint64               `json:"expected_operation_revision"`
	Observation               OperationObservation `json:"observation"`
}

// ControllerKey explicitly names a configured global or tenant sweep authority.
type ControllerKey struct {
	Installation string     `json:"installation"`
	Authority    string     `json:"authority"`
	Name         string     `json:"name"`
	Scope        AuditScope `json:"scope"`
}

// Continuation preserves the unprocessed page and in-flight slot across restart.
type Continuation struct {
	Key        ControllerKey `json:"key"`
	Revision   uint64        `json:"revision"`
	LeaseOwner string        `json:"lease_owner"`
	LeaseEpoch uint64        `json:"lease_epoch"`
	LeaseUntil time.Time     `json:"lease_until"`
	Cursor     string        `json:"cursor"`
	Pending    []Key         `json:"pending"`
	InFlight   *Key          `json:"in_flight,omitempty"`
}

// ContinuationAdvance replaces one bounded sweep snapshot under a fenced lease.
type ContinuationAdvance struct {
	Key              ControllerKey `json:"key"`
	ID               id.ID         `json:"id"`
	ExpectedRevision uint64        `json:"expected_revision"`
	LeaseEpoch       uint64        `json:"lease_epoch"`
	Owner            string        `json:"owner"`
	Duration         time.Duration `json:"duration"`
	Cursor           string        `json:"cursor"`
	Pending          []Key         `json:"pending"`
	InFlight         *Key          `json:"in_flight,omitempty"`
}

// ReleaseKey identifies a tenant-owned immutable release snapshot.
type ReleaseKey struct {
	TenantID  string `json:"tenant_id"`
	ReleaseID id.ID  `json:"release_id"`
}

// ReleaseBinding preserves the accepted manifest association and release identity.
type ReleaseBinding struct {
	Key        ReleaseKey `json:"key"`
	Target     Key        `json:"target"`
	ActionID   id.ID      `json:"action_id"`
	Manifest   Manifest   `json:"manifest"`
	Version    uint64     `json:"version"`
	AcceptedAt time.Time  `json:"accepted_at"`
}

// ReleaseBindingRequest selects persisted acceptance; callers supply no snapshot rows.
type ReleaseBindingRequest struct {
	Command

	ActionID  id.ID `json:"action_id"`
	ReleaseID id.ID `json:"release_id"`
}

// AuditPageRequest selects pending sink delivery in one explicit audit scope.
type AuditPageRequest struct {
	Installation string     `json:"installation"`
	Authority    string     `json:"authority"`
	Scope        AuditScope `json:"scope"`
	Cursor       string     `json:"cursor"`
	Limit        int        `json:"limit"`
}

// AuditPage contains bounded immutable intents and its scope-bound continuation.
type AuditPage struct {
	Items      []AuditIntent `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

// AuditAcknowledgment records a completely bound sink receipt.
type AuditAcknowledgment struct {
	Intent  AuditIntent      `json:"intent"`
	Receipt AuditSinkReceipt `json:"receipt"`
}

// ProviderStepKind names finite calls derived from the accepted service set.
type ProviderStepKind string

const (
	StepCreateNetwork   ProviderStepKind = "create_network"
	StepCreateContainer ProviderStepKind = "create_container"
	StepStartContainer  ProviderStepKind = "start_container"
	StepRemoveContainer ProviderStepKind = "remove_container"
	StepRemoveNetwork   ProviderStepKind = "remove_network"
)

// ProviderStepKey binds a call to the immutable provider operation and resource slot.
type ProviderStepKey struct {
	OperationKey

	ProviderOperationID id.ID            `json:"provider_operation_id"`
	Kind                ProviderStepKind `json:"kind"`
	Service             string           `json:"service"`
}

// ProviderStepPermission is persisted before send; replay never permits another send.
type ProviderStepPermission struct {
	Step          ProviderStepKey `json:"step"`
	CommandID     id.ID           `json:"command_id"`
	Digest        string          `json:"digest"`
	SpecDigest    string          `json:"spec_digest"`
	CreateProfile string          `json:"create_profile"`
	Physical      Physical        `json:"physical"`
	Seal          uint64          `json:"seal"`
	FenceDigest   string          `json:"fence_digest"`
	ResourceID    string          `json:"resource_id"`
	IssuedAt      time.Time       `json:"issued_at"`
	Revision      uint64          `json:"revision"`
}

// ProviderStepGrant separates a newly committed permission from inspect-only recovery.
type ProviderStepGrant struct {
	Permission  ProviderStepPermission `json:"permission"`
	NewlyIssued bool                   `json:"newly_issued"`
}

// ProviderStepAuthorization selects only an accepted finite step under the parent CAS.
type ProviderStepAuthorization struct {
	Command

	Step ProviderStepKey `json:"step"`
}

// ProviderStepResult retains definitive responses or uncertainty for the same permission.
type ProviderStepResult struct {
	Permission ProviderStepPermission `json:"permission"`
	Outcome    PortOutcome            `json:"outcome"`
	ResourceID string                 `json:"resource_id"`
	Evidence   []byte                 `json:"evidence"`
	ObservedAt time.Time              `json:"observed_at"`
	Diagnostic string                 `json:"diagnostic,omitempty"`
}

// ProviderStepRecord is a retained permission with its optional definitive result.
type ProviderStepRecord struct {
	Permission ProviderStepPermission `json:"permission"`
	Result     *ProviderStepResult    `json:"result,omitempty"`
}

// ControllerStore adds same-authority acceptance and persistence to the lifecycle store.
// No method performs a remote call or authenticates a caller on behalf of the host.
type ControllerStore interface {
	Store
	AcceptManagedQuiescence(ctx context.Context, request QuiescenceAcceptanceRequest) (Receipt, error)
	ReadManagedQuiescence(ctx context.Context, request QuiescenceKey) (QuiescenceRecord, error)
	AcceptManagedOriginalRegistration(ctx context.Context, request OriginalRegistrationAcceptanceRequest) (Receipt, error)
	RecordManagedOriginalDrain(ctx context.Context, request OriginalDrainAcceptanceRequest) (Receipt, error)
	ReadManagedOriginalLifecycle(ctx context.Context, request OriginalLifecycleKey) (OriginalLifecycleRecord, error)
	SubmitManagedAction(ctx context.Context, request ActionSubmission) (ActionReceipt, error)
	RecordManagedAccessAudit(ctx context.Context, request AccessAuditRequest) (AccessAuditReceipt, error)
	ReadManagedOperation(ctx context.Context, request OperationKey) (OperationState, error)
	AdvanceManagedOperation(ctx context.Context, request OperationAdvance) (OperationState, error)
	ReadManagedContinuation(ctx context.Context, request ControllerKey) (Continuation, error)
	AdvanceManagedContinuation(ctx context.Context, request ContinuationAdvance) (Continuation, error)
	BindManagedRelease(ctx context.Context, request ReleaseBindingRequest) (ReleaseBinding, error)
	ReadManagedRelease(ctx context.Context, request ReleaseKey) (ReleaseBinding, error)
	ListManagedAuditIntents(ctx context.Context, request AuditPageRequest) (AuditPage, error)
	AcknowledgeManagedAudit(ctx context.Context, request AuditAcknowledgment) error
	AuthorizeManagedProviderStep(ctx context.Context, request ProviderStepAuthorization) (ProviderStepGrant, error)
	RecordManagedProviderStepResult(ctx context.Context, request ProviderStepResult) error
	ReadManagedProviderStep(ctx context.Context, request ProviderStepKey) (ProviderStepRecord, error)
}
