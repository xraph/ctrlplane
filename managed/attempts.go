package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/xraph/ctrlplane/id"
)

// Attempt record limits apply to the complete encoded JSON document.
const (
	MaxAttemptDescriptor         = 16384
	MaxAttemptPointer            = 2048
	MaxAttemptFact               = 8192
	MaxAttemptCommand            = 16384
	MaxAttemptFence              = 49152
	MaxAttemptSettlement         = 16384
	MaxAttemptAssociation        = 1024
	MaxAttemptReceipt            = 2048
	MaxAttemptBindingResponse    = 192 << 10
	MaxAttemptSettlementResponse = 1 << 20
	AttemptBegin                 = "begin"
	AttemptAbort                 = "abort"
	AttemptFinish                = "finish"
	LocalNoDispatchProfile       = "ctrlplane.managed.local-no-dispatch.v1"
)

// AttemptKey selects one immutable removal attempt in its original target.
type AttemptKey struct {
	Key

	AttemptID  id.ID   `json:"attempt_id"`
	Generation Decimal `json:"generation"`
}

// AttemptBindingKey selects one original reservation within an attempt.
type AttemptBindingKey struct {
	AttemptKey

	ReservationID id.ID `json:"reservation_id"`
}

// AttemptCommandKey identifies the one original command of each native kind.
type AttemptCommandKey struct {
	AttemptBindingKey

	Kind string `json:"kind"`
}

// AttemptRootReference anchors the original operation without granting permission.
type AttemptRootReference struct {
	AttemptID        id.ID   `json:"attempt_id"`
	Generation       Decimal `json:"generation"`
	DescriptorDigest string  `json:"descriptor_digest"`
}

// AttemptRef identifies an immutable descriptor.
type AttemptRef = AttemptRootReference

// AttemptBindingReference binds retained native commands to original registration.
type AttemptBindingReference struct {
	ReservationID                   id.ID   `json:"reservation_id"`
	ReservationVersion              Decimal `json:"reservation_version"`
	OriginalRegistrationOperationID id.ID   `json:"original_registration_operation_id"`
	OriginalRegistrationDigest      string  `json:"original_registration_digest"`
	ProcessDigest                   string  `json:"process_digest"`
}

// AttemptRequest carries fresh parent authority for an existing attempt.
type AttemptRequest struct {
	Command

	AttemptID  id.ID   `json:"attempt_id"`
	Generation Decimal `json:"generation"`
}

// AttemptStartRequest reserves both removal identities before the first native call.
type AttemptStartRequest struct {
	Command

	ActionID            id.ID  `json:"action_id"`
	AttemptID           id.ID  `json:"attempt_id"`
	ProviderOperationID id.ID  `json:"provider_operation_id"`
	VerifierID          string `json:"verifier_id"`
}

// AttemptReceipt associates a local transition with permanent removal identities.
type AttemptReceipt struct {
	Receipt             Receipt `json:"receipt"`
	AttemptID           id.ID   `json:"attempt_id"`
	Generation          Decimal `json:"generation"`
	ProviderOperationID id.ID   `json:"provider_operation_id"`
	DescriptorDigest    string  `json:"descriptor_digest"`
}

// AttemptDescriptor freezes the complete original target and resource associations.
type AttemptDescriptor struct {
	SchemaVersion         Decimal     `json:"schema_version"`
	Key                   Key         `json:"key"`
	AttemptID             id.ID       `json:"attempt_id"`
	Generation            Decimal     `json:"generation"`
	Predecessor           *AttemptRef `json:"predecessor,omitempty"`
	ActionID              id.ID       `json:"action_id"`
	ProjectionActionID    id.ID       `json:"projection_action_id"`
	ProviderOperationID   id.ID       `json:"provider_operation_id"`
	IdentityDigest        string      `json:"identity_digest"`
	ManifestDigest        string      `json:"manifest_digest"`
	Seal                  Decimal     `json:"seal"`
	RegistrationSetDigest string      `json:"registration_set_digest"`
	ReservationIDs        []id.ID     `json:"reservation_ids"`
	ResourcesDigest       string      `json:"resources_digest"`
	VerifierID            string      `json:"verifier_id"`
	AcceptedAt            time.Time   `json:"accepted_at"`
}

// AttemptState indexes immutable facts; its phase alone is never authority.
type AttemptState struct {
	BindingSlots     []byte      `json:"binding_slots"`
	ProviderSlots    []byte      `json:"provider_slots"`
	SchemaVersion    Decimal     `json:"schema_version"`
	AttemptRef       AttemptRef  `json:"attempt_ref"`
	Revision         Decimal     `json:"revision"`
	Phase            string      `json:"phase"`
	AdmissionDigest  string      `json:"admission_digest,omitempty"`
	ProviderIssuedAt *time.Time  `json:"provider_issued_at,omitempty"`
	RevocationDigest string      `json:"revocation_digest,omitempty"`
	SettlementDigest string      `json:"settlement_digest,omitempty"`
	TerminalDigest   string      `json:"terminal_digest,omitempty"`
	Successor        *AttemptRef `json:"successor,omitempty"`
	ClosureDigest    string      `json:"closure_digest,omitempty"`
}

// AttemptRecord returns the selected immutable descriptor and bounded state.
type AttemptRecord struct {
	Descriptor AttemptDescriptor `json:"descriptor"`
	State      AttemptState      `json:"state"`
	Receipt    AttemptReceipt    `json:"receipt"`
}

// AttemptCommandRequest supplies exactly the original native request before send.
type AttemptCommandRequest struct {
	AttemptRequest

	ReservationID         id.ID                             `json:"reservation_id"`
	Kind                  string                            `json:"kind"`
	ExpectedNativeVersion Decimal                           `json:"expected_native_version"`
	NativeCommand         PortCommand                       `json:"native_command"`
	CurrentBinding        *AttemptCurrentBindingObservation `json:"current_binding,omitempty"`
}

// AttemptCommandRecord is immutable may-have-issued evidence, including before transport.
type AttemptCommandRecord struct {
	SchemaVersion         Decimal                 `json:"schema_version"`
	Key                   AttemptCommandKey       `json:"key"`
	DescriptorDigest      string                  `json:"descriptor_digest"`
	BindingReference      AttemptBindingReference `json:"binding_reference"`
	ExpectedNativeVersion Decimal                 `json:"expected_native_version"`
	NativeCommand         PortCommand             `json:"native_command"`
	AuthorizationID       id.ID                   `json:"authorization_id"`
	AuthorizationDigest   string                  `json:"authorization_digest"`
	IssuedAt              time.Time               `json:"issued_at"`
	CurrentBindingDigest  string                  `json:"current_binding_digest,omitempty"`
}

// AttemptCommandGrant permits only a newly accepted original native invocation.
type AttemptCommandGrant struct {
	Receipt     AttemptReceipt       `json:"receipt"`
	Command     AttemptCommandRecord `json:"command"`
	NewlyIssued bool                 `json:"newly_issued"`
}

