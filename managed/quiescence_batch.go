package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"slices"

	"github.com/xraph/ctrlplane/id"
)

// Quiescence batch bounds cover complete encoded values, including nested evidence.
const (
	MaxQuiescenceBatchEntries    = 64
	MaxQuiescenceBatchEntry      = 16384
	MaxQuiescenceBatchInput      = 1052672
	MaxQuiescenceBatchRecord     = 32768
	MaxQuiescenceBatchReceipt    = 4096
	QuiescenceBatchHistory       = "quiescence_observation_batch"
	QuiescenceBatchRequestSchema = "ctrlplane.quiescence-batch-request.v1"
	QuiescenceBatchRecordSchema  = "ctrlplane.quiescence-batch-record.v1"
	QuiescenceBatchReceiptSchema = "ctrlplane.quiescence-batch-receipt.v1"
)

// QuiescenceBatchStore optionally retains observations as one atomic non-issuing batch.
type QuiescenceBatchStore interface {
	AcceptManagedQuiescenceBatch(ctx context.Context, request QuiescenceBatchAcceptanceRequest) (QuiescenceBatchReceipt, error)
	ReadManagedQuiescenceBatch(ctx context.Context, key QuiescenceBatchKey) (QuiescenceBatchReceipt, error)
}

// QuiescenceBatchCommand binds the aggregate to fresh parent authority.
type QuiescenceBatchCommand struct {
	Key        Key     `json:"key"`
	ID         id.ID   `json:"id"`
	Revision   Decimal `json:"revision"`
	LeaseEpoch Decimal `json:"lease_epoch"`
	Owner      string  `json:"owner"`
}

// QuiescenceBatchAcceptanceRequest preserves descriptor order and each original observation.
type QuiescenceBatchAcceptanceRequest struct {
	Schema  string                 `json:"schema"`
	Command QuiescenceBatchCommand `json:"command"`
	Attempt AttemptRef             `json:"attempt"`
	Entries []QuiescenceBatchEntry `json:"entries"`
}

// QuiescenceBatchEntry selects one ordinary observation operation.
type QuiescenceBatchEntry struct {
	OperationID id.ID                 `json:"operation_id"`
	Observation QuiescenceObservation `json:"observation"`
}

// QuiescenceBatchKey selects one immutable aggregate in its original parent.
type QuiescenceBatchKey struct {
	Key     Key   `json:"key"`
	BatchID id.ID `json:"batch_id"`
}

// QuiescenceBatchReceipt is retained acceptance, never send permission.
type QuiescenceBatchReceipt struct {
	Schema  string               `json:"schema"`
	Key     Key                  `json:"key"`
	BatchID id.ID                `json:"batch_id"`
	Attempt AttemptRef           `json:"attempt"`
	Count   Decimal              `json:"count"`
	Receipt OriginalLocalReceipt `json:"receipt"`
}

// QuiescenceBatchScope uses a separate domain and the exact nested target key.
func QuiescenceBatchScope(k Key) (string, error) {
	if !originalToken(k.TenantID) || !originalID(k.InstanceID, id.PrefixInstance) {
		return "", ErrInvalid
	}

	return originalHash("ctrlplane.managed.quiescence-batch-scope.v1", struct {
		Key Key `json:"key"`
	}{k})
}

func batchAttemptValid(v AttemptRef) bool {
	return originalID(v.AttemptID, PrefixOperation) && v.Generation == "1" && hexDigest(v.DescriptorDigest)
}

