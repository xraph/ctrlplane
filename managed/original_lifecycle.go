package managed

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/xraph/ctrlplane/id"
)

// Original lifecycle bounds apply to actual JSON bytes, including base64 expansion.
const (
	MaxOriginalLifecycleRecord    = 32768
	MaxOriginalLifecycleReference = 1024
	MaxOriginalNativeReceipt      = 4096
	OriginalRegistration          = "original_registration"
	OriginalDrain                 = "original_drain"
)

// OriginalCommand retains the parent CAS using canonical decimal wire counters.
type OriginalCommand struct {
	Key

	ID            id.ID   `json:"id"`
	Revision      Decimal `json:"revision"`
	LeaseEpoch    Decimal `json:"lease_epoch"`
	Owner         string  `json:"owner"`
	ReservationID id.ID   `json:"reservation_id"`
}

// LocalCommand validates the signed parent counter domain without changing A's codec.
func (c OriginalCommand) LocalCommand() (Command, error) {
	revision, e1 := c.Revision.Uint64()

	epoch, e2 := c.LeaseEpoch.Uint64()
	if e1 != nil || e2 != nil || revision == 0 || revision >= math.MaxInt64 || epoch == 0 || epoch > math.MaxInt64 || !originalToken(c.TenantID) || !originalID(c.InstanceID, id.PrefixInstance) || !originalID(c.ID, PrefixOperation) || !originalID(c.ReservationID, PrefixReservation) || !originalToken(c.Owner) {
		return Command{}, ErrInvalid
	}

	return Command{Key: c.Key, ID: c.ID, Revision: revision, LeaseEpoch: epoch, Owner: c.Owner}, nil
}

// OriginalProcessFacts binds the native process to an immutable startup descriptor.
// ConfigurationVersion is an opaque native token, not a numeric counter.
type OriginalProcessFacts struct {
	Installation             string            `json:"installation"`
	Namespace                string            `json:"namespace"`
	Build                    string            `json:"build"`
	Queue                    string            `json:"queue"`
	RuntimeID                string            `json:"runtime_id"`
	InstanceID               string            `json:"instance_id"`
	HostID                   string            `json:"host_id"`
	IdentityVersion          Decimal           `json:"identity_version"`
	ArtifactDigest           string            `json:"artifact_digest"`
	ConfigurationDigest      string            `json:"configuration_digest"`
	ConfigurationVersion     string            `json:"configuration_version"`
	EnrollmentEvidenceDigest string            `json:"enrollment_evidence_digest"`
	ProbePolicyID            string            `json:"probe_policy_id"`
	ProbePolicyVersion       Decimal           `json:"probe_policy_version"`
	MaximumProofValidityNS   Decimal           `json:"maximum_proof_validity_ns"`
	VerifierID               string            `json:"verifier_id"`
	Descriptor               StartupBindingRef `json:"descriptor"`
	ProducerVerifier         string            `json:"producer_verifier"`
}

// OriginalNativeAcceptance keeps the native request and operator digest domains distinct.
type OriginalNativeAcceptance struct {
	Operation       string    `json:"operation"`
	RequestID       string    `json:"request_id"`
	RequestDigest   string    `json:"request_digest"`
	CommandDigest   string    `json:"command_digest"`
	ResponseVersion Decimal   `json:"response_version"`
	DeliveryID      string    `json:"delivery_id"`
	AcceptedAt      time.Time `json:"accepted_at"`
}

// OriginalRegistrationFacts describes only the originally accepted initial binding.
// The exact reservation is retained once in the material's OriginalCommand.
type OriginalRegistrationFacts struct {
	Process             OriginalProcessFacts     `json:"process"`
	Native              OriginalNativeAcceptance `json:"native"`
	Member              string                   `json:"member"`
	Role                RuntimeRole              `json:"role"`
	StoredCommandDigest string                   `json:"stored_command_digest"`
	BindingVersion      Decimal                  `json:"binding_version"`
	BindingState        string                   `json:"binding_state"`
	RemovalEpoch        Decimal                  `json:"removal_epoch"`
	CreatedAt           time.Time                `json:"created_at"`
	ChangedAt           time.Time                `json:"changed_at"`
}

// OriginalDrainFacts retains process acceptance and permanent admission closure.
// Native control identifies its handle solely by runtime, operation and deadline.
type OriginalDrainFacts struct {
	Process         OriginalProcessFacts     `json:"process"`
	Native          OriginalNativeAcceptance `json:"native"`
	OperationID     string                   `json:"operation_id"`
	Deadline        time.Time                `json:"deadline"`
	ProcessAccepted bool                     `json:"process_accepted"`
	AdmissionClosed bool                     `json:"admission_closed"`
	ObservedAt      time.Time                `json:"observed_at"`
}

