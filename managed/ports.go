package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/xraph/ctrlplane/id"
)

// MachineScope distinguishes authenticated enumeration from tenant mutation.
// The host resolves credentials from configuration and authenticates on each use.
type MachineScope struct {
	GlobalEnumeration bool   `json:"global_enumeration"`
	Installation      string `json:"installation"`
	Application       string `json:"application"`
	Environment       string `json:"environment"`
	TenantID          string `json:"tenant_id"`
	Action            string `json:"action"`
	ResourceID        string `json:"resource_id"`
}

// MachineContextFactory supplies verified identities without synthesizing roles.
type MachineContextFactory interface {
	Context(ctx context.Context, request MachineScope) (context.Context, error)
}

// PortOutcome keeps absence distinct from acceptance and definitive settlement.
type PortOutcome string

const (
	PortAbsent      PortOutcome = "absent"
	PortDenied      PortOutcome = "denied"
	PortUnavailable PortOutcome = "unavailable"
	PortConflict    PortOutcome = "conflict"
	PortAccepted    PortOutcome = "accepted"
	PortTerminal    PortOutcome = "terminal"
)

// PortCommand retains both digest domains and the original immutable commands.
// Endpoints, verifier selection and credentials are trusted host configuration.
type PortCommand struct {
	SchemaVersion  Decimal `json:"schema_version"`
	OperationID    id.ID   `json:"operation_id"`
	RequestID      string  `json:"request_id"`
	Original       []byte  `json:"original"`
	OriginalDigest string  `json:"original_digest"`
	Native         []byte  `json:"native"`
	NativeDigest   string  `json:"native_digest"`
}

// RoleBinding binds configured runtime behavior to an exact native incarnation.
type RoleBinding struct {
	Target              WireIdentity `json:"target"`
	MemberIndex         Decimal      `json:"member_index"`
	MemberName          string       `json:"member_name"`
	Role                RuntimeRole  `json:"role"`
	ReservationID       id.ID        `json:"reservation_id"`
	ReservationVersion  Decimal      `json:"reservation_version"`
	RuntimeID           string       `json:"runtime_id"`
	InstanceID          string       `json:"instance_id"`
	HostID              string       `json:"host_id"`
	NativeBuild         string       `json:"native_build"`
	ConfigurationDigest string       `json:"configuration_digest"`
	EnrollmentEvidence  []byte       `json:"enrollment_evidence"`
}

// LifecycleRequest addresses an accepted reservation and its original command.
type LifecycleRequest struct {
	Command PortCommand `json:"command"`
	Binding RoleBinding `json:"binding"`
}

// LifecycleAcceptance preserves the complete native acceptance and its identity.
type LifecycleAcceptance struct {
	Outcome        PortOutcome `json:"outcome"`
	Command        PortCommand `json:"command"`
	Binding        RoleBinding `json:"binding"`
	ReceiptID      string      `json:"receipt_id"`
	ReceiptVersion Decimal     `json:"receipt_version"`
	Receipt        []byte      `json:"receipt"`
	ReceiptDigest  string      `json:"receipt_digest"`
	AcceptedAt     time.Time   `json:"accepted_at"`
}

// DrainHandle permanently identifies the original process drain and deadline.
type DrainHandle struct {
	OperationID   string    `json:"operation_id"`
	HandleID      string    `json:"handle_id"`
	RuntimeID     string    `json:"runtime_id"`
	InstanceID    string    `json:"instance_id"`
	HostID        string    `json:"host_id"`
	Deadline      time.Time `json:"deadline"`
	Receipt       []byte    `json:"receipt"`
	ReceiptDigest string    `json:"receipt_digest"`
}

// DrainObservationRequest challenges the original handle on its configured host.
type DrainObservationRequest struct {
	LifecycleRequest

	Drain       DrainHandle `json:"drain"`
	ChallengeID string      `json:"challenge_id"`
}

// LifecycleObservation separates immutable drain results from fresh observations.
type LifecycleObservation struct {
	Outcome         PortOutcome `json:"outcome"`
	Binding         RoleBinding `json:"binding"`
	Drain           DrainHandle `json:"drain"`
	AdmissionClosed bool        `json:"admission_closed"`
	ActiveClaims    Decimal     `json:"active_claims"`
	UnknownClaims   Decimal     `json:"unknown_claims"`
	Complete        bool        `json:"complete"`
	Quiescent       bool        `json:"quiescent"`
	CompletedAt     time.Time   `json:"completed_at"`
	ObservedAt      time.Time   `json:"observed_at"`
	ValidUntil      time.Time   `json:"valid_until"`
	ObservationID   string      `json:"observation_id"`
	ChallengeID     string      `json:"challenge_id"`
	VerifierID      string      `json:"verifier_id"`
	Evidence        []byte      `json:"evidence"`
}

// RemovalBinding preserves historical attempt identity and the full original fence.
type RemovalBinding struct {
	LifecycleRequest

	AttemptID           id.ID     `json:"attempt_id"`
	AttemptGeneration   Decimal   `json:"attempt_generation"`
	ProviderOperationID id.ID     `json:"provider_operation_id"`
	Seal                Decimal   `json:"seal"`
	Fence               WireFence `json:"fence"`
	SurvivorProof       []byte    `json:"survivor_proof"`
}

