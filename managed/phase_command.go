package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"time"

	"github.com/xraph/ctrlplane/id"
)

// MaxPhaseCommand bounds a complete retained original command in the existing record table.
const MaxPhaseCommand = 262144

// PhaseParent preserves parent CAS in canonical decimal adapter-facing counters.
type PhaseParent struct {
	Key

	ID         id.ID   `json:"id"`
	Revision   Decimal `json:"revision"`
	LeaseEpoch Decimal `json:"lease_epoch"`
	Owner      string  `json:"owner"`
}

// LocalCommand validates signed authority counters without changing their stored digest.
func (p PhaseParent) LocalCommand() (Command, error) {
	r, e1 := p.Revision.Uint64()

	e, e2 := p.LeaseEpoch.Uint64()
	if e1 != nil || e2 != nil || r == 0 || r >= math.MaxInt64 || e == 0 || e > math.MaxInt64 || !discoveryKey(p.Key) || !originalID(p.ID, PrefixOperation) || !originalToken(p.Owner) {
		return Command{}, ErrInvalid
	}

	return Command{Key: p.Key, ID: p.ID, Revision: r, LeaseEpoch: e, Owner: p.Owner}, nil
}

// PhaseNativeIdentity preserves the native canonical digest domains independently of byte hashes.
// Configured C verifies translation before retention; storage never derives these from byte SHA.
type PhaseNativeIdentity struct {
	Operation     string `json:"operation"`
	RequestID     string `json:"request_id"`
	RequestDigest string `json:"request_digest"`
	CommandDigest string `json:"command_digest"`
}

// ProcessPhaseCommand binds registration, drain or activation to the exact startup mapping.
// DrainOperation/Deadline exist only for a drain; an observed handle cannot replace them.
type ProcessPhaseCommand struct {
	Native         PhaseNativeIdentity  `json:"native"`
	Request        LifecycleRequest     `json:"request"`
	Process        OriginalProcessFacts `json:"process"`
	DrainOperation string               `json:"drain_operation"`
	Deadline       time.Time            `json:"deadline"`
	Scope          MachineScope         `json:"scope"`
}

// PhaseCommandRequest retains one original request and required fresh access decision.
// Kind is registration, drain, route or activation. Removal attempt commands remain B2.3.
type PhaseCommandRequest struct {
	Schema   string               `json:"schema"`
	Parent   PhaseParent          `json:"parent"`
	ActionID id.ID                `json:"action_id"`
	Kind     string               `json:"kind"`
	Process  *ProcessPhaseCommand `json:"process,omitempty"`
	Route    *RoutingRequest      `json:"route,omitempty"`
	Audit    AccessAuditRequest   `json:"audit"`
}

// PhaseCommandRecord is immutable may-have-issued accounting, never remote acceptance.
// Acceptance binds the full request through the independent parent receipt history.
type PhaseCommandRecord struct {
	Schema        string               `json:"schema"`
	Request       PhaseCommandRequest  `json:"request"`
	RequestDigest string               `json:"request_digest"`
	Acceptance    OriginalLocalReceipt `json:"acceptance"`
	AuditReceipt  AccessAuditReceipt   `json:"audit_receipt"`
	Work          WorkReference        `json:"work"`
}

// PhaseCommandGrant distinguishes fresh retention from exact historical recovery.
// Every remote call still needs current host authorization and its native recovery semantics.
type PhaseCommandGrant struct {
	Record        PhaseCommandRecord `json:"record"`
	NewlyRetained bool               `json:"newly_retained"`
}

// PhaseCommandStore retains original commands in the same authority transaction as issuance.
// Result/closure attachment remains separately owned by exact native/recovery protocols.
type PhaseCommandStore interface {
	RetainManagedPhaseCommand(ctx context.Context, request PhaseCommandRequest) (PhaseCommandGrant, error)
	ReadManagedPhaseCommand(ctx context.Context, reference WorkReference) (PhaseCommandRecord, error)
}

