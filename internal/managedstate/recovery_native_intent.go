package managedstate

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"time"

	"github.com/xraph/ctrlplane/managed"
)

const recoveryNativeCloseSelectionKind = "removal_native_close_selection"

// One immutable intent identity is selected for each original guard. Only its
// exact authenticated close result may fill Close; neither field grants a send.
type recoveryNativeCloseSelection struct {
	SchemaVersion managed.Decimal              `json:"schema_version"`
	BindingDigest string                       `json:"binding_digest"`
	Intent        managed.RecoveryAssociation  `json:"intent"`
	Close         *managed.RecoveryAssociation `json:"close,omitempty"`
}

func recoveryNativeIntentFingerprint(request managed.RecoveryNativeIntentRequest) (string, error) {
	return attemptHash("ctrlplane.managed.native-intent-acceptance.v1", request)
}

func recoveryNativeIntentShape(facts managed.RecoveryNativeIntent) error {
	if facts.Profile != managed.RemovalRecoveryProfile || !token(facts.RequestID) || !digest(facts.RequestDigest) || len(facts.Command) == 0 || facts.CommandDigest != managed.Digest(facts.Command) {
		return managed.ErrInvalid
	}

	switch facts.Operation {
	case "revalidate":
		if facts.Permission != nil || facts.Proof != nil || facts.Cutoff != nil || facts.Coverage != nil {
			return managed.ErrInvalid
		}
	case "commit":
		if facts.Permission == nil || facts.Proof == nil || facts.Cutoff != nil || facts.Coverage != nil {
			return managed.ErrInvalid
		}
	case "close":
		if facts.Permission != nil || facts.Proof == nil || facts.Cutoff == nil {
			return managed.ErrInvalid
		}
	default:
		return managed.ErrUnsupported
	}

	return nil
}

func recoveryNativeIntentRequestHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, reference managed.RecoveryAssociation, record managed.RecoveryRecord[managed.RecoveryNativeIntent]) (managed.RecoveryNativeIntentRequest, error) {
	var zero managed.RecoveryNativeIntentRequest

	header, err := attemptRead[recoveryRequestHeader](ctx, tx, recoveryRequestKind, view.scope, reference.Key, managed.MaxRecoveryReceipt)
	if err != nil {
		return zero, err
	}

	if header.SchemaVersion != "1" || header.Kind != managed.RecoveryIntentRecord || header.ExpectedCurrentDigest != "" || header.Command.AttemptRef != view.ref || header.Command.Key != view.descriptor.Key || header.Command.ID != record.Acceptance.OperationID {
		return zero, managed.ErrUnavailable
	}

	if _, err = header.Command.LocalCommand(); err != nil {
		return zero, err
	}

	request := managed.RecoveryNativeIntentRequest{Command: header.Command, Intent: record.Facts}
	if header.DerivedCoverage {
		if record.Facts.Operation != "close" || record.Facts.Coverage == nil {
			return zero, managed.ErrUnavailable
		}

		selection, readErr := attemptRead[recoveryCoverageSelection](ctx, tx, recoveryCoverageSelectionKind, view.scope, view.ref.AttemptID.String(), managed.MaxRecoveryReceipt)
		if readErr != nil {
			return zero, readErr
		}

		if selection.Sponsor != reference || selection.Command != header.Command {
			return zero, managed.ErrUnavailable
		}

		if _, readErr = recoveryCoverageReadHeld(ctx, tx, target, view, record.Facts.Coverage); readErr != nil {
			return zero, readErr
		}

		request.Intent.Coverage = nil
	} else {
		fingerprint, hashErr := recoveryNativeIntentFingerprint(request)
		if hashErr != nil {
			return zero, hashErr
		}

		if fingerprint != record.Acceptance.RequestDigest {
			return zero, managed.ErrUnavailable
		}
	}

	return request, nil
}