// RemovalAcceptance retains full accepted fence, abort or finish evidence.
type RemovalAcceptance struct {
	Outcome              PortOutcome    `json:"outcome"`
	Binding              RemovalBinding `json:"binding"`
	ReceiptID            string         `json:"receipt_id"`
	Receipt              []byte         `json:"receipt"`
	ReceiptDigest        string         `json:"receipt_digest"`
	AcceptedAt           time.Time      `json:"accepted_at"`
	OriginalBindingProof []byte         `json:"original_binding_proof"`
}

// LifecyclePort connects only to trusted, preconfigured lifecycle authorities.
// A missing result or expired proof never establishes nonissuance or settlement.
type LifecyclePort interface {
	Inspect(ctx context.Context, request LifecycleRequest) (LifecycleObservation, error)
	Register(ctx context.Context, request LifecycleRequest) (LifecycleAcceptance, error)
	LookupRegistration(ctx context.Context, request LifecycleRequest) (LifecycleAcceptance, error)
	BeginDrain(ctx context.Context, request DrainObservationRequest) (LifecycleAcceptance, error)
	ObserveDrain(ctx context.Context, request DrainObservationRequest) (LifecycleObservation, error)
	PrepareRemoval(ctx context.Context, request RemovalBinding) (RemovalAcceptance, error)
	VerifyFence(ctx context.Context, request RemovalBinding) (RemovalAcceptance, error)
	FinishRemoval(ctx context.Context, request RemovalBinding) (RemovalAcceptance, error)
	AbortRemoval(ctx context.Context, request RemovalBinding) (RemovalAcceptance, error)
}

// RouteIdentity names the complete compatible route selection and monotonic floors.
type RouteIdentity struct {
	Key

	Installation        string  `json:"installation"`
	Namespace           string  `json:"namespace"`
	Route               string  `json:"route"`
	Queue               string  `json:"queue"`
	ReleaseID           id.ID   `json:"release_id"`
	ManifestDigest      string  `json:"manifest_digest"`
	Build               string  `json:"build"`
	ArtifactDigest      string  `json:"artifact_digest"`
	ConfigurationDigest string  `json:"configuration_digest"`
	WriterFloor         Decimal `json:"writer_floor"`
	SchemaFloor         Decimal `json:"schema_floor"`
}

// RoutingRequest selects a host-owned route with a stable exact command and CAS.
type RoutingRequest struct {
	Command          PortCommand   `json:"command"`
	Route            RouteIdentity `json:"route"`
	ExpectedRevision Decimal       `json:"expected_revision"`
	Compatibility    []byte        `json:"compatibility"`
	Readiness        []byte        `json:"readiness"`
}

// RoutingReceipt certifies a host selection, never cross-database workflow commit.
type RoutingReceipt struct {
	Outcome    PortOutcome    `json:"outcome"`
	Request    RoutingRequest `json:"request"`
	Revision   Decimal        `json:"revision"`
	ReceiptID  string         `json:"receipt_id"`
	AuditID    string         `json:"audit_id"`
	Receipt    []byte         `json:"receipt"`
	AcceptedAt time.Time      `json:"accepted_at"`
}

// RootDispatchReservation freezes the original root selection across route changes.
// Existing executions and continuations keep their native persisted build semantics.
type RootDispatchReservation struct {
	OriginalCommand  PortCommand   `json:"original_command"`
	Route            RouteIdentity `json:"route"`
	RouteRevision    Decimal       `json:"route_revision"`
	DispatchCommand  PortCommand   `json:"dispatch_command"`
	MayHaveForwarded bool          `json:"may_have_forwarded"`
}

// RoutingPort delegates compatible root selection to the host route authority.
type RoutingPort interface {
	CheckCompatibility(ctx context.Context, request RoutingRequest) (RoutingReceipt, error)
	SetActiveBuild(ctx context.Context, request RoutingRequest) (RoutingReceipt, error)
	LookupRouting(ctx context.Context, request RoutingRequest) (RoutingReceipt, error)
}

// AuditDeliveryPort uses authorized exact idempotent delivery for both methods.
// Recover may create an absent sink event and is never a read-only lookup.
type AuditDeliveryPort interface {
	Deliver(ctx context.Context, request AuditIntent) (AuditSinkReceipt, error)
	Recover(ctx context.Context, request AuditIntent) (AuditSinkReceipt, error)
}

// HistoricalSettlementRequest selects an exact historical removal association.
type HistoricalSettlementRequest struct {
	Key

	SchemaVersion Decimal `json:"schema_version"`

	Installation        string  `json:"installation"`
	Authority           string  `json:"authority"`
	ManifestDigest      string  `json:"manifest_digest"`
	Seal                Decimal `json:"seal"`
	AttemptID           id.ID   `json:"attempt_id"`
	AttemptGeneration   Decimal `json:"attempt_generation"`
	ProviderOperationID id.ID   `json:"provider_operation_id"`
	FenceDigest         string  `json:"fence_digest"`
}

