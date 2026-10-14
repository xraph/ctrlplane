package managedstate

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

const recoveryTerminalSelectionKind = "removal_recovery_terminal_selection"

type recoveryTerminalSelection struct {
	SchemaVersion managed.Decimal    `json:"schema_version"`
	AttemptRef    managed.AttemptRef `json:"attempt_ref"`
	OperationID   id.ID              `json:"operation_id"`
	RequestDigest string             `json:"request_digest"`
}

func recoveryTerminalFactMatchesHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, authority recoveryTerminalAuthority, event attemptEvent, input any) error {
	var expected any

	kind, key, err := attemptMutationFact(view, input)
	if err != nil {
		return err
	}

	if event.FactKind != kind || event.FactKey != key || event.Receipt.AcceptedAt.Before(authority.acceptedAt) {
		return managed.ErrUnavailable
	}

	if _, settling := input.(managed.AttemptSettlementRequest); !settling {
		settlement, readErr := attemptSettlementRead(ctx, tx, view)
		if readErr != nil {
			return readErr
		}

		if settlement.Disposition != authority.coverage.Facts.Disposition || settlement.CoverageDigest != authority.coverage.Digest {
			return managed.ErrUnavailable
		}
	}

	switch q := input.(type) {
	case managed.AttemptSettlementRequest:
		expected, err = recoverySettlementFact(view, q, authority, event.Receipt.AcceptedAt)
	case managed.AttemptBindingSettlementRequest:
		expected, err = recoveryBindingSettlementFact(ctx, tx, target, view, q, authority, event.Receipt)
	case managed.AttemptCommandRequest:
		if q.NativeCommand.Validate() != nil || q.CurrentBinding != nil {
			return managed.ErrUnavailable
		}

		fence, readErr := attemptFenceRead(ctx, tx, target, view, attemptBinding(view, q.ReservationID))
		if readErr != nil {
			return readErr
		}

		original, readErr := attemptOriginal(ctx, tx, target, q.ReservationID)
		if readErr != nil {
			return readErr
		}

		if q.ExpectedNativeVersion != fence.NativeAcceptance.BindingVersion || q.Kind == managed.AttemptFinish && authority.coverage.Facts.Disposition != managed.Removed || q.Kind == managed.AttemptAbort && authority.coverage.Facts.Disposition != managed.SettledWithoutDeletion {
			return managed.ErrUnavailable
		}

		if q.Kind == managed.AttemptAbort {
			if _, readErr = attemptWitnessRead(ctx, tx, target, view, attemptBinding(view, q.ReservationID)); readErr != nil {
				return readErr
			}
		}

		expected = managed.AttemptCommandRecord{SchemaVersion: "1", Key: managed.AttemptCommandKey{AttemptBindingKey: attemptBinding(view, q.ReservationID), Kind: q.Kind}, DescriptorDigest: view.ref.DescriptorDigest, BindingReference: original, ExpectedNativeVersion: q.ExpectedNativeVersion, NativeCommand: q.NativeCommand, AuthorizationID: q.ID, AuthorizationDigest: event.RequestDigest, IssuedAt: event.Receipt.AcceptedAt}
	case managed.AttemptFinishRequest:
		expected, err = recoveryTerminalFact(ctx, tx, target, view, q.ID, q.ReservationID, q.Acceptance, time.Time{}, time.Time{}, managed.AttemptFinish, authority, event.Receipt.AcceptedAt)
	case managed.AttemptAbortRequest:
		expected, err = recoveryTerminalFact(ctx, tx, target, view, q.ID, q.ReservationID, q.Acceptance, q.ProofVerifiedAt, q.ProofValidUntil, managed.AttemptAbort, authority, event.Receipt.AcceptedAt)
	case managed.AttemptRequest:
		if view.state.TerminalDigest != view.state.SettlementDigest || view.state.TerminalDigest == "" || target.Phase != managed.Deleted || target.TerminalDisposition != managed.Removed || authority.coverage.Facts.Disposition != managed.Removed {
			return managed.ErrUnavailable
		}

		for _, reservationID := range view.descriptor.ReservationIDs {
			retained, readErr := reservation(target, reservationID)
			if readErr != nil {
				return readErr
			}

			if retained.Phase == managed.Revoked {
				continue
			}

			record, readErr := attemptBindingRead(ctx, tx, target, view, attemptBinding(view, reservationID))
			if readErr != nil {
				return readErr
			}

			if record.Finish == nil || record.Abort != nil {
				return managed.ErrUnavailable
			}
		}

		return nil
	default:
		return managed.ErrUnsupported
	}

	if err != nil {
		return err
	}

	raw, err := tx.ReadRecord(ctx, kind, view.scope, key)
	if err != nil {
		return err
	}

	expectedRaw, err := json.Marshal(expected)
	if err != nil {
		return err
	}

	if len(expectedRaw) > managed.MaxAttemptFence {
		return managed.ErrCapacity
	}

	if !reflect.DeepEqual(raw, expectedRaw) {
		return managed.ErrUnavailable
	}

	return nil
}