func recoveryCoverageBindingRecordHeld(ctx context.Context, tx ControllerTransaction, view attemptView, coverage managed.RecoveryRecord[managed.EffectiveRemovalCoverage], binding managed.RemovalRecoveryBinding) (managed.RecoveryRecord[managed.EffectiveRemovalBinding], error) {
	var zero managed.RecoveryRecord[managed.EffectiveRemovalBinding]

	key, err := recoveryFactKey(attemptKey(view), managed.RecoveryCoverageBindingRecord, coverage.Acceptance.OperationID, managed.EffectiveRemovalBinding{Binding: binding})
	if err != nil {
		return zero, err
	}

	record, err := recoveryReadNamedFact[managed.EffectiveRemovalBinding](ctx, tx, attemptKey(view), managed.RecoveryCoverageBindingRecord, key)
	if err != nil {
		return zero, err
	}

	if record.Acceptance != coverage.Acceptance || record.Facts.Binding != binding {
		return zero, managed.ErrUnavailable
	}

	return record, nil
}

func recoveryNativeIntentInputsHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, facts managed.RecoveryNativeIntent, acceptedAt time.Time) error {
	if err := recoveryNativeIntentShape(facts); err != nil {
		return err
	}

	binding, err := recoveryBindingInspectionHeld(ctx, tx, target, view, facts.Binding.Key)
	if err != nil {
		return err
	}

	if binding != facts.Binding {
		return managed.ErrAuthority
	}

	switch facts.Operation {
	case "revalidate":
		return nil
	case "commit":
		if facts.Permission.Kind != managed.RecoveryPermissionRecord || facts.Proof.Kind != managed.RecoveryProofRecord {
			return managed.ErrAuthority
		}

		permission, readErr := recoveryReadFact[managed.ProviderInvocationPermission](ctx, tx, attemptKey(view), *facts.Permission)
		if readErr != nil {
			return readErr
		}

		selected, readErr := invocationStatusSelectionHeld(ctx, tx, view, permission.Facts.Key)
		if readErr != nil {
			return readErr
		}

		if selected.current.Permission != *facts.Permission || selected.current.Ordinal == "0" && !invocationGuardedOriginal(selected) {
			return managed.ErrBlocked
		}

		proof, readErr := recoveryProofExactHeld(ctx, tx, target, view, *facts.Proof)
		if readErr != nil {
			return readErr
		}

		if proof.Facts.Binding != binding || proof.Acceptance.Receipt.AcceptedAt.After(acceptedAt) || !managed.RecoveryProofFresh(proof.Facts, acceptedAt) {
			return managed.ErrAuthority
		}
	case "close":
		if facts.Coverage == nil {
			return managed.ErrInvalid
		}

		coverage, readErr := recoveryCoverageReadHeld(ctx, tx, target, view, facts.Coverage)
		if readErr != nil {
			return readErr
		}

		if *facts.Cutoff != coverage.Facts.Cutoff || coverage.Acceptance.Receipt.AcceptedAt.After(acceptedAt) {
			return managed.ErrAuthority
		}

		member, readErr := recoveryCoverageBindingRecordHeld(ctx, tx, view, coverage, binding)
		if readErr != nil {
			return readErr
		}

		if member.Facts.Proof != *facts.Proof {
			return managed.ErrAuthority
		}
	}

	return nil
}

func recoveryNativeIntentAcceptedHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, reference managed.RecoveryAssociation) (managed.RecoveryRecord[managed.RecoveryNativeIntent], error) {
	var zero managed.RecoveryRecord[managed.RecoveryNativeIntent]
	if reference.Kind != managed.RecoveryIntentRecord {
		return zero, managed.ErrInvalid
	}

	record, err := recoveryReadFact[managed.RecoveryNativeIntent](ctx, tx, attemptKey(view), reference)
	if err != nil {
		return zero, err
	}

	request, err := recoveryNativeIntentRequestHeld(ctx, tx, target, view, reference, record)
	if err != nil {
		return zero, err
	}

	kind := "recovery_native_intent"
	if request.Intent.Operation == "close" {
		kind = "recovery_close_intent"
	}

	if err = recoveryProtocolEventHeld(ctx, tx, view, kind, reference, record.Acceptance); err != nil {
		return zero, err
	}

	return record, nil
}