// HistoricalSettlement preserves full resource and step coverage for a trusted verifier.
// A missing record is unresolved and never proof that a command was not issued.
type HistoricalSettlement struct {
	Request            HistoricalSettlementRequest `json:"request"`
	Physical           Physical                    `json:"physical"`
	Resources          map[string]string           `json:"resources"`
	Steps              []WireProviderStepRecord    `json:"steps"`
	Disposition        Disposition                 `json:"disposition"`
	RevokedBeforeIssue bool                        `json:"revoked_before_issue"`
	Settlement         Settlement                  `json:"settlement"`
	Receipt            WireReceipt                 `json:"receipt"`
}

// HistoricalSettlementReader reads accepted settlement without invoking reconciliation.
// It includes retained continuation settlements within the fixed removal profile.
type HistoricalSettlementReader interface {
	ReadManagedHistoricalSettlement(ctx context.Context, request HistoricalSettlementRequest) (HistoricalSettlement, error)
}

// ManagedPlan freezes the exact first-profile service and resource set before execution.
type ManagedPlan struct {
	SchemaVersion Decimal       `json:"schema_version"`
	Identity      WireIdentity  `json:"identity"`
	ActionID      id.ID         `json:"action_id"`
	Provision     ProvisionSpec `json:"provision"`
	CreateProfile string        `json:"create_profile"`
}

// ResourceObservation reports inspection without asserting terminal settlement.
type ResourceObservation struct {
	SchemaVersion Decimal           `json:"schema_version"`
	Outcome       PortOutcome       `json:"outcome"`
	Identity      WireIdentity      `json:"identity"`
	Resources     map[string]string `json:"resources"`
	ObservedAt    time.Time         `json:"observed_at"`
}

// ProviderExecutionResult retains every issued step and its exact resource association.
type ProviderExecutionResult struct {
	SchemaVersion Decimal                  `json:"schema_version"`
	Outcome       PortOutcome              `json:"outcome"`
	OperationID   id.ID                    `json:"operation_id"`
	Resources     map[string]string        `json:"resources"`
	Steps         []WireProviderStepRecord `json:"steps"`
	Settlement    *Settlement              `json:"settlement,omitempty"`
}

// RemovalPlan binds exact persisted resources and the admitted historical attempt.
type RemovalPlan struct {
	SchemaVersion       Decimal           `json:"schema_version"`
	Identity            WireIdentity      `json:"identity"`
	ActionID            id.ID             `json:"action_id"`
	AttemptID           id.ID             `json:"attempt_id"`
	AttemptGeneration   Decimal           `json:"attempt_generation"`
	ProviderOperationID id.ID             `json:"provider_operation_id"`
	FenceDigest         string            `json:"fence_digest"`
	Resources           map[string]string `json:"resources"`
}

// ServiceSample preserves present, unavailable and failed observations per container.
type ServiceSample struct {
	Service      string      `json:"service"`
	ResourceID   string      `json:"resource_id"`
	Outcome      PortOutcome `json:"outcome"`
	ObservedAt   time.Time   `json:"observed_at"`
	CPUTimeNanos Decimal     `json:"cpu_time_nanos"`
	MemoryBytes  Decimal     `json:"memory_bytes"`
	PIDCount     Decimal     `json:"pid_count"`
}

// ManagedProvider is separate from ordinary provider strategies and mutation retries.
// The implementation persists each typed permission before its one external send.
type ManagedProvider interface {
	ValidatePlan(ctx context.Context, request ManagedPlan) error
	InspectResources(ctx context.Context, request ManagedPlan) (ResourceObservation, error)
	ExecuteProvision(ctx context.Context, request ManagedPlan) (ProviderExecutionResult, error)
	ExecuteRemoval(ctx context.Context, request RemovalPlan) (ProviderExecutionResult, error)
	SampleServices(ctx context.Context, request ManagedPlan) ([]ServiceSample, error)
}

// ProviderExecutionAuthority is the exact store used by the managed provider capability.
type ProviderExecutionAuthority interface {
	AuthorizeManagedProviderStep(ctx context.Context, request ProviderStepAuthorization) (ProviderStepGrant, error)
	RecordManagedProviderStepResult(ctx context.Context, request ProviderStepResult) error
	ReadManagedProviderStep(ctx context.Context, request ProviderStepKey) (ProviderStepRecord, error)
}

// Ports contains host capabilities required before later managed consumers start.
type Ports struct {
	Machine   MachineContextFactory
	Lifecycle LifecyclePort
	Routing   RoutingPort
	Provider  ManagedProvider
	Audit     AuditDeliveryPort
}

// ControllerConfig fixes trusted host authority and finite work budgets.
// Runtime roles are explicit in each accepted immutable manifest, never inferred here.
type ControllerConfig struct {
	Installation     string        `json:"installation"`
	Authority        string        `json:"authority"`
	PageSize         int           `json:"page_size"`
	TargetBudget     int           `json:"target_budget"`
	RemoteCallBudget int           `json:"remote_call_budget"`
	CallTimeout      time.Duration `json:"call_timeout"`
}