// AttemptRecoveryRequest requests fresh authority for an already retained command.
type AttemptRecoveryRequest struct {
	AttemptRequest

	ReservationID id.ID  `json:"reservation_id"`
	Kind          string `json:"kind"`
	CommandDigest string `json:"command_digest"`
}

// AttemptRecoveryGrant cannot authorize a provider call or another native identity.
type AttemptRecoveryGrant struct {
	Receipt         AttemptReceipt       `json:"receipt"`
	Command         AttemptCommandRecord `json:"command"`
	NewlyAuthorized bool                 `json:"newly_authorized"`
}

// AttemptNativeFacts keeps native request, command and binding version domains distinct.
type AttemptNativeFacts struct {
	RequestID       string    `json:"request_id"`
	RequestDigest   string    `json:"request_digest"`
	CommandDigest   string    `json:"command_digest"`
	ResponseVersion Decimal   `json:"response_version"`
	BindingVersion  Decimal   `json:"binding_version"`
	RemovalEpoch    Decimal   `json:"removal_epoch"`
	AcceptedAt      time.Time `json:"accepted_at"`
}

// AttemptNativeAcceptance contains bounded producer facts, never a copied native schema.
type AttemptNativeAcceptance struct {
	NativeAcceptance     AttemptNativeFacts `json:"native_acceptance"`
	NativeFenceDigest    string             `json:"native_fence_digest"`
	ReceiptID            string             `json:"receipt_id"`
	Receipt              []byte             `json:"receipt"`
	ReceiptDigest        string             `json:"receipt_digest"`
	OriginalBindingProof []byte             `json:"original_binding_proof"`
	ProofDigest          string             `json:"proof_digest"`
}

// AttemptFenceRecord retains an accepted original Begin even after fence expiry.
//
//nolint:embeddedstructfieldcheck // Preserve the accepted attempt field order.
type AttemptFenceRecord struct {
	LocalOperationID id.ID             `json:"local_operation_id"`
	SchemaVersion    Decimal           `json:"schema_version"`
	Key              AttemptBindingKey `json:"key"`
	DescriptorDigest string            `json:"descriptor_digest"`
	CommandDigest    string            `json:"command_digest"`
	AttemptNativeAcceptance
	Fence WireFence `json:"fence"`
}

// AttemptFenceRequest records an authenticated original native acceptance.
type AttemptFenceRequest struct {
	AttemptRequest

	ReservationID id.ID                   `json:"reservation_id"`
	Acceptance    AttemptNativeAcceptance `json:"acceptance"`
	Fence         WireFence               `json:"fence"`
}

// AttemptAdmissionRequest selects the descriptor's configured verifier.
type AttemptAdmissionRequest struct {
	AttemptRequest

	VerifierID string `json:"verifier_id"`
}

// AttemptAdmission retains the original complete fence-set acceptance.
type AttemptAdmission struct {
	LocalOperationID      id.ID      `json:"local_operation_id"`
	SchemaVersion         Decimal    `json:"schema_version"`
	AttemptRef            AttemptRef `json:"attempt_ref"`
	ProviderOperationID   id.ID      `json:"provider_operation_id"`
	FenceSetDigest        string     `json:"fence_set_digest"`
	RegistrationSetDigest string     `json:"registration_set_digest"`
	ResourcesDigest       string     `json:"resources_digest"`
	VerifierID            string     `json:"verifier_id"`
	AcceptedAt            time.Time  `json:"accepted_at"`
	ParentRevision        Decimal    `json:"parent_revision"`
}

// AttemptRevocation is a permanent cutoff of every remaining finite issue slot.
type AttemptRevocation struct {
	LocalOperationID    id.ID       `json:"local_operation_id"`
	SchemaVersion       Decimal     `json:"schema_version"`
	AttemptRef          AttemptRef  `json:"attempt_ref"`
	ProviderOperationID id.ID       `json:"provider_operation_id"`
	CoverageDigest      string      `json:"coverage_digest"`
	Disposition         Disposition `json:"disposition"`
	SettlementDigest    string      `json:"settlement_digest,omitempty"`
	RevokedAt           time.Time   `json:"revoked_at"`
	ParentRevision      Decimal     `json:"parent_revision"`
}

// AttemptSettlement records whole-operation disposition after complete slot coverage.
type AttemptSettlement struct {
	LocalOperationID    id.ID       `json:"local_operation_id"`
	SchemaVersion       Decimal     `json:"schema_version"`
	AttemptRef          AttemptRef  `json:"attempt_ref"`
	ProviderOperationID id.ID       `json:"provider_operation_id"`
	ResourcesDigest     string      `json:"resources_digest"`
	AdmissionDigest     string      `json:"admission_digest,omitempty"`
	RevocationDigest    string      `json:"revocation_digest,omitempty"`
	Disposition         Disposition `json:"disposition"`
	StepReferences      []string    `json:"step_references"`
	CoverageDigest      string      `json:"coverage_digest"`
	ProducerProfile     string      `json:"producer_profile"`
	VerifierID          string      `json:"verifier_id"`
	Evidence            []byte      `json:"evidence"`
	EvidenceDigest      string      `json:"evidence_digest"`
	SettledAt           time.Time   `json:"settled_at"`
}

// AttemptSettlementRequest supplies a qualified producer fact, not caller-selected coverage.
type AttemptSettlementRequest struct {
	AttemptRequest

	Disposition     Disposition `json:"disposition"`
	ProducerProfile string      `json:"producer_profile"`
	VerifierID      string      `json:"verifier_id"`
	Evidence        []byte      `json:"evidence"`
	SettledAt       time.Time   `json:"settled_at"`
}

// AttemptBindingSettlement is a fresh exact-binding witness of continuing settlement.
type AttemptBindingSettlement struct {
	LocalOperationID  id.ID             `json:"local_operation_id"`
	SchemaVersion     Decimal           `json:"schema_version"`
	Key               AttemptBindingKey `json:"key"`
	DescriptorDigest  string            `json:"descriptor_digest"`
	FenceFactDigest   string            `json:"fence_fact_digest"`
	NativeFenceDigest string            `json:"native_fence_digest"`
	RevocationDigest  string            `json:"revocation_digest"`
	SettlementDigest  string            `json:"settlement_digest,omitempty"`
	CoverageDigest    string            `json:"coverage_digest"`
	Disposition       Disposition       `json:"disposition"`
	WitnessedAt       time.Time         `json:"witnessed_at"`
	ParentRevision    Decimal           `json:"parent_revision"`
}

// AttemptBindingSettlementRequest cannot supply or rewrite the witness time.
type AttemptBindingSettlementRequest struct {
	AttemptRequest

	ReservationID   id.ID  `json:"reservation_id"`
	FenceFactDigest string `json:"fence_fact_digest"`
}

