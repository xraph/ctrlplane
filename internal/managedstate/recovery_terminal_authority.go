package managedstate

import (
	"context"
	"encoding/json"
	"reflect"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

const recoveryTerminalRequestKind = "removal_recovery_terminal_request"
const maxRecoveryTerminalRequest = managed.MaxAttemptFence

type recoveryTerminalAuthority struct {
	coverage     managed.RecoveryRecord[managed.EffectiveRemovalCoverage]
	reference    managed.RecoveryAssociation
	closes       []managed.RecoveryAssociation
	closesDigest string
	acceptedAt   time.Time
}

// recoveryTerminalAuthorityHeld requires every fixed guard to have an exact
// accepted close. Fenced cutoff alone cannot settle possible effects.
func recoveryTerminalAuthorityHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView) (recoveryTerminalAuthority, error) {
	var out recoveryTerminalAuthority
	if !view.validated || view.recoveryActivation == nil {
		return out, managed.ErrUnsupported
	}

	coverage, err := recoveryCoverageReadHeld(ctx, tx, target, view, nil)
	if err != nil {
		return out, err
	}

	key, err := recoveryFactKey(attemptKey(view), managed.RecoveryCoverageRecord, coverage.Acceptance.OperationID, coverage.Facts)
	if err != nil {
		return out, err
	}

	out.reference, err = managed.RecoveryAssociationFor(key, coverage)
	if err != nil {
		return out, err
	}

	out.coverage = coverage

	out.acceptedAt = coverage.Acceptance.Receipt.AcceptedAt
	if len(view.descriptor.ReservationIDs) > managed.MaxRecoveryBindings {
		return out, managed.ErrCapacity
	}

	for _, reservationID := range view.descriptor.ReservationIDs {
		retained, readErr := reservation(target, reservationID)
		if readErr != nil {
			return out, readErr
		}

		if retained.Phase == managed.Revoked {
			continue
		}

		binding, readErr := recoveryBindingInspectionHeld(ctx, tx, target, view, attemptBinding(view, reservationID))
		if readErr != nil {
			return out, readErr
		}

		selected, readErr := recoveryNativeCloseSelectionHeld(ctx, tx, view, binding)
		if readErr != nil {
			return out, readErr
		}

		if selected.Close == nil {
			return out, managed.ErrBlocked
		}

		closeRecord, readErr := recoveryNativeCloseExactHeld(ctx, tx, target, view, *selected.Close)
		if readErr != nil {
			return out, readErr
		}

		if closeRecord.Facts.Binding != binding || closeRecord.Facts.Coverage != out.reference {
			return out, managed.ErrAuthority
		}

		out.closes = append(out.closes, *selected.Close)
		if out.acceptedAt.Before(closeRecord.Acceptance.Receipt.AcceptedAt) {
			out.acceptedAt = closeRecord.Acceptance.Receipt.AcceptedAt
		}
	}

	count, err := coverage.Facts.BindingCount.Uint64()
	if err != nil || count != uint64(len(out.closes)) {
		return out, managed.ErrUnavailable
	}

	out.closesDigest, err = attemptHash("ctrlplane.managed.closed-removal-guards.v1", out.closes)

	return out, err
}

// The witness supplies a bounded canonical request preimage. Its digest binds
// accepted coverage and the complete closed-guard set to the independent receipt.
// Original fact codecs, native command bytes and original hash domains stay intact.
type recoveryTerminalRequest struct {
	SchemaVersion      managed.Decimal             `json:"schema_version"`
	AttemptRef         managed.AttemptRef          `json:"attempt_ref"`
	Kind               string                      `json:"kind"`
	Request            json.RawMessage             `json:"request"`
	Coverage           managed.RecoveryAssociation `json:"coverage"`
	ClosedGuardsDigest string                      `json:"closed_guards_digest"`
}