// ValidateRoleBinding checks the exact accepted target, member and reservation mapping.
// It does not attest the remote process or replace authenticated host verification.
func ValidateRoleBinding(target Target, binding RoleBinding) error {
	spec, err := ParseManagedSpec(target.Manifest)
	if err != nil {
		return err
	}

	identity, err := binding.Target.Value()
	if err != nil {
		return err
	}

	targetBytes, err := json.Marshal(target.Identity)
	if err != nil {
		return err
	}

	bindingBytes, err := json.Marshal(identity)
	if err != nil {
		return err
	}

	if !bytes.Equal(targetBytes, bindingBytes) {
		return ErrAuthority
	}

	index, err := binding.MemberIndex.Uint64()
	if err != nil || index >= uint64(len(target.Manifest.Members)) {
		return ErrInvalid
	}

	member := target.Manifest.Members[index]
	if member.Name != binding.MemberName || spec.RuntimeRoles[index] != binding.Role || member.Build != binding.NativeBuild || member.ConfigurationDigest != binding.ConfigurationDigest || len(binding.EnrollmentEvidence) == 0 || len(binding.EnrollmentEvidence) > MaxEvidence {
		return ErrAuthority
	}

	version, err := binding.ReservationVersion.Uint64()
	if err != nil || version == 0 {
		return ErrInvalid
	}

	found := false

	for _, reservation := range target.Reservations {
		if reservation.ID == binding.ReservationID {
			if reservation.Member != binding.MemberName || reservation.RuntimeID != binding.RuntimeID || reservation.InstanceID != binding.InstanceID || reservation.HostID != binding.HostID {
				return ErrAuthority
			}

			found = true
		}

		if reservation.RuntimeID == binding.RuntimeID {
			for n, other := range target.Manifest.Members {
				if reservation.Member == other.Name && spec.RuntimeRoles[n] != binding.Role {
					return ErrConflict
				}
			}
		}
	}

	if !found {
		return ErrNotFound
	}

	return nil
}

// WireManifest carries the unchanged manifest bytes with a lossless decimal version.
type WireManifest struct {
	Version    Decimal  `json:"version"`
	Members    []Member `json:"members"`
	Spec       []byte   `json:"spec"`
	SpecDigest string   `json:"spec_digest"`
}

// WireIdentity is the transport form of an authoritative identity.
// Conversion preserves Spec bytes and all stored digest domains.
type WireIdentity struct {
	Key

	ProvisionOperationID id.ID        `json:"provision_operation_id"`
	Physical             Physical     `json:"physical"`
	Manifest             WireManifest `json:"manifest"`
}

// IdentityWire copies the immutable value without recomputing any digest.
func IdentityWire(v Identity) WireIdentity {
	return WireIdentity{Key: v.Key, ProvisionOperationID: v.ProvisionOperationID, Physical: v.Physical, Manifest: WireManifest{Version: Decimal(strconv.FormatUint(v.Manifest.Version, 10)), Members: append([]Member(nil), v.Manifest.Members...), Spec: append([]byte(nil), v.Manifest.Spec...), SpecDigest: v.Manifest.SpecDigest}}
}

// Value checks the wire version before restoring its exact internal representation.
func (w WireIdentity) Value() (Identity, error) {
	version, err := w.Manifest.Version.Uint64()
	if err != nil || version == 0 {
		return Identity{}, ErrInvalid
	}

	if len(w.Manifest.Members) > MaxMembers || len(w.Manifest.Spec) > MaxManifestSpec {
		return Identity{}, ErrCapacity
	}

	specValid := wireProof(w.Manifest.Spec)

	var profile struct {
		Profile string `json:"profile"`
	}

	parseErr := json.Unmarshal(w.Manifest.Spec, &profile)
	if len(w.Manifest.Spec) > MaxEvidence || profile.Profile == "ctrlplane.managed.v1" || parseErr != nil && bytes.Contains(w.Manifest.Spec, []byte("ctrlplane.managed.")) {
		_, specErr := ParseProvisionSpec(Manifest{Version: version, Members: w.Manifest.Members, Spec: w.Manifest.Spec, SpecDigest: w.Manifest.SpecDigest})
		specValid = parseErr == nil && specErr == nil
	}

	if !wireToken(w.TenantID) || w.InstanceID.Prefix() != id.PrefixInstance || w.ProvisionOperationID.Prefix() != PrefixOperation || !wireToken(w.Physical.Provider) || !wireToken(w.Physical.Placement) || !wireToken(w.Physical.Project) || !wireToken(w.Physical.Generation) || len(w.Manifest.Members) == 0 || !specValid || Digest(w.Manifest.Spec) != w.Manifest.SpecDigest {
		return Identity{}, ErrInvalid
	}

	for _, m := range w.Manifest.Members {
		if !wireToken(m.Name) || !wireToken(m.Service) || !wireToken(m.Installation) || !wireToken(m.Namespace) || !wireToken(m.Queue) || !wireToken(m.Build) || !hexDigest(m.ArtifactDigest) || !hexDigest(m.ConfigurationDigest) {
			return Identity{}, ErrInvalid
		}
	}

	return Identity{Key: w.Key, ProvisionOperationID: w.ProvisionOperationID, Physical: w.Physical, Manifest: Manifest{Version: version, Members: append([]Member(nil), w.Manifest.Members...), Spec: append([]byte(nil), w.Manifest.Spec...), SpecDigest: w.Manifest.SpecDigest}}, nil
}

// WireFence preserves the original opaque fence and its decimal epoch.
type WireFence struct {
	CompatibilityEvidence []byte    `json:"compatibility_evidence"`
	Epoch                 Decimal   `json:"epoch"`
	Evidence              []byte    `json:"evidence"`
	Digest                string    `json:"digest"`
	ValidUntil            time.Time `json:"valid_until"`
	Survivors             []string  `json:"survivors"`
}

