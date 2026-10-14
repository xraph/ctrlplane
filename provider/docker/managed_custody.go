package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"time"

	"github.com/xraph/ctrlplane/managed"
)

// ManagedDockerProfile identifies exact single-mutation Docker transport custody.
const ManagedDockerProfile = "ctrlplane.docker.custody.v1"

// ManagedDockerJournalLimit bounds a complete row, including base64 encoded evidence.
const ManagedDockerJournalLimit = 65536

// ManagedDockerCommand contains non-secret inputs to one Docker mutation. Resource and
// network IDs are actual create response IDs. Names are creation hints only.
type ManagedDockerCommand struct {
	Kind       managed.ProviderStepKind  `json:"kind"`
	Name       string                    `json:"name"`
	ResourceID string                    `json:"resource_id"`
	NetworkID  string                    `json:"network_id"`
	Service    *managed.ProvisionService `json:"service"`
}

// ManagedDockerRequest retains the original permission and independent receipt. This
// first slice handles ordinal zero only. It cannot select or issue retry permission.
type ManagedDockerRequest struct {
	BackendAuthority string                                `json:"backend_authority"`
	Permission       managed.WireProviderStepPermission    `json:"permission"`
	Receipt          managed.WireReceipt                   `json:"receipt"`
	Command          ManagedDockerCommand                  `json:"command"`
	Binding          *managed.ProviderEffectBindingFacts   `json:"binding"`
	Invocation       *managed.ProviderInvocationPermission `json:"invocation"`
}

// ManagedDockerResponse records an actual successful API response, not authenticated
// process custody. In particular a returned creation ID does not establish enrollment.
type ManagedDockerResponse struct {
	ResourceID string    `json:"resource_id"`
	ObservedAt time.Time `json:"observed_at"`
}

// ManagedDockerCustody adds independently authenticated creation/incarnation facts.
// OriginalProcess is required for start; it is never synthesized from Docker inspection.
type ManagedDockerCustody struct {
	ResourceID      string                        `json:"resource_id"`
	Incarnation     string                        `json:"incarnation"`
	OriginalProcess *managed.OriginalProcessFacts `json:"original_process"`
	Attestation     []byte                        `json:"attestation"`
}

// ManagedDockerAuthority is trusted configuration, not a request-selected resolver.
// VerifyRequest authenticates retained permission, receipt, exact transport semantics,
// backend identity and any original resource/process custody. Admit additionally checks
// current authority and the newly issued one-shot grant/marker before delegation.
// SealResponse recovers stable enrollment evidence without launching or sending Docker
// commands. VerifyCustody authenticates that evidence against this exact response.
// NoDispatch signs the permanently closed journal fact; its witness must bind the digest.
// Implementations must refuse absent enrollment, stale/replaced processes and restore
// quarantine. The library supplies no permissive default or external launcher setup.
type ManagedDockerAuthority interface {
	VerifyRequest(ctx context.Context, request ManagedDockerRequest) error
	Admit(ctx context.Context, request ManagedDockerRequest) error
	SealResponse(ctx context.Context, request ManagedDockerRequest, response ManagedDockerResponse) (ManagedDockerCustody, error)
	VerifyCustody(ctx context.Context, request ManagedDockerRequest, response ManagedDockerResponse, custody ManagedDockerCustody) error
	NoDispatch(ctx context.Context, request ManagedDockerRequest, closedDigest string) (managed.ProviderInvocationFailure, error)
	VerifyNoDispatch(ctx context.Context, request ManagedDockerRequest, closedDigest string, witness managed.ProviderInvocationFailure) error
}

// ManagedDockerJournal is a protected durable byte CAS. Empty expected means absent.
// CompareAndSwap must be atomic across processes and durable before success. An error
// can mean committed but unacknowledged. Callers must never send after such an error.
// Rows and permanent closed/delegated states must not be deleted or restored backwards.
type ManagedDockerJournal interface {
	Load(ctx context.Context, key string) ([]byte, error)
	CompareAndSwap(ctx context.Context, key string, expected, next []byte) (bool, error)
}

// ManagedDockerTransport sends exactly one mutation, with no retry, adoption, name
// removal, compensating cleanup, pull, or NotFound-to-success conversion.
type ManagedDockerTransport interface {
	Execute(ctx context.Context, command ManagedDockerCommand) (string, error)
}

