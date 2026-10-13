package managedstate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/xraph/ctrlplane/managed"
)

// phaseAuditTransaction gives the audit the accepted parent receipt's timestamp.
// Atomic samples a fresh clock after the controller transaction starts; retaining
// its earlier Now value would break the exact receipt association on later reads.
type phaseAuditTransaction struct {
	ControllerTransaction

	acceptedAt time.Time
}

func (t phaseAuditTransaction) Now() time.Time { return t.acceptedAt }

func phaseSelection(q managed.PhaseCommandRequest) (string, error) {
	selection := struct {
		Key        managed.Key
		Kind, Slot string
	}{Key: q.Parent.Key, Kind: q.Kind}
	switch {
	case q.Process != nil:
		selection.Slot = q.Process.Request.Binding.ReservationID.String()
		if q.Kind == "drain" || q.Kind == "activation" {
			selection.Slot = q.Process.Process.RuntimeID
		}
	case q.Route != nil:
		var err error

		selection.Slot, err = controllerFingerprint([4]string{q.Route.Route.Installation, q.Route.Route.Namespace, q.Route.Route.Route, q.Route.Route.Queue})
		if err != nil {
			return "", err
		}
	default:
		return "", managed.ErrInvalid
	}

	return controllerFingerprint(selection)
}

func phaseReceiptDigest(q managed.PhaseCommandRequest) (string, error) {
	raw, err := json.Marshal(struct {
		Kind  string `json:"kind"`
		Input any    `json:"input"`
	}{Kind: "phase_command", Input: q})
	if err != nil {
		return "", err
	}

	return managed.Digest(raw), nil
}

func readPhaseHeld(ctx context.Context, tx ControllerTransaction, ref managed.WorkReference) (managed.PhaseCommandRecord, error) {
	scope, err := originalScope(ref.Key)
	if err != nil {
		return managed.PhaseCommandRecord{}, err
	}

	raw, err := tx.ReadRecord(ctx, "phase_command", scope, ref.ID.String())
	if err != nil {
		return managed.PhaseCommandRecord{}, err
	}

	record, err := managed.DecodePhaseCommandRecord(raw)
	if err != nil || record.Work != ref {
		return managed.PhaseCommandRecord{}, managed.ErrUnavailable
	}

	fingerprint, err := phaseReceiptDigest(record.Request)
	if err != nil {
		return managed.PhaseCommandRecord{}, err
	}

	receipt, err := tx.ReadReceipt(ctx, ref.Key, ref.ID, fingerprint)
	if err != nil {
		return managed.PhaseCommandRecord{}, err
	}

	if managed.OriginalReceipt(receipt) != record.Acceptance {
		return managed.PhaseCommandRecord{}, managed.ErrUnavailable
	}

	selection, err := phaseSelection(record.Request)
	if err != nil {
		return managed.PhaseCommandRecord{}, err
	}

	selected, err := tx.ReadRecord(ctx, "phase_selection", scope, selection)
	if err != nil {
		return managed.PhaseCommandRecord{}, err
	}

	selectedRef, err := managed.DecodeWorkReference(selected)
	if err != nil || selectedRef != ref {
		return managed.PhaseCommandRecord{}, managed.ErrUnavailable
	}

	audit := record.Request.Audit

	auditDigest, err := controllerFingerprint(audit)
	if err != nil {
		return managed.PhaseCommandRecord{}, err
	}

	access, err := controllerRead[managed.AccessAuditReceipt](ctx, tx, "access", "", audit.AttemptID.String())
	if err != nil {
		return managed.PhaseCommandRecord{}, err
	}

	if access != record.AuditReceipt || access.Digest != auditDigest || access.AcceptedAt != record.Acceptance.AcceptedAt {
		return managed.PhaseCommandRecord{}, managed.ErrUnavailable
	}

	auditScope, err := controllerScope(audit.Installation, audit.Authority, audit.Scope)
	if err != nil {
		return managed.PhaseCommandRecord{}, err
	}

	intent, err := controllerRead[managed.AuditIntent](ctx, tx, "audit", auditScope, audit.AttemptID.String())
	if err != nil {
		return managed.PhaseCommandRecord{}, err
	}

	if !reflect.DeepEqual(intent, intentFromAccess(audit, auditDigest, access.AcceptedAt)) {
		return managed.PhaseCommandRecord{}, managed.ErrUnavailable
	}

	return record, nil
}