func recoveryTerminalFact(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, operation, reservationID id.ID, acceptance managed.AttemptNativeAcceptance, verified, until time.Time, kind string, authority recoveryTerminalAuthority, acceptedAt time.Time) (managed.AttemptTerminalRecord, error) {
	var zero managed.AttemptTerminalRecord

	key := attemptBinding(view, reservationID)

	command, err := attemptCommandRead(ctx, tx, target, view, managed.AttemptCommandKey{AttemptBindingKey: key, Kind: kind})
	if err != nil {
		return zero, err
	}

	if err = attemptNativeMatch(command, acceptance, acceptedAt); err != nil {
		return zero, err
	}

	fence, err := attemptFenceRead(ctx, tx, target, view, key)
	if err != nil {
		return zero, err
	}

	if fence.NativeFenceDigest != acceptance.NativeFenceDigest {
		return zero, managed.ErrAuthority
	}

	fenceHash, err := attemptFact("removal_fence", fence)
	if err != nil {
		return zero, err
	}

	commandHash, err := attemptHash(attemptCommandDomain, command)
	if err != nil {
		return zero, err
	}

	witnessDigest := ""

	if kind == managed.AttemptAbort {
		if authority.coverage.Facts.Disposition != managed.SettledWithoutDeletion {
			return zero, managed.ErrBlocked
		}

		witness, err := attemptWitnessRead(ctx, tx, target, view, key)
		if err != nil {
			return zero, err
		}

		if verified.Before(witness.WitnessedAt) || verified.After(acceptedAt) || !until.After(acceptedAt) || !until.After(verified) {
			return zero, managed.ErrBlocked
		}

		witnessDigest, err = attemptFact("removal_binding_settlement", witness)
		if err != nil {
			return zero, err
		}
	} else if kind != managed.AttemptFinish || authority.coverage.Facts.Disposition != managed.Removed {
		return zero, managed.ErrBlocked
	}

	return managed.AttemptTerminalRecord{LocalOperationID: operation, SchemaVersion: "1", Key: key, DescriptorDigest: view.ref.DescriptorDigest, CommandDigest: commandHash, FenceFactDigest: fenceHash, AttemptNativeAcceptance: acceptance, ProofVerifiedAt: verified, ProofValidUntil: until, SettlementWitnessDigest: witnessDigest}, nil
}

func recoveryTerminalDecodeRequest(witness recoveryTerminalRequest) (any, error) {
	switch witness.Kind {
	case "attempt_settle":
		return attemptDecode[managed.AttemptSettlementRequest](witness.Request, maxRecoveryTerminalRequest)
	case "attempt_witness":
		return attemptDecode[managed.AttemptBindingSettlementRequest](witness.Request, maxRecoveryTerminalRequest)
	case "attempt_command":
		return attemptDecode[managed.AttemptCommandRequest](witness.Request, maxRecoveryTerminalRequest)
	case "attempt_finish":
		return attemptDecode[managed.AttemptFinishRequest](witness.Request, maxRecoveryTerminalRequest)
	case "attempt_abort":
		return attemptDecode[managed.AttemptAbortRequest](witness.Request, maxRecoveryTerminalRequest)
	case "attempt_tombstone":
		return attemptDecode[managed.AttemptRequest](witness.Request, maxRecoveryTerminalRequest)
	default:
		return nil, managed.ErrUnsupported
	}
}

// Historical inspection authenticates every terminal request before accepting the
// completed binding. It never skips original slot, authority or receipt checks.
func validateRecoveryTerminalInspectionHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView) error {
	if view.state.SettlementDigest == "" {
		return nil
	}

	authority, err := recoveryTerminalAuthorityHeld(ctx, tx, target, view)
	if err != nil {
		return err
	}

	settlement, err := attemptSettlementRead(ctx, tx, view)
	if err != nil {
		return err
	}

	operations := []id.ID{settlement.LocalOperationID}

	for _, reservationID := range view.descriptor.ReservationIDs {
		retained, readErr := reservation(target, reservationID)
		if readErr != nil {
			return readErr
		}

		if retained.Phase == managed.Revoked {
			continue
		}

		record, readErr := attemptBindingRead(ctx, tx, target, view, attemptBinding(view, reservationID))
		if readErr != nil {
			return readErr
		}

		for _, command := range record.Commands {
			if command.Key.Kind == managed.AttemptFinish || command.Key.Kind == managed.AttemptAbort {
				operations = append(operations, command.AuthorizationID)
			}
		}

		if record.Witness != nil {
			operations = append(operations, record.Witness.LocalOperationID)
		}

		if record.Finish != nil {
			operations = append(operations, record.Finish.LocalOperationID)
		}

		if record.Abort != nil {
			operations = append(operations, record.Abort.LocalOperationID)
		}
	}

	terminal, terminalErr := attemptRead[recoveryTerminalSelection](ctx, tx, recoveryTerminalSelectionKind, view.scope, view.ref.AttemptID.String(), managed.MaxRecoveryReceipt)
	if view.state.TerminalDigest != "" {
		if terminalErr != nil {
			return terminalErr
		}

		if terminal.SchemaVersion != "1" || terminal.AttemptRef != view.ref || !digest(terminal.RequestDigest) {
			return managed.ErrUnavailable
		}

		operations = append(operations, terminal.OperationID)
	} else if !errors.Is(terminalErr, managed.ErrNotFound) {
		if terminalErr != nil {
			return terminalErr
		}

		return managed.ErrUnavailable
	}

	for _, operation := range operations {
		witness, readErr := attemptRead[recoveryTerminalRequest](ctx, tx, recoveryTerminalRequestKind, view.scope, operation.String(), maxRecoveryTerminalRequest)
		if readErr != nil {
			return readErr
		}

		input, readErr := recoveryTerminalDecodeRequest(witness)
		if readErr != nil {
			return readErr
		}

		event, readErr := recoveryTerminalAcceptedRequestHeld(ctx, tx, view, operation, witness.Kind, input, authority)
		if readErr != nil {
			return readErr
		}

		if terminalErr == nil && operation == terminal.OperationID && (event.Kind != "attempt_tombstone" || event.RequestDigest != terminal.RequestDigest) {
			return managed.ErrUnavailable
		}

		if readErr = recoveryTerminalFactMatchesHeld(ctx, tx, target, view, authority, event, input); readErr != nil {
			return readErr
		}
	}

	return nil
}