// ManagedDockerRecord is a bounded observation. Only complete has authenticated custody.
// delegated means possible effect; observed retains a response awaiting authentication.
// Neither state permits resend. Closed is a permanent affirmative no-dispatch fact.
type ManagedDockerRecord struct {
	SchemaVersion managed.Decimal                    `json:"schema_version"`
	Request       ManagedDockerRequest               `json:"request"`
	State         string                             `json:"state"`
	Response      *ManagedDockerResponse             `json:"response"`
	Custody       *ManagedDockerCustody              `json:"custody"`
	Closure       *managed.ProviderInvocationFailure `json:"closure"`
}

// ManagedDockerExecutor owns a journal and configured authority/transport pair. It is
// an independently usable single-step component, not the complete ManagedProvider.
type ManagedDockerExecutor struct {
	journal   ManagedDockerJournal
	transport ManagedDockerTransport
	authority ManagedDockerAuthority
	verifier  managed.ProviderEffectBindingVerifier
	backend   string
}

var (
	_ managed.ProviderEffectBindingProducer     = (*ManagedDockerExecutor)(nil)
	_ managed.ProviderInvocationClosureProducer = (*ManagedDockerExecutor)(nil)
)

// NewManagedDockerExecutor refuses missing or typed-nil trusted dependencies.
func NewManagedDockerExecutor(journal ManagedDockerJournal, transport ManagedDockerTransport, authority ManagedDockerAuthority, verifier managed.ProviderEffectBindingVerifier, backend string) (*ManagedDockerExecutor, error) {
	if absent(journal) || absent(transport) || absent(authority) || absent(verifier) || !custodyToken(backend) {
		return nil, managed.ErrUnsupported
	}

	return &ManagedDockerExecutor{journal: journal, transport: transport, authority: authority, verifier: verifier, backend: backend}, nil
}

func absent(value any) bool {
	if value == nil {
		return true
	}

	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}

	return false
}

func custodyToken(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}

	for _, c := range value {
		allowed := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == ':' || c == '/' || c == '@' || c == '+' || c == '='
		if c > 127 || !allowed {
			return false
		}
	}

	return true
}

func dockerObjectID(value string) bool {
	if len(value) != 64 {
		return false
	}

	for _, c := range value {
		allowed := c >= '0' && c <= '9' || c >= 'a' && c <= 'f'
		if !allowed {
			return false
		}
	}

	return true
}

func custodyBytes(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, managed.ErrInvalid
	}

	if len(raw) > ManagedDockerJournalLimit {
		return nil, managed.ErrCapacity
	}

	return raw, nil
}

func custodyClone[T any](value T) T {
	raw, err := json.Marshal(value)

	var result T
	if err != nil {
		return result
	}

	if err = json.Unmarshal(raw, &result); err != nil {
		var zero T

		return zero
	}

	return result
}

func custodyKey(permission managed.WireProviderStepPermission) string {
	raw, err := json.Marshal(struct {
		Step    managed.ProviderStepKey `json:"Step"`
		Command string                  `json:"Command"`
	}{permission.Step, permission.CommandID.String()})
	if err != nil {
		return ""
	}

	return managed.Digest(raw)
}