func validatePhaseWorkHeld(ctx context.Context, tx ControllerTransaction, ref managed.WorkReference) error {
	_, err := readPhaseHeld(ctx, tx, ref)

	return err
}

func phaseProcessAssociation(ctx context.Context, tx ControllerTransaction, target *managed.Target, q managed.PhaseCommandRequest) (*managed.Reservation, error) {
	p := q.Process
	if p == nil {
		return nil, managed.ErrInvalid
	}

	if err := managed.ValidateRoleBinding(*target, p.Request.Binding); err != nil {
		return nil, err
	}

	binding, err := reservation(target, p.Request.Binding.ReservationID)
	if err != nil {
		return nil, err
	}

	material := managed.OriginalLifecycleMaterial{Command: managed.OriginalCommand{ReservationID: binding.ID}, Registration: &managed.OriginalRegistrationFacts{Process: p.Process, Native: managed.OriginalNativeAcceptance{RequestID: binding.RequestID}, Member: binding.Member, Role: p.Request.Binding.Role, StoredCommandDigest: binding.CommandDigest}}
	if err = originalRegistrationMatch(ctx, tx, target, material); err != nil {
		return nil, err
	}

	if q.Kind == "registration" {
		if p.Request.Command.RequestID != binding.RequestID || p.Request.Command.OriginalDigest != binding.CommandDigest || !bytes.Equal(p.Request.Command.Original, binding.Command) {
			return nil, managed.ErrAuthority
		}

		return binding, nil
	}

	reference, err := managed.ParseOriginalReference(binding.Receipt)
	if err != nil || reference.Kind != managed.OriginalRegistration {
		return nil, managed.ErrUnavailable
	}

	original, _, err := readOriginal(ctx, tx, managed.OriginalLifecycleKey{Key: target.Key, Kind: reference.Kind, OperationID: reference.OperationID}, target)
	if err != nil {
		return nil, err
	}

	if original.Material.Registration == nil || !reflect.DeepEqual(original.Material.Registration.Process, p.Process) {
		return nil, managed.ErrAuthority
	}

	return binding, nil
}

func phaseFreshEligibility(ctx context.Context, tx ControllerTransaction, target *managed.Target, q managed.PhaseCommandRequest, operation managed.OperationState, head managed.WorkHead) error {
	if operation.Receipt.Target != target.Key && operation.Receipt.Key != target.Key {
		return managed.ErrAuthority
	}

	if q.Audit.Installation != operation.Submission.Installation || q.Audit.Authority != operation.Submission.Authority || q.Audit.ApplicationID != operation.Submission.ApplicationID || q.Audit.OrganizationID != operation.Submission.OrganizationID {
		return managed.ErrAuthority
	}

	if q.Kind != "drain" && (head.Desired.Operation != operation.Key || target.Phase != managed.Open || target.Desired != managed.Active || !target.Provision.Complete) {
		return managed.ErrBlocked
	}

	if q.Kind == "route" {
		route := q.Route.Route

		binding, err := controllerRead[managed.ReleaseBinding](ctx, tx, "release", target.TenantID, route.ReleaseID.String())
		if err != nil {
			return err
		}

		manifestDigest, err := controllerFingerprint(target.Manifest)
		if err != nil {
			return err
		}

		if binding.Target != target.Key || binding.ActionID != q.ActionID || !reflect.DeepEqual(binding.Manifest, target.Manifest) || route.ManifestDigest != manifestDigest {
			return managed.ErrAuthority
		}

		for _, member := range target.Manifest.Members {
			if route.Installation == member.Installation && route.Namespace == member.Namespace && route.Queue == member.Queue && route.Build == member.Build && route.ArtifactDigest == member.ArtifactDigest && route.ConfigurationDigest == member.ConfigurationDigest {
				return nil
			}
		}

		return managed.ErrAuthority
	}

	binding, err := phaseProcessAssociation(ctx, tx, target, q)
	if err != nil {
		return err
	}

	switch q.Kind {
	case "registration":
		if binding.Phase != managed.Reserved {
			return managed.ErrBlocked
		}
	case "activation":
		if binding.Phase != managed.Accepted || binding.Drain != nil || binding.Abort != nil || q.Process.Request.Binding.Role != managed.Polling {
			return managed.ErrBlocked
		}
	case "drain":
		if head.Desired.Operation != operation.Key || target.Phase != managed.Sealed || binding.Phase != managed.Accepted || binding.Drain != nil || !q.Process.Deadline.After(tx.Now()) {
			return managed.ErrBlocked
		}

		switch operation.Submission.Kind {
		case managed.ActionPause, managed.ActionRetire, managed.ActionContinue:
			if operation.Key.Key != target.Key || (target.Desired != managed.Paused && target.Desired != managed.Retiring) {
				return managed.ErrBlocked
			}
		case managed.ActionDeploy, managed.ActionRollback:
			if operation.Receipt.Key != target.Key || operation.Receipt.Target == target.Key || target.Replacement == nil || target.Replacement.Key != operation.Receipt.Target || target.ReplacementOperationID != q.ActionID {
				return managed.ErrAuthority
			}
		default:
			return managed.ErrAuthority
		}

		if err = originalDrainFreshEligibility(target, binding.ID); err != nil {
			return err
		}
	default:
		return managed.ErrInvalid
	}

	return nil
}

