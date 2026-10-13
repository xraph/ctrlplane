package managedstate

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"time"

	"github.com/xraph/ctrlplane/managed"
)

func recoveryCoverageRecords(plan recoveryCoveragePlan, acceptance managed.RecoveryAcceptance) ([]managed.RecoveryRecord[managed.EffectiveRemovalBinding], managed.RecoveryRecord[managed.EffectiveRemovalCoverage], error) {
	var zero managed.RecoveryRecord[managed.EffectiveRemovalCoverage]
	if len(plan.bindings) == 0 || len(plan.bindings) > managed.MaxRecoveryBindings || plan.facts.BindingCount != managed.Decimal(strconv.Itoa(len(plan.bindings))) {
		return nil, zero, managed.ErrCapacity
	}

	disposition, err := recoveryCoverageDisposition(plan.facts.Steps)
	if err != nil || disposition != plan.facts.Disposition {
		return nil, zero, managed.ErrBlocked
	}

	bindings := make([]managed.RecoveryRecord[managed.EffectiveRemovalBinding], 0, len(plan.bindings))
	references := make([]managed.RecoveryAssociation, 0, len(plan.bindings))

	seen := make(map[string]bool, len(plan.bindings))
	for _, facts := range plan.bindings {
		if facts.Binding.Key.AttemptKey != plan.facts.Key || facts.Proof.Kind != managed.RecoveryProofRecord {
			return nil, zero, managed.ErrAuthority
		}

		binding, recordErr := activationRecord(managed.RecoveryCoverageBindingRecord, facts, acceptance)
		if recordErr != nil {
			return nil, zero, recordErr
		}

		if _, recordErr = managed.EncodeRecoveryRecord(binding); recordErr != nil {
			return nil, zero, recordErr
		}

		key, recordErr := recoveryFactKey(plan.facts.Key, binding.Kind, acceptance.OperationID, facts)
		if recordErr != nil {
			return nil, zero, recordErr
		}

		if seen[key] {
			return nil, zero, managed.ErrUnavailable
		}

		seen[key] = true

		reference, recordErr := managed.RecoveryAssociationFor(key, binding)
		if recordErr != nil {
			return nil, zero, recordErr
		}

		bindings, references = append(bindings, binding), append(references, reference)
	}

	facts := plan.facts

	facts.BindingSetDigest, err = attemptHash("ctrlplane.managed.effective-binding-set.v1", references)
	if err != nil {
		return nil, zero, err
	}

	coverage, err := activationRecord(managed.RecoveryCoverageRecord, facts, acceptance)
	if err != nil {
		return nil, zero, err
	}

	if _, err = managed.EncodeRecoveryRecord(coverage); err != nil {
		return nil, zero, err
	}

	return bindings, coverage, nil
}

