package managedstate

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/xraph/ctrlplane/managed"
)

type phaseResultAcceptance struct {
	Request         managed.PhaseOriginalResultRequest `json:"request"`
	Original        managed.OriginalLifecycleReference `json:"original"`
	OriginalReceipt managed.OriginalLocalReceipt       `json:"original_receipt"`
}

func phaseOriginalResultEvidence(ctx context.Context, tx ControllerTransaction, work managed.WorkReference, key managed.OriginalLifecycleKey) (managed.PhaseCommandRecord, managed.OriginalLifecycleRecord, error) {
	var (
		phase    managed.PhaseCommandRecord
		original managed.OriginalLifecycleRecord
	)
	if err := validateWorkHeld(ctx, tx, work); err != nil {
		return phase, original, err
	}

	var err error

	phase, err = readPhaseHeld(ctx, tx, work)
	if err != nil {
		return phase, original, err
	}

	target, err := tx.Read(ctx, work.Key)
	if err != nil {
		return phase, original, err
	}

	original, _, err = readOriginal(ctx, tx, key, target)
	if err != nil {
		return phase, original, err
	}

	p := phase.Request.Process
	if p == nil || original.Material.Command.ReservationID != p.Request.Binding.ReservationID || original.Receipt.AcceptedAt.Before(phase.Acceptance.AcceptedAt) {
		return phase, original, managed.ErrAuthority
	}

	var (
		process managed.OriginalProcessFacts
		native  managed.OriginalNativeAcceptance
	)

	switch work.Kind {
	case "registration":
		f := original.Material.Registration
		if f == nil || f.StoredCommandDigest != p.Request.Command.OriginalDigest || f.Member != p.Request.Binding.MemberName || f.Role != p.Request.Binding.Role {
			return phase, original, managed.ErrAuthority
		}

		process, native = f.Process, f.Native
	case "drain":
		f := original.Material.Drain
		if f == nil || f.OperationID != p.DrainOperation || !f.Deadline.Equal(p.Deadline) || !f.ProcessAccepted || !f.AdmissionClosed {
			return phase, original, managed.ErrAuthority
		}

		process, native = f.Process, f.Native
	default:
		return phase, original, managed.ErrUnsupported
	}

	if !reflect.DeepEqual(process, p.Process) || native.Operation != p.Native.Operation || native.RequestID != p.Native.RequestID || native.RequestDigest != p.Native.RequestDigest || native.CommandDigest != p.Native.CommandDigest {
		return phase, original, managed.ErrAuthority
	}

	return phase, original, nil
}

func phaseResultReceiptDigest(input phaseResultAcceptance) (string, error) {
	raw, err := json.Marshal(struct {
		Kind  string `json:"kind"`
		Input any    `json:"input"`
	}{"phase_original_result", input})
	if err != nil {
		return "", err
	}

	return managed.Digest(raw), nil
}

func readPhaseOriginalResultHeld(ctx context.Context, tx ControllerTransaction, work managed.WorkReference) (managed.PhaseOriginalResult, error) {
	var out managed.PhaseOriginalResult

	scope, err := originalScope(work.Key)
	if err != nil {
		return out, err
	}

	raw, err := tx.ReadRecord(ctx, "phase_original_result", scope, work.ID.String())
	if err != nil {
		return out, err
	}

	out, err = managed.DecodePhaseOriginalResult(raw)
	if err != nil || out.Request.Work != work {
		return managed.PhaseOriginalResult{}, managed.ErrUnavailable
	}

	phase, original, err := phaseOriginalResultEvidence(ctx, tx, work, out.Request.Original)
	if err != nil {
		return managed.PhaseOriginalResult{}, err
	}

	if original.Reference != out.Original || original.Receipt != out.OriginalReceipt || !samePhaseAuditAuthority(out.Request.Audit, phase.Request.Audit) {
		return managed.PhaseOriginalResult{}, managed.ErrUnavailable
	}

	fingerprint, err := phaseResultReceiptDigest(phaseResultAcceptance{out.Request, out.Original, out.OriginalReceipt})
	if err != nil {
		return managed.PhaseOriginalResult{}, err
	}

	receipt, err := tx.ReadReceipt(ctx, work.Key, out.Request.Parent.ID, fingerprint)
	if err != nil {
		return managed.PhaseOriginalResult{}, err
	}

	if managed.OriginalReceipt(receipt) != out.Acceptance {
		return managed.PhaseOriginalResult{}, managed.ErrUnavailable
	}

	audit := out.Request.Audit

	auditDigest, err := controllerFingerprint(audit)
	if err != nil {
		return managed.PhaseOriginalResult{}, err
	}

	access, err := controllerRead[managed.AccessAuditReceipt](ctx, tx, "access", "", audit.AttemptID.String())
	if err != nil {
		return managed.PhaseOriginalResult{}, err
	}

	auditScope, err := controllerScope(audit.Installation, audit.Authority, audit.Scope)
	if err != nil {
		return managed.PhaseOriginalResult{}, err
	}

	intent, err := controllerRead[managed.AuditIntent](ctx, tx, "audit", auditScope, audit.AttemptID.String())
	if err != nil {
		return managed.PhaseOriginalResult{}, err
	}

	if access != out.AuditReceipt || access.Digest != auditDigest || access.AcceptedAt != out.Acceptance.AcceptedAt || !reflect.DeepEqual(intent, intentFromAccess(audit, auditDigest, access.AcceptedAt)) {
		return managed.PhaseOriginalResult{}, managed.ErrUnavailable
	}

	return out, nil
}