// AttemptTerminalRecord retains one exact accepted Abort or Finish command.
//
//nolint:embeddedstructfieldcheck // Preserve the accepted attempt field order.
type AttemptTerminalRecord struct {
	LocalOperationID id.ID             `json:"local_operation_id"`
	SchemaVersion    Decimal           `json:"schema_version"`
	Key              AttemptBindingKey `json:"key"`
	DescriptorDigest string            `json:"descriptor_digest"`
	CommandDigest    string            `json:"command_digest"`
	FenceFactDigest  string            `json:"fence_fact_digest"`
	AttemptNativeAcceptance
	ProofVerifiedAt         time.Time `json:"proof_verified_at"`
	ProofValidUntil         time.Time `json:"proof_valid_until"`
	SettlementWitnessDigest string    `json:"settlement_witness_digest"`
}

// AttemptAbortRequest records an original Abort receipt and separately fresh binding proof.
type AttemptAbortRequest struct {
	AttemptRequest

	ReservationID   id.ID                   `json:"reservation_id"`
	Acceptance      AttemptNativeAcceptance `json:"acceptance"`
	ProofVerifiedAt time.Time               `json:"proof_verified_at"`
	ProofValidUntil time.Time               `json:"proof_valid_until"`
}

// AttemptFinishRequest records a completed original Finish without renewing an old fence.
type AttemptFinishRequest struct {
	AttemptRequest

	ReservationID id.ID                   `json:"reservation_id"`
	Acceptance    AttemptNativeAcceptance `json:"acceptance"`
}

// AttemptBindingRecord returns one complete exact binding, never a page of obligations.
type AttemptBindingRecord struct {
	Key              AttemptBindingKey                 `json:"key"`
	DescriptorDigest string                            `json:"descriptor_digest"`
	Commands         []AttemptCommandRecord            `json:"commands"`
	Fence            *AttemptFenceRecord               `json:"fence,omitempty"`
	Abort            *AttemptTerminalRecord            `json:"abort,omitempty"`
	Finish           *AttemptTerminalRecord            `json:"finish,omitempty"`
	Witness          *AttemptBindingSettlement         `json:"witness,omitempty"`
	CurrentBinding   *AttemptCurrentBindingObservation `json:"current_binding,omitempty"`
}

// AttemptSettlementReadRequest selects a native binding independently of aggregate A fences.
type AttemptSettlementReadRequest struct {
	AttemptBindingKey

	ProviderOperationID id.ID  `json:"provider_operation_id"`
	NativeFenceDigest   string `json:"native_fence_digest"`
}

// AttemptHistoricalSettlement supports partial, unadmitted original attempts.
type AttemptHistoricalSettlement struct {
	Request         AttemptSettlementReadRequest `json:"request"`
	AttemptRef      AttemptRef                   `json:"attempt_ref"`
	Admission       *AttemptAdmission            `json:"admission,omitempty"`
	Revocation      *AttemptRevocation           `json:"revocation,omitempty"`
	Settlement      *AttemptSettlement           `json:"settlement,omitempty"`
	Witness         AttemptBindingSettlement     `json:"witness"`
	StepReferences  []string                     `json:"step_references"`
	ResourcesDigest string                       `json:"resources_digest"`
}

// ProviderStepFailure proves only that this exact invocation never reached its transport.
type ProviderStepFailure struct {
	SchemaVersion   Decimal                `json:"schema_version"`
	Permission      ProviderStepPermission `json:"permission"`
	AttemptRef      AttemptRef             `json:"attempt_ref"`
	InvocationID    id.ID                  `json:"invocation_id"`
	ProducerProfile string                 `json:"producer_profile"`
	VerifierID      string                 `json:"verifier_id"`
	Class           string                 `json:"class"`
	Phase           string                 `json:"phase"`
	Reason          string                 `json:"reason"`
	ObservedAt      time.Time              `json:"observed_at"`
	Witness         []byte                 `json:"witness"`
	WitnessDigest   string                 `json:"witness_digest"`
}

// ProviderStepFailureAuthority is required by a qualified unsuccessful-step producer.
type ProviderStepFailureAuthority interface {
	RecordManagedProviderStepFailure(ctx context.Context, request ProviderStepFailure) error
	ReadManagedProviderStepFailure(ctx context.Context, request ProviderStepKey) (ProviderStepFailure, error)
}

// AttemptRecoveryInvocation binds producer recovery to an exact stored command and grant.
type AttemptRecoveryInvocation struct {
	Grant AttemptRecoveryGrant `json:"grant"`
}

// OriginalRemovalRecoveryPort invokes only the same authenticated native idempotent request.
type OriginalRemovalRecoveryPort interface {
	RecoverOriginalRemoval(ctx context.Context, request AttemptRecoveryInvocation) (AttemptNativeAcceptance, error)
}

// RemovalAttemptStore is the optional complete generation-one removal capability.
type RemovalAttemptStore interface {
	BeginManagedRemovalAttempt(ctx context.Context, request AttemptStartRequest) (AttemptReceipt, error)
	AuthorizeManagedAttemptCommand(ctx context.Context, request AttemptCommandRequest) (AttemptCommandGrant, error)
	AuthorizeManagedAttemptRecovery(ctx context.Context, request AttemptRecoveryRequest) (AttemptRecoveryGrant, error)
	RecordManagedAttemptFence(ctx context.Context, request AttemptFenceRequest) (Receipt, error)
	AdmitManagedRemovalAttempt(ctx context.Context, request AttemptAdmissionRequest) (Receipt, error)
	RevokeManagedRemovalAttempt(ctx context.Context, request AttemptRequest) (Receipt, error)
	SettleManagedRemovalAttempt(ctx context.Context, request AttemptSettlementRequest) (Receipt, error)
	RecordManagedAttemptBindingSettlement(ctx context.Context, request AttemptBindingSettlementRequest) (Receipt, error)
	RecordManagedAttemptAbort(ctx context.Context, request AttemptAbortRequest) (Receipt, error)
	RecordManagedAttemptFinish(ctx context.Context, request AttemptFinishRequest) (Receipt, error)
	TombstoneManagedRemovalAttempt(ctx context.Context, request AttemptRequest) (Receipt, error)
	ReadManagedRemovalAttempt(ctx context.Context, request AttemptKey) (AttemptRecord, error)
	ReadManagedAttemptCommand(ctx context.Context, request AttemptCommandKey) (AttemptCommandRecord, error)
	ReadManagedAttemptBinding(ctx context.Context, request AttemptBindingKey) (AttemptBindingRecord, error)
	ReadManagedAttemptHistoricalSettlement(ctx context.Context, request AttemptSettlementReadRequest) (AttemptHistoricalSettlement, error)
}

// New attempt wire envelopes preserve signed counters as canonical decimal strings.
func attemptDecodeJSON(raw []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	if err := dec.Decode(out); err != nil {
		return ErrInvalid
	}

	return nil
}
func attemptParentCounters(revision, epoch Decimal) (uint64, uint64, error) {
	r, e1 := revision.Uint64()

	e, e2 := epoch.Uint64()
	if e1 != nil || e2 != nil || r == 0 || r > math.MaxInt64 || e > math.MaxInt64 {
		return 0, 0, ErrInvalid
	}

	return r, e, nil
}