// stageRecoveryCoverageCloseIntent prepares the first close intent and complete
// coverage as one batch. The surrounding typed transaction still owns commit.
// The request leaves Coverage nil; the server derives it from retained authority.
// Request hashing precedes that association, avoiding a receipt self-reference.
func stageRecoveryCoverageCloseIntent(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, request managed.RecoveryNativeIntentRequest, now time.Time) (*recoveryActivationRows, managed.RecoveryRecord[managed.EffectiveRemovalCoverage], error) {
	var zero managed.RecoveryRecord[managed.EffectiveRemovalCoverage]

	command, err := request.Command.LocalCommand()
	if err != nil {
		return nil, zero, err
	}

	if !view.validated || view.recoveryActivation == nil || view.ref != request.Command.AttemptRef || target.Key != command.Key || view.state.TerminalDigest != "" || view.state.SettlementDigest != "" || view.state.Successor != nil || target.Phase == managed.Deleted {
		return nil, zero, managed.ErrBlocked
	}

	if target.Revision != command.Revision || target.LeaseEpoch != command.LeaseEpoch || target.LeaseOwner != command.Owner || !now.Before(target.LeaseUntil) {
		return nil, zero, managed.ErrConflict
	}

	if request.Intent.Operation != "close" || request.Intent.Coverage != nil || request.Intent.Permission != nil || request.Intent.Cutoff == nil || request.Intent.Proof == nil {
		return nil, zero, managed.ErrInvalid
	}

	if _, readErr := tx.ReadRecord(ctx, recoveryCoverageSelectionKind, view.scope, view.ref.AttemptID.String()); !errors.Is(readErr, managed.ErrNotFound) {
		if readErr != nil {
			return nil, zero, readErr
		}

		return nil, zero, managed.ErrConflict
	}

	if err = removalLocalIDAvailable(ctx, tx, command.ID); err != nil {
		return nil, zero, err
	}

	if _, readErr := tx.ReadReceipt(ctx, target.Key, command.ID, ""); !errors.Is(readErr, managed.ErrNotFound) {
		if readErr != nil {
			return nil, zero, readErr
		}

		return nil, zero, managed.ErrConflict
	}

	plan, err := prepareRecoveryCoverageHeld(ctx, tx, target, view, now)
	if err != nil {
		return nil, zero, err
	}

	if *request.Intent.Cutoff != plan.facts.Cutoff {
		return nil, zero, managed.ErrAuthority
	}

	member := false

	for _, binding := range plan.bindings {
		if binding.Binding == request.Intent.Binding && binding.Proof == *request.Intent.Proof {
			member = true
		}
	}

	if !member {
		return nil, zero, managed.ErrAuthority
	}

	fingerprint, err := recoveryCoverageAcceptanceDigest(request, plan)
	if err != nil {
		return nil, zero, err
	}

	revision, err := view.state.Revision.Uint64()
	if err != nil || revision >= 1<<63-1 || target.Revision >= 1<<63-1 {
		return nil, zero, managed.ErrCapacity
	}

	receipt := managed.Receipt{ID: command.ID, Digest: fingerprint, Revision: target.Revision + 1, LeaseEpoch: target.LeaseEpoch, AcceptedAt: now.UTC(), LeaseUntil: target.LeaseUntil}
	acceptance := managed.RecoveryAcceptance{OperationID: command.ID, RequestDigest: fingerprint, Receipt: managed.ReceiptWire(receipt)}

	staged, coverage, err := stageRecoveryCoveragePlan(ctx, tx, view, plan, request, acceptance)
	if err != nil {
		return nil, zero, err
	}

	state := recoveryAttemptState{AttemptState: view.state, Activation: *view.recoveryActivation}

	state.Revision = managed.Decimal(strconv.FormatUint(revision+1, 10))
	if err = attemptWrite(ctx, staged, "removal_attempt_state", view.scope, view.ref.AttemptID.String(), state, false, managed.MaxAttemptFact); err != nil {
		return nil, zero, err
	}

	proposed := *target
	proposed.Revision = receipt.Revision

	checked, active, err := readRecoveryAttemptInspection(ctx, staged, &proposed, attemptKey(view))
	if err != nil {
		return nil, zero, err
	}

	if checked.ref != active {
		return nil, zero, managed.ErrConflict
	}

	if _, err = recoveryCoverageReadHeld(ctx, staged, &proposed, checked, nil); err != nil {
		return nil, zero, err
	}

	reserve, err := invocationRemainingReserve(ctx, staged, &proposed, checked)
	if err != nil {
		return nil, zero, err
	}

	if err = recoveryCoverageCloseIntentHeadroom(target.Revision, revision, reserve); err != nil {
		return nil, zero, err
	}

	return staged, coverage, nil
}