// Normalize bounds and owns the complete request without selecting authority.
func (q QuiescenceBatchAcceptanceRequest) Normalize() (QuiescenceBatchAcceptanceRequest, error) {
	if len(q.Entries) < 1 || len(q.Entries) > MaxQuiescenceBatchEntries {
		return q, ErrCapacity
	}

	if q.Schema != QuiescenceBatchRequestSchema || !batchAttemptValid(q.Attempt) {
		return q, ErrInvalid
	}

	if _, err := QuiescenceBatchScope(q.Command.Key); err != nil {
		return q, err
	}

	r, err := q.Command.Revision.Uint64()
	if err != nil || r == 0 || r >= math.MaxInt64 || !originalPositive(q.Command.LeaseEpoch) || !originalToken(q.Command.Owner) || !originalID(q.Command.ID, PrefixOperation) {
		return q, ErrInvalid
	}

	if uint64(len(q.Entries)+1) > math.MaxInt64-r {
		return q, ErrCapacity
	}
	// Check raw evidence before making any request-owned copies.
	for _, entry := range q.Entries {
		if len(entry.Observation.Attestation) > MaxQuiescenceAttestation {
			return q, ErrCapacity
		}
	}

	entries := make([]QuiescenceBatchEntry, 0, len(q.Entries))
	seen := map[id.ID]bool{}

	reservations := map[id.ID]bool{}
	for _, entry := range q.Entries {
		if !originalID(entry.OperationID, PrefixOperation) || entry.OperationID == q.Command.ID || seen[entry.OperationID] || reservations[entry.Observation.Reference.ReservationID] {
			return q, ErrInvalid
		}

		seen[entry.OperationID] = true
		reservations[entry.Observation.Reference.ReservationID] = true

		entry.Observation, err = entry.Observation.Normalize()
		if err != nil {
			return q, err
		}

		raw, e := json.Marshal(entry)
		if e != nil {
			return q, e
		}

		if len(raw) > MaxQuiescenceBatchEntry {
			return q, ErrCapacity
		}

		entry.Observation.Attestation = slices.Clone(entry.Observation.Attestation)
		entries = append(entries, entry)
	}

	q.Entries = entries

	raw, err := json.Marshal(struct {
		Kind  string                           `json:"kind"`
		Input QuiescenceBatchAcceptanceRequest `json:"input"`
	}{QuiescenceBatchHistory, q})
	if err != nil {
		return q, err
	}

	if len(raw) > MaxQuiescenceBatchInput {
		return q, ErrCapacity
	}

	return q, nil
}

// Bytes produces the strict canonical request representation.
func (q QuiescenceBatchAcceptanceRequest) Bytes() ([]byte, error) {
	v, err := q.Normalize()
	if err != nil {
		return nil, err
	}

	return json.Marshal(v)
}

// ParseQuiescenceBatchAcceptanceRequest refuses noncanonical nested values.
func ParseQuiescenceBatchAcceptanceRequest(raw []byte) (QuiescenceBatchAcceptanceRequest, error) {
	var q QuiescenceBatchAcceptanceRequest
	if len(raw) > MaxQuiescenceBatchInput {
		return q, ErrCapacity
	}

	if err := strictObject(raw, &q); err != nil {
		return q, err
	}

	encoded, err := q.Bytes()
	if err != nil {
		return q, err
	}

	if !bytes.Equal(raw, encoded) {
		return q, ErrInvalid
	}

	return q, nil
}

// Bytes validates and encodes the complete aggregate receipt.
func (q QuiescenceBatchReceipt) Bytes() ([]byte, error) {
	if _, err := QuiescenceBatchScope(q.Key); err != nil {
		return nil, err
	}

	n, err := q.Count.Uint64()
	if err != nil || n < 1 || n > 64 || q.Schema != QuiescenceBatchReceiptSchema || !originalID(q.BatchID, PrefixOperation) || !batchAttemptValid(q.Attempt) || q.Receipt.ID != q.BatchID || !hexDigest(q.Receipt.Digest) || !originalPositive(q.Receipt.Revision) || !originalPositive(q.Receipt.LeaseEpoch) || !originalTime(q.Receipt.AcceptedAt) || !originalTime(q.Receipt.LeaseUntil) {
		return nil, ErrInvalid
	}

	q.Receipt.AcceptedAt = q.Receipt.AcceptedAt.UTC()
	q.Receipt.LeaseUntil = q.Receipt.LeaseUntil.UTC()

	raw, err := json.Marshal(q)
	if err != nil {
		return nil, err
	}

	if len(raw) > MaxQuiescenceBatchReceipt {
		return nil, ErrCapacity
	}

	return raw, nil
}

// ParseQuiescenceBatchReceipt refuses noncanonical aggregate results.
func ParseQuiescenceBatchReceipt(raw []byte) (QuiescenceBatchReceipt, error) {
	var q QuiescenceBatchReceipt
	if len(raw) > MaxQuiescenceBatchReceipt {
		return q, ErrCapacity
	}

	if err := strictObject(raw, &q); err != nil {
		return q, err
	}

	encoded, err := q.Bytes()
	if err != nil {
		return q, err
	}

	if !bytes.Equal(raw, encoded) {
		return q, ErrInvalid
	}

	return q, nil
}