type attemptParentWire struct {
	Key

	ID         id.ID   `json:"id"`
	Revision   Decimal `json:"revision"`
	LeaseEpoch Decimal `json:"lease_epoch"`
	Owner      string  `json:"owner"`
}
type attemptRequestWire struct {
	attemptParentWire

	AttemptID  id.ID   `json:"attempt_id"`
	Generation Decimal `json:"generation"`
}

func parentAttemptWire(c Command) attemptParentWire {
	return attemptParentWire{c.Key, c.ID, Decimal(strconv.FormatUint(c.Revision, 10)), Decimal(strconv.FormatUint(c.LeaseEpoch, 10)), c.Owner}
}
func (w attemptParentWire) local() (Command, error) {
	r, e, err := attemptParentCounters(w.Revision, w.LeaseEpoch)

	return Command{Key: w.Key, ID: w.ID, Revision: r, LeaseEpoch: e, Owner: w.Owner}, err
}

// MarshalJSON projects AttemptRequest parent counters into canonical decimal strings.
func (v AttemptRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{ attemptRequestWire }{attemptRequestWire{parentAttemptWire(v.Command), v.AttemptID, v.Generation}})
}

// UnmarshalJSON validates the exact accepted AttemptRequest encoding.
func (v *AttemptRequest) UnmarshalJSON(raw []byte) error {
	var wire struct{ attemptRequestWire }
	if err := attemptDecodeJSON(raw, &wire); err != nil {
		return err
	}

	command, err := wire.local()
	if err != nil {
		return err
	}

	*v = AttemptRequest{Command: command, AttemptID: wire.AttemptID, Generation: wire.Generation}

	canonical, err := v.MarshalJSON()
	if err != nil || !bytes.Equal(canonical, raw) {
		return ErrInvalid
	}

	return nil
}

// MarshalJSON projects AttemptStartRequest parent counters into canonical decimal strings.
func (v AttemptStartRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		attemptParentWire

		ActionID            id.ID  `json:"action_id"`
		AttemptID           id.ID  `json:"attempt_id"`
		ProviderOperationID id.ID  `json:"provider_operation_id"`
		VerifierID          string `json:"verifier_id"`
	}{parentAttemptWire(v.Command), v.ActionID, v.AttemptID, v.ProviderOperationID, v.VerifierID})
}

// UnmarshalJSON validates the exact accepted AttemptStartRequest encoding.
func (v *AttemptStartRequest) UnmarshalJSON(raw []byte) error {
	var wire struct {
		attemptParentWire

		ActionID            id.ID  `json:"action_id"`
		AttemptID           id.ID  `json:"attempt_id"`
		ProviderOperationID id.ID  `json:"provider_operation_id"`
		VerifierID          string `json:"verifier_id"`
	}
	if err := attemptDecodeJSON(raw, &wire); err != nil {
		return err
	}

	command, err := wire.local()
	if err != nil {
		return err
	}

	*v = AttemptStartRequest{Command: command, ActionID: wire.ActionID, AttemptID: wire.AttemptID, ProviderOperationID: wire.ProviderOperationID, VerifierID: wire.VerifierID}

	canonical, err := v.MarshalJSON()
	if err != nil || !bytes.Equal(canonical, raw) {
		return ErrInvalid
	}

	return nil
}

// MarshalJSON projects AttemptCommandRequest parent counters into canonical decimal strings.
func (v AttemptCommandRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		attemptRequestWire

		ReservationID         id.ID                             `json:"reservation_id"`
		Kind                  string                            `json:"kind"`
		ExpectedNativeVersion Decimal                           `json:"expected_native_version"`
		NativeCommand         PortCommand                       `json:"native_command"`
		CurrentBinding        *AttemptCurrentBindingObservation `json:"current_binding,omitempty"`
	}{attemptRequestWire{parentAttemptWire(v.Command), v.AttemptID, v.Generation}, v.ReservationID, v.Kind, v.ExpectedNativeVersion, v.NativeCommand, v.CurrentBinding})
}

// UnmarshalJSON validates the exact accepted AttemptCommandRequest encoding.
func (v *AttemptCommandRequest) UnmarshalJSON(raw []byte) error {
	var wire struct {
		attemptRequestWire

		ReservationID         id.ID                             `json:"reservation_id"`
		Kind                  string                            `json:"kind"`
		ExpectedNativeVersion Decimal                           `json:"expected_native_version"`
		NativeCommand         PortCommand                       `json:"native_command"`
		CurrentBinding        *AttemptCurrentBindingObservation `json:"current_binding,omitempty"`
	}
	if err := attemptDecodeJSON(raw, &wire); err != nil {
		return err
	}

	command, err := wire.local()
	if err != nil {
		return err
	}

	*v = AttemptCommandRequest{AttemptRequest: AttemptRequest{Command: command, AttemptID: wire.AttemptID, Generation: wire.Generation}, ReservationID: wire.ReservationID, Kind: wire.Kind, ExpectedNativeVersion: wire.ExpectedNativeVersion, NativeCommand: wire.NativeCommand, CurrentBinding: wire.CurrentBinding}

	canonical, err := v.MarshalJSON()
	if err != nil || !bytes.Equal(canonical, raw) {
		return ErrInvalid
	}

	return nil
}

// MarshalJSON projects AttemptRecoveryRequest parent counters into canonical decimal strings.
func (v AttemptRecoveryRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		attemptRequestWire

		ReservationID id.ID  `json:"reservation_id"`
		Kind          string `json:"kind"`
		CommandDigest string `json:"command_digest"`
	}{attemptRequestWire{parentAttemptWire(v.Command), v.AttemptID, v.Generation}, v.ReservationID, v.Kind, v.CommandDigest})
}

// UnmarshalJSON validates the exact accepted AttemptRecoveryRequest encoding.
func (v *AttemptRecoveryRequest) UnmarshalJSON(raw []byte) error {
	var wire struct {
		attemptRequestWire

		ReservationID id.ID  `json:"reservation_id"`
		Kind          string `json:"kind"`
		CommandDigest string `json:"command_digest"`
	}
	if err := attemptDecodeJSON(raw, &wire); err != nil {
		return err
	}

	command, err := wire.local()
	if err != nil {
		return err
	}

	*v = AttemptRecoveryRequest{AttemptRequest: AttemptRequest{Command: command, AttemptID: wire.AttemptID, Generation: wire.Generation}, ReservationID: wire.ReservationID, Kind: wire.Kind, CommandDigest: wire.CommandDigest}

	canonical, err := v.MarshalJSON()
	if err != nil || !bytes.Equal(canonical, raw) {
		return ErrInvalid
	}

	return nil
}

// MarshalJSON projects AttemptFenceRequest parent counters into canonical decimal strings.
func (v AttemptFenceRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		attemptRequestWire

		ReservationID id.ID                   `json:"reservation_id"`
		Acceptance    AttemptNativeAcceptance `json:"acceptance"`
		Fence         WireFence               `json:"fence"`
	}{attemptRequestWire{parentAttemptWire(v.Command), v.AttemptID, v.Generation}, v.ReservationID, v.Acceptance, v.Fence})
}