func recoveryTerminalRequestFor(view attemptView, kind string, input any, authority recoveryTerminalAuthority) (recoveryTerminalRequest, string, error) {
	var zero recoveryTerminalRequest
	if !recoveryTerminalInputKind(kind, input) {
		return zero, "", managed.ErrUnsupported
	}

	local, err := recoveryTerminalLocalRequest(input)
	if err != nil {
		return zero, "", err
	}

	if !validAttemptCommand(local.Command) || attemptLocal(local) != attemptKey(view) {
		return zero, "", managed.ErrAuthority
	}

	raw, err := json.Marshal(input)
	if err != nil {
		return zero, "", err
	}

	witness := recoveryTerminalRequest{SchemaVersion: "1", AttemptRef: view.ref, Kind: kind, Request: raw, Coverage: authority.reference, ClosedGuardsDigest: authority.closesDigest}

	encoded, err := json.Marshal(witness)
	if err != nil {
		return zero, "", err
	}

	if len(encoded) > maxRecoveryTerminalRequest {
		return zero, "", managed.ErrCapacity
	}

	fingerprint, err := attemptHash("ctrlplane.managed.recovery-terminal-acceptance.v1", witness)

	return witness, fingerprint, err
}

func recoveryTerminalInputKind(kind string, input any) bool {
	switch q := input.(type) {
	case managed.AttemptSettlementRequest:
		return kind == "attempt_settle"
	case managed.AttemptBindingSettlementRequest:
		return kind == "attempt_witness"
	case managed.AttemptCommandRequest:
		return kind == "attempt_command" && (q.Kind == managed.AttemptAbort || q.Kind == managed.AttemptFinish)
	case managed.AttemptFinishRequest:
		return kind == "attempt_finish"
	case managed.AttemptAbortRequest:
		return kind == "attempt_abort"
	case managed.AttemptRequest:
		return kind == "attempt_tombstone"
	default:
		return false
	}
}

func recoveryTerminalAcceptedRequestHeld(ctx context.Context, tx ControllerTransaction, view attemptView, operation id.ID, kind string, input any, authority recoveryTerminalAuthority) (attemptEvent, error) {
	var zero attemptEvent

	witness, err := attemptRead[recoveryTerminalRequest](ctx, tx, recoveryTerminalRequestKind, view.scope, operation.String(), maxRecoveryTerminalRequest)
	if err != nil {
		return zero, err
	}

	expected, fingerprint, err := recoveryTerminalRequestFor(view, kind, input, authority)
	if err != nil {
		return zero, err
	}

	if !reflect.DeepEqual(witness, expected) {
		return zero, managed.ErrConflict
	}

	local, err := recoveryTerminalLocalRequest(input)
	if err != nil {
		return zero, err
	}

	if local.ID != operation {
		return zero, managed.ErrUnavailable
	}

	event, err := attemptEventRead(ctx, tx, view, operation)
	if err != nil {
		return zero, err
	}

	if event.Kind != kind || event.RequestDigest != fingerprint {
		return zero, managed.ErrUnavailable
	}

	return event, nil
}

func recoveryEffectiveStepReferences(coverage managed.EffectiveRemovalCoverage) ([]string, error) {
	if len(coverage.Steps) == 0 || len(coverage.Steps) > managed.MaxRecoverySteps {
		return nil, managed.ErrCapacity
	}

	references := make([]string, 0, len(coverage.Steps))
	for _, step := range coverage.Steps {
		reference, err := attemptHash("ctrlplane.managed.effective-step-reference.v1", step)
		if err != nil {
			return nil, err
		}

		references = append(references, reference)
	}

	return references, nil
}

func recoveryTerminalLocalRequest(input any) (managed.AttemptRequest, error) {
	switch q := input.(type) {
	case managed.AttemptSettlementRequest:
		return q.AttemptRequest, nil
	case managed.AttemptBindingSettlementRequest:
		return q.AttemptRequest, nil
	case managed.AttemptCommandRequest:
		return q.AttemptRequest, nil
	case managed.AttemptFinishRequest:
		return q.AttemptRequest, nil
	case managed.AttemptAbortRequest:
		return q.AttemptRequest, nil
	case managed.AttemptRequest:
		return q, nil
	default:
		return managed.AttemptRequest{}, managed.ErrUnsupported
	}
}