// OriginalRegistrationAcceptanceRequest supplies validated leaf facts and opaque export bytes.
// The caller authenticates and validates the native export before using this capability.
type OriginalRegistrationAcceptanceRequest struct {
	Command             OriginalCommand           `json:"command"`
	Facts               OriginalRegistrationFacts `json:"facts"`
	NativeReceipt       []byte                    `json:"native_receipt"`
	NativeReceiptDigest string                    `json:"native_receipt_digest"`
}

// OriginalDrainAcceptanceRequest records acceptance only, without terminal outcome fields.
type OriginalDrainAcceptanceRequest struct {
	Command             OriginalCommand    `json:"command"`
	Facts               OriginalDrainFacts `json:"facts"`
	NativeReceipt       []byte             `json:"native_receipt"`
	NativeReceiptDigest string             `json:"native_receipt_digest"`
}

// OriginalLifecycleKey selects an exact original operation in a canonical target scope.
type OriginalLifecycleKey struct {
	Key

	Kind        string `json:"kind"`
	OperationID id.ID  `json:"operation_id"`
}

// OriginalLifecycleMaterial is the sole immutable material snapshot in a retained row.
// Material hashing excludes the computed reference and local receipt.
type OriginalLifecycleMaterial struct {
	Command              OriginalCommand            `json:"command"`
	Kind                 string                     `json:"kind"`
	TargetIdentityDigest string                     `json:"target_identity_digest"`
	Registration         *OriginalRegistrationFacts `json:"registration,omitempty"`
	Drain                *OriginalDrainFacts        `json:"drain,omitempty"`
	NativeReceipt        []byte                     `json:"native_receipt"`
	NativeReceiptDigest  string                     `json:"native_receipt_digest"`
}

// OriginalLifecycleReference is the canonical compact association saved in A's evidence.
type OriginalLifecycleReference struct {
	Schema               string `json:"schema"`
	Kind                 string `json:"kind"`
	OperationID          id.ID  `json:"operation_id"`
	TargetIdentityDigest string `json:"target_identity_digest"`
	FactsDigest          string `json:"facts_digest"`
	NativeReceiptDigest  string `json:"native_receipt_digest"`
	MaterialDigest       string `json:"material_digest"`
}

// OriginalLocalReceipt projects the unchanged A receipt into decimal adapter-facing counters.
type OriginalLocalReceipt struct {
	ID         id.ID     `json:"id"`
	Digest     string    `json:"digest"`
	Revision   Decimal   `json:"revision"`
	LeaseEpoch Decimal   `json:"lease_epoch"`
	AcceptedAt time.Time `json:"accepted_at"`
	LeaseUntil time.Time `json:"lease_until"`
}

// OriginalLifecycleRecord retains material, its computed reference and the original local result.
type OriginalLifecycleRecord struct {
	Material  OriginalLifecycleMaterial  `json:"material"`
	Reference OriginalLifecycleReference `json:"reference"`
	Receipt   OriginalLocalReceipt       `json:"receipt"`
}

// OriginalReceipt projects a saved local receipt without changing its timestamps or digest.
func OriginalReceipt(r Receipt) OriginalLocalReceipt {
	return OriginalLocalReceipt{r.ID, r.Digest, Decimal(strconv.FormatUint(r.Revision, 10)), Decimal(strconv.FormatUint(r.LeaseEpoch, 10)), r.AcceptedAt.UTC(), r.LeaseUntil.UTC()}
}

func originalID(value id.ID, prefix id.Prefix) bool {
	return value.Prefix() == prefix && len(value.String()) <= 64
}
func originalToken(s string) bool {
	if len(s) == 0 || len(s) > 256 {
		return false
	}

	for _, c := range s {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.:/@+=", c) {
			return false
		}
	}

	return true
}
func originalTime(t time.Time) bool { return !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999 }
func originalPositive(d Decimal) bool {
	n, e := d.Uint64()

	return e == nil && n > 0 && n <= math.MaxInt64
}