// UnmarshalJSON validates the exact accepted AttemptFenceRequest encoding.
func (v *AttemptFenceRequest) UnmarshalJSON(raw []byte) error {
	var wire struct {
		attemptRequestWire

		ReservationID id.ID                   `json:"reservation_id"`
		Acceptance    AttemptNativeAcceptance `json:"acceptance"`
		Fence         WireFence               `json:"fence"`
	}
	if err := attemptDecodeJSON(raw, &wire); err != nil {
		return err
	}

	command, err := wire.local()
	if err != nil {
		return err
	}

	*v = AttemptFenceRequest{AttemptRequest: AttemptRequest{Command: command, AttemptID: wire.AttemptID, Generation: wire.Generation}, ReservationID: wire.ReservationID, Acceptance: wire.Acceptance, Fence: wire.Fence}

	canonical, err := v.MarshalJSON()
	if err != nil || !bytes.Equal(canonical, raw) {
		return ErrInvalid
	}

	return nil
}

// MarshalJSON projects AttemptAdmissionRequest parent counters into canonical decimal strings.
func (v AttemptAdmissionRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		attemptRequestWire

		VerifierID string `json:"verifier_id"`
	}{attemptRequestWire{parentAttemptWire(v.Command), v.AttemptID, v.Generation}, v.VerifierID})
}

// UnmarshalJSON validates the exact accepted AttemptAdmissionRequest encoding.
func (v *AttemptAdmissionRequest) UnmarshalJSON(raw []byte) error {
	var wire struct {
		attemptRequestWire

		VerifierID string `json:"verifier_id"`
	}
	if err := attemptDecodeJSON(raw, &wire); err != nil {
		return err
	}

	command, err := wire.local()
	if err != nil {
		return err
	}

	*v = AttemptAdmissionRequest{AttemptRequest: AttemptRequest{Command: command, AttemptID: wire.AttemptID, Generation: wire.Generation}, VerifierID: wire.VerifierID}

	canonical, err := v.MarshalJSON()
	if err != nil || !bytes.Equal(canonical, raw) {
		return ErrInvalid
	}

	return nil
}

// MarshalJSON projects AttemptSettlementRequest parent counters into canonical decimal strings.
func (v AttemptSettlementRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		attemptRequestWire

		Disposition     Disposition `json:"disposition"`
		ProducerProfile string      `json:"producer_profile"`
		VerifierID      string      `json:"verifier_id"`
		Evidence        []byte      `json:"evidence"`
		SettledAt       time.Time   `json:"settled_at"`
	}{attemptRequestWire{parentAttemptWire(v.Command), v.AttemptID, v.Generation}, v.Disposition, v.ProducerProfile, v.VerifierID, v.Evidence, v.SettledAt})
}

// UnmarshalJSON validates the exact accepted AttemptSettlementRequest encoding.
func (v *AttemptSettlementRequest) UnmarshalJSON(raw []byte) error {
	var wire struct {
		attemptRequestWire

		Disposition     Disposition `json:"disposition"`
		ProducerProfile string      `json:"producer_profile"`
		VerifierID      string      `json:"verifier_id"`
		Evidence        []byte      `json:"evidence"`
		SettledAt       time.Time   `json:"settled_at"`
	}
	if err := attemptDecodeJSON(raw, &wire); err != nil {
		return err
	}

	command, err := wire.local()
	if err != nil {
		return err
	}

	*v = AttemptSettlementRequest{AttemptRequest: AttemptRequest{Command: command, AttemptID: wire.AttemptID, Generation: wire.Generation}, Disposition: wire.Disposition, ProducerProfile: wire.ProducerProfile, VerifierID: wire.VerifierID, Evidence: wire.Evidence, SettledAt: wire.SettledAt}

	canonical, err := v.MarshalJSON()
	if err != nil || !bytes.Equal(canonical, raw) {
		return ErrInvalid
	}

	return nil
}

// MarshalJSON projects AttemptBindingSettlementRequest parent counters into canonical decimal strings.
func (v AttemptBindingSettlementRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		attemptRequestWire

		ReservationID   id.ID  `json:"reservation_id"`
		FenceFactDigest string `json:"fence_fact_digest"`
	}{attemptRequestWire{parentAttemptWire(v.Command), v.AttemptID, v.Generation}, v.ReservationID, v.FenceFactDigest})
}

// UnmarshalJSON validates the exact accepted AttemptBindingSettlementRequest encoding.
func (v *AttemptBindingSettlementRequest) UnmarshalJSON(raw []byte) error {
	var wire struct {
		attemptRequestWire

		ReservationID   id.ID  `json:"reservation_id"`
		FenceFactDigest string `json:"fence_fact_digest"`
	}
	if err := attemptDecodeJSON(raw, &wire); err != nil {
		return err
	}

	command, err := wire.local()
	if err != nil {
		return err
	}

	*v = AttemptBindingSettlementRequest{AttemptRequest: AttemptRequest{Command: command, AttemptID: wire.AttemptID, Generation: wire.Generation}, ReservationID: wire.ReservationID, FenceFactDigest: wire.FenceFactDigest}

	canonical, err := v.MarshalJSON()
	if err != nil || !bytes.Equal(canonical, raw) {
		return ErrInvalid
	}

	return nil
}

// MarshalJSON projects AttemptAbortRequest parent counters into canonical decimal strings.
func (v AttemptAbortRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		attemptRequestWire

		ReservationID   id.ID                   `json:"reservation_id"`
		Acceptance      AttemptNativeAcceptance `json:"acceptance"`
		ProofVerifiedAt time.Time               `json:"proof_verified_at"`
		ProofValidUntil time.Time               `json:"proof_valid_until"`
	}{attemptRequestWire{parentAttemptWire(v.Command), v.AttemptID, v.Generation}, v.ReservationID, v.Acceptance, v.ProofVerifiedAt, v.ProofValidUntil})
}

// UnmarshalJSON validates the exact accepted AttemptAbortRequest encoding.
func (v *AttemptAbortRequest) UnmarshalJSON(raw []byte) error {
	var wire struct {
		attemptRequestWire

		ReservationID   id.ID                   `json:"reservation_id"`
		Acceptance      AttemptNativeAcceptance `json:"acceptance"`
		ProofVerifiedAt time.Time               `json:"proof_verified_at"`
		ProofValidUntil time.Time               `json:"proof_valid_until"`
	}
	if err := attemptDecodeJSON(raw, &wire); err != nil {
		return err
	}

	command, err := wire.local()
	if err != nil {
		return err
	}

	*v = AttemptAbortRequest{AttemptRequest: AttemptRequest{Command: command, AttemptID: wire.AttemptID, Generation: wire.Generation}, ReservationID: wire.ReservationID, Acceptance: wire.Acceptance, ProofVerifiedAt: wire.ProofVerifiedAt, ProofValidUntil: wire.ProofValidUntil}

	canonical, err := v.MarshalJSON()
	if err != nil || !bytes.Equal(canonical, raw) {
		return ErrInvalid
	}

	return nil
}