// FenceWire copies the complete original fence without replacing its evidence.
func FenceWire(v Fence) WireFence {
	return WireFence{CompatibilityEvidence: append([]byte(nil), v.CompatibilityEvidence...), Epoch: Decimal(strconv.FormatUint(v.Epoch, 10)), Evidence: append([]byte(nil), v.Evidence...), Digest: v.Digest, ValidUntil: v.ValidUntil, Survivors: append([]string(nil), v.Survivors...)}
}

// Value refuses malformed epochs and oversized individual proofs.
func (w WireFence) Value() (Fence, error) {
	epoch, err := w.Epoch.Uint64()
	if err != nil {
		return Fence{}, err
	}

	if epoch == 0 {
		return Fence{}, ErrInvalid
	}

	if len(w.Evidence) > MaxEvidence || len(w.CompatibilityEvidence) > MaxEvidence || len(w.Survivors) > MaxReservations {
		return Fence{}, ErrCapacity
	}

	for _, survivor := range w.Survivors {
		if !wireToken(survivor) {
			return Fence{}, ErrInvalid
		}
	}

	return Fence{CompatibilityEvidence: append([]byte(nil), w.CompatibilityEvidence...), Epoch: epoch, Evidence: append([]byte(nil), w.Evidence...), Digest: w.Digest, ValidUntil: w.ValidUntil, Survivors: append([]string(nil), w.Survivors...)}, nil
}

// WireReceipt carries the historical receipt unchanged except for integer encoding.
type WireReceipt struct {
	ID         id.ID     `json:"id"`
	Digest     string    `json:"digest"`
	Revision   Decimal   `json:"revision"`
	LeaseEpoch Decimal   `json:"lease_epoch"`
	AcceptedAt time.Time `json:"accepted_at"`
	LeaseUntil time.Time `json:"lease_until"`
}

// ReceiptWire preserves historical identity and acceptance times.
func ReceiptWire(v Receipt) WireReceipt {
	return WireReceipt{ID: v.ID, Digest: v.Digest, Revision: Decimal(strconv.FormatUint(v.Revision, 10)), LeaseEpoch: Decimal(strconv.FormatUint(v.LeaseEpoch, 10)), AcceptedAt: v.AcceptedAt, LeaseUntil: v.LeaseUntil}
}

// Value checks both canonical counters before restoring a receipt.
func (w WireReceipt) Value() (Receipt, error) {
	revision, err := w.Revision.Uint64()
	if err != nil {
		return Receipt{}, err
	}

	epoch, err := w.LeaseEpoch.Uint64()
	if err != nil {
		return Receipt{}, err
	}

	if revision == 0 || revision > 1<<63-1 || epoch > 1<<63-1 {
		return Receipt{}, ErrInvalid
	}

	return Receipt{ID: w.ID, Digest: w.Digest, Revision: revision, LeaseEpoch: epoch, AcceptedAt: w.AcceptedAt, LeaseUntil: w.LeaseUntil}, nil
}

// WireProviderStepPermission is the transport form of a retained step permission.
type WireProviderStepPermission struct {
	Step          ProviderStepKey `json:"step"`
	CommandID     id.ID           `json:"command_id"`
	Digest        string          `json:"digest"`
	SpecDigest    string          `json:"spec_digest"`
	CreateProfile string          `json:"create_profile"`
	Physical      Physical        `json:"physical"`
	Seal          Decimal         `json:"seal"`
	FenceDigest   string          `json:"fence_digest"`
	ResourceID    string          `json:"resource_id"`
	IssuedAt      time.Time       `json:"issued_at"`
	Revision      Decimal         `json:"revision"`
}

// WireProviderStepResult preserves the complete definitive or uncertain response.
type WireProviderStepResult struct {
	Permission WireProviderStepPermission `json:"permission"`
	Outcome    PortOutcome                `json:"outcome"`
	ResourceID string                     `json:"resource_id"`
	Evidence   []byte                     `json:"evidence"`
	ObservedAt time.Time                  `json:"observed_at"`
	Diagnostic string                     `json:"diagnostic,omitempty"`
}

// WireProviderStepRecord keeps permission and result counters canonical on the wire.
type WireProviderStepRecord struct {
	Permission WireProviderStepPermission `json:"permission"`
	Result     *WireProviderStepResult    `json:"result,omitempty"`
}

// ProviderPermissionWire preserves the stored permission digest rather than rehashing it.
func ProviderPermissionWire(v ProviderStepPermission) WireProviderStepPermission {
	return WireProviderStepPermission{Step: v.Step, CommandID: v.CommandID, Digest: v.Digest, SpecDigest: v.SpecDigest, CreateProfile: v.CreateProfile, Physical: v.Physical, Seal: Decimal(strconv.FormatUint(v.Seal, 10)), FenceDigest: v.FenceDigest, ResourceID: v.ResourceID, IssuedAt: v.IssuedAt, Revision: Decimal(strconv.FormatUint(v.Revision, 10))}
}

