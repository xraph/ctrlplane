package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/xraph/ctrlplane/id"
)

// WriterFloorSchema identifies protected deployment metadata, not an earned writer protocol.
const WriterFloorSchema = "ctrlplane.managed.writer-floor.v1"

// MaxWriterFloor bounds each complete canonical request and retained activation in bytes.
const MaxWriterFloor = 4096

// ErrWriterFloorCorrupt distinguishes damaged protected metadata from unavailable storage.
var ErrWriterFloorCorrupt = errors.New("managed: protected writer floor is corrupt")

// WriterFloorActivation is deployment administration input, never a runtime permission.
// ExpectedDigest names the exact previous marker; only initial installation leaves it empty.
type WriterFloorActivation struct {
	Schema          string  `json:"schema"`
	AuthorityID     string  `json:"authority_id"`
	MinimumProtocol Decimal `json:"minimum_protocol"`
	Epoch           Decimal `json:"epoch"`
	ActivationID    id.ID   `json:"activation_id"`
	ExpectedDigest  string  `json:"expected_digest"`
}

// WriterFloor is one protected marker and its identical immutable activation history row.
// ActivatedAt is assigned at the administrative transaction callback, never by its request.
// Digest commits the entire record except itself. ActivationDigest commits its request.
type WriterFloor struct {
	Schema           string    `json:"schema"`
	AuthorityID      string    `json:"authority_id"`
	MinimumProtocol  Decimal   `json:"minimum_protocol"`
	Epoch            Decimal   `json:"epoch"`
	ActivationID     id.ID     `json:"activation_id"`
	PreviousDigest   string    `json:"previous_digest"`
	ActivationDigest string    `json:"activation_digest"`
	ActivatedAt      time.Time `json:"activated_at"`
	Digest           string    `json:"digest"`
}

// WriterFloorReference names an exact activation for inspection or external equality checks.
// Possession proves neither admission nor permission to invoke a provider or native command.
type WriterFloorReference struct {
	AuthorityID  string `json:"authority_id"`
	ActivationID id.ID  `json:"activation_id"`
	Digest       string `json:"digest"`
}

// ManagedWriterFloorReader reads the configured authority's protected current activation.
// Implementations validate its identical immutable history row in one read snapshot.
// Missing metadata refuses; historical target reads remain separate and nonissuing.
type ManagedWriterFloorReader interface {
	ReadManagedWriterFloor(ctx context.Context) (WriterFloor, error)
}

// ManagedWriterFloorHistoryReader provides exact inspection without moving current.
// Runtime stores deliberately expose no floor activation or downgrade method here.
type ManagedWriterFloorHistoryReader interface {
	ReadManagedWriterFloorActivation(ctx context.Context, reference WriterFloorReference) (WriterFloor, error)
}

// WriterFloorActivationDigest validates and hashes a bounded canonical activation request.
func WriterFloorActivationDigest(request WriterFloorActivation) (string, error) {
	if request.Schema != WriterFloorSchema {
		return "", ErrUnsupported
	}

	if !originalToken(request.AuthorityID) || !originalID(request.ActivationID, PrefixOperation) || !originalPositive(request.MinimumProtocol) || !originalPositive(request.Epoch) {
		return "", ErrInvalid
	}

	if request.Epoch == "1" && request.ExpectedDigest != "" || request.Epoch != "1" && !hexDigest(request.ExpectedDigest) {
		return "", ErrInvalid
	}

	raw, err := writerFloorBytes(request)
	if err != nil {
		return "", err
	}

	return Digest(append([]byte("ctrlplane.managed.writer-floor-activation.v1\n"), raw...)), nil
}

// EncodeWriterFloorActivation validates administration input without accepting it.
func EncodeWriterFloorActivation(request WriterFloorActivation) ([]byte, error) {
	if _, err := WriterFloorActivationDigest(request); err != nil {
		return nil, err
	}

	return writerFloorBytes(request)
}

// DecodeWriterFloorActivation refuses noncanonical or oversized administration input.
func DecodeWriterFloorActivation(raw []byte) (WriterFloorActivation, error) {
	var request WriterFloorActivation
	if len(raw) == 0 || len(raw) > MaxWriterFloor {
		return request, ErrCapacity
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&request); err != nil {
		return WriterFloorActivation{}, ErrInvalid
	}

	canonical, err := EncodeWriterFloorActivation(request)
	if err != nil {
		return WriterFloorActivation{}, err
	}

	if !bytes.Equal(raw, canonical) {
		return WriterFloorActivation{}, ErrInvalid
	}

	return request, nil
}