func (e *ManagedDockerExecutor) validate(ctx context.Context, request ManagedDockerRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if _, err := custodyBytes(request); err != nil {
		return err
	}

	p, err := request.Permission.Value()
	if err != nil {
		return err
	}

	r, err := request.Receipt.Value()
	if err != nil {
		return err
	}

	if request.BackendAuthority != e.backend || p.Physical.Provider != "docker" || r.ID != p.CommandID || r.Revision != p.Revision || !r.LeaseUntil.After(r.AcceptedAt) || request.Command.Kind != p.Step.Kind {
		return managed.ErrInvalid
	}

	if err = validateDockerCommand(request.Command); err != nil {
		return err
	}

	if request.Command.Service != nil && request.Command.Service.Name != p.Step.Service {
		return managed.ErrInvalid
	}

	if request.Command.ResourceID != p.ResourceID {
		return managed.ErrInvalid
	}

	removal := p.Step.Kind == managed.StepRemoveContainer || p.Step.Kind == managed.StepRemoveNetwork
	if removal {
		if request.Binding == nil || request.Invocation == nil {
			return managed.ErrUnsupported
		}

		b := request.Binding
		if err = managed.ValidateProviderEffectBindingFacts(*b); err != nil {
			return err
		}

		i := request.Invocation
		if !reflect.DeepEqual(b.Original.Permission, request.Permission) || !reflect.DeepEqual(b.Original.Receipt, request.Receipt) || !reflect.DeepEqual(i.Permission, request.Permission) || i.Ordinal != "0" || i.Key.CommandID != p.CommandID || i.Key.Step != p.Step || i.Key.AttemptKey != b.Original.Key.AttemptKey || i.ProviderCommandDigest != b.ProviderCommandDigest || !dockerObjectID(i.RootDigest) {
			return managed.ErrInvalid
		}

		command, decodeErr := managed.DecodeProviderEffectCommand(b.Command)
		if decodeErr != nil {
			return decodeErr
		}

		expectedKind, method := "container", "container_remove"
		options := []managed.ProviderEffectOption{{Name: "force", Value: "true"}, {Name: "remove_volumes", Value: "false"}}

		if p.Step.Kind == managed.StepRemoveNetwork {
			expectedKind, method = "network", "network_remove"
			options = []managed.ProviderEffectOption{}
		}

		if command.Profile != ManagedDockerProfile || command.BackendAuthority != e.backend || command.ResourceKind != expectedKind || command.ResourceID != p.ResourceID || command.Method != method || !reflect.DeepEqual(command.Options, options) {
			return managed.ErrInvalid
		}

		if err = e.verifier.VerifyProviderEffectBinding(ctx, custodyClone(b.Original), custodyClone(*b)); err != nil {
			return err
		}
	} else if request.Binding != nil || request.Invocation != nil {
		return managed.ErrInvalid
	}

	// Provision permission and apply receipt use independent digest domains and
	// callback clocks. The configured authority must authenticate the exact retained
	// pair and original request; matching ID/revision alone grants no authority.
	if err = e.authority.VerifyRequest(ctx, custodyClone(request)); err != nil {
		return err
	}

	return ctx.Err()
}

func validateDockerCommand(c ManagedDockerCommand) error {
	switch c.Kind {
	case managed.StepCreateNetwork:
		if !custodyToken(c.Name) || c.ResourceID != "" || c.NetworkID != "" || c.Service != nil {
			return managed.ErrInvalid
		}
	case managed.StepCreateContainer:
		if !custodyToken(c.Name) || c.ResourceID != "" || !dockerObjectID(c.NetworkID) || c.Service == nil {
			return managed.ErrInvalid
		}

		s := c.Service
		if !custodyToken(s.Name) || s.Role != "main" && s.Role != "sidecar" || len(s.Image) > 256 || len(s.Image) < 72 || s.Image[len(s.Image)-72:len(s.Image)-64] != "@sha256:" || !dockerObjectID(s.Image[len(s.Image)-64:]) || !custodyToken(s.Startup.BindingID) || !dockerObjectID(s.Startup.BindingDigest) {
			return managed.ErrInvalid
		}

		for _, d := range []managed.Decimal{s.CPUMillis, s.MemoryMiB, s.PIDLimit, s.Startup.BindingGeneration} {
			v, err := d.Uint64()
			if err != nil || v == 0 || v > 1<<63-1 {
				return managed.ErrInvalid
			}
		}

		cpu, _ := s.CPUMillis.Uint64()

		memory, _ := s.MemoryMiB.Uint64()
		if cpu > (1<<63-1)/1000000 || memory > (1<<63-1)/1048576 {
			return managed.ErrCapacity
		}
	case managed.StepStartContainer, managed.StepRemoveContainer, managed.StepRemoveNetwork:
		if !dockerObjectID(c.ResourceID) || c.Name != "" || c.NetworkID != "" || c.Service != nil {
			return managed.ErrInvalid
		}
	default:
		return managed.ErrUnsupported
	}

	return nil
}