func originalProcess(p OriginalProcessFacts) bool {
	for _, s := range []string{p.Installation, p.Namespace, p.Build, p.Queue, p.RuntimeID, p.InstanceID, p.HostID, p.ConfigurationVersion, p.ProbePolicyID, p.VerifierID, p.Descriptor.BindingID, p.ProducerVerifier} {
		if !originalToken(s) {
			return false
		}
	}

	for _, s := range []string{p.ArtifactDigest, p.ConfigurationDigest, p.EnrollmentEvidenceDigest, p.Descriptor.BindingDigest} {
		if !hexDigest(s) {
			return false
		}
	}

	return p.IdentityVersion == "1" && originalPositive(p.ProbePolicyVersion) && originalPositive(p.MaximumProofValidityNS) && originalPositive(p.Descriptor.BindingGeneration)
}
func originalNative(n OriginalNativeAcceptance, operation string) bool {
	return n.Operation == operation && originalToken(n.RequestID) && hexDigest(n.RequestDigest) && hexDigest(n.CommandDigest) && n.ResponseVersion == "1" && originalToken(n.DeliveryID) && originalTime(n.AcceptedAt)
}

// Normalize validates leaf shape and normalizes the timestamp codec to UTC.
// It does not authenticate producers or interpret the native receipt schema.
func (m OriginalLifecycleMaterial) Normalize() (OriginalLifecycleMaterial, error) {
	if _, err := m.Command.LocalCommand(); err != nil {
		return m, err
	}

	if len(m.NativeReceipt) > MaxOriginalNativeReceipt {
		return m, ErrCapacity
	}

	if len(m.NativeReceipt) == 0 || !hexDigest(m.NativeReceiptDigest) || Digest(m.NativeReceipt) != m.NativeReceiptDigest || !hexDigest(m.TargetIdentityDigest) {
		return m, ErrInvalid
	}

	switch m.Kind {
	case OriginalRegistration:
		if m.Registration == nil || m.Drain != nil {
			return m, ErrInvalid
		}

		f := *m.Registration
		if !originalProcess(f.Process) || !originalNative(f.Native, "query_runtime.register") || !originalToken(f.Member) || (f.Role != Polling && f.Role != RetainedQuery) || !hexDigest(f.StoredCommandDigest) || f.BindingVersion != "1" || f.BindingState != "active" || f.RemovalEpoch != "0" || !originalTime(f.CreatedAt) || !f.CreatedAt.Equal(f.ChangedAt) || f.CreatedAt.After(f.Native.AcceptedAt) {
			return m, ErrInvalid
		}

		f.Native.AcceptedAt = f.Native.AcceptedAt.UTC()
		f.CreatedAt = f.CreatedAt.UTC()
		f.ChangedAt = f.ChangedAt.UTC()
		m.Registration = &f
	case OriginalDrain:
		if m.Drain == nil || m.Registration != nil {
			return m, ErrInvalid
		}

		f := *m.Drain
		if !originalProcess(f.Process) || !originalNative(f.Native, "worker.drain.request") || !originalToken(f.OperationID) || !originalTime(f.Deadline) || !originalTime(f.ObservedAt) || !f.Deadline.After(f.Native.AcceptedAt) || f.ObservedAt.Before(f.Native.AcceptedAt) || !f.ProcessAccepted || !f.AdmissionClosed {
			return m, ErrInvalid
		}

		f.Native.AcceptedAt = f.Native.AcceptedAt.UTC()
		f.Deadline = f.Deadline.UTC()
		f.ObservedAt = f.ObservedAt.UTC()
		m.Drain = &f
	default:
		return m, ErrInvalid
	}

	return m, nil
}

func originalHash(domain string, value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}

	return Digest(append([]byte(domain+"\n"), raw...)), nil
}

// OriginalIdentityDigest hashes the complete immutable Identity in fixed field order.
// Only manifest.version is projected to decimal; the spec bytes remain opaque and exact.
func OriginalIdentityDigest(i Identity) (string, error) {
	type manifest struct {
		Version    Decimal  `json:"version"`
		Members    []Member `json:"members"`
		Spec       []byte   `json:"spec"`
		SpecDigest string   `json:"spec_digest"`
	}

	return originalHash("ctrlplane.managed.identity.v1", struct {
		Key                  Key      `json:"key"`
		ProvisionOperationID id.ID    `json:"provision_operation_id"`
		Physical             Physical `json:"physical"`
		Manifest             manifest `json:"manifest"`
	}{i.Key, i.ProvisionOperationID, i.Physical, manifest{Decimal(strconv.FormatUint(i.Manifest.Version, 10)), i.Manifest.Members, i.Manifest.Spec, i.Manifest.SpecDigest}})
}