// Value checks the complete bounded permission before restoring its stored value.
func (w WireProviderStepPermission) Value() (ProviderStepPermission, error) {
	seal, err := wireSeal(w.Seal)
	if err != nil {
		return ProviderStepPermission{}, err
	}

	revision, err := w.Revision.Uint64()
	if err != nil {
		return ProviderStepPermission{}, err
	}

	if revision == 0 || revision > 1<<63-1 {
		return ProviderStepPermission{}, ErrInvalid
	}

	if !wireToken(w.Step.TenantID) || w.Step.InstanceID.Prefix() != id.PrefixInstance || w.Step.ActionID.Prefix() != PrefixOperation || w.Step.ProviderOperationID.Prefix() != PrefixOperation || w.CommandID.Prefix() != PrefixOperation || !hexDigest(w.Digest) || !hexDigest(w.SpecDigest) || w.CreateProfile != "ctrlplane.managed.create.v1" || !wireToken(w.Physical.Provider) || !wireToken(w.Physical.Placement) || !wireToken(w.Physical.Project) || !wireToken(w.Physical.Generation) || w.ResourceID != "" && !wireToken(w.ResourceID) || w.IssuedAt.IsZero() {
		return ProviderStepPermission{}, ErrInvalid
	}

	switch w.Step.Kind {
	case StepCreateNetwork, StepRemoveNetwork:
		if w.Step.Service != "" {
			return ProviderStepPermission{}, ErrInvalid
		}
	case StepCreateContainer, StepStartContainer, StepRemoveContainer:
		if !wireToken(w.Step.Service) {
			return ProviderStepPermission{}, ErrInvalid
		}
	default:
		return ProviderStepPermission{}, ErrInvalid
	}

	if w.Step.Kind == StepRemoveContainer || w.Step.Kind == StepRemoveNetwork {
		if seal == 0 || !hexDigest(w.FenceDigest) || !wireToken(w.ResourceID) {
			return ProviderStepPermission{}, ErrInvalid
		}
	} else if w.FenceDigest != "" {
		return ProviderStepPermission{}, ErrInvalid
	}

	return ProviderStepPermission{Step: w.Step, CommandID: w.CommandID, Digest: w.Digest, SpecDigest: w.SpecDigest, CreateProfile: w.CreateProfile, Physical: w.Physical, Seal: seal, FenceDigest: w.FenceDigest, ResourceID: w.ResourceID, IssuedAt: w.IssuedAt, Revision: revision}, nil
}

// ProviderStepWire copies the exact evidence and both copies of its permission.
func ProviderStepWire(v ProviderStepRecord) WireProviderStepRecord {
	w := WireProviderStepRecord{Permission: ProviderPermissionWire(v.Permission)}
	if v.Result != nil {
		r := v.Result
		w.Result = &WireProviderStepResult{Permission: ProviderPermissionWire(r.Permission), Outcome: r.Outcome, ResourceID: r.ResourceID, Evidence: append([]byte(nil), r.Evidence...), ObservedAt: r.ObservedAt, Diagnostic: r.Diagnostic}
	}

	return w
}

// Value validates matching permission copies and the complete encoded step bound.
func (w WireProviderStepRecord) Value() (ProviderStepRecord, error) {
	permission, err := w.Permission.Value()
	if err != nil {
		return ProviderStepRecord{}, err
	}

	v := ProviderStepRecord{Permission: permission}

	if w.Result != nil {
		r := w.Result

		p, err := r.Permission.Value()
		if err != nil {
			return ProviderStepRecord{}, err
		}

		if len(r.Evidence) > MaxEvidence || len(r.Diagnostic) > 256 {
			return ProviderStepRecord{}, ErrCapacity
		}

		if p != permission {
			return ProviderStepRecord{}, ErrConflict
		}

		if r.ResourceID != "" && !wireToken(r.ResourceID) || r.Diagnostic != "" && !wireToken(r.Diagnostic) || r.Outcome != PortTerminal && r.Outcome != PortUnavailable || r.ObservedAt.IsZero() || r.ObservedAt.Before(permission.IssuedAt) {
			return ProviderStepRecord{}, ErrInvalid
		}

		v.Result = &ProviderStepResult{Permission: p, Outcome: r.Outcome, ResourceID: r.ResourceID, Evidence: append([]byte(nil), r.Evidence...), ObservedAt: r.ObservedAt, Diagnostic: r.Diagnostic}
	}

	raw, err := json.Marshal(w)
	if err != nil {
		return ProviderStepRecord{}, err
	}

	if len(raw) > MaxProviderStep {
		return ProviderStepRecord{}, ErrCapacity
	}

	return v, nil
}

func wireSeal(value Decimal) (uint64, error) {
	seal, err := value.Uint64()
	if err != nil || seal > 1<<63-1 {
		return 0, ErrInvalid
	}

	return seal, nil
}

// Validate checks the frozen command envelopes without translating their digest domains.
func (c PortCommand) Validate() error {
	if c.SchemaVersion != "1" || c.OperationID.Prefix() != PrefixOperation || !wireToken(c.RequestID) || !wireProof(c.Original) || !wireProof(c.Native) || Digest(c.Original) != c.OriginalDigest || Digest(c.Native) != c.NativeDigest {
		return ErrInvalid
	}

	return nil
}