// MarshalJSON projects AttemptFinishRequest parent counters into canonical decimal strings.
func (v AttemptFinishRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		attemptRequestWire

		ReservationID id.ID                   `json:"reservation_id"`
		Acceptance    AttemptNativeAcceptance `json:"acceptance"`
	}{attemptRequestWire{parentAttemptWire(v.Command), v.AttemptID, v.Generation}, v.ReservationID, v.Acceptance})
}

// UnmarshalJSON validates the exact accepted AttemptFinishRequest encoding.
func (v *AttemptFinishRequest) UnmarshalJSON(raw []byte) error {
	var wire struct {
		attemptRequestWire

		ReservationID id.ID                   `json:"reservation_id"`
		Acceptance    AttemptNativeAcceptance `json:"acceptance"`
	}
	if err := attemptDecodeJSON(raw, &wire); err != nil {
		return err
	}

	command, err := wire.local()
	if err != nil {
		return err
	}

	*v = AttemptFinishRequest{AttemptRequest: AttemptRequest{Command: command, AttemptID: wire.AttemptID, Generation: wire.Generation}, ReservationID: wire.ReservationID, Acceptance: wire.Acceptance}

	canonical, err := v.MarshalJSON()
	if err != nil || !bytes.Equal(canonical, raw) {
		return ErrInvalid
	}

	return nil
}

// MarshalJSON preserves the existing receipt through an explicit decimal wire projection.
func (v AttemptReceipt) MarshalJSON() ([]byte, error) {
	type plain AttemptReceipt

	return json.Marshal(struct {
		*plain

		Receipt WireReceipt `json:"receipt"`
	}{(*plain)(&v), ReceiptWire(v.Receipt)})
}

// UnmarshalJSON requires canonical nested receipt counters.
func (v *AttemptReceipt) UnmarshalJSON(raw []byte) error {
	type plain AttemptReceipt

	var value plain

	wire := struct {
		*plain

		Receipt WireReceipt `json:"receipt"`
	}{plain: &value}
	if err := attemptDecodeJSON(raw, &wire); err != nil {
		return err
	}

	receipt, err := wire.Receipt.Value()
	if err != nil {
		return err
	}

	value.Receipt = receipt
	*v = AttemptReceipt(value)

	canonical, err := v.MarshalJSON()
	if err != nil || !bytes.Equal(canonical, raw) {
		return ErrInvalid
	}

	return nil
}

// MarshalJSON preserves positive permission semantics in a new decimal failure envelope.
func (v ProviderStepFailure) MarshalJSON() ([]byte, error) {
	type plain ProviderStepFailure

	return json.Marshal(struct {
		*plain

		Permission WireProviderStepPermission `json:"permission"`
	}{(*plain)(&v), ProviderPermissionWire(v.Permission)})
}

// UnmarshalJSON refuses noncanonical or changed permission identity.
func (v *ProviderStepFailure) UnmarshalJSON(raw []byte) error {
	type plain ProviderStepFailure

	var value plain

	wire := struct {
		*plain

		Permission WireProviderStepPermission `json:"permission"`
	}{plain: &value}
	if err := attemptDecodeJSON(raw, &wire); err != nil {
		return err
	}

	permission, err := wire.Permission.Value()
	if err != nil {
		return err
	}

	value.Permission = permission
	*v = ProviderStepFailure(value)

	canonical, err := v.MarshalJSON()
	if err != nil || !bytes.Equal(canonical, raw) {
		return ErrInvalid
	}

	return nil
}

// Removal continuation limits bound complete encoded records.
const (
	MaxRemovalContinuationMaterial    = 16384
	MaxRemovalContinuationClosure     = 8192
	MaxRemovalContinuationAssociation = 1024
	MaxRemovalContinuationReceipt     = 2048
	MaxRemovalContinuationRecord      = 32768
	MaxRemovalContinuationRequest     = 16384
	MaxSuccessorCommandRequest        = 32768
	MaxAttemptCurrentBinding          = 8192
)

// ContinuationKey selects an immutable typed acceptance.
type ContinuationKey struct {
	Key      Key   `json:"key"`
	ActionID id.ID `json:"action_id"`
}

// ContinuationRequest closes one settled predecessor without granting a send.
type ContinuationRequest struct {
	Action              ActionSubmission `json:"action"`
	Predecessor         AttemptRef       `json:"predecessor"`
	SuccessorAttemptID  id.ID            `json:"successor_attempt_id"`
	ProviderOperationID id.ID            `json:"provider_operation_id"`
}

// ContinuationReceipt retains the original acceptance, never current authority.
type ContinuationReceipt struct {
	Receipt             Receipt       `json:"receipt"`
	ActionReceipt       ActionReceipt `json:"action_receipt"`
	Successor           AttemptRef    `json:"successor"`
	ProviderOperationID id.ID         `json:"provider_operation_id"`
}

// ContinuationAssociation binds a journal to its typed acceptance.
type ContinuationAssociation struct {
	MaterialID          id.ID      `json:"material_id"`
	MaterialDigest      string     `json:"material_digest"`
	Predecessor         AttemptRef `json:"predecessor"`
	Successor           AttemptRef `json:"successor"`
	ProviderOperationID id.ID      `json:"provider_operation_id"`
}

// ContinuationRecord returns immutable historical acceptance material.
type ContinuationRecord struct {
	Material ContinuationMaterial `json:"material"`
	Closure  ContinuationClosure  `json:"closure"`
	Receipt  ContinuationReceipt  `json:"receipt"`
}

// ContinuationMaterial retains the exact request and independently derived generation.
type ContinuationMaterial struct {
	SchemaVersion       Decimal             `json:"schema_version"`
	Request             ContinuationRequest `json:"request"`
	SuccessorGeneration Decimal             `json:"successor_generation"`
	Digest              string              `json:"digest"`
}

// ContinuationLineage retains the fixed root and first accepted link.
type ContinuationLineage struct {
	SchemaVersion          Decimal      `json:"schema_version"`
	Root                   AttemptRef   `json:"root"`
	RootInitialOperationID id.ID        `json:"root_initial_operation_id"`
	OriginalOperationKey   OperationKey `json:"original_operation_key"`
	FirstMaterialID        id.ID        `json:"first_material_id"`
	FirstMaterialDigest    string       `json:"first_material_digest"`
	FirstSuccessor         AttemptRef   `json:"first_successor"`
}