// PhaseCommandDigest validates the closed request union and hashes its original bytes.
func PhaseCommandDigest(q PhaseCommandRequest) (string, error) {
	if q.Schema != DiscoverySchema {
		return "", ErrUnsupported
	}

	if _, err := q.Parent.LocalCommand(); err != nil {
		return "", err
	}

	if !originalID(q.ActionID, PrefixOperation) || q.Parent.ID == q.ActionID || !originalID(q.Audit.AttemptID, PrefixOperation) || q.Audit.AttemptID == q.Parent.ID || q.Audit.AttemptID == q.ActionID {
		return "", ErrInvalid
	}

	if q.Audit.Actor.Unauthenticated || !originalToken(q.Audit.Actor.Subject) || q.Audit.Outcome != "allowed" || q.Audit.Reason != "authorized" || q.Audit.Action != "managed.reconcile" || q.Audit.ResourceKind != "instance" || q.Audit.ResourceID != q.Parent.InstanceID.String() || q.Audit.Scope.Kind != "tenant" || q.Audit.Scope.TenantID != q.Parent.TenantID || q.Audit.OccurredAt.IsZero() {
		return "", ErrAuthority
	}

	for _, value := range []string{q.Audit.Installation, q.Audit.Authority, q.Audit.ApplicationID, q.Audit.OrganizationID} {
		if !originalToken(value) {
			return "", ErrInvalid
		}
	}

	if !originalTime(q.Audit.OccurredAt) || q.Audit.OccurredAt.Location() != time.UTC || q.Audit.SelectorDigest != "" && !hexDigest(q.Audit.SelectorDigest) || q.Audit.DecisionID != "" && !originalToken(q.Audit.DecisionID) || q.Audit.CorrelationID != "" && !originalToken(q.Audit.CorrelationID) {
		return "", ErrInvalid
	}

	switch q.Kind {
	case "registration", "drain", "activation":
		if q.Process == nil || q.Route != nil {
			return "", ErrInvalid
		}

		p := q.Process
		if q.Kind == "activation" {
			if p.Native != (PhaseNativeIdentity{}) {
				return "", ErrInvalid
			}
		} else {
			expectedOperation := "query_runtime.register"
			if q.Kind == "drain" {
				expectedOperation = "worker.drain.request"
			}

			if p.Native.Operation != expectedOperation || !originalToken(p.Native.RequestID) || !hexDigest(p.Native.RequestDigest) || !hexDigest(p.Native.CommandDigest) {
				return "", ErrInvalid
			}
		}

		if err := p.Request.Validate(); err != nil {
			return "", err
		}

		if !originalProcess(p.Process) {
			return "", ErrInvalid
		}

		b := p.Request.Binding
		if !originalPositive(b.ReservationVersion) || !originalPositive(b.Target.Manifest.Version) {
			return "", ErrInvalid
		}

		if b.Target.TenantID != q.Parent.TenantID || b.Target.InstanceID != q.Parent.InstanceID || p.Process.RuntimeID != b.RuntimeID || p.Process.InstanceID != b.InstanceID || p.Process.HostID != b.HostID || p.Process.Build != b.NativeBuild || p.Process.ConfigurationDigest != b.ConfigurationDigest || p.Process.EnrollmentEvidenceDigest != Digest(b.EnrollmentEvidence) {
			return "", ErrAuthority
		}

		if p.Scope.GlobalEnumeration || p.Scope.TenantID != q.Parent.TenantID || p.Scope.Installation != p.Process.Installation || p.Scope.Application != q.Audit.ApplicationID || !originalToken(p.Scope.Environment) || p.Scope.ResourceID != q.Parent.InstanceID.String() || !originalToken(p.Scope.Action) {
			return "", ErrAuthority
		}

		if q.Kind == "drain" {
			if !originalToken(p.DrainOperation) || !originalTime(p.Deadline) || p.Deadline.Location() != time.UTC {
				return "", ErrInvalid
			}
		} else if p.DrainOperation != "" || !p.Deadline.IsZero() {
			return "", ErrInvalid
		}

		if q.Kind == "activation" && b.Role != Polling {
			return "", ErrAuthority
		}
	case "route":
		if q.Route == nil || q.Process != nil {
			return "", ErrInvalid
		}

		if err := q.Route.Validate(); err != nil {
			return "", err
		}

		if !originalPositive(q.Route.Route.WriterFloor) || !originalPositive(q.Route.Route.SchemaFloor) {
			return "", ErrInvalid
		}

		if revision, err := q.Route.ExpectedRevision.Uint64(); err != nil || revision > math.MaxInt64 {
			return "", ErrInvalid
		}

		if q.Route.Route.Key != q.Parent.Key {
			return "", ErrAuthority
		}
	default:
		return "", ErrInvalid
	}

	raw, err := discoveryBytes(q, MaxPhaseCommand)
	if err != nil {
		return "", err
	}

	return Digest(append([]byte("ctrlplane.managed.phase-command.v1\n"), raw...)), nil
}

// EncodePhaseCommandRecord bounds the complete envelope including base64 command expansion.
func EncodePhaseCommandRecord(r PhaseCommandRecord) ([]byte, error) {
	hash, err := PhaseCommandDigest(r.Request)
	if err != nil {
		return nil, err
	}

	if r.Schema != DiscoverySchema || r.RequestDigest != hash || r.Acceptance.ID != r.Request.Parent.ID || !hexDigest(r.Acceptance.Digest) || !originalPositive(r.Acceptance.Revision) || !originalPositive(r.Acceptance.LeaseEpoch) || r.Acceptance.AcceptedAt.IsZero() || r.AuditReceipt.ID != r.Request.Audit.AttemptID || r.AuditReceipt.AuditID != r.AuditReceipt.ID || !hexDigest(r.AuditReceipt.Digest) || r.Work.ID != r.Request.Parent.ID || r.Work.Kind != r.Request.Kind || r.Work.Key != r.Request.Parent.Key || (r.Work.Operation.ActionID != r.Request.ActionID || r.Work.Operation.TenantID != r.Request.Parent.TenantID) || r.Work.Digest != hash {
		return nil, ErrInvalid
	}

	if err = r.Work.Validate(); err != nil {
		return nil, err
	}

	return discoveryBytes(r, MaxPhaseCommand)
}

// DecodePhaseCommandRecord refuses any alternate JSON representation of retained commands.
func DecodePhaseCommandRecord(raw []byte) (PhaseCommandRecord, error) {
	var r PhaseCommandRecord
	if err := discoveryDecode(raw, &r, MaxPhaseCommand); err != nil {
		return r, err
	}

	canonical, err := EncodePhaseCommandRecord(r)
	if err != nil || !bytes.Equal(raw, canonical) {
		return PhaseCommandRecord{}, ErrInvalid
	}

	return r, nil
}

// ClonePhaseCommandRequest freezes caller-owned commands and proof bytes before locking.
func ClonePhaseCommandRequest(q PhaseCommandRequest) (PhaseCommandRequest, error) {
	if _, err := PhaseCommandDigest(q); err != nil {
		return PhaseCommandRequest{}, err
	}

	raw, err := json.Marshal(q)
	if err != nil {
		return PhaseCommandRequest{}, err
	}

	var clone PhaseCommandRequest
	if err = json.Unmarshal(raw, &clone); err != nil {
		return PhaseCommandRequest{}, err
	}

	return clone, nil
}