// OriginalReference derives all compact associations from a normalized material snapshot.
func OriginalReference(m OriginalLifecycleMaterial) (OriginalLifecycleReference, error) {
	m, err := m.Normalize()
	if err != nil {
		return OriginalLifecycleReference{}, err
	}

	var facts any = m.Registration
	if m.Kind == OriginalDrain {
		facts = m.Drain
	}

	fd, err := originalHash("ctrlplane.managed.original-facts.v1", struct {
		Kind  string `json:"kind"`
		Facts any    `json:"facts"`
	}{m.Kind, facts})
	if err != nil {
		return OriginalLifecycleReference{}, err
	}

	md, err := originalHash("ctrlplane.managed.original-material.v1", m)
	if err != nil {
		return OriginalLifecycleReference{}, err
	}

	ref := OriginalLifecycleReference{"ctrlplane.original-reference.v1", m.Kind, m.Command.ID, m.TargetIdentityDigest, fd, m.NativeReceiptDigest, md}
	_, err = ref.Bytes()

	return ref, err
}

// Bytes returns the fixed typed reference JSON and enforces its actual byte bound.
func (r OriginalLifecycleReference) Bytes() ([]byte, error) {
	if r.Schema != "ctrlplane.original-reference.v1" || (r.Kind != OriginalRegistration && r.Kind != OriginalDrain) || !originalID(r.OperationID, PrefixOperation) || !hexDigest(r.TargetIdentityDigest) || !hexDigest(r.FactsDigest) || !hexDigest(r.NativeReceiptDigest) || !hexDigest(r.MaterialDigest) {
		return nil, ErrInvalid
	}

	raw, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}

	if len(raw) > MaxOriginalLifecycleReference {
		return nil, ErrCapacity
	}

	return raw, nil
}

// ParseOriginalReference accepts only byte-exact canonical typed JSON.
func ParseOriginalReference(raw []byte) (OriginalLifecycleReference, error) {
	var r OriginalLifecycleReference
	if len(raw) > MaxOriginalLifecycleReference {
		return r, ErrCapacity
	}

	if err := strictObject(raw, &r); err != nil {
		return r, err
	}

	encoded, err := r.Bytes()
	if err != nil {
		return r, err
	}

	if !bytes.Equal(raw, encoded) {
		return r, ErrInvalid
	}

	return r, nil
}

// ParseOriginalLifecycleRecord refuses noncanonical or internally inconsistent retained rows.
func ParseOriginalLifecycleRecord(raw []byte) (OriginalLifecycleRecord, error) {
	var r OriginalLifecycleRecord
	if len(raw) > MaxOriginalLifecycleRecord {
		return r, ErrCapacity
	}

	if err := strictObject(raw, &r); err != nil {
		return r, err
	}

	material, err := r.Material.Normalize()
	if err != nil {
		return r, err
	}

	r.Material = material

	reference, err := OriginalReference(material)
	if err != nil {
		return r, err
	}

	if reference != r.Reference || !originalID(r.Receipt.ID, PrefixOperation) || r.Receipt.ID != material.Command.ID || !hexDigest(r.Receipt.Digest) || !originalPositive(r.Receipt.Revision) || !originalPositive(r.Receipt.LeaseEpoch) || !originalTime(r.Receipt.AcceptedAt) || !originalTime(r.Receipt.LeaseUntil) {
		return r, ErrInvalid
	}

	r.Receipt.AcceptedAt = r.Receipt.AcceptedAt.UTC()
	r.Receipt.LeaseUntil = r.Receipt.LeaseUntil.UTC()

	encoded, err := json.Marshal(r)
	if err != nil {
		return r, err
	}

	if !bytes.Equal(raw, encoded) {
		return r, ErrInvalid
	}

	return r, nil
}

// OriginalDrainHandleDigest hashes the tuple and its complete immutable process scope.
// This is a profile reference, not an additional native handle field.
func OriginalDrainHandleDigest(targetDigest string, f OriginalDrainFacts) (string, error) {
	if !hexDigest(targetDigest) || !originalProcess(f.Process) || !originalToken(f.OperationID) || !originalTime(f.Deadline) {
		return "", ErrInvalid
	}

	return originalHash("ctrlplane.managed.drain-handle.v1", struct {
		TargetIdentityDigest string               `json:"target_identity_digest"`
		Process              OriginalProcessFacts `json:"process"`
		OperationID          string               `json:"operation_id"`
		Deadline             time.Time            `json:"deadline"`
	}{targetDigest, f.Process, f.OperationID, f.Deadline.UTC()})
}