// ContinuationClosure commits a definitive nondeletion predecessor transition.
type ContinuationClosure struct {
	SchemaVersion       Decimal             `json:"schema_version"`
	Key                 Key                 `json:"key"`
	ActionID            id.ID               `json:"action_id"`
	MaterialDigest      string              `json:"material_digest"`
	Predecessor         AttemptRef          `json:"predecessor"`
	Successor           AttemptRef          `json:"successor"`
	ProviderOperationID id.ID               `json:"provider_operation_id"`
	Lineage             ContinuationLineage `json:"lineage"`
	Disposition         Disposition         `json:"disposition"`
	RevocationDigest    string              `json:"revocation_digest"`
	SettlementDigest    string              `json:"settlement_digest"`
	CoverageDigest      string              `json:"coverage_digest"`
	PredecessorRevision Decimal             `json:"predecessor_revision"`
	PointerRevision     Decimal             `json:"pointer_revision"`
	ParentRevision      Decimal             `json:"parent_revision"`
	AcceptedAt          time.Time           `json:"accepted_at"`
}

// RemovalContinuationStore provides typed acceptance and exact historical reads.
type RemovalContinuationStore interface {
	BeginManagedRemovalContinuation(ctx context.Context, request ContinuationRequest) (ContinuationReceipt, error)
	ReadManagedRemovalContinuation(ctx context.Context, key ContinuationKey) (ContinuationRecord, error)
}

// AttemptCurrentBindingObservation is supplied by the trusted native producer.
// Store validation binds supplied facts; it does not authenticate live native provenance.
type AttemptCurrentBindingObservation struct {
	SchemaVersion    Decimal                 `json:"schema_version"`
	Key              AttemptBindingKey       `json:"key"`
	DescriptorDigest string                  `json:"descriptor_digest"`
	BindingReference AttemptBindingReference `json:"binding_reference"`
	BindingVersion   Decimal                 `json:"binding_version"`
	RemovalEpoch     Decimal                 `json:"removal_epoch"`
	State            string                  `json:"state"`
	ObservedAt       time.Time               `json:"observed_at"`
	ValidUntil       time.Time               `json:"valid_until"`
	VerifierID       string                  `json:"verifier_id"`
	Evidence         []byte                  `json:"evidence"`
	EvidenceDigest   string                  `json:"evidence_digest"`
}

type continuationActionWire struct {
	Key                     Key        `json:"key"`
	ID                      id.ID      `json:"id"`
	Revision                Decimal    `json:"revision"`
	LeaseEpoch              Decimal    `json:"lease_epoch"`
	Owner                   string     `json:"owner"`
	Kind                    ActionKind `json:"kind"`
	Installation            string     `json:"installation"`
	Authority               string     `json:"authority"`
	ApplicationID           string     `json:"application_id"`
	OrganizationID          string     `json:"organization_id"`
	Actor                   AuditActor `json:"actor"`
	OriginalCommand         []byte     `json:"original_command"`
	OriginalDigest          string     `json:"original_digest"`
	CanonicalDigest         string     `json:"canonical_digest"`
	ExpectedDesiredRevision Decimal    `json:"expected_desired_revision"`
	OccurredAt              time.Time  `json:"occurred_at"`
}

func continuationAction(q ActionSubmission) continuationActionWire {
	return continuationActionWire{q.Key, q.ID, Decimal(strconv.FormatUint(q.Revision, 10)), Decimal(strconv.FormatUint(q.LeaseEpoch, 10)), q.Owner, q.Kind, q.Installation, q.Authority, q.ApplicationID, q.OrganizationID, q.Actor, q.OriginalCommand, q.OriginalDigest, q.CanonicalDigest, Decimal(strconv.FormatUint(q.ExpectedDesiredRevision, 10)), q.OccurredAt.UTC()}
}

func (q continuationActionWire) value() (ActionSubmission, error) {
	rev, e := q.Revision.Uint64()
	if e != nil {
		return ActionSubmission{}, e
	}

	epoch, e := q.LeaseEpoch.Uint64()
	if e != nil {
		return ActionSubmission{}, e
	}

	desired, e := q.ExpectedDesiredRevision.Uint64()
	if e != nil {
		return ActionSubmission{}, e
	}

	return ActionSubmission{Command: Command{Key: q.Key, ID: q.ID, Revision: rev, LeaseEpoch: epoch, Owner: q.Owner}, Kind: q.Kind, Installation: q.Installation, Authority: q.Authority, ApplicationID: q.ApplicationID, OrganizationID: q.OrganizationID, Actor: q.Actor, OriginalCommand: q.OriginalCommand, OriginalDigest: q.OriginalDigest, CanonicalDigest: q.CanonicalDigest, ExpectedDesiredRevision: desired, OccurredAt: q.OccurredAt}, nil
}

// MarshalJSON encodes all new request counters as canonical decimal strings.
func (q ContinuationRequest) MarshalJSON() ([]byte, error) {
	if _, err := RemovalContinuationDigest(q); err != nil {
		return nil, err
	}

	return json.Marshal(struct {
		Action              continuationActionWire `json:"action"`
		Predecessor         AttemptRef             `json:"predecessor"`
		SuccessorAttemptID  id.ID                  `json:"successor_attempt_id"`
		ProviderOperationID id.ID                  `json:"provider_operation_id"`
	}{continuationAction(q.Action), q.Predecessor, q.SuccessorAttemptID, q.ProviderOperationID})
}

// UnmarshalJSON refuses noncanonical, unknown and unbound request fields.
func (q *ContinuationRequest) UnmarshalJSON(raw []byte) error {
	if len(raw) > MaxRemovalContinuationRequest {
		return ErrInvalid
	}

	var w struct {
		Action              continuationActionWire `json:"action"`
		Predecessor         AttemptRef             `json:"predecessor"`
		SuccessorAttemptID  id.ID                  `json:"successor_attempt_id"`
		ProviderOperationID id.ID                  `json:"provider_operation_id"`
	}
	if e := strictObject(raw, &w); e != nil {
		return e
	}

	action, e := w.Action.value()
	if e != nil {
		return e
	}

	value := ContinuationRequest{action, w.Predecessor, w.SuccessorAttemptID, w.ProviderOperationID}

	digest, e := RemovalContinuationDigest(value)
	if e != nil || digest != action.CanonicalDigest {
		return ErrInvalid
	}

	encoded, e := value.MarshalJSON()
	if e != nil || !bytes.Equal(encoded, raw) {
		return ErrInvalid
	}

	*q = value

	return nil
}