// NewWriterFloor seals a request at an authority callback time; it grants no activation.
// The protected store must separately check current CAS, monotonicity and administration.
func NewWriterFloor(request WriterFloorActivation, now time.Time) (WriterFloor, error) {
	hash, err := WriterFloorActivationDigest(request)
	if err != nil {
		return WriterFloor{}, err
	}

	if !originalTime(now) || now.Location() != time.UTC {
		return WriterFloor{}, ErrInvalid
	}

	floor := WriterFloor{Schema: request.Schema, AuthorityID: request.AuthorityID, MinimumProtocol: request.MinimumProtocol, Epoch: request.Epoch, ActivationID: request.ActivationID, PreviousDigest: request.ExpectedDigest, ActivationDigest: hash, ActivatedAt: now}

	floor.Digest, err = writerFloorDigest(floor)
	if err != nil {
		return WriterFloor{}, err
	}

	if _, err = EncodeWriterFloor(floor); err != nil {
		return WriterFloor{}, err
	}

	return floor, nil
}

// Activation returns the exact request retained by this marker.
func (floor WriterFloor) Activation() WriterFloorActivation {
	return WriterFloorActivation{Schema: floor.Schema, AuthorityID: floor.AuthorityID, MinimumProtocol: floor.MinimumProtocol, Epoch: floor.Epoch, ActivationID: floor.ActivationID, ExpectedDigest: floor.PreviousDigest}
}

// Reference returns an inspection identity, never a delegation capability.
func (floor WriterFloor) Reference() WriterFloorReference {
	return WriterFloorReference{AuthorityID: floor.AuthorityID, ActivationID: floor.ActivationID, Digest: floor.Digest}
}

// EncodeWriterFloor validates the schema, signed counters, UTC timestamp and both hashes.
func EncodeWriterFloor(floor WriterFloor) ([]byte, error) {
	activation, err := WriterFloorActivationDigest(floor.Activation())
	if err != nil {
		return nil, err
	}

	if activation != floor.ActivationDigest || !originalTime(floor.ActivatedAt) || floor.ActivatedAt.Location() != time.UTC {
		return nil, ErrInvalid
	}

	digest, err := writerFloorDigest(floor)
	if err != nil {
		return nil, err
	}

	if digest != floor.Digest {
		return nil, ErrInvalid
	}

	return writerFloorBytes(floor)
}

// DecodeWriterFloor refuses unknown fields, duplicate keys, trailing data and noncanonical bytes.
func DecodeWriterFloor(raw []byte) (WriterFloor, error) {
	var floor WriterFloor
	if len(raw) == 0 || len(raw) > MaxWriterFloor {
		return floor, ErrCapacity
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&floor); err != nil {
		return WriterFloor{}, ErrInvalid
	}

	canonical, err := EncodeWriterFloor(floor)
	if err != nil {
		return WriterFloor{}, err
	}

	if !bytes.Equal(canonical, raw) {
		return WriterFloor{}, ErrInvalid
	}

	return floor, nil
}

// MatchWriterFloor compares validated local state with an externally retained exact reference.
// The host must authenticate and freshly obtain the reference. Equality alone cannot reopen
// a restored database that omitted a later delegation under the same activation.
func MatchWriterFloor(floor WriterFloor, external WriterFloorReference) error {
	if _, err := EncodeWriterFloor(floor); err != nil {
		return err
	}

	if !originalToken(external.AuthorityID) || !originalID(external.ActivationID, PrefixOperation) || !hexDigest(external.Digest) {
		return ErrInvalid
	}

	if floor.Reference() != external {
		return ErrAuthority
	}

	return nil
}

func writerFloorDigest(floor WriterFloor) (string, error) {
	floor.Digest = ""

	raw, err := writerFloorBytes(floor)
	if err != nil {
		return "", err
	}

	return Digest(append([]byte(WriterFloorSchema+"\n"), raw...)), nil
}

func writerFloorBytes(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}

	if len(raw) > MaxWriterFloor {
		return nil, ErrCapacity
	}

	return raw, nil
}