func stageRecoveryCoveragePlan(ctx context.Context, tx ControllerTransaction, view attemptView, plan recoveryCoveragePlan, request managed.RecoveryNativeIntentRequest, acceptance managed.RecoveryAcceptance) (*recoveryActivationRows, managed.RecoveryRecord[managed.EffectiveRemovalCoverage], error) {
	var zero managed.RecoveryRecord[managed.EffectiveRemovalCoverage]

	fingerprint, err := recoveryCoverageAcceptanceDigest(request, plan)
	if err != nil {
		return nil, zero, err
	}

	if fingerprint != acceptance.RequestDigest || request.Command.ID != acceptance.OperationID {
		return nil, zero, managed.ErrAuthority
	}

	intentFacts := request.Intent

	bindings, coverage, err := recoveryCoverageRecords(plan, acceptance)
	if err != nil {
		return nil, zero, err
	}

	receipt, err := acceptance.Receipt.Value()
	if err != nil {
		return nil, zero, err
	}

	key, err := recoveryFactKey(plan.facts.Key, managed.RecoveryCoverageRecord, acceptance.OperationID, coverage.Facts)
	if err != nil {
		return nil, zero, err
	}

	reference, err := managed.RecoveryAssociationFor(key, coverage)
	if err != nil {
		return nil, zero, err
	}

	if intentFacts.Coverage != nil || intentFacts.Cutoff == nil || *intentFacts.Cutoff != plan.facts.Cutoff {
		return nil, zero, managed.ErrAuthority
	}

	intentFacts.Coverage = &reference

	intent, err := activationRecord(managed.RecoveryIntentRecord, intentFacts, acceptance)
	if err != nil {
		return nil, zero, err
	}

	intentKey, err := recoveryFactKey(plan.facts.Key, managed.RecoveryIntentRecord, acceptance.OperationID, intentFacts)
	if err != nil {
		return nil, zero, err
	}

	intentRef, err := managed.RecoveryAssociationFor(intentKey, intent)
	if err != nil {
		return nil, zero, err
	}

	if err = recoveryIntentMatches(intent, intentRef, intentFacts.Binding, "close", intentFacts.RequestID, intentFacts.RequestDigest); err != nil {
		return nil, zero, err
	}

	staged := &recoveryActivationRows{ControllerTransaction: tx, key: plan.facts.Key.Key, receipt: receipt}
	for _, binding := range bindings {
		if _, err = recoveryWriteFact(ctx, staged, plan.facts.Key, binding); err != nil {
			return nil, zero, err
		}
	}

	if _, err = recoveryWriteFact(ctx, staged, plan.facts.Key, coverage); err != nil {
		return nil, zero, err
	}

	if _, err = recoveryWriteFact(ctx, staged, plan.facts.Key, intent); err != nil {
		return nil, zero, err
	}

	if err = stageRecoveryProtocolEvent(ctx, staged, view, "recovery_close_intent", intentRef, acceptance); err != nil {
		return nil, zero, err
	}

	selection := recoveryCoverageSelection{SchemaVersion: "1", AttemptRef: view.ref, Coverage: reference, Sponsor: intentRef, Command: request.Command}
	if err = attemptWrite(ctx, staged, recoveryCoverageSelectionKind, view.scope, view.ref.AttemptID.String(), selection, true, managed.MaxRecoveryReceipt); err != nil {
		return nil, zero, err
	}

	return staged, coverage, nil
}

// recoveryCoverageSponsorHeld requires the original lifecycle event and independent
// receipt in addition to both normalized retained facts. Self-hashed coverage alone fails.
func recoveryCoverageSponsorHeld(ctx context.Context, tx ControllerTransaction, view attemptView, selection recoveryCoverageSelection, coverage managed.RecoveryRecord[managed.EffectiveRemovalCoverage]) (managed.RecoveryRecord[managed.RecoveryNativeIntent], error) {
	var zero managed.RecoveryRecord[managed.RecoveryNativeIntent]
	if selection.SchemaVersion != "1" || selection.AttemptRef != view.ref || selection.Coverage.Kind != managed.RecoveryCoverageRecord || selection.Sponsor.Kind != managed.RecoveryIntentRecord {
		return zero, managed.ErrUnavailable
	}

	intent, err := recoveryReadFact[managed.RecoveryNativeIntent](ctx, tx, attemptKey(view), selection.Sponsor)
	if err != nil {
		return zero, err
	}

	if intent.Acceptance != coverage.Acceptance || intent.Facts.Binding.Key.AttemptKey != attemptKey(view) || intent.Facts.Coverage == nil || *intent.Facts.Coverage != selection.Coverage || intent.Facts.Cutoff == nil || *intent.Facts.Cutoff != coverage.Facts.Cutoff || intent.Facts.Permission != nil || intent.Facts.Proof == nil {
		return zero, managed.ErrUnavailable
	}

	if err = recoveryIntentMatches(intent, selection.Sponsor, intent.Facts.Binding, "close", intent.Facts.RequestID, intent.Facts.RequestDigest); err != nil {
		return zero, err
	}

	if err = recoveryProtocolEventHeld(ctx, tx, view, "recovery_close_intent", selection.Sponsor, intent.Acceptance); err != nil {
		return zero, err
	}

	return intent, nil
}

func recoveryCoverageReadHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, exact *managed.RecoveryAssociation) (managed.RecoveryRecord[managed.EffectiveRemovalCoverage], error) {
	var zero managed.RecoveryRecord[managed.EffectiveRemovalCoverage]
	if !view.validated || view.recoveryActivation == nil {
		return zero, managed.ErrAuthority
	}

	selection, err := attemptRead[recoveryCoverageSelection](ctx, tx, recoveryCoverageSelectionKind, view.scope, view.ref.AttemptID.String(), managed.MaxRecoveryReceipt)
	if err != nil {
		return zero, err
	}

	if exact != nil && *exact != selection.Coverage {
		return zero, managed.ErrConflict
	}

	coverage, err := recoveryReadFact[managed.EffectiveRemovalCoverage](ctx, tx, attemptKey(view), selection.Coverage)
	if err != nil {
		return zero, err
	}

	intent, err := recoveryCoverageSponsorHeld(ctx, tx, view, selection, coverage)
	if err != nil {
		return zero, err
	}

	cutoff, err := recoveryCutoffHeld(ctx, tx, view)
	if err != nil {
		return zero, err
	}

	cutoffKey, err := recoveryFactKey(attemptKey(view), managed.RecoveryCutoffRecord, cutoff.Acceptance.OperationID, cutoff.Facts)
	if err != nil {
		return zero, err
	}

	cutoffRef, err := managed.RecoveryAssociationFor(cutoffKey, cutoff)
	if err != nil {
		return zero, err
	}

	facts := coverage.Facts
	if facts.Key != attemptKey(view) || facts.AttemptRef != view.ref || facts.ProviderOperationID != view.descriptor.ProviderOperationID || facts.ResourcesDigest != view.descriptor.ResourcesDigest || facts.AdmissionDigest != view.state.AdmissionDigest || !digest(facts.ResourcesDigest) || !digest(facts.AdmissionDigest) || facts.Cutoff != cutoffRef || coverage.Acceptance.Receipt.AcceptedAt.Before(cutoff.Facts.CutoffAt) {
		return zero, managed.ErrUnavailable
	}

	steps, err := recoveryCoverageStepsHeld(ctx, tx, target, view, coverage.Acceptance.Receipt.AcceptedAt)
	if err != nil {
		return zero, err
	}

	if !reflect.DeepEqual(steps, facts.Steps) {
		return zero, managed.ErrUnavailable
	}

	disposition, err := recoveryCoverageDisposition(steps)
	if err != nil || disposition != facts.Disposition {
		return zero, managed.ErrUnavailable
	}

	references := make([]managed.RecoveryAssociation, 0, len(view.descriptor.ReservationIDs))
	sponsorMember := false

	bindings := make([]managed.EffectiveRemovalBinding, 0, len(view.descriptor.ReservationIDs))
	for _, reservationID := range view.descriptor.ReservationIDs {
		reservation, readErr := reservation(target, reservationID)
		if readErr != nil {
			return zero, readErr
		}

		if reservation.Phase == managed.Revoked {
			continue
		}

		bindingKey := attemptBinding(view, reservationID)

		key, readErr := recoveryFactKey(attemptKey(view), managed.RecoveryCoverageBindingRecord, coverage.Acceptance.OperationID, managed.EffectiveRemovalBinding{Binding: managed.RemovalRecoveryBinding{Key: bindingKey}})
		if readErr != nil {
			return zero, readErr
		}

		binding, readErr := recoveryReadNamedFact[managed.EffectiveRemovalBinding](ctx, tx, attemptKey(view), managed.RecoveryCoverageBindingRecord, key)
		if readErr != nil {
			return zero, readErr
		}

		if binding.Acceptance != coverage.Acceptance || binding.Facts.Binding.Key != bindingKey {
			return zero, managed.ErrUnavailable
		}

		if readErr = recoveryCoverageBindingHeld(ctx, tx, target, view, binding.Facts, coverage.Acceptance.Receipt.AcceptedAt); readErr != nil {
			return zero, readErr
		}

		if binding.Facts.Binding == intent.Facts.Binding && binding.Facts.Proof == *intent.Facts.Proof {
			sponsorMember = true
		}

		reference, readErr := managed.RecoveryAssociationFor(key, binding)
		if readErr != nil {
			return zero, readErr
		}

		references = append(references, reference)
		bindings = append(bindings, binding.Facts)
	}

	if !sponsorMember || len(references) == 0 || len(references) > managed.MaxRecoveryBindings || facts.BindingCount != managed.Decimal(strconv.Itoa(len(references))) {
		return zero, managed.ErrUnavailable
	}

	setDigest, err := attemptHash("ctrlplane.managed.effective-binding-set.v1", references)
	if err != nil || facts.BindingSetDigest != setDigest {
		return zero, managed.ErrUnavailable
	}

	if err = recoveryCoverageAcceptanceMatches(selection, intent, coverage, bindings); err != nil {
		return zero, err
	}

	return coverage, nil
}

// ReadManagedEffectiveRemovalCoverage returns the fixed complete retained coverage.
// Unknown effects remain unresolved and never create a record through this read.
func (r *Repository) ReadManagedEffectiveRemovalCoverage(ctx context.Context, key managed.AttemptKey) (managed.RecoveryRecord[managed.EffectiveRemovalCoverage], error) {
	return r.readManagedEffectiveCoverage(ctx, key, nil)
}