func samePhaseAuditAuthority(a, b managed.AccessAuditRequest) bool {
	return a.Installation == b.Installation && a.Authority == b.Authority && a.ApplicationID == b.ApplicationID && a.OrganizationID == b.OrganizationID
}

// AttachManagedPhaseOriginalResult persists exact accepted progress and audit atomically.
// It retains unresolved membership and cannot synthesize an original result or terminal closure.
func (r *Repository) AttachManagedPhaseOriginalResult(ctx context.Context, q managed.PhaseOriginalResultRequest) (managed.PhaseOriginalResult, error) {
	if err := q.Validate(); err != nil {
		return managed.PhaseOriginalResult{}, err
	}

	command, err := q.Parent.LocalCommand()
	if err != nil {
		return managed.PhaseOriginalResult{}, err
	}

	var out managed.PhaseOriginalResult

	err = r.controller(ctx, "phase-result:"+q.Parent.InstanceID.String(), []managed.Key{q.Parent.Key, q.Work.Operation.Key}, func(tx ControllerTransaction) error {
		scope, e := originalScope(q.Parent.Key)
		if e != nil {
			return e
		}

		_, e = tx.ReadRecord(ctx, "phase_original_result", scope, q.Work.ID.String())
		if e == nil {
			saved, readErr := readPhaseOriginalResultHeld(ctx, tx, q.Work)
			if readErr != nil {
				return readErr
			}

			if !reflect.DeepEqual(saved.Request, q) {
				return managed.ErrConflict
			}

			out = saved

			return nil
		}

		if !errors.Is(e, managed.ErrNotFound) {
			return e
		}

		phase, original, e := phaseOriginalResultEvidence(ctx, tx, q.Work, q.Original)
		if e != nil {
			return e
		}

		if !samePhaseAuditAuthority(q.Audit, phase.Request.Audit) {
			return managed.ErrAuthority
		}

		if _, e = controllerParent(ctx, tx, command); e != nil {
			return e
		}

		if _, e = tx.ReadRecord(ctx, "access", "", q.Audit.AttemptID.String()); !controllerAbsent(e) {
			if e == nil {
				return managed.ErrConflict
			}

			return e
		}

		input := phaseResultAcceptance{q, original.Reference, original.Receipt}
		fresh := false

		receipt, e := New(tx).apply(ctx, command, "phase_original_result", input, true, func(*managed.Target, time.Time) error {
			fresh = true

			return nil
		})
		if e != nil {
			return e
		}

		if !fresh {
			return managed.ErrUnavailable
		}

		audit, e := New(phaseAuditTransaction{ControllerTransaction: tx, acceptedAt: receipt.AcceptedAt}).RecordManagedAccessAudit(ctx, q.Audit)
		if e != nil {
			return e
		}

		out = managed.PhaseOriginalResult{Schema: managed.DiscoverySchema, Request: q, Original: original.Reference, OriginalReceipt: original.Receipt, Acceptance: managed.OriginalReceipt(receipt), AuditReceipt: audit}

		raw, e := managed.EncodePhaseOriginalResult(out)
		if e != nil {
			return e
		}

		return tx.WriteRecord(ctx, "phase_original_result", scope, q.Work.ID.String(), raw, true)
	})
	if err != nil {
		return managed.PhaseOriginalResult{}, err
	}

	return out, nil
}

// ReadManagedPhaseOriginalResult verifies accepted progress without closing discovery work.
func (r *Repository) ReadManagedPhaseOriginalResult(ctx context.Context, work managed.WorkReference) (managed.PhaseOriginalResult, error) {
	if err := work.Validate(); err != nil {
		return managed.PhaseOriginalResult{}, err
	}

	if work.Kind != "registration" && work.Kind != "drain" {
		return managed.PhaseOriginalResult{}, managed.ErrUnsupported
	}

	var out managed.PhaseOriginalResult

	err := r.controller(ctx, "phase-result:"+work.Key.InstanceID.String(), []managed.Key{work.Key, work.Operation.Key}, func(tx ControllerTransaction) error {
		var e error

		out, e = readPhaseOriginalResultHeld(ctx, tx, work)

		return e
	})
	if err != nil {
		return managed.PhaseOriginalResult{}, err
	}

	return out, nil
}