func wireToken(value string) bool {
	if len(value) == 0 || len(value) > 256 {
		return false
	}

	for _, c := range value {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && !strings.ContainsRune("-_.:/@+=", c) {
			return false
		}
	}

	return true
}
func wireProof(value []byte) bool { return len(value) > 0 && len(value) <= MaxEvidence }
func wireOutcome(v PortOutcome) bool {
	return v == PortAbsent || v == PortDenied || v == PortUnavailable || v == PortConflict || v == PortAccepted || v == PortTerminal
}
func wireDecimals(values ...Decimal) bool {
	for _, v := range values {
		if _, err := v.Uint64(); err != nil {
			return false
		}
	}

	return true
}

// Validate checks bounded enrollment values; the caller still needs authenticated attestation.
func (b RoleBinding) Validate() error {
	target, err := b.Target.Value()
	if err != nil {
		return err
	}

	index, err := b.MemberIndex.Uint64()
	if err != nil || index >= uint64(len(target.Manifest.Members)) {
		return ErrInvalid
	}

	version, err := b.ReservationVersion.Uint64()
	if err != nil || version == 0 {
		return ErrInvalid
	}

	if b.ReservationID.Prefix() != PrefixReservation || !wireToken(b.MemberName) || !wireToken(b.RuntimeID) || !wireToken(b.InstanceID) || !wireToken(b.HostID) || !wireToken(b.NativeBuild) || !hexDigest(b.ConfigurationDigest) || !wireProof(b.EnrollmentEvidence) || (b.Role != Polling && b.Role != RetainedQuery) {
		return ErrInvalid
	}

	return nil
}

// Validate enforces the version and individual proof bounds before a lifecycle call.
func (q LifecycleRequest) Validate() error {
	if err := q.Command.Validate(); err != nil {
		return err
	}

	return q.Binding.Validate()
}

// Validate preserves complete native acceptance, separate from a successful observation.
func (q LifecycleAcceptance) Validate() error {
	if !wireOutcome(q.Outcome) {
		return ErrInvalid
	}

	if err := q.Command.Validate(); err != nil {
		return err
	}

	if err := q.Binding.Validate(); err != nil {
		return err
	}

	if q.Outcome == PortAccepted || q.Outcome == PortTerminal {
		v, err := q.ReceiptVersion.Uint64()
		if err != nil || v == 0 || !wireToken(q.ReceiptID) || !wireProof(q.Receipt) || Digest(q.Receipt) != q.ReceiptDigest || q.AcceptedAt.IsZero() {
			return ErrInvalid
		}
	} else if len(q.Receipt) > MaxEvidence {
		return ErrCapacity
	}

	return nil
}

// Validate checks the original drain handle, permanent closure and fresh proof fields.
func (q LifecycleObservation) Validate() error {
	if !wireOutcome(q.Outcome) {
		return ErrInvalid
	}

	if err := q.Binding.Validate(); err != nil {
		return err
	}

	if !wireDecimals(q.ActiveClaims, q.UnknownClaims) || !wireToken(q.ObservationID) || !wireToken(q.ChallengeID) || !wireToken(q.VerifierID) || !wireProof(q.Evidence) || q.ObservedAt.IsZero() || !q.ValidUntil.After(q.ObservedAt) {
		return ErrInvalid
	}

	if !wireToken(q.Drain.OperationID) || !wireToken(q.Drain.HandleID) || !wireToken(q.Drain.RuntimeID) || !wireToken(q.Drain.InstanceID) || !wireToken(q.Drain.HostID) || q.Drain.Deadline.IsZero() || !wireProof(q.Drain.Receipt) || Digest(q.Drain.Receipt) != q.Drain.ReceiptDigest {
		return ErrInvalid
	}

	if q.Quiescent && (!q.Complete || !q.AdmissionClosed || q.ActiveClaims != "0" || q.UnknownClaims != "0" || q.CompletedAt.IsZero()) {
		return ErrInvalid
	}

	return nil
}

// Validate checks the entire original removal binding before any remote operation.
func (q RemovalBinding) Validate() error {
	if err := q.LifecycleRequest.Validate(); err != nil {
		return err
	}

	if _, err := q.Fence.Value(); err != nil {
		return err
	}

	if _, err := wireSeal(q.Seal); err != nil {
		return err
	}

	if q.AttemptID.Prefix() != PrefixOperation || q.ProviderOperationID.Prefix() != PrefixOperation || !wireDecimals(q.AttemptGeneration) || q.AttemptGeneration == "0" || q.Seal == "0" || !wireProof(q.SurvivorProof) {
		return ErrInvalid
	}

	return nil
}

// Validate retains accepted abort/finish evidence and the original binding proof separately.
func (q RemovalAcceptance) Validate() error {
	if !wireOutcome(q.Outcome) {
		return ErrInvalid
	}

	if err := q.Binding.Validate(); err != nil {
		return err
	}

	if !wireToken(q.ReceiptID) || !wireProof(q.Receipt) || Digest(q.Receipt) != q.ReceiptDigest || !wireProof(q.OriginalBindingProof) || q.AcceptedAt.IsZero() {
		return ErrInvalid
	}

	return nil
}

