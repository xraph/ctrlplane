package managedstate

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

const guardedOriginalKind = "removal_guarded_original"
const guardedOriginalSelectionKind = "guarded_original_selection"
const guardedOriginalMaterializationKind = "guarded_original_materialization"
const maxGuardedOriginal = managed.MaxAttemptFence

type guardedOriginal struct {
	SchemaVersion managed.Decimal                `json:"schema_version"`
	Request       managed.GuardedOriginalRequest `json:"request"`
	Original      managed.ProviderEffectOriginal `json:"original"`
	Proofs        []managed.RecoveryAssociation  `json:"proofs"`
	Digest        string                         `json:"digest"`
}

type guardedOriginalSelection struct {
	SchemaVersion      managed.Decimal              `json:"schema_version"`
	AttemptRef         managed.AttemptRef           `json:"attempt_ref"`
	Step               managed.ProviderStepKey      `json:"step"`
	PendingOperationID id.ID                        `json:"pending_operation_id"`
	PendingDigest      string                       `json:"pending_digest"`
	Root               *managed.RecoveryAssociation `json:"root,omitempty"`
}

type guardedOriginalMaterialization struct {
	SchemaVersion managed.Decimal                               `json:"schema_version"`
	Request       managed.GuardedOriginalMaterializationRequest `json:"request"`
	EffectDigest  string                                        `json:"effect_digest"`
	Root          managed.RecoveryAssociation                   `json:"root"`
}

func guardedOriginalRequestDigest(request managed.GuardedOriginalRequest, proofs []managed.RecoveryAssociation) (string, error) {
	return attemptHash("ctrlplane.managed.guarded-original-request.v1", struct {
		Request managed.GuardedOriginalRequest `json:"request"`
		Proofs  []managed.RecoveryAssociation  `json:"proofs"`
	}{request, proofs})
}
func guardedOriginalDigest(record guardedOriginal) (string, error) {
	record.Digest = ""

	return attemptHash("ctrlplane.managed.guarded-original.v1", record)
}
func guardedOriginalMaterializationDigest(request managed.GuardedOriginalMaterializationRequest, effect string) (string, error) {
	return attemptHash("ctrlplane.managed.guarded-original-materialization.v1", struct {
		Request managed.GuardedOriginalMaterializationRequest `json:"request"`
		Effect  string                                        `json:"effect_digest"`
	}{request, effect})
}

// Fixed activation rows remain immutable. Only this additional selector can name
// a later accepted original, and its pending row must authenticate independently.
func guardedOriginalSelectionHeld(ctx context.Context, tx ControllerTransaction, view attemptView, step managed.ProviderStepKey) (guardedOriginalSelection, error) {
	var zero guardedOriginalSelection

	key, err := retryStepKey(attemptKey(view), step)
	if err != nil {
		return zero, err
	}

	selected, err := attemptRead[guardedOriginalSelection](ctx, tx, guardedOriginalSelectionKind, view.scope, key, managed.MaxStepRetryPointer)
	if err != nil {
		return zero, err
	}

	if selected.SchemaVersion != "1" || selected.AttemptRef != view.ref || selected.Step != step || selected.PendingOperationID.Prefix() != managed.PrefixOperation || !digest(selected.PendingDigest) {
		return zero, managed.ErrUnavailable
	}

	return selected, nil
}

func guardedOriginalPermission(target *managed.Target, view attemptView, step managed.ProviderStepKey, receipt managed.Receipt, admission managed.AttemptAdmission) (managed.ProviderEffectOriginal, error) {
	var zero managed.ProviderEffectOriginal
	if target.Deletion == nil || target.Seal == 0 || admission.AttemptRef != view.ref {
		return zero, managed.ErrUnavailable
	}

	resource := step.Service
	if resource == "" {
		resource = "network"
	}

	fence := admission.FenceSetDigest
	if view.descriptor.Generation == "1" {
		fence = target.Deletion.FenceDigest
	}

	permission := managed.ProviderStepPermission{CreateProfile: "ctrlplane.managed.create.v1", Step: step, CommandID: receipt.ID, Digest: receipt.Digest, SpecDigest: target.Manifest.SpecDigest, Physical: target.Physical, Seal: target.Seal, FenceDigest: fence, ResourceID: target.Resources[resource], IssuedAt: receipt.AcceptedAt, Revision: receipt.Revision}

	original := managed.ProviderEffectOriginal{Key: managed.ProviderEffectBindingKey{AttemptKey: attemptKey(view), Step: step, OriginalCommandID: receipt.ID}, AttemptRef: view.ref, Permission: managed.ProviderPermissionWire(permission), Receipt: managed.ReceiptWire(receipt)}
	if err := managed.ValidateProviderEffectOriginal(original); err != nil {
		return zero, err
	}

	return original, nil
}

func guardedOriginalFixedHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, step managed.ProviderStepKey) (int, error) {
	if !view.validated || view.recoveryActivation == nil {
		return 0, managed.ErrUnsupported
	}

	steps, err := attemptSteps(target, view)
	if err != nil {
		return 0, err
	}

	index := -1

	for i, candidate := range steps {
		if candidate == step {
			index = i

			break
		}
	}

	if index < 0 {
		return 0, managed.ErrAuthority
	}

	key, err := retryStepKey(attemptKey(view), step)
	if err != nil {
		return 0, err
	}

	fixed, err := attemptRead[recoveryFixedSelection](ctx, tx, recoveryFixedSelectionKind, view.scope, key, managed.MaxStepRetryPointer)
	if err != nil {
		return 0, err
	}

	if fixed.SchemaVersion != "1" || fixed.AttemptRef != view.ref || fixed.Activation != *view.recoveryActivation || fixed.Step != step || fixed.Disposition != "unissued" || fixed.Root != nil || view.state.ProviderSlots[index] != 0 {
		return 0, managed.ErrAuthority
	}

	return index, nil
}

// Pending reads never resolve a root or provider evidence. This keeps effect
// retention and the full attempt inspection graph acyclic.
func guardedOriginalPendingHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, step managed.ProviderStepKey) (guardedOriginal, guardedOriginalSelection, error) {
	var zero guardedOriginal
	if _, err := guardedOriginalFixedHeld(ctx, tx, target, view, step); err != nil {
		return zero, guardedOriginalSelection{}, err
	}

	selected, err := guardedOriginalSelectionHeld(ctx, tx, view, step)
	if err != nil {
		return zero, selected, err
	}

	record, err := attemptRead[guardedOriginal](ctx, tx, guardedOriginalKind, view.scope, selected.PendingOperationID.String(), maxGuardedOriginal)
	if err != nil {
		return zero, selected, err
	}

	hash, err := guardedOriginalDigest(record)
	if err != nil {
		return zero, selected, err
	}

	if record.SchemaVersion != "1" || hash != record.Digest || hash != selected.PendingDigest || record.Request.Command.AttemptRef != view.ref || record.Request.Command.ID != selected.PendingOperationID || record.Request.Step != step {
		return zero, selected, managed.ErrUnavailable
	}

	local, err := record.Request.Command.LocalCommand()
	if err != nil || local.Key != target.Key {
		return zero, selected, managed.ErrAuthority
	}

	fingerprint, err := guardedOriginalRequestDigest(record.Request, record.Proofs)
	if err != nil {
		return zero, selected, err
	}

	receipt, err := record.Original.Receipt.Value()
	if err != nil {
		return zero, selected, err
	}

	if receipt.ID != local.ID || receipt.Digest != fingerprint || local.Revision >= 1<<63-1 || receipt.Revision != local.Revision+1 || receipt.LeaseEpoch != local.LeaseEpoch {
		return zero, selected, managed.ErrUnavailable
	}

	if err = attemptReceiptCheck(ctx, tx, target.Key, receipt); err != nil {
		return zero, selected, err
	}

	identity, err := attemptRead[attemptIdentity](ctx, tx, "removal_identity", "", receipt.ID.String(), managed.MaxAttemptPointer)
	if err != nil {
		return zero, selected, err
	}

	if identity.SchemaVersion != "1" || identity.ID != receipt.ID || identity.Role != "invocation" || identity.Key != target.Key || identity.AttemptRef != view.ref {
		return zero, selected, managed.ErrUnavailable
	}

	admission, err := attemptRead[managed.AttemptAdmission](ctx, tx, "removal_admission", view.scope, view.ref.AttemptID.String(), managed.MaxAttemptFact)
	if err != nil {
		return zero, selected, err
	}

	expected, err := guardedOriginalPermission(target, view, step, receipt, admission)
	if err != nil {
		return zero, selected, err
	}

	if !managed.ProviderEffectOriginalEqual(expected, record.Original) {
		return zero, selected, managed.ErrAuthority
	}

	if err = guardedOriginalProofsHeld(ctx, tx, target, view, record, nil, time.Time{}); err != nil {
		return zero, selected, err
	}

	event, err := attemptEventRead(ctx, tx, view, receipt.ID)
	if err != nil {
		return zero, selected, err
	}

	if event.Kind != "recovery_original_pending" || event.RequestDigest != fingerprint || event.FactKind != "" || event.FactKey != "" || event.FactDigest != fingerprint {
		return zero, selected, managed.ErrUnavailable
	}

	return record, selected, nil
}