// Prepare retains authenticated original inputs without granting a send. Exact replay
// succeeds; any altered command, permission, receipt or enrollment association refuses.
func (e *ManagedDockerExecutor) Prepare(ctx context.Context, request ManagedDockerRequest) (ManagedDockerRecord, error) {
	request = custodyClone(request)
	if err := e.validate(ctx, request); err != nil {
		return ManagedDockerRecord{}, err
	}

	record := ManagedDockerRecord{SchemaVersion: "1", Request: request, State: "prepared"}

	raw, err := custodyBytes(record)
	if err != nil {
		return ManagedDockerRecord{}, err
	}

	_, err = e.journal.CompareAndSwap(ctx, custodyKey(request.Permission), nil, raw)
	if err != nil {
		return ManagedDockerRecord{}, err
	}

	return e.read(ctx, request)
}

func (e *ManagedDockerExecutor) read(ctx context.Context, request ManagedDockerRequest) (ManagedDockerRecord, error) {
	raw, err := e.journal.Load(ctx, custodyKey(request.Permission))
	if err != nil {
		return ManagedDockerRecord{}, err
	}

	if len(raw) == 0 {
		return ManagedDockerRecord{}, managed.ErrNotFound
	}

	var record ManagedDockerRecord
	if len(raw) > ManagedDockerJournalLimit || json.Unmarshal(raw, &record) != nil || record.SchemaVersion != "1" {
		return record, managed.ErrInvalid
	}

	encoded, err := custodyBytes(record)
	if err != nil || !bytes.Equal(raw, encoded) || !reflect.DeepEqual(record.Request, request) {
		return ManagedDockerRecord{}, managed.ErrConflict
	}

	if err = e.validate(ctx, record.Request); err != nil {
		return ManagedDockerRecord{}, err
	}

	if record.State != "closed" && record.Closure != nil {
		return ManagedDockerRecord{}, managed.ErrInvalid
	}

	if record.Closure != nil {
		if err = e.verifyClosure(ctx, record, *record.Closure); err != nil {
			return ManagedDockerRecord{}, err
		}
	}

	switch record.State {
	case "prepared", "delegated", "closed":
		if record.Response != nil || record.Custody != nil {
			return ManagedDockerRecord{}, managed.ErrInvalid
		}
	case "observed", "complete":
		if record.Response == nil || !dockerObjectID(record.Response.ResourceID) || record.Response.ObservedAt.Before(request.Permission.IssuedAt) {
			return ManagedDockerRecord{}, managed.ErrInvalid
		}

		if request.Command.ResourceID != "" && request.Command.ResourceID != record.Response.ResourceID {
			return ManagedDockerRecord{}, managed.ErrInvalid
		}

		if record.State == "observed" && record.Custody != nil {
			return ManagedDockerRecord{}, managed.ErrInvalid
		}

		if record.State == "complete" {
			if err = e.verifyCustody(ctx, record); err != nil {
				return ManagedDockerRecord{}, err
			}
		}
	default:
		return ManagedDockerRecord{}, managed.ErrInvalid
	}

	return record, ctx.Err()
}

func (e *ManagedDockerExecutor) verifyCustody(ctx context.Context, r ManagedDockerRecord) error {
	c := r.Custody
	if r.Response == nil || r.Response.ObservedAt.Before(r.Request.Permission.IssuedAt) || c == nil || c.ResourceID != r.Response.ResourceID || !custodyToken(c.Incarnation) || len(c.Attestation) == 0 || len(c.Attestation) > managed.MaxProviderEffectAttestation || r.Request.Command.Kind == managed.StepStartContainer && c.OriginalProcess == nil {
		return managed.ErrInvalid
	}

	if r.Request.Binding != nil {
		command, err := managed.DecodeProviderEffectCommand(r.Request.Binding.Command)
		if err != nil || command.Incarnation != c.Incarnation {
			return managed.ErrAuthority
		}
	}

	return e.authority.VerifyCustody(ctx, custodyClone(r.Request), *r.Response, custodyClone(*c))
}

func (e *ManagedDockerExecutor) swap(ctx context.Context, old, next ManagedDockerRecord) (bool, error) {
	oldRaw, err := custodyBytes(old)
	if err != nil {
		return false, err
	}

	nextRaw, err := custodyBytes(next)
	if err != nil {
		return false, err
	}

	return e.journal.CompareAndSwap(ctx, custodyKey(old.Request.Permission), oldRaw, nextRaw)
}