// Full authority follows accepted request custody. Revalidate intents have no
// predecessor proof edge, keeping proof and coverage validation acyclic.
func recoveryNativeIntentExactHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, reference managed.RecoveryAssociation) (managed.RecoveryRecord[managed.RecoveryNativeIntent], error) {
	var zero managed.RecoveryRecord[managed.RecoveryNativeIntent]

	record, err := recoveryNativeIntentAcceptedHeld(ctx, tx, target, view, reference)
	if err != nil {
		return zero, err
	}

	if err = recoveryNativeIntentInputsHeld(ctx, tx, target, view, record.Facts, record.Acceptance.Receipt.AcceptedAt); err != nil {
		return zero, err
	}

	return record, nil
}

func recoveryNativeIntentReplayHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, request managed.RecoveryNativeIntentRequest) (managed.RecoveryRecord[managed.RecoveryNativeIntent], error) {
	var zero managed.RecoveryRecord[managed.RecoveryNativeIntent]

	key, err := recoveryFactKey(attemptKey(view), managed.RecoveryIntentRecord, request.Command.ID, request.Intent)
	if err != nil {
		return zero, err
	}

	record, err := recoveryReadNamedFact[managed.RecoveryNativeIntent](ctx, tx, attemptKey(view), managed.RecoveryIntentRecord, key)
	if err != nil {
		if errors.Is(err, managed.ErrNotFound) {
			if _, rowErr := tx.ReadRecord(ctx, string(managed.RecoveryIntentRecord), view.scope, key); !errors.Is(rowErr, managed.ErrNotFound) {
				return zero, managed.ErrUnavailable
			}

			if _, receiptErr := tx.ReadReceipt(ctx, target.Key, request.Command.ID, ""); !errors.Is(receiptErr, managed.ErrNotFound) {
				return zero, managed.ErrUnavailable
			}
		}

		return zero, err
	}

	reference, err := managed.RecoveryAssociationFor(key, record)
	if err != nil {
		return zero, err
	}

	saved, err := recoveryNativeIntentRequestHeld(ctx, tx, target, view, reference, record)
	if err != nil {
		return zero, err
	}

	if !reflect.DeepEqual(saved, request) {
		return zero, managed.ErrConflict
	}

	return recoveryNativeIntentExactHeld(ctx, tx, target, view, reference)
}

func recoveryNativeFreshCommand(target *managed.Target, view attemptView, command managed.RecoveryCommand, now time.Time) error {
	local, err := command.LocalCommand()
	if err != nil {
		return err
	}

	if !view.validated || view.recoveryActivation == nil || target.Key != local.Key || view.ref != command.AttemptRef {
		return managed.ErrAuthority
	}

	if target.Revision != local.Revision || target.LeaseEpoch != local.LeaseEpoch || target.LeaseOwner != local.Owner || !now.Before(target.LeaseUntil) {
		return managed.ErrConflict
	}

	if view.state.TerminalDigest != "" || view.state.SettlementDigest != "" || view.state.Successor != nil || target.Phase == managed.Deleted {
		return managed.ErrBlocked
	}

	return nil
}

