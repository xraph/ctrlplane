package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/xraph/ctrlplane/id"
)

// Quiescence bounds apply to actual typed JSON, including attestation base64 expansion.
const (
	MaxQuiescenceRecord      = 16384
	MaxQuiescenceIndex       = 1024
	MaxQuiescenceAttestation = 4096
	QuiescenceHistory        = "quiescence_observation"
	QuiescenceCurrent        = "quiescence_current"
)

// QuiescenceReference selects exact original evidence and its immutable process scope.
type QuiescenceReference struct {
	Schema                      string               `json:"schema"`
	TargetIdentityDigest        string               `json:"target_identity_digest"`
	ReservationID               id.ID                `json:"reservation_id"`
	Member                      string               `json:"member"`
	Role                        RuntimeRole          `json:"role"`
	RegistrationOperationID     id.ID                `json:"registration_operation_id"`
	RegistrationReferenceDigest string               `json:"registration_reference_digest"`
	RegistrationMaterialDigest  string               `json:"registration_material_digest"`
	DrainOperationID            id.ID                `json:"drain_operation_id"`
	DrainReferenceDigest        string               `json:"drain_reference_digest"`
	DrainMaterialDigest         string               `json:"drain_material_digest"`
	Process                     OriginalProcessFacts `json:"process"`
	OperationID                 string               `json:"operation_id"`
	Deadline                    time.Time            `json:"deadline"`
	HandleDigest                string               `json:"handle_digest"`
}

// NewQuiescenceReference derives a leaf reference from previously retained originals.
// The store independently resolves both rows and verifies authoritative associations.
func NewQuiescenceReference(registration, drain OriginalLifecycleRecord) (QuiescenceReference, error) {
	var out QuiescenceReference

	r, d := registration.Material, drain.Material
	if r.Kind != OriginalRegistration || d.Kind != OriginalDrain || r.Registration == nil || d.Drain == nil || r.Command.Key != d.Command.Key || r.TargetIdentityDigest != d.TargetIdentityDigest || r.Registration.Process != d.Drain.Process {
		return out, ErrAuthority
	}

	rr, err := OriginalReference(r)
	if err != nil {
		return out, err
	}

	dr, err := OriginalReference(d)
	if err != nil {
		return out, err
	}

	if rr != registration.Reference || dr != drain.Reference {
		return out, ErrInvalid
	}

	rb, err := rr.Bytes()
	if err != nil {
		return out, err
	}

	db, err := dr.Bytes()
	if err != nil {
		return out, err
	}

	handle, err := OriginalDrainHandleDigest(r.TargetIdentityDigest, *d.Drain)
	if err != nil {
		return out, err
	}

	out = QuiescenceReference{"ctrlplane.quiescence-reference.v1", r.TargetIdentityDigest, r.Command.ReservationID, r.Registration.Member, r.Registration.Role, r.Command.ID, Digest(rb), rr.MaterialDigest, d.Command.ID, Digest(db), dr.MaterialDigest, r.Registration.Process, d.Drain.OperationID, d.Drain.Deadline.UTC(), handle}

	return out, nil
}

func (r QuiescenceReference) valid() bool {
	if r.Schema != "ctrlplane.quiescence-reference.v1" || !originalID(r.ReservationID, PrefixReservation) || !originalID(r.RegistrationOperationID, PrefixOperation) || !originalID(r.DrainOperationID, PrefixOperation) || !originalToken(r.Member) || (r.Role != Polling && r.Role != RetainedQuery) || !originalProcess(r.Process) || !originalToken(r.OperationID) || !originalTime(r.Deadline) {
		return false
	}

	for _, value := range []string{r.TargetIdentityDigest, r.RegistrationReferenceDigest, r.RegistrationMaterialDigest, r.DrainReferenceDigest, r.DrainMaterialDigest, r.HandleDigest} {
		if !hexDigest(value) {
			return false
		}
	}

	h, err := OriginalDrainHandleDigest(r.TargetIdentityDigest, OriginalDrainFacts{Process: r.Process, OperationID: r.OperationID, Deadline: r.Deadline})

	return err == nil && h == r.HandleDigest
}