// Every original proof remains fixed at permission acceptance. A later live
// observation may refresh the same guard, but cannot replace its identity.
func guardedOriginalProofsHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, record guardedOriginal, current []invocationProofMember, now time.Time) error {
	if len(record.Proofs) == 0 || len(record.Proofs) > managed.MaxRecoveryBindings {
		return managed.ErrCapacity
	}

	index := 0

	for _, reservationID := range view.descriptor.ReservationIDs {
		retained, err := reservation(target, reservationID)
		if err != nil {
			return err
		}

		if retained.Phase == managed.Revoked {
			continue
		}

		if index >= len(record.Proofs) {
			return managed.ErrUnavailable
		}

		binding, err := recoveryBindingInspectionHeld(ctx, tx, target, view, attemptBinding(view, reservationID))
		if err != nil {
			return err
		}

		proof, err := recoveryProofExactHeld(ctx, tx, target, view, record.Proofs[index])
		if err != nil {
			return err
		}

		proofRevision, err := proof.Acceptance.Receipt.Revision.Uint64()
		if err != nil {
			return err
		}

		permissionRevision, err := record.Original.Permission.Revision.Uint64()
		if err != nil {
			return err
		}

		if proof.Facts.Binding != binding || proof.Acceptance.Receipt.AcceptedAt.After(record.Original.Permission.IssuedAt) || proofRevision >= permissionRevision || !managed.RecoveryProofFresh(proof.Facts, record.Original.Permission.IssuedAt) {
			return managed.ErrAuthority
		}

		if current != nil {
			if index >= len(current) || current[index].binding != binding || current[index].proof.Facts.GuardID != proof.Facts.GuardID || current[index].proof.Facts.GuardRootDigest != proof.Facts.GuardRootDigest || !managed.RecoveryProofFresh(current[index].proof.Facts, now) {
				return managed.ErrAuthority
			}
		}

		index++
	}

	if index != len(record.Proofs) || current != nil && len(current) != index {
		return managed.ErrUnavailable
	}

	return nil
}

func guardedOriginalPredecessorsHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, index int) error {
	steps, err := attemptSteps(target, view)
	if err != nil {
		return err
	}

	for _, step := range steps[:index] {
		selected, readErr := invocationSelectedHeld(ctx, tx, view, step)
		if readErr != nil {
			return readErr
		}

		if selected.result == nil || selected.result.Facts.Result.Outcome != managed.PortTerminal {
			return managed.ErrBlocked
		}
	}

	return nil
}