func stageRecoveryNativeIntent(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, request managed.RecoveryNativeIntentRequest, now time.Time) (*recoveryActivationRows, managed.RecoveryRecord[managed.RecoveryNativeIntent], error) {
	var zero managed.RecoveryRecord[managed.RecoveryNativeIntent]
	if err := recoveryNativeFreshCommand(target, view, request.Command, now); err != nil {
		return nil, zero, err
	}

	if err := recoveryNativeIntentShape(request.Intent); err != nil {
		return nil, zero, err
	}

	if _, err := recoveryBindingHeld(ctx, tx, target, view, request.Intent.Binding.Key); err != nil {
		return nil, zero, err
	}

	if err := removalLocalIDAvailable(ctx, tx, request.Command.ID); err != nil {
		return nil, zero, err
	}

	firstClose := request.Intent.Operation == "close" && request.Intent.Coverage == nil

	var (
		staged *recoveryActivationRows
		record managed.RecoveryRecord[managed.RecoveryNativeIntent]
		err    error
	)
	if firstClose {
		staged, _, err = stageRecoveryCoverageCloseIntent(ctx, tx, target, view, request, now)
		if err != nil {
			return nil, zero, err
		}

		key, keyErr := recoveryFactKey(attemptKey(view), managed.RecoveryIntentRecord, request.Command.ID, request.Intent)
		if keyErr != nil {
			return nil, zero, keyErr
		}

		record, err = recoveryReadNamedFact[managed.RecoveryNativeIntent](ctx, staged, attemptKey(view), managed.RecoveryIntentRecord, key)
		if err != nil {
			return nil, zero, err
		}
	} else {
		if err = recoveryNativeIntentInputsHeld(ctx, tx, target, view, request.Intent, now); err != nil {
			return nil, zero, err
		}

		if request.Intent.Operation != "close" {
			if _, readErr := tx.ReadRecord(ctx, recoveryCoverageSelectionKind, view.scope, view.ref.AttemptID.String()); !errors.Is(readErr, managed.ErrNotFound) {
				if readErr != nil {
					return nil, zero, readErr
				}

				return nil, zero, managed.ErrBlocked
			}
		}

		if request.Intent.Operation == "commit" {
			if view.state.RevocationDigest != "" {
				return nil, zero, managed.ErrBlocked
			}

			permission, readErr := recoveryReadFact[managed.ProviderInvocationPermission](ctx, tx, attemptKey(view), *request.Intent.Permission)
			if readErr != nil {
				return nil, zero, readErr
			}

			selected, readErr := invocationSelectedHeld(ctx, tx, view, permission.Facts.Key.Step)
			if readErr != nil {
				return nil, zero, readErr
			}

			if selected.current.Permission != *request.Intent.Permission || selected.marker != nil || selected.result != nil || selected.closure != nil {
				return nil, zero, managed.ErrBlocked
			}

			current, _, readErr := recoveryReadCurrent(ctx, tx, request.Intent.Binding)
			if readErr != nil {
				return nil, zero, readErr
			}

			if current.Proof != *request.Intent.Proof {
				return nil, zero, managed.ErrConflict
			}
		}

		fingerprint, hashErr := recoveryNativeIntentFingerprint(request)
		if hashErr != nil {
			return nil, zero, hashErr
		}

		cost := uint64(0)
		if request.Intent.Operation == "close" {
			cost = 1
		}

		revision, parseErr := view.state.Revision.Uint64()
		if parseErr != nil || target.Revision > 1<<63-1-cost || revision > 1<<63-1-cost {
			return nil, zero, managed.ErrCapacity
		}

		receipt := managed.Receipt{ID: request.Command.ID, Digest: fingerprint, Revision: target.Revision + cost, LeaseEpoch: target.LeaseEpoch, AcceptedAt: now.UTC(), LeaseUntil: target.LeaseUntil}
		acceptance := managed.RecoveryAcceptance{OperationID: request.Command.ID, RequestDigest: fingerprint, Receipt: managed.ReceiptWire(receipt)}

		record, err = activationRecord(managed.RecoveryIntentRecord, request.Intent, acceptance)
		if err != nil {
			return nil, zero, err
		}

		staged = &recoveryActivationRows{ControllerTransaction: tx, key: target.Key, receipt: receipt}

		reference, writeErr := recoveryWriteFact(ctx, staged, attemptKey(view), record)
		if writeErr != nil {
			return nil, zero, writeErr
		}

		kind := "recovery_native_intent"
		if cost == 1 {
			kind = "recovery_close_intent"
		}

		if err = stageRecoveryProtocolEvent(ctx, staged, view, kind, reference, acceptance); err != nil {
			return nil, zero, err
		}

		if cost == 1 {
			if view.recoveryActivation == nil {
				return nil, zero, managed.ErrUnsupported
			}

			state := recoveryAttemptState{AttemptState: view.state, Activation: *view.recoveryActivation, PendingOriginals: append([]string(nil), view.recoveryPendingOriginals...)}

			state.Revision = managed.Decimal(strconv.FormatUint(revision+1, 10))
			if err = attemptWrite(ctx, staged, "removal_attempt_state", view.scope, view.ref.AttemptID.String(), state, false, managed.MaxAttemptFact); err != nil {
				return nil, zero, err
			}
		}
	}

	key, err := recoveryFactKey(attemptKey(view), managed.RecoveryIntentRecord, request.Command.ID, record.Facts)
	if err != nil {
		return nil, zero, err
	}

	reference, err := managed.RecoveryAssociationFor(key, record)
	if err != nil {
		return nil, zero, err
	}

	header := recoveryRequestHeader{SchemaVersion: "1", Kind: managed.RecoveryIntentRecord, Command: request.Command, DerivedCoverage: firstClose}
	if err = attemptWrite(ctx, staged, recoveryRequestKind, view.scope, key, header, true, managed.MaxRecoveryReceipt); err != nil {
		return nil, zero, err
	}

	if request.Intent.Operation == "close" {
		bindingKey, keyErr := recoveryBindingKey(request.Intent.Binding)
		if keyErr != nil {
			return nil, zero, keyErr
		}

		if _, readErr := tx.ReadRecord(ctx, recoveryNativeCloseSelectionKind, view.scope, bindingKey); !errors.Is(readErr, managed.ErrNotFound) {
			if readErr != nil {
				return nil, zero, readErr
			}

			return nil, zero, managed.ErrConflict
		}

		selected := recoveryNativeCloseSelection{SchemaVersion: "1", BindingDigest: bindingKey, Intent: reference}
		if err = attemptWrite(ctx, staged, recoveryNativeCloseSelectionKind, view.scope, bindingKey, selected, true, managed.MaxRecoveryReceipt); err != nil {
			return nil, zero, err
		}
	}

	return staged, record, nil
}

