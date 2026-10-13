package managed

import (
	"bytes"
	"context"
	"time"
)

// MaxPhaseOriginalResult bounds the complete evidence link and its independent receipts.
const MaxPhaseOriginalResult = 16384

// PhaseOriginalResultRequest links a retained phase to independently accepted B2.2 material.
// It cannot create native facts, mark work resolved, or replace the original command.
type PhaseOriginalResultRequest struct {
	Schema   string               `json:"schema"`
	Parent   PhaseParent          `json:"parent"`
	Work     WorkReference        `json:"work"`
	Original OriginalLifecycleKey `json:"original"`
	Audit    AccessAuditRequest   `json:"audit"`
}

// PhaseOriginalResult records accepted native progress while keeping the work unresolved.
// The original reference and receipt are independently re-read, never caller attestations.
type PhaseOriginalResult struct {
	Schema          string                     `json:"schema"`
	Request         PhaseOriginalResultRequest `json:"request"`
	Original        OriginalLifecycleReference `json:"original"`
	OriginalReceipt OriginalLocalReceipt       `json:"original_receipt"`
	Acceptance      OriginalLocalReceipt       `json:"acceptance"`
	AuditReceipt    AccessAuditReceipt         `json:"audit_receipt"`
}

// PhaseOriginalResultStore attaches exact accepted registration/drain progress only.
// Provider terminal coverage, route and host activation results use their own authority.
type PhaseOriginalResultStore interface {
	AttachManagedPhaseOriginalResult(ctx context.Context, request PhaseOriginalResultRequest) (PhaseOriginalResult, error)
	ReadManagedPhaseOriginalResult(ctx context.Context, reference WorkReference) (PhaseOriginalResult, error)
}

// Validate checks a closed original-result request without granting remote authority.
func (q PhaseOriginalResultRequest) Validate() error {
	if q.Schema != DiscoverySchema {
		return ErrUnsupported
	}

	if _, err := q.Parent.LocalCommand(); err != nil {
		return err
	}

	if err := q.Work.Validate(); err != nil {
		return err
	}

	if q.Work.Key != q.Parent.Key || q.Original.Key != q.Parent.Key || !originalID(q.Original.OperationID, PrefixOperation) || q.Parent.ID == q.Work.ID || q.Parent.ID == q.Original.OperationID {
		return ErrInvalid
	}

	switch q.Work.Kind {
	case "registration":
		if q.Original.Kind != OriginalRegistration {
			return ErrAuthority
		}
	case "drain":
		if q.Original.Kind != OriginalDrain {
			return ErrAuthority
		}
	default:
		return ErrUnsupported
	}

	if !originalID(q.Audit.AttemptID, PrefixOperation) || q.Audit.AttemptID == q.Parent.ID || q.Audit.AttemptID == q.Work.ID || q.Audit.AttemptID == q.Original.OperationID || q.Audit.Actor.Unauthenticated || !originalToken(q.Audit.Actor.Subject) || q.Audit.Outcome != "allowed" || q.Audit.Reason != "authorized" || q.Audit.Action != "managed.reconcile" || q.Audit.ResourceKind != "instance" || q.Audit.ResourceID != q.Parent.InstanceID.String() || q.Audit.Scope.Kind != "tenant" || q.Audit.Scope.TenantID != q.Parent.TenantID || (!originalTime(q.Audit.OccurredAt) || q.Audit.OccurredAt.Location() != time.UTC) {
		return ErrAuthority
	}

	for _, value := range []string{q.Audit.Installation, q.Audit.Authority, q.Audit.ApplicationID, q.Audit.OrganizationID} {
		if !originalToken(value) {
			return ErrInvalid
		}
	}

	if q.Audit.SelectorDigest != "" && !hexDigest(q.Audit.SelectorDigest) || q.Audit.DecisionID != "" && !originalToken(q.Audit.DecisionID) || q.Audit.CorrelationID != "" && !originalToken(q.Audit.CorrelationID) {
		return ErrInvalid
	}

	_, err := discoveryBytes(q, MaxPhaseOriginalResult)

	return err
}

// EncodePhaseOriginalResult bounds the full result link after checking exact identities.
func EncodePhaseOriginalResult(r PhaseOriginalResult) ([]byte, error) {
	if err := r.Request.Validate(); err != nil {
		return nil, err
	}

	if r.Schema != DiscoverySchema || r.Original.Kind != r.Request.Original.Kind || r.Original.OperationID != r.Request.Original.OperationID || r.OriginalReceipt.ID != r.Original.OperationID || r.Acceptance.ID != r.Request.Parent.ID || !hexDigest(r.Acceptance.Digest) || !hexDigest(r.OriginalReceipt.Digest) || !originalPositive(r.Acceptance.Revision) || !originalPositive(r.Acceptance.LeaseEpoch) || !originalPositive(r.OriginalReceipt.Revision) || !originalPositive(r.OriginalReceipt.LeaseEpoch) || !originalTime(r.Acceptance.AcceptedAt) || !originalTime(r.OriginalReceipt.AcceptedAt) || r.AuditReceipt.ID != r.Request.Audit.AttemptID || r.AuditReceipt.AuditID != r.AuditReceipt.ID || !hexDigest(r.AuditReceipt.Digest) {
		return nil, ErrInvalid
	}

	if _, err := r.Original.Bytes(); err != nil {
		return nil, err
	}

	return discoveryBytes(r, MaxPhaseOriginalResult)
}

// DecodePhaseOriginalResult refuses noncanonical, ambiguous or oversized result links.
func DecodePhaseOriginalResult(raw []byte) (PhaseOriginalResult, error) {
	var out PhaseOriginalResult
	if err := discoveryDecode(raw, &out, MaxPhaseOriginalResult); err != nil {
		return out, err
	}

	canonical, err := EncodePhaseOriginalResult(out)
	if err != nil || !bytes.Equal(raw, canonical) {
		return PhaseOriginalResult{}, ErrInvalid
	}

	return out, nil
}