func stageGuardedOriginalPending(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, request managed.GuardedOriginalRequest, now time.Time) (*recoveryActivationRows, guardedOriginal, error) {
	var zero guardedOriginal
	if err := recoveryNativeFreshCommand(target, view, request.Command, now); err != nil {
		return nil, zero, err
	}

	index, err := guardedOriginalFixedHeld(ctx, tx, target, view, request.Step)
	if err != nil {
		return nil, zero, err
	}

	if _, err = guardedOriginalSelectionHeld(ctx, tx, view, request.Step); !errors.Is(err, managed.ErrNotFound) {
		if err != nil {
			return nil, zero, err
		}

		return nil, zero, managed.ErrConflict
	}

	if err = guardedOriginalPredecessorsHeld(ctx, tx, target, view, index); err != nil {
		return nil, zero, err
	}

	members, err := invocationLiveProofsHeld(ctx, tx, target, view, now)
	if err != nil {
		return nil, zero, err
	}

	refs := make([]managed.RecoveryAssociation, 0, len(members))
	for _, member := range members {
		refs = append(refs, member.current.Proof)
	}

	fingerprint, err := guardedOriginalRequestDigest(request, refs)
	if err != nil {
		return nil, zero, err
	}

	if target.Revision >= 1<<63-1 {
		return nil, zero, managed.ErrCapacity
	}

	receipt := managed.Receipt{ID: request.Command.ID, Digest: fingerprint, Revision: target.Revision + 1, LeaseEpoch: target.LeaseEpoch, AcceptedAt: now.UTC(), LeaseUntil: target.LeaseUntil}

	admission, err := attemptRead[managed.AttemptAdmission](ctx, tx, "removal_admission", view.scope, view.ref.AttemptID.String(), managed.MaxAttemptFact)
	if err != nil {
		return nil, zero, err
	}

	original, err := guardedOriginalPermission(target, view, request.Step, receipt, admission)
	if err != nil {
		return nil, zero, err
	}

	record := guardedOriginal{SchemaVersion: "1", Request: request, Original: original, Proofs: refs}

	record.Digest, err = guardedOriginalDigest(record)
	if err != nil {
		return nil, zero, err
	}

	staged := &recoveryActivationRows{ControllerTransaction: tx, key: target.Key, receipt: receipt}
	if err = attemptWrite(ctx, staged, guardedOriginalKind, view.scope, request.Command.ID.String(), record, true, maxGuardedOriginal); err != nil {
		return nil, zero, err
	}

	selector := guardedOriginalSelection{SchemaVersion: "1", AttemptRef: view.ref, Step: request.Step, PendingOperationID: request.Command.ID, PendingDigest: record.Digest}

	stepKey, err := retryStepKey(attemptKey(view), request.Step)
	if err != nil {
		return nil, zero, err
	}

	if err = attemptWrite(ctx, staged, guardedOriginalSelectionKind, view.scope, stepKey, selector, true, managed.MaxStepRetryPointer); err != nil {
		return nil, zero, err
	}

	event := attemptEvent{SchemaVersion: "1", AttemptRef: view.ref, Kind: "recovery_original_pending", RequestDigest: fingerprint, FactDigest: fingerprint, Receipt: receipt}
	if err = attemptWrite(ctx, staged, "removal_attempt_event", view.scope, request.Command.ID.String(), event, true, managed.MaxAttemptFact); err != nil {
		return nil, zero, err
	}

	identity := attemptIdentity{SchemaVersion: "1", ID: request.Command.ID, Role: "invocation", Key: target.Key, AttemptRef: view.ref}
	if err = attemptWrite(ctx, staged, "removal_identity", "", request.Command.ID.String(), identity, true, managed.MaxAttemptPointer); err != nil {
		return nil, zero, err
	}

	return staged, record, nil
}

// A positive proof count identifies normalized guarded ordinal zero only after
// its selector, pending receipt and complete proof set have passed inspection.
func invocationGuardedOriginal(selected retrySelected) bool {
	return selected.current.Ordinal == "0" && selected.permission.Facts.PermissionProofCount != "0" && selected.permission.Facts.PermissionProofSetDigest != ""
}

func guardedOriginalProofRows(ctx context.Context, tx *recoveryActivationRows, view attemptView, pending guardedOriginal, acceptance managed.RecoveryAcceptance) ([]managed.RecoveryAssociation, error) {
	refs := make([]managed.RecoveryAssociation, 0, len(pending.Proofs))
	invocation := managed.ProviderInvocationKey{AttemptKey: attemptKey(view), Step: pending.Request.Step, CommandID: pending.Original.Permission.CommandID}

	permissionDigest, err := recoveryPermissionDigest(pending.Original.Permission)
	if err != nil {
		return nil, err
	}

	for _, reference := range pending.Proofs {
		proof, readErr := recoveryReadFact[managed.RemovalRevalidationFacts](ctx, tx, attemptKey(view), reference)
		if readErr != nil {
			return nil, readErr
		}

		bindingDigest, readErr := recoveryBindingKey(proof.Facts.Binding)
		if readErr != nil {
			return nil, readErr
		}

		facts := managed.ProviderPermissionProof{Invocation: invocation, BindingDigest: bindingDigest, PermissionDigest: permissionDigest, Proof: reference, NativeMaterialDigest: proof.Facts.NativeMaterialDigest}

		record, readErr := activationRecord(managed.RecoveryPermissionProofRecord, facts, acceptance)
		if readErr != nil {
			return nil, readErr
		}

		saved, readErr := recoveryWriteFact(ctx, tx, attemptKey(view), record)
		if readErr != nil {
			return nil, readErr
		}

		refs = append(refs, saved)
	}

	return refs, nil
}