// Execute claims durable delegation before calling transport once. Replay of delegated
// refuses, even when the earlier call may have failed before reaching Docker.
func (e *ManagedDockerExecutor) Execute(ctx context.Context, request ManagedDockerRequest) (ManagedDockerRecord, error) {
	record, err := e.Prepare(ctx, request)
	if err != nil {
		return record, err
	}

	switch record.State {
	case "complete":
		return record, nil
	case "observed":
		return e.finish(ctx, record)
	case "prepared":
	default:
		return record, managed.ErrUnavailable
	}

	if err = e.authority.Admit(ctx, custodyClone(record.Request)); err != nil {
		return record, err
	}

	if err = ctx.Err(); err != nil {
		return record, err
	}

	delegated := record
	delegated.State = "delegated"

	ok, err := e.swap(ctx, record, delegated)
	if err != nil {
		return ManagedDockerRecord{}, err
	}

	if !ok {
		return ManagedDockerRecord{}, managed.ErrConflict
	}

	resourceID, err := e.transport.Execute(ctx, custodyClone(record.Request.Command))
	if err != nil {
		return delegated, err
	}

	if !dockerObjectID(resourceID) || record.Request.Command.ResourceID != "" && resourceID != record.Request.Command.ResourceID {
		return delegated, managed.ErrInvalid
	}

	observed := delegated
	observed.State = "observed"
	observed.Response = &ManagedDockerResponse{ResourceID: resourceID, ObservedAt: time.Now().UTC()}

	ok, err = e.swap(ctx, delegated, observed)
	if err != nil {
		return delegated, err
	}

	if !ok {
		return delegated, managed.ErrConflict
	}

	return e.finish(ctx, observed)
}

func (e *ManagedDockerExecutor) finish(ctx context.Context, record ManagedDockerRecord) (ManagedDockerRecord, error) {
	custody, err := e.authority.SealResponse(ctx, custodyClone(record.Request), *record.Response)
	if err != nil {
		return record, err
	}

	custody = custodyClone(custody)
	completed := record
	completed.State = "complete"

	completed.Custody = &custody
	if err = e.verifyCustody(ctx, completed); err != nil {
		return record, err
	}

	ok, err := e.swap(ctx, record, completed)
	if err != nil {
		return record, err
	}

	if !ok {
		return e.read(ctx, record.Request)
	}

	return custodyClone(completed), nil
}

// Read observes exact retained facts without producing a delegation grant.
func (e *ManagedDockerExecutor) Read(ctx context.Context, request ManagedDockerRequest) (ManagedDockerRecord, error) {
	return e.read(ctx, custodyClone(request))
}

// RecoverProviderEffectBinding only returns evidence retained before the transport.
func (e *ManagedDockerExecutor) RecoverProviderEffectBinding(ctx context.Context, original managed.ProviderEffectOriginal) (managed.ProviderEffectBindingFacts, error) {
	if err := managed.ValidateProviderEffectOriginal(original); err != nil {
		return managed.ProviderEffectBindingFacts{}, err
	}

	raw, err := e.journal.Load(ctx, custodyKey(original.Permission))
	if err != nil {
		return managed.ProviderEffectBindingFacts{}, err
	}

	var record ManagedDockerRecord

	if len(raw) == 0 {
		return managed.ProviderEffectBindingFacts{}, managed.ErrUnsupported
	}

	if len(raw) > ManagedDockerJournalLimit || json.Unmarshal(raw, &record) != nil {
		return managed.ProviderEffectBindingFacts{}, managed.ErrInvalid
	}

	record, err = e.read(ctx, record.Request)
	if err != nil {
		return managed.ProviderEffectBindingFacts{}, err
	}

	if record.Request.Binding == nil || !reflect.DeepEqual(record.Request.Binding.Original, original) {
		return managed.ProviderEffectBindingFacts{}, managed.ErrConflict
	}

	return custodyClone(*record.Request.Binding), nil
}