// QuiescenceLeaf is the complete value covered by the producer's authenticated challenge.
// Native completion has no timestamp; ObservedAt remains the exact source observation time.
type QuiescenceLeaf struct {
	Reference       QuiescenceReference `json:"reference"`
	ObservedAt      time.Time           `json:"observed_at"`
	ValidUntil      time.Time           `json:"valid_until"`
	AdmissionClosed bool                `json:"admission_closed"`
	Quiescent       bool                `json:"quiescent"`
	Complete        bool                `json:"complete"`
	DeadlineExpired bool                `json:"deadline_expired"`
	ErrorClass      string              `json:"error_class"`
	ActiveClaims    Decimal             `json:"active_claims"`
	UnknownClaims   Decimal             `json:"unknown_claims"`
	ObservationID   string              `json:"observation_id"`
	ChallengeID     string              `json:"challenge_id"`
	VerifierID      string              `json:"verifier_id"`
}

// QuiescenceObservation carries opaque authenticated attestation over the whole leaf.
// The configured adapter authenticates this value before storage admission.
type QuiescenceObservation struct {
	QuiescenceLeaf

	ChallengeDigest string `json:"challenge_digest"`
	Attestation     []byte `json:"attestation"`
}

// QuiescenceObservationRequest challenges an exact original association.
type QuiescenceObservationRequest struct {
	Reference   QuiescenceReference `json:"reference"`
	ChallengeID string              `json:"challenge_id"`
}

// QuiescenceObservationPort is an optional authenticated native observation producer.
// Call it before entering any store authority transaction.
type QuiescenceObservationPort interface {
	ObserveQuiescence(ctx context.Context, request QuiescenceObservationRequest) (QuiescenceObservation, error)
}

// QuiescenceAcceptanceRequest binds an immutable observation to the parent CAS.
type QuiescenceAcceptanceRequest struct {
	Command     OriginalCommand       `json:"command"`
	Observation QuiescenceObservation `json:"observation"`
}

// QuiescenceKey selects immutable history in one exact reservation scope.
type QuiescenceKey struct {
	Key

	ReservationID id.ID `json:"reservation_id"`
	OperationID   id.ID `json:"operation_id"`
}

// QuiescenceRecord retains one request snapshot and its original local acceptance.
type QuiescenceRecord struct {
	Request    QuiescenceAcceptanceRequest `json:"request"`
	VerifiedAt time.Time                   `json:"verified_at"`
	Receipt    OriginalLocalReceipt        `json:"receipt"`
}

// QuiescenceIndex binds the current source time to the exact immutable record bytes.
type QuiescenceIndex struct {
	Schema        string    `json:"schema"`
	ReservationID id.ID     `json:"reservation_id"`
	OperationID   id.ID     `json:"operation_id"`
	ObservedAt    time.Time `json:"observed_at"`
	RecordDigest  string    `json:"record_digest"`
}

// QuiescenceScope hashes the typed target key and reservation, in that field order.
func QuiescenceScope(k Key, reservationID id.ID) (string, error) {
	if !originalToken(k.TenantID) || !originalID(k.InstanceID, id.PrefixInstance) || !originalID(reservationID, PrefixReservation) {
		return "", ErrInvalid
	}

	return originalHash("ctrlplane.managed.quiescence-scope.v1", struct {
		Key           Key   `json:"key"`
		ReservationID id.ID `json:"reservation_id"`
	}{k, reservationID})
}

// QuiescenceChallengeDigest hashes every typed leaf field, excluding the attestation.
func QuiescenceChallengeDigest(leaf QuiescenceLeaf) (string, error) {
	return originalHash("ctrlplane.managed.quiescence-challenge.v1", leaf)
}