func stageGuardedOriginalMaterialization(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, request managed.GuardedOriginalMaterializationRequest, now time.Time) (*recoveryActivationRows, managed.RecoveryAssociation, error) {
	var zero managed.RecoveryAssociation
	if err := recoveryNativeFreshCommand(target, view, request.Command, now); err != nil {
		return nil, zero, err
	}

	pending, selector, err := guardedOriginalPendingHeld(ctx, tx, target, view, request.Step)
	if err != nil {
		return nil, zero, err
	}

	if selector.Root != nil || pending.Digest != request.PendingDigest {
		return nil, zero, managed.ErrConflict
	}
	// After cutoff, materialization only establishes evidence identity for custody
	// closure. Bind, retry and marker entry points continue to refuse issuing authority.
	var members []invocationProofMember
	if view.state.RevocationDigest == "" {
		members, err = invocationLiveProofsHeld(ctx, tx, target, view, now)
		if err != nil {
			return nil, zero, err
		}
	}

	if err = guardedOriginalProofsHeld(ctx, tx, target, view, pending, members, now); err != nil {
		return nil, zero, err
	}

	effect, err := providerEffectReadHeld(ctx, tx, pending.Original)
	if err != nil {
		return nil, zero, err
	}

	rootFacts, err := providerEffectRootHeld(ctx, tx, pending.Original)
	if err != nil {
		return nil, zero, err
	}

	fingerprint, err := guardedOriginalMaterializationDigest(request, effect.Digest)
	if err != nil {
		return nil, zero, err
	}

	receipt := managed.Receipt{ID: request.Command.ID, Digest: fingerprint, Revision: target.Revision, LeaseEpoch: target.LeaseEpoch, AcceptedAt: now.UTC(), LeaseUntil: target.LeaseUntil}
	acceptance := managed.RecoveryAcceptance{OperationID: request.Command.ID, RequestDigest: fingerprint, Receipt: managed.ReceiptWire(receipt)}
	staged := &recoveryActivationRows{ControllerTransaction: tx, key: target.Key, receipt: receipt}

	root, err := activationRecord(managed.RecoveryRootRecord, rootFacts, acceptance)
	if err != nil {
		return nil, zero, err
	}

	rootRef, err := recoveryWriteFact(ctx, staged, attemptKey(view), root)
	if err != nil {
		return nil, zero, err
	}

	proofRefs, err := guardedOriginalProofRows(ctx, staged, view, pending, acceptance)
	if err != nil {
		return nil, zero, err
	}

	setDigest, err := attemptHash("ctrlplane.managed.permission-proof-set.v1", proofRefs)
	if err != nil {
		return nil, zero, err
	}

	invocation := managed.ProviderInvocationKey{AttemptKey: attemptKey(view), Step: request.Step, CommandID: pending.Original.Permission.CommandID}
	permissionFacts := managed.ProviderInvocationPermission{Key: invocation, RootDigest: root.Digest, Ordinal: "0", Permission: pending.Original.Permission, ProviderCommandDigest: rootFacts.ProviderCommandDigest, PermissionProofSetDigest: setDigest, PermissionProofCount: managed.Decimal(strconv.Itoa(len(proofRefs)))}

	permission, err := activationRecord(managed.RecoveryPermissionRecord, permissionFacts, acceptance)
	if err != nil {
		return nil, zero, err
	}

	permissionRef, err := recoveryWriteFact(ctx, staged, attemptKey(view), permission)
	if err != nil {
		return nil, zero, err
	}

	current := managed.ProviderStepRetryCurrent{SchemaVersion: "1", RootDigest: root.Digest, CommandID: invocation.CommandID, Ordinal: "0", Permission: permissionRef, Authority: rootRef}

	current.Digest, err = managed.RetryCurrentDigest(current)
	if err != nil {
		return nil, zero, err
	}

	key, err := retryStepKey(attemptKey(view), request.Step)
	if err != nil {
		return nil, zero, err
	}

	if err = attemptWrite(ctx, staged, retryCurrentKind, view.scope, key, current, true, managed.MaxStepRetryPointer); err != nil {
		return nil, zero, err
	}

	selector.Root = &rootRef
	if err = attemptWrite(ctx, staged, guardedOriginalSelectionKind, view.scope, key, selector, false, managed.MaxStepRetryPointer); err != nil {
		return nil, zero, err
	}

	if err = stageInvocationStatus(ctx, staged, retrySelected{root: root, current: current, permission: permission}, true); err != nil {
		return nil, zero, err
	}

	witness := guardedOriginalMaterialization{SchemaVersion: "1", Request: request, EffectDigest: effect.Digest, Root: rootRef}
	if err = attemptWrite(ctx, staged, guardedOriginalMaterializationKind, view.scope, request.Command.ID.String(), witness, true, managed.MaxAttemptFact); err != nil {
		return nil, zero, err
	}

	if err = stageRecoveryProtocolEvent(ctx, staged, view, "recovery_original_materialize", rootRef, acceptance); err != nil {
		return nil, zero, err
	}

	return staged, rootRef, nil
}
