package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"math"

	"github.com/xraph/ctrlplane/id"
)

// Discovery bounds apply to actual canonical JSON bytes, including envelope fields.
const (
	DiscoverySchema  = "ctrlplane.managed.discovery.v1"
	MaxWorkReference = 2048
	MaxWorkHead      = 8192
	MaxWorkPage      = 73728
	MaxWorkPageItems = 32
	MaxWorkCursor    = 1024
)

// WorkReference addresses accepted work without embedding a target or remote command.
// Membership means unresolved accounting. It never establishes permission or closure.
type WorkReference struct {
	Schema    string       `json:"schema"`
	Key       Key          `json:"key"`
	Ordinal   Decimal      `json:"ordinal"`
	Operation OperationKey `json:"operation"`
	Kind      string       `json:"kind"`
	ID        id.ID        `json:"id"`
	Digest    string       `json:"digest"`
}

// WorkHead separates current desired selection from all retained unresolved work.
// LastOrdinal is an authority-assigned append sequence, unrelated to command ID ordering.
type WorkHead struct {
	Schema       string        `json:"schema"`
	Key          Key           `json:"key"`
	LastOrdinal  Decimal       `json:"last_ordinal"`
	Provisioning WorkReference `json:"provisioning"`
	Desired      WorkReference `json:"desired"`
}

// WorkPageRequest selects a bounded stable window for scheduling, never physical closure.
type WorkPageRequest struct {
	Key    Key    `json:"key"`
	Cursor string `json:"cursor"`
	Limit  int    `json:"limit"`
}

// WorkPage retains compact exact references; full commands use the exact readers.
// An empty NextCursor finishes this window and allows the next visit to start a new sweep.
type WorkPage struct {
	Schema     string          `json:"schema"`
	Key        Key             `json:"key"`
	Items      []WorkReference `json:"items"`
	NextCursor string          `json:"next_cursor"`
}

// WorkDiscoveryStore adds authoritative discovery over the existing retained record store.
// Legacy actions without acceptance-time associations remain unavailable, with no backfill.
type WorkDiscoveryStore interface {
	ReadManagedWorkHead(ctx context.Context, key Key) (WorkHead, error)
	ListManagedUnresolvedWork(ctx context.Context, request WorkPageRequest) (WorkPage, error)
	ReadManagedWork(ctx context.Context, reference WorkReference) (WorkReference, error)
}

// Validate requires canonical signed sequence counters and bounded immutable identities.
func (r WorkReference) Validate() error {
	if r.Schema != DiscoverySchema {
		return ErrUnsupported
	}

	if !discoveryKey(r.Key) || !discoveryKey(r.Operation.Key) || r.Key.TenantID != r.Operation.TenantID || !originalID(r.Operation.ActionID, PrefixOperation) || !originalID(r.ID, PrefixOperation) || !originalPositive(r.Ordinal) || !hexDigest(r.Digest) {
		return ErrInvalid
	}

	switch r.Kind {
	case "action", "registration", "drain", "route", "activation":
	default:
		return ErrInvalid
	}

	if r.Kind == "action" && r.ID != r.Operation.ActionID {
		return ErrInvalid
	}

	_, err := discoveryBytes(r, MaxWorkReference)

	return err
}

func discoveryKey(k Key) bool {
	return originalToken(k.TenantID) && originalID(k.InstanceID, id.PrefixInstance)
}

// EncodeWorkReference preserves the exact canonical reference bytes.
func EncodeWorkReference(r WorkReference) ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}

	return discoveryBytes(r, MaxWorkReference)
}

// DecodeWorkReference refuses duplicate fields, unknown fields and noncanonical encodings.
func DecodeWorkReference(raw []byte) (WorkReference, error) {
	var r WorkReference
	if err := discoveryDecode(raw, &r, MaxWorkReference); err != nil {
		return r, err
	}

	canonical, err := EncodeWorkReference(r)
	if err != nil || !bytes.Equal(raw, canonical) {
		return WorkReference{}, ErrInvalid
	}

	return r, nil
}

// EncodeWorkHead validates both retained selections without interpreting progress as closure.
func EncodeWorkHead(h WorkHead) ([]byte, error) {
	if h.Schema != DiscoverySchema || !discoveryKey(h.Key) || !originalPositive(h.LastOrdinal) {
		return nil, ErrInvalid
	}

	for _, ref := range []WorkReference{h.Provisioning, h.Desired} {
		if err := ref.Validate(); err != nil {
			return nil, err
		}

		n, _ := ref.Ordinal.Uint64()

		end, _ := h.LastOrdinal.Uint64()
		if ref.Key != h.Key || ref.Kind != "action" || n > end {
			return nil, ErrInvalid
		}
	}

	return discoveryBytes(h, MaxWorkHead)
}

// DecodeWorkHead requires exact canonical bytes for both authoritative pointers.
func DecodeWorkHead(raw []byte) (WorkHead, error) {
	var h WorkHead
	if err := discoveryDecode(raw, &h, MaxWorkHead); err != nil {
		return h, err
	}

	canonical, err := EncodeWorkHead(h)
	if err != nil || !bytes.Equal(raw, canonical) {
		return WorkHead{}, ErrInvalid
	}

	return h, nil
}

// EncodeWorkPage enforces its full envelope cap after checking all compact items.
func EncodeWorkPage(p WorkPage) ([]byte, error) {
	if p.Schema != DiscoverySchema || !discoveryKey(p.Key) || len(p.Items) > MaxWorkPageItems || len(p.NextCursor) > MaxWorkCursor {
		return nil, ErrInvalid
	}

	var previous uint64

	for _, ref := range p.Items {
		if err := ref.Validate(); err != nil {
			return nil, err
		}

		n, _ := ref.Ordinal.Uint64()
		if ref.Key != p.Key || n <= previous || n > math.MaxInt64 {
			return nil, ErrInvalid
		}

		previous = n
	}

	return discoveryBytes(p, MaxWorkPage)
}

func discoveryBytes(value any, maximum int) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}

	if len(raw) > maximum {
		return nil, ErrCapacity
	}

	return raw, nil
}

func discoveryDecode(raw []byte, value any, maximum int) error {
	if len(raw) == 0 || len(raw) > maximum {
		return ErrCapacity
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(value); err != nil {
		return ErrInvalid
	}

	return nil
}