// RetainManagedPhaseCommand commits original bytes, required audit and discoverability together.
// Registration also changes the exact reservation to MayHaveIssued in that same transaction.
// The grant is original-obligation accounting; C must still authenticate every send/recovery.
func (r *Repository) RetainManagedPhaseCommand(ctx context.Context, input managed.PhaseCommandRequest) (managed.PhaseCommandGrant, error) {
	q, err := managed.ClonePhaseCommandRequest(input)
	if err != nil {
		return managed.PhaseCommandGrant{}, err
	}

	digest, err := managed.PhaseCommandDigest(q)
	if err != nil {
		return managed.PhaseCommandGrant{}, err
	}

	command, err := q.Parent.LocalCommand()
	if err != nil {
		return managed.PhaseCommandGrant{}, err
	}

	var out managed.PhaseCommandGrant

	err = r.controller(ctx, "phase:"+command.InstanceID.String(), []managed.Key{command.Key}, func(tx ControllerTransaction) error {
		scope, scopeErr := originalScope(command.Key)
		if scopeErr != nil {
			return scopeErr
		}

		raw, savedErr := tx.ReadRecord(ctx, "phase_command", scope, command.ID.String())
		if savedErr == nil {
			saved, decodeErr := managed.DecodePhaseCommandRecord(raw)
			if decodeErr != nil {
				return managed.ErrUnavailable
			}

			if saved.RequestDigest != digest || !reflect.DeepEqual(saved.Request, q) {
				return managed.ErrConflict
			}

			if decodeErr = validateWorkHeld(ctx, tx, saved.Work); decodeErr != nil {
				return decodeErr
			}

			out.Record = saved

			return nil
		}

		if !errors.Is(savedErr, managed.ErrNotFound) {
			return savedErr
		}

		selection, selectErr := phaseSelection(q)
		if selectErr != nil {
			return selectErr
		}

		if _, selectErr = tx.ReadRecord(ctx, "phase_selection", scope, selection); !controllerAbsent(selectErr) {
			if selectErr == nil {
				return managed.ErrConflict
			}

			return selectErr
		}

		head, headErr := readWorkHeadHeld(ctx, tx, command.Key)
		if headErr != nil {
			return headErr
		}

		if headErr = validateWorkHeld(ctx, tx, head.Provisioning); headErr != nil {
			return headErr
		}

		if headErr = validateWorkHeld(ctx, tx, head.Desired); headErr != nil {
			return headErr
		}

		actionRaw, operationErr := tx.ReadRecord(ctx, "work_lookup", scope, "action:"+q.ActionID.String())
		if operationErr != nil {
			return operationErr
		}

		actionRef, operationErr := managed.DecodeWorkReference(actionRaw)
		if operationErr != nil || actionRef.Key != command.Key || actionRef.Kind != "action" || actionRef.ID != q.ActionID {
			return managed.ErrUnavailable
		}

		if operationErr = validateWorkHeld(ctx, tx, actionRef); operationErr != nil {
			return operationErr
		}

		operationKey := actionRef.Operation

		operation, operationErr := acceptedOperation(ctx, tx, operationKey)
		if operationErr != nil {
			return operationErr
		}

		if operationErr = discoveryActionReplay(ctx, tx, operation.Receipt); operationErr != nil {
			return operationErr
		}

		target, parentErr := controllerParent(ctx, tx, command)
		if parentErr != nil {
			return parentErr
		}

		if parentErr = phaseFreshEligibility(ctx, tx, target, q, operation, head); parentErr != nil {
			return parentErr
		}

		if _, auditErr := tx.ReadRecord(ctx, "access", "", q.Audit.AttemptID.String()); !controllerAbsent(auditErr) {
			if auditErr == nil {
				return managed.ErrConflict
			}

			return auditErr
		}

		fresh := false

		receipt, applyErr := New(tx).apply(ctx, command, "phase_command", q, true, func(parent *managed.Target, _ time.Time) error {
			fresh = true

			if q.Kind == "registration" {
				binding, bindingErr := reservation(parent, q.Process.Request.Binding.ReservationID)
				if bindingErr != nil {
					return bindingErr
				}

				if parent.Phase != managed.Open || binding.Phase != managed.Reserved {
					return managed.ErrBlocked
				}

				binding.Phase = managed.MayHaveIssued
			}

			return nil
		})
		if applyErr != nil {
			return applyErr
		}

		if !fresh {
			return managed.ErrUnavailable
		}

		auditReceipt, auditErr := New(phaseAuditTransaction{ControllerTransaction: tx, acceptedAt: receipt.AcceptedAt}).RecordManagedAccessAudit(ctx, q.Audit)
		if auditErr != nil {
			return auditErr
		}

		ref := managed.WorkReference{Schema: managed.DiscoverySchema, Key: command.Key, Operation: operationKey, Kind: q.Kind, ID: command.ID, Digest: digest}

		ref, appendErr := appendWorkHeld(ctx, tx, &head, ref, false, false)
		if appendErr != nil {
			return appendErr
		}

		record := managed.PhaseCommandRecord{Schema: managed.DiscoverySchema, Request: q, RequestDigest: digest, Acceptance: managed.OriginalReceipt(receipt), AuditReceipt: auditReceipt, Work: ref}

		body, encodeErr := managed.EncodePhaseCommandRecord(record)
		if encodeErr != nil {
			return encodeErr
		}

		if encodeErr = tx.WriteRecord(ctx, "phase_command", scope, command.ID.String(), body, true); encodeErr != nil {
			return encodeErr
		}

		selected, encodeErr := managed.EncodeWorkReference(ref)
		if encodeErr != nil {
			return encodeErr
		}

		if encodeErr = tx.WriteRecord(ctx, "phase_selection", scope, selection, selected, true); encodeErr != nil {
			return encodeErr
		}

		out = managed.PhaseCommandGrant{Record: record, NewlyRetained: true}

		return nil
	})
	if err != nil {
		return managed.PhaseCommandGrant{}, err
	}

	return out, nil
}

// ReadManagedPhaseCommand validates the immutable command, original parent receipt and audit.
// No result, progress label or missing response can rewrite it or remove its work reference.
func (r *Repository) ReadManagedPhaseCommand(ctx context.Context, ref managed.WorkReference) (managed.PhaseCommandRecord, error) {
	if err := ref.Validate(); err != nil {
		return managed.PhaseCommandRecord{}, err
	}

	if ref.Kind == "action" {
		return managed.PhaseCommandRecord{}, managed.ErrInvalid
	}

	var out managed.PhaseCommandRecord

	err := r.controller(ctx, "phase:"+ref.Key.InstanceID.String(), []managed.Key{ref.Key}, func(tx ControllerTransaction) error {
		if err := validateWorkHeld(ctx, tx, ref); err != nil {
			return err
		}

		var err error

		out, err = readPhaseHeld(ctx, tx, ref)

		return err
	})
	if err != nil {
		return managed.PhaseCommandRecord{}, err
	}

	return out, nil
}