// Normalize checks finite terminal outcomes and the fixed UTC timestamp profile.
func (o QuiescenceObservation) Normalize() (QuiescenceObservation, error) {
	if len(o.Attestation) > MaxQuiescenceAttestation {
		return o, ErrCapacity
	}

	if !o.Reference.valid() || !originalTime(o.ObservedAt) || !originalTime(o.ValidUntil) || !o.ValidUntil.After(o.ObservedAt) || o.ValidUntil.Sub(o.ObservedAt) > time.Minute || !o.AdmissionClosed || !o.Quiescent || o.ActiveClaims != "0" || o.UnknownClaims != "0" || !originalToken(o.ObservationID) || !originalToken(o.ChallengeID) || !originalToken(o.VerifierID) || len(o.Attestation) == 0 {
		return o, ErrInvalid
	}

	if (!o.Complete || o.DeadlineExpired || o.ErrorClass != "") && (o.Complete || !o.DeadlineExpired || o.ErrorClass != "incomplete") {
		return o, ErrInvalid
	}

	o.Reference.Deadline = o.Reference.Deadline.UTC()
	o.ObservedAt = o.ObservedAt.UTC()
	o.ValidUntil = o.ValidUntil.UTC()

	expected, err := QuiescenceChallengeDigest(o.QuiescenceLeaf)
	if err != nil {
		return o, err
	}

	if expected != o.ChallengeDigest {
		return o, ErrInvalid
	}

	return o, nil
}

// Bytes validates a retained record and bounds its actual JSON encoding.
func (r QuiescenceRecord) Bytes() ([]byte, error) {
	if _, err := r.Request.Command.LocalCommand(); err != nil {
		return nil, err
	}

	o, err := r.Request.Observation.Normalize()
	if err != nil {
		return nil, err
	}

	r.Request.Observation = o

	c := r.Request.Command
	if c.ReservationID != o.Reference.ReservationID || !originalTime(r.VerifiedAt) || r.VerifiedAt.Before(o.ObservedAt) || !r.VerifiedAt.Before(o.ValidUntil) || r.Receipt.ID != c.ID || !hexDigest(r.Receipt.Digest) || !originalPositive(r.Receipt.Revision) || !originalPositive(r.Receipt.LeaseEpoch) || !r.Receipt.AcceptedAt.Equal(r.VerifiedAt) || !originalTime(r.Receipt.LeaseUntil) {
		return nil, ErrInvalid
	}

	r.VerifiedAt = r.VerifiedAt.UTC()
	r.Receipt.AcceptedAt = r.Receipt.AcceptedAt.UTC()
	r.Receipt.LeaseUntil = r.Receipt.LeaseUntil.UTC()

	raw, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}

	if len(raw) > MaxQuiescenceRecord {
		return nil, ErrCapacity
	}

	return raw, nil
}

// ParseQuiescenceRecord requires strict typed decoding and an accepted-byte round trip.
func ParseQuiescenceRecord(raw []byte) (QuiescenceRecord, error) {
	var r QuiescenceRecord
	if len(raw) > MaxQuiescenceRecord {
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

// QuiescenceRecordDigest hashes the entire final record, including the saved local receipt.
func QuiescenceRecordDigest(r QuiescenceRecord) (string, error) {
	raw, err := r.Bytes()
	if err != nil {
		return "", err
	}

	return Digest(append([]byte("ctrlplane.managed.quiescence-record.v1\n"), raw...)), nil
}

// Bytes returns the bounded canonical current-index encoding.
func (i QuiescenceIndex) Bytes() ([]byte, error) {
	if i.Schema != "ctrlplane.quiescence-current.v1" || !originalID(i.ReservationID, PrefixReservation) || !originalID(i.OperationID, PrefixOperation) || !originalTime(i.ObservedAt) || !hexDigest(i.RecordDigest) {
		return nil, ErrInvalid
	}

	i.ObservedAt = i.ObservedAt.UTC()

	raw, err := json.Marshal(i)
	if err != nil {
		return nil, err
	}

	if len(raw) > MaxQuiescenceIndex {
		return nil, ErrCapacity
	}

	return raw, nil
}

// ParseQuiescenceIndex refuses ambiguous or noncanonical current pointers.
func ParseQuiescenceIndex(raw []byte) (QuiescenceIndex, error) {
	var i QuiescenceIndex
	if len(raw) > MaxQuiescenceIndex {
		return i, ErrCapacity
	}

	if err := strictObject(raw, &i); err != nil {
		return i, err
	}

	encoded, err := i.Bytes()
	if err != nil {
		return i, err
	}

	if !bytes.Equal(raw, encoded) {
		return i, ErrInvalid
	}

	return i, nil
}