// ReadManagedEffectiveRemovalCoverageExact requires the named lifecycle acceptance.
func (r *Repository) ReadManagedEffectiveRemovalCoverageExact(ctx context.Context, request managed.RecoveryExactRequest) (managed.RecoveryRecord[managed.EffectiveRemovalCoverage], error) {
	if request.Association.Kind != managed.RecoveryCoverageRecord {
		return managed.RecoveryRecord[managed.EffectiveRemovalCoverage]{}, managed.ErrInvalid
	}

	return r.readManagedEffectiveCoverage(ctx, request.Key, &request.Association)
}

func (r *Repository) readManagedEffectiveCoverage(ctx context.Context, key managed.AttemptKey, exact *managed.RecoveryAssociation) (managed.RecoveryRecord[managed.EffectiveRemovalCoverage], error) {
	var out managed.RecoveryRecord[managed.EffectiveRemovalCoverage]
	if r == nil || r.backend == nil {
		return out, managed.ErrUnsupported
	}

	if err := attemptVersion(key); err != nil {
		return out, err
	}

	err := r.controller(ctx, "attempt:"+key.InstanceID.String(), []managed.Key{key.Key}, func(tx ControllerTransaction) error {
		target, readErr := tx.Read(ctx, key.Key)
		if readErr != nil {
			return readErr
		}

		view, _, readErr := readRecoveryAttemptInspection(ctx, tx, target, key)
		if readErr != nil {
			return readErr
		}

		out, readErr = recoveryCoverageReadHeld(ctx, tx, target, view, exact)

		return readErr
	})
	if err != nil {
		return managed.RecoveryRecord[managed.EffectiveRemovalCoverage]{}, err
	}

	return out, nil
}

func recoveryCoverageCloseIntentHeadroom(parent, attempt uint64, remaining recoveryReserveAfter) error {
	if remaining.closeIntentsMissing == 0 {
		return managed.ErrUnavailable
	}
	// Only this first retained intent discharges its one reserved close-intent slot.
	remaining.closeIntentsMissing--

	return recoveryCheckHeadroom(parent, attempt, 1, remaining)
}

// recoveryCoverageAcceptanceDigest binds the original request and derived authority
// to the independent parent receipt. It excludes new acceptance associations.
func recoveryCoverageAcceptanceDigest(request managed.RecoveryNativeIntentRequest, plan recoveryCoveragePlan) (string, error) {
	if _, err := request.Command.LocalCommand(); err != nil {
		return "", err
	}

	if request.Intent.Coverage != nil || request.Command.Key != plan.facts.Key.Key || request.Command.AttemptRef != plan.facts.AttemptRef {
		return "", managed.ErrAuthority
	}

	requestDigest, err := attemptHash("ctrlplane.managed.recovery-close-intent.v1", request)
	if err != nil {
		return "", err
	}

	facts := plan.facts
	facts.BindingSetDigest = ""

	planDigest, err := attemptHash("ctrlplane.managed.effective-coverage-plan.v1", struct {
		Facts    managed.EffectiveRemovalCoverage  `json:"facts"`
		Bindings []managed.EffectiveRemovalBinding `json:"bindings"`
	}{facts, plan.bindings})
	if err != nil {
		return "", err
	}

	return attemptHash("ctrlplane.managed.recovery-close-acceptance.v1", struct {
		RequestDigest string `json:"request_digest"`
		PlanDigest    string `json:"plan_digest"`
	}{requestDigest, planDigest})
}

func recoveryCoverageAcceptanceMatches(selection recoveryCoverageSelection, intent managed.RecoveryRecord[managed.RecoveryNativeIntent], coverage managed.RecoveryRecord[managed.EffectiveRemovalCoverage], bindings []managed.EffectiveRemovalBinding) error {
	request := managed.RecoveryNativeIntentRequest{Command: selection.Command, Intent: intent.Facts}
	request.Intent.Coverage = nil

	fingerprint, err := recoveryCoverageAcceptanceDigest(request, recoveryCoveragePlan{facts: coverage.Facts, bindings: bindings})
	if err != nil {
		return err
	}

	if selection.Command.ID != coverage.Acceptance.OperationID || intent.Acceptance != coverage.Acceptance || fingerprint != coverage.Acceptance.RequestDigest {
		return managed.ErrUnavailable
	}

	return nil
}