// RemovalContinuationDigest binds typed inputs, without reading or authorizing state.
func RemovalContinuationDigest(q ContinuationRequest) (string, error) {
	a := q.Action

	generation, e := q.Predecessor.Generation.Uint64()
	if e != nil || generation == 0 || generation >= math.MaxInt64 || !hexDigest(q.Predecessor.DescriptorDigest) || a.Kind != ActionContinue || a.NewTarget != nil || !a.ReleaseID.IsNil() || !a.RollbackReleaseID.IsNil() || a.InstanceID.Prefix() != id.PrefixInstance || !plainToken(a.TenantID) || !plainToken(a.Owner) || a.Revision == 0 || a.Revision > math.MaxInt64 || a.LeaseEpoch > math.MaxInt64 || a.ExpectedDesiredRevision == 0 || a.ExpectedDesiredRevision > math.MaxInt64 || !plainToken(a.Installation) || !plainToken(a.Authority) || !plainToken(a.ApplicationID) || !plainToken(a.OrganizationID) || !plainToken(a.Actor.Subject) || a.Actor.Unauthenticated || len(a.OriginalCommand) == 0 || len(a.OriginalCommand) > 4096 || Digest(a.OriginalCommand) != a.OriginalDigest || a.OccurredAt.IsZero() || a.OccurredAt.Year() < 1 || a.OccurredAt.Year() > 9999 || a.CanonicalDigest != "" && !hexDigest(a.CanonicalDigest) {
		return "", ErrInvalid
	}

	ids := []id.ID{a.ID, q.Predecessor.AttemptID, q.SuccessorAttemptID, q.ProviderOperationID}
	for i, x := range ids {
		if x.Prefix() != PrefixOperation {
			return "", ErrInvalid
		}

		if slices.Contains(ids[:i], x) {
			return "", ErrInvalid
		}
	}

	type actionInput struct {
		Key                     Key        `json:"key"`
		ID                      id.ID      `json:"id"`
		Revision                Decimal    `json:"revision"`
		LeaseEpoch              Decimal    `json:"lease_epoch"`
		Owner                   string     `json:"owner"`
		Kind                    ActionKind `json:"kind"`
		Installation            string     `json:"installation"`
		Authority               string     `json:"authority"`
		ApplicationID           string     `json:"application_id"`
		OrganizationID          string     `json:"organization_id"`
		Actor                   AuditActor `json:"actor"`
		OriginalCommand         []byte     `json:"original_command"`
		OriginalDigest          string     `json:"original_digest"`
		ExpectedDesiredRevision Decimal    `json:"expected_desired_revision"`
		NewTarget               *struct{}  `json:"new_target"`
		ReleaseID               string     `json:"release_id"`
		RollbackReleaseID       string     `json:"rollback_release_id"`
		OccurredAt              time.Time  `json:"occurred_at"`
	}

	type auditInput struct {
		MappingVersion Decimal    `json:"mapping_version"`
		SourceKey      string     `json:"source_key"`
		Scope          AuditScope `json:"scope"`
		Action         string     `json:"action"`
		ResourceKind   string     `json:"resource_kind"`
		ResourceID     string     `json:"resource_id"`
		Outcome        string     `json:"outcome"`
		Reason         string     `json:"reason"`
		SelectorDigest string     `json:"selector_digest"`
		DecisionID     string     `json:"decision_id"`
		CorrelationID  string     `json:"correlation_id"`
	}

	w := continuationAction(a)

	raw, e := json.Marshal(struct {
		SchemaVersion       string      `json:"schema_version"`
		Action              actionInput `json:"action"`
		Predecessor         AttemptRef  `json:"predecessor"`
		SuccessorAttemptID  id.ID       `json:"successor_attempt_id"`
		SuccessorGeneration Decimal     `json:"successor_generation"`
		ProviderOperationID id.ID       `json:"provider_operation_id"`
		Audit               auditInput  `json:"audit"`
	}{"1", actionInput{w.Key, w.ID, w.Revision, w.LeaseEpoch, w.Owner, w.Kind, w.Installation, w.Authority, w.ApplicationID, w.OrganizationID, w.Actor, w.OriginalCommand, w.OriginalDigest, w.ExpectedDesiredRevision, nil, "", "", w.OccurredAt}, q.Predecessor, q.SuccessorAttemptID, Decimal(strconv.FormatUint(generation+1, 10)), q.ProviderOperationID, auditInput{"1", a.ID.String(), AuditScope{Kind: "tenant", TenantID: a.TenantID}, "managed.continue", "instance", a.InstanceID.String(), "allowed", "authorized", "", "", ""}})
	if e != nil {
		return "", e
	}

	return Digest(append([]byte("ctrlplane.managed.removal-continuation.v1\n"), raw...)), nil
}

type continuationActionReceiptWire struct {
	ID              id.ID     `json:"id"`
	Key             Key       `json:"key"`
	Target          Key       `json:"target"`
	CanonicalDigest string    `json:"canonical_digest"`
	OriginalDigest  string    `json:"original_digest"`
	Revision        Decimal   `json:"revision"`
	DesiredRevision Decimal   `json:"desired_revision"`
	AuditID         id.ID     `json:"audit_id"`
	AcceptedAt      time.Time `json:"accepted_at"`
}

// MarshalJSON retains receipt counters as canonical decimal strings.
func (v ContinuationReceipt) MarshalJSON() ([]byte, error) {
	a := v.ActionReceipt
	if !a.ReleaseID.IsNil() {
		return nil, ErrInvalid
	}

	return json.Marshal(struct {
		Receipt             WireReceipt                   `json:"receipt"`
		ActionReceipt       continuationActionReceiptWire `json:"action_receipt"`
		Successor           AttemptRef                    `json:"successor"`
		ProviderOperationID id.ID                         `json:"provider_operation_id"`
	}{ReceiptWire(v.Receipt), continuationActionReceiptWire{a.ID, a.Key, a.Target, a.CanonicalDigest, a.OriginalDigest, Decimal(strconv.FormatUint(a.Revision, 10)), Decimal(strconv.FormatUint(a.DesiredRevision, 10)), a.AuditID, a.AcceptedAt}, v.Successor, v.ProviderOperationID})
}

// UnmarshalJSON validates exact decimal receipt encodings.
func (v *ContinuationReceipt) UnmarshalJSON(raw []byte) error {
	if len(raw) > MaxRemovalContinuationReceipt {
		return ErrInvalid
	}

	var w struct {
		Receipt             WireReceipt                   `json:"receipt"`
		ActionReceipt       continuationActionReceiptWire `json:"action_receipt"`
		Successor           AttemptRef                    `json:"successor"`
		ProviderOperationID id.ID                         `json:"provider_operation_id"`
	}
	if e := strictObject(raw, &w); e != nil {
		return e
	}

	r, e := w.Receipt.Value()
	if e != nil {
		return e
	}

	a := w.ActionReceipt

	rev, e := a.Revision.Uint64()
	if e != nil || rev == 0 || rev > math.MaxInt64 {
		return ErrInvalid
	}

	desired, e := a.DesiredRevision.Uint64()
	if e != nil || desired == 0 || desired > math.MaxInt64 {
		return ErrInvalid
	}

	value := ContinuationReceipt{r, ActionReceipt{ID: a.ID, Key: a.Key, Target: a.Target, CanonicalDigest: a.CanonicalDigest, OriginalDigest: a.OriginalDigest, Revision: rev, DesiredRevision: desired, AuditID: a.AuditID, AcceptedAt: a.AcceptedAt}, w.Successor, w.ProviderOperationID}

	canonical, e := value.MarshalJSON()
	if e != nil || !bytes.Equal(raw, canonical) {
		return ErrInvalid
	}

	*v = value

	return nil
}