// WitnessManagedProviderNoDispatch closes prepared custody by the same CAS used by
// Execute. Once delegated, neither absence nor errors can produce this witness.
func (e *ManagedDockerExecutor) WitnessManagedProviderNoDispatch(ctx context.Context, key managed.ProviderInvocationKey) (managed.ProviderInvocationFailure, error) {
	permission := managed.WireProviderStepPermission{Step: key.Step, CommandID: key.CommandID}

	raw, err := e.journal.Load(ctx, custodyKey(permission))
	if err != nil {
		return managed.ProviderInvocationFailure{}, err
	}

	var record ManagedDockerRecord

	if len(raw) == 0 {
		return managed.ProviderInvocationFailure{}, managed.ErrUnsupported
	}

	if len(raw) > ManagedDockerJournalLimit || json.Unmarshal(raw, &record) != nil {
		return managed.ProviderInvocationFailure{}, managed.ErrInvalid
	}

	record, err = e.read(ctx, record.Request)
	if err != nil {
		return managed.ProviderInvocationFailure{}, err
	}

	if record.Request.Invocation == nil || record.Request.Invocation.Key != key {
		return managed.ProviderInvocationFailure{}, managed.ErrConflict
	}

	if record.State == "prepared" {
		closed := record
		closed.State = "closed"

		ok, swapErr := e.swap(ctx, record, closed)
		if swapErr != nil {
			return managed.ProviderInvocationFailure{}, swapErr
		}

		if !ok {
			return managed.ProviderInvocationFailure{}, managed.ErrConflict
		}

		record = closed
	}

	if record.State != "closed" {
		return managed.ProviderInvocationFailure{}, managed.ErrUnavailable
	}

	if record.Closure != nil {
		return custodyClone(*record.Closure), nil
	}

	closedRaw, _ := custodyBytes(record)

	witness, err := e.authority.NoDispatch(ctx, custodyClone(record.Request), managed.Digest(closedRaw))
	if err != nil {
		return managed.ProviderInvocationFailure{}, err
	}

	witness = custodyClone(witness)
	if err = e.verifyClosure(ctx, record, witness); err != nil {
		return managed.ProviderInvocationFailure{}, err
	}

	witnessed := record
	witnessed.Closure = &witness

	ok, err := e.swap(ctx, record, witnessed)
	if err != nil {
		return managed.ProviderInvocationFailure{}, err
	}

	if !ok {
		retained, readErr := e.read(ctx, record.Request)
		if readErr != nil {
			return managed.ProviderInvocationFailure{}, readErr
		}

		if retained.Closure == nil {
			return managed.ProviderInvocationFailure{}, managed.ErrConflict
		}

		return custodyClone(*retained.Closure), nil
	}

	return custodyClone(witness), ctx.Err()
}

func (e *ManagedDockerExecutor) verifyClosure(ctx context.Context, record ManagedDockerRecord, witness managed.ProviderInvocationFailure) error {
	if record.Request.Invocation == nil || record.Request.Binding == nil {
		return managed.ErrInvalid
	}

	p, _ := record.Request.Permission.Value()
	i := record.Request.Invocation
	key := i.Key

	permissionRaw, err := json.Marshal(p)
	if err != nil {
		return managed.ErrInvalid
	}

	permissionDigest := managed.Digest(permissionRaw)
	if witness.Key != key || witness.RootDigest != i.RootDigest || witness.PermissionDigest != permissionDigest || !reflect.DeepEqual(witness.Failure.Permission, p) || witness.Failure.AttemptRef != record.Request.Binding.Original.AttemptRef || witness.Failure.InvocationID != key.CommandID || witness.Failure.SchemaVersion != "1" || witness.Failure.ProducerProfile != managed.LocalNoDispatchProfile || witness.Failure.Class != "local_no_dispatch" || witness.Failure.Phase != "before_transport_delegate" || !custodyToken(witness.Failure.VerifierID) || !custodyToken(witness.Failure.Reason) || witness.Failure.ObservedAt.Before(p.IssuedAt) || len(witness.Failure.Witness) == 0 || len(witness.Failure.Witness) > managed.MaxProviderEffectAttestation || witness.Failure.WitnessDigest != managed.Digest(witness.Failure.Witness) {
		return managed.ErrInvalid
	}

	record.Closure = nil

	closedRaw, err := custodyBytes(record)
	if err != nil {
		return err
	}

	return e.authority.VerifyNoDispatch(ctx, custodyClone(record.Request), managed.Digest(closedRaw), custodyClone(witness))
}