// recoveryNativeValidateStaged checks the proposed full graph and both counters
// before any underlying write. Each consumed close obligation is read exactly.
func recoveryNativeValidateStaged(ctx context.Context, staged *recoveryActivationRows, target *managed.Target, view attemptView) (attemptView, error) {
	var zero attemptView

	proposed := *target
	proposed.Revision = staged.receipt.Revision

	after, active, err := readRecoveryAttemptInspection(ctx, staged, &proposed, attemptKey(view))
	if err != nil {
		return zero, err
	}

	if active != view.ref || after.ref != view.ref {
		return zero, managed.ErrConflict
	}

	remaining, err := invocationRemainingReserve(ctx, staged, &proposed, after)
	if err != nil {
		return zero, err
	}

	revision, err := view.state.Revision.Uint64()
	if err != nil {
		return zero, err
	}

	if staged.receipt.Revision < target.Revision || staged.receipt.Revision-target.Revision > 1 {
		return zero, managed.ErrUnavailable
	}

	if err = recoveryCheckHeadroom(target.Revision, revision, staged.receipt.Revision-target.Revision, remaining); err != nil {
		return zero, err
	}

	return after, nil
}

// RecordManagedRecoveryNativeIntent retains the exact producer request before
// external work. Replaying it returns custody, never a transport grant.
func (r *Repository) RecordManagedRecoveryNativeIntent(ctx context.Context, request managed.RecoveryNativeIntentRequest) (managed.RecoveryRecord[managed.RecoveryNativeIntent], error) {
	var out managed.RecoveryRecord[managed.RecoveryNativeIntent]
	if r == nil || r.backend == nil {
		return out, managed.ErrUnsupported
	}

	if _, held := r.backend.(ControllerTransaction); held {
		return out, managed.ErrUnsupported
	}

	cloned, err := recoverySnapshot(request)
	if err != nil {
		return out, err
	}

	command, err := cloned.Command.LocalCommand()
	if err != nil {
		return out, err
	}

	key := managed.AttemptKey{Key: command.Key, AttemptID: cloned.Command.AttemptRef.AttemptID, Generation: cloned.Command.AttemptRef.Generation}

	err = r.controller(ctx, "attempt:"+key.InstanceID.String(), []managed.Key{key.Key}, func(tx ControllerTransaction) error {
		target, readErr := tx.Read(ctx, key.Key)
		if readErr != nil {
			return readErr
		}

		view, active, readErr := readRecoveryAttemptInspection(ctx, tx, target, key)
		if readErr != nil {
			return readErr
		}

		if view.ref != cloned.Command.AttemptRef {
			return managed.ErrAuthority
		}

		out, readErr = recoveryNativeIntentReplayHeld(ctx, tx, target, view, cloned)
		if readErr == nil {
			return nil
		}

		if !errors.Is(readErr, managed.ErrNotFound) {
			return readErr
		}

		if readErr = r.requireConfiguredRecovery(); readErr != nil {
			return readErr
		}

		if active != view.ref {
			return managed.ErrConflict
		}

		staged, _, readErr := stageRecoveryNativeIntent(ctx, tx, target, view, cloned, tx.Now())
		if readErr != nil {
			return readErr
		}
		// First-close request identity includes its derived coverage plan. The receipt
		// binds that plan, so atomic revalidation must reproduce this exact digest.
		fingerprint := staged.receipt.Digest

		_, atomicErr := tx.Atomic(ctx, key.Key, command.ID, fingerprint, func(current *managed.Target, now time.Time) (managed.Receipt, error) {
			checked, currentRef, heldErr := readRecoveryAttemptInspection(ctx, tx, current, key)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if currentRef != cloned.Command.AttemptRef || checked.ref != currentRef {
				return managed.Receipt{}, managed.ErrConflict
			}

			rows, _, heldErr := stageRecoveryNativeIntent(ctx, tx, current, checked, cloned, now)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if rows.receipt.Digest != fingerprint {
				return managed.Receipt{}, managed.ErrConflict
			}

			after, heldErr := recoveryNativeValidateStaged(ctx, rows, current, checked)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			proposed := *current

			proposed.Revision = rows.receipt.Revision
			if _, heldErr = recoveryNativeIntentReplayHeld(ctx, rows, &proposed, after, cloned); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			checked.validated, after.validated, view.validated = false, false, false

			if heldErr = applyRecoveryActivationRows(ctx, tx, rows); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			current.Revision = rows.receipt.Revision

			return rows.receipt, nil
		})
		if atomicErr != nil {
			return atomicErr
		}

		target, readErr = tx.Read(ctx, key.Key)
		if readErr != nil {
			return readErr
		}

		view, _, readErr = readRecoveryAttemptInspection(ctx, tx, target, key)
		if readErr != nil {
			return readErr
		}

		out, readErr = recoveryNativeIntentReplayHeld(ctx, tx, target, view, cloned)

		return readErr
	})
	if err != nil {
		return managed.RecoveryRecord[managed.RecoveryNativeIntent]{}, err
	}

	return out, nil
}

func (r *Repository) readRecoveryNativeIntent(ctx context.Context, request managed.RecoveryExactRequest) (managed.RecoveryRecord[managed.RecoveryNativeIntent], error) {
	var out managed.RecoveryRecord[managed.RecoveryNativeIntent]
	if r == nil || r.backend == nil {
		return out, managed.ErrUnsupported
	}

	if err := attemptVersion(request.Key); err != nil {
		return out, err
	}

	err := r.controller(ctx, "attempt:"+request.Key.InstanceID.String(), []managed.Key{request.Key.Key}, func(tx ControllerTransaction) error {
		target, err := tx.Read(ctx, request.Key.Key)
		if err != nil {
			return err
		}

		view, _, err := readRecoveryAttemptInspection(ctx, tx, target, request.Key)
		if err != nil {
			return err
		}

		out, err = recoveryNativeIntentExactHeld(ctx, tx, target, view, request.Association)

		return err
	})
	if err != nil {
		return managed.RecoveryRecord[managed.RecoveryNativeIntent]{}, err
	}

	return out, nil
}