// Validate checks exact route identity and bounded compatibility/readiness proofs.
func (q RoutingRequest) Validate() error {
	if err := q.Command.Validate(); err != nil {
		return err
	}

	r := q.Route
	if !wireToken(r.TenantID) || r.InstanceID.Prefix() != id.PrefixInstance || !wireToken(r.Installation) || !wireToken(r.Namespace) || !wireToken(r.Route) || !wireToken(r.Queue) || r.ReleaseID.Prefix() != id.PrefixRelease || !hexDigest(r.ManifestDigest) || !wireToken(r.Build) || !hexDigest(r.ArtifactDigest) || !hexDigest(r.ConfigurationDigest) || !wireDecimals(r.WriterFloor, r.SchemaFloor, q.ExpectedRevision) || !wireProof(q.Compatibility) || !wireProof(q.Readiness) {
		return ErrInvalid
	}

	return nil
}

// Validate checks a host selection receipt without treating it as workflow commit.
func (q RoutingReceipt) Validate() error {
	if err := q.Request.Validate(); err != nil {
		return err
	}

	if !wireOutcome(q.Outcome) || !wireDecimals(q.Revision) || !wireToken(q.ReceiptID) || !wireToken(q.AuditID) || !wireProof(q.Receipt) || q.AcceptedAt.IsZero() {
		return ErrInvalid
	}

	return nil
}

// Validate checks the bounded first profile without resolving private startup material.
func (q ManagedPlan) Validate() error {
	if q.SchemaVersion != "1" || q.ActionID.Prefix() != PrefixOperation || q.CreateProfile != "ctrlplane.managed.create.v1" {
		return ErrInvalid
	}

	identity, err := q.Identity.Value()
	if err != nil {
		return err
	}

	spec, err := ParseProvisionSpec(identity.Manifest)
	if err != nil {
		return err
	}

	a, err := json.Marshal(spec)
	if err != nil {
		return err
	}

	b, err := json.Marshal(q.Provision)
	if err != nil {
		return err
	}

	if !bytes.Equal(a, b) {
		return ErrConflict
	}

	return nil
}

func wireResources(resources map[string]string) bool {
	if len(resources) > MaxMembers+1 {
		return false
	}

	for slot, resource := range resources {
		if !wireToken(slot) || !wireToken(resource) {
			return false
		}
	}

	return true
}

// Validate bounds inspection without treating existence as settlement.
func (q ResourceObservation) Validate() error {
	if q.SchemaVersion != "1" || !wireOutcome(q.Outcome) || !wireResources(q.Resources) || q.ObservedAt.IsZero() {
		return ErrInvalid
	}

	_, err := q.Identity.Value()

	return err
}

// Validate bounds every retained step and optional settlement proof.
// Full service coverage and trusted verifier acceptance are separate predicates.
func (q ProviderExecutionResult) Validate() error {
	if q.SchemaVersion != "1" || !wireOutcome(q.Outcome) || q.OperationID.Prefix() != PrefixOperation || !wireResources(q.Resources) || len(q.Steps) > MaxProviderSteps {
		return ErrInvalid
	}

	for _, step := range q.Steps {
		if _, err := step.Value(); err != nil {
			return err
		}
	}

	if q.Settlement != nil && (!wireProof(q.Settlement.Evidence) || !wireToken(q.Settlement.Verifier)) {
		return ErrInvalid
	}

	return nil
}

// Validate checks the exact resource association before a removal executor starts.
func (q RemovalPlan) Validate() error {
	if q.SchemaVersion != "1" || q.ActionID.Prefix() != PrefixOperation || q.AttemptID.Prefix() != PrefixOperation || q.ProviderOperationID.Prefix() != PrefixOperation || !wireDecimals(q.AttemptGeneration) || q.AttemptGeneration == "0" || !hexDigest(q.FenceDigest) || !wireResources(q.Resources) {
		return ErrInvalid
	}

	_, err := q.Identity.Value()

	return err
}

// Validate checks the historical request without accepting a caller-selected verifier.
func (q HistoricalSettlementRequest) Validate() error {
	if _, err := wireSeal(q.Seal); err != nil {
		return err
	}

	if q.SchemaVersion != "1" || !wireToken(q.TenantID) || q.InstanceID.Prefix() != id.PrefixInstance || !wireToken(q.Installation) || !wireToken(q.Authority) || !hexDigest(q.ManifestDigest) || !wireDecimals(q.AttemptGeneration) || q.Seal == "0" || q.AttemptGeneration == "0" || q.AttemptID.Prefix() != PrefixOperation || q.ProviderOperationID.Prefix() != PrefixOperation || !hexDigest(q.FenceDigest) {
		return ErrInvalid
	}

	return nil
}

// Validate preserves bounded historical evidence without fabricating terminal status.
func (q HistoricalSettlement) Validate() error {
	if err := q.Request.Validate(); err != nil {
		return err
	}

	if !wireResources(q.Resources) || len(q.Steps) > MaxProviderSteps || !wireToken(q.Physical.Provider) || !wireToken(q.Physical.Placement) || !wireToken(q.Physical.Project) || !wireToken(q.Physical.Generation) || len(q.Settlement.Evidence) > MaxEvidence {
		return ErrInvalid
	}

	for _, step := range q.Steps {
		if _, err := step.Value(); err != nil {
			return err
		}
	}

	_, err := q.Receipt.Value()

	return err
}
