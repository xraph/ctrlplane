package managedstate

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"time"

	"github.com/xraph/ctrlplane/managed"
)

// recoveryAttemptState keeps the original index and binds its recovery activation.
// Schema 2 makes existing exact readers refuse before interpreting original slots.
type recoveryAttemptState struct {
	managed.AttemptState

	Activation managed.RecoveryAssociation `json:"recovery_activation"`
}

// recoveryActivationPlan is transaction-local preparation. It is never persisted as a unit.
// A later callback must derive it again before materializing all original selections.
type recoveryActivationPlan struct {
	Profile    managed.RecoveryRecord[managed.RemovalRecoveryActivation] `json:"profile"`
	State      recoveryAttemptState                                      `json:"state"`
	Selections []recoveryOriginalSelection                               `json:"selections"`
}

type recoveryOriginalSelection struct {
	Step        managed.ProviderStepKey `json:"step"`
	Disposition string                  `json:"disposition"`
	Association *attemptProviderSlot    `json:"association,omitempty"`
}

// recoveryBindingHeld derives identity from retained originals, never producer fields.
func recoveryBindingHeld(ctx context.Context, tx ControllerTransaction, t *managed.Target, v attemptView, key managed.AttemptBindingKey) (managed.RemovalRecoveryBinding, error) {
	var out managed.RemovalRecoveryBinding
	if !v.validated || key.AttemptKey != attemptKey(v) || v.state.AdmissionDigest == "" {
		return out, managed.ErrAuthority
	}

	binding, err := attemptBindingRead(ctx, tx, t, v, key)
	if err != nil {
		return out, err
	}

	if binding.Fence == nil || binding.Abort != nil || binding.Finish != nil {
		return out, managed.ErrBlocked
	}

	var begin *managed.AttemptCommandRecord

	for i := range binding.Commands {
		if binding.Commands[i].Key.Kind == managed.AttemptBegin {
			begin = &binding.Commands[i]
		}
	}

	if begin == nil || begin.BindingReference.ReservationID != key.ReservationID {
		return out, managed.ErrUnavailable
	}

	retained, err := reservation(t, key.ReservationID)
	if err != nil {
		return out, err
	}

	if retained.Phase != managed.Accepted || retained.Drain == nil {
		return out, managed.ErrUnavailable
	}

	drain, err := managed.ParseOriginalReference(retained.Drain.Evidence)
	if err != nil {
		return out, err
	}

	if _, _, err = readOriginal(ctx, tx, managed.OriginalLifecycleKey{Key: t.Key, Kind: managed.OriginalDrain, OperationID: drain.OperationID}, t); err != nil {
		return out, err
	}

	out = managed.RemovalRecoveryBinding{
		Key: key, DescriptorDigest: v.ref.DescriptorDigest, ProviderOperationID: v.descriptor.ProviderOperationID,
		OriginalRegistrationDigest: begin.BindingReference.OriginalRegistrationDigest,
		OriginalDrainDigest:        drain.MaterialDigest, OriginalCommandDigest: binding.Fence.CommandDigest,
		OriginalBeginRequestID:     binding.Fence.NativeAcceptance.RequestID,
		OriginalBeginRequestDigest: binding.Fence.NativeAcceptance.RequestDigest,
		NativeFenceDigest:          binding.Fence.NativeFenceDigest, OriginalProcessDigest: begin.BindingReference.ProcessDigest,
		AdmissionDigest: v.state.AdmissionDigest,
	}

	return out, nil
}

// recoveryOriginalSelectionsHeld fixes every original slot, including never-issued slots.
// A saved permission stays possibly effective even if it has no result or marker.
func recoveryOriginalSelectionsHeld(ctx context.Context, tx ControllerTransaction, t *managed.Target, v attemptView) ([]recoveryOriginalSelection, error) {
	if !v.validated {
		return nil, managed.ErrUnavailable
	}

	steps, err := attemptSteps(t, v)
	if err != nil {
		return nil, err
	}

	if err = recoveryActivationSlots(t, v, len(steps)); err != nil {
		return nil, err
	}

	selections := make([]recoveryOriginalSelection, 0, len(steps))
	for i, step := range steps {
		_, association, readErr := attemptProviderRead(ctx, tx, t, v, step, i)
		if readErr != nil {
			return nil, readErr
		}

		selected := recoveryOriginalSelection{Step: step, Disposition: "unissued"}
		if v.state.ProviderSlots[i]&attemptPermissionBit != 0 {
			selected.Disposition = "original"
			selected.Association = &association
		}

		selections = append(selections, selected)
	}

	return selections, nil
}

// recoveryActivationSlots separates fixed storage capacity from active obligations.
func recoveryActivationSlots(target *managed.Target, view attemptView, stepCount int) error {
	bindingCount := len(view.descriptor.ReservationIDs)
	if !view.validated || target == nil || bindingCount == 0 || bindingCount > managed.MaxRecoveryBindings || bindingCount != len(target.Reservations) || stepCount == 0 || stepCount > managed.MaxRecoverySteps || len(view.state.BindingSlots) != managed.MaxRecoveryBindings || len(view.state.ProviderSlots) != managed.MaxRecoverySteps {
		return managed.ErrUnavailable
	}

	for _, mask := range view.state.BindingSlots[bindingCount:] {
		if mask != 0 {
			return managed.ErrUnavailable
		}
	}

	for _, mask := range view.state.ProviderSlots[stepCount:] {
		if mask != 0 {
			return managed.ErrUnavailable
		}
	}

	return nil
}

// recoveryActivationReserve derives the existing finite obligations without the legacy
// terminal shortcut. Keep this formula synchronized with attemptReserve until extraction.
func recoveryActivationReserve(t *managed.Target, v attemptView) (recoveryReserveAfter, error) {
	var after recoveryReserveAfter
	if !v.validated {
		return after, managed.ErrUnavailable
	}

	steps, err := attemptSteps(t, v)
	if err != nil {
		return after, err
	}

	if err = recoveryActivationSlots(t, v, len(steps)); err != nil {
		return after, err
	}

	after.existing = 8

	for _, hash := range []string{v.state.AdmissionDigest, v.state.RevocationDigest, v.state.SettlementDigest, v.state.TerminalDigest} {
		if hash != "" {
			after.existing--
		}
	}

	positive := false

	if v.state.AdmissionDigest != "" {
		for _, mask := range v.state.ProviderSlots[:len(steps)] {
			positive = positive || mask&attemptPositiveBit != 0
		}
	}

	for i, reservationID := range v.descriptor.ReservationIDs {
		binding, readErr := reservation(t, reservationID)
		if readErr != nil {
			return after, readErr
		}

		if binding.Phase == managed.Revoked {
			continue
		}

		mask := v.state.BindingSlots[i]
		if positive && mask&(attemptBeginBit|attemptFenceBit) != attemptBeginBit|attemptFenceBit {
			return after, managed.ErrUnavailable
		}

		for _, bit := range []byte{attemptBeginBit, attemptAbortCommandBit, attemptFinishCommandBit, attemptFenceBit, attemptWitnessBit, attemptAbortBit, attemptFinishBit} {
			if positive && (bit == attemptAbortCommandBit || bit == attemptWitnessBit || bit == attemptAbortBit) {
				continue
			}

			if mask&bit == 0 {
				after.existing++
			}
		}

		after.closeIntentsMissing++
		after.closeResultsMissing++
	}

	for _, mask := range v.state.ProviderSlots[:len(steps)] {
		if mask&attemptPermissionBit == 0 {
			after.existing++
		}

		if mask&attemptPositiveBit == 0 {
			after.markerObligations++
			after.closureObligations++
		}
	}

	return after, nil
}

// prepareRecoveryActivationHeld derives a bounded plan at the actual callback clock.
// It performs no writes, increments no counters and grants no provider authority.
// Materializing roots/current/terminals and the parent receipt remains a later slice.
func (r *Repository) prepareRecoveryActivationHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, q managed.RemovalRecoveryActivationRequest, now time.Time) (recoveryActivationPlan, error) {
	var zero recoveryActivationPlan
	if r == nil || r.recoveryHost == nil || tx == nil || target == nil {
		return zero, managed.ErrUnsupported
	}

	command, err := q.Command.LocalCommand()
	if err != nil {
		return zero, err
	}

	if now.IsZero() || target.Key != command.Key || target.Revision != command.Revision || target.LeaseEpoch != command.LeaseEpoch || target.LeaseOwner != command.Owner || !now.Before(target.LeaseUntil) {
		return zero, managed.ErrConflict
	}

	key := managed.AttemptKey{Key: command.Key, AttemptID: q.Command.AttemptRef.AttemptID, Generation: q.Command.AttemptRef.Generation}

	view, selected, err := readAttemptWithCurrent(ctx, tx, target, key)
	if err != nil {
		return zero, err
	}

	if view.ref != q.Command.AttemptRef || selected != view.ref || view.state.SchemaVersion != "1" || view.state.AdmissionDigest == "" || view.state.Successor != nil || view.state.RevocationDigest != "" || view.state.SettlementDigest != "" || view.state.TerminalDigest != "" || target.Phase == managed.Deleted {
		return zero, managed.ErrBlocked
	}

	if err = removalLocalIDAvailable(ctx, tx, command.ID); err != nil {
		return zero, err
	}

	if len(view.descriptor.ReservationIDs) == 0 || len(view.descriptor.ReservationIDs) > managed.MaxRecoveryBindings {
		return zero, managed.ErrUnavailable
	}

	bindingCount := 0

	for _, reservationID := range view.descriptor.ReservationIDs {
		retained, readErr := reservation(target, reservationID)
		if readErr != nil {
			return zero, readErr
		}

		if retained.Phase == managed.Revoked {
			continue
		}

		bindingCount++

		if _, err = recoveryBindingHeld(ctx, tx, target, view, attemptBinding(view, reservationID)); err != nil {
			return zero, err
		}
	}

	return prepareRecoveryActivationPlanHeld(ctx, tx, target, view, q, now, bindingCount)
}

// prepareRecoveryActivationPlanHeld assembles the plan after the callback has checked
// selected authority and every active original binding. It retains the checked view only
// for this call and never writes the proposed profile or state.
func prepareRecoveryActivationPlanHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, q managed.RemovalRecoveryActivationRequest, now time.Time, bindingCount int) (recoveryActivationPlan, error) {
	var zero recoveryActivationPlan
	if !view.validated || bindingCount < 0 || bindingCount > len(view.descriptor.ReservationIDs) {
		return zero, managed.ErrUnavailable
	}

	command, err := q.Command.LocalCommand()
	if err != nil {
		return zero, err
	}

	key := attemptKey(view)

	selections, err := recoveryOriginalSelectionsHeld(ctx, tx, target, view)
	if err != nil {
		return zero, err
	}

	selectionDigest, err := attemptHash("ctrlplane.managed.recovery-original-selections.v1", selections)
	if err != nil {
		return zero, err
	}

	after, err := recoveryActivationReserve(target, view)
	if err != nil {
		return zero, err
	}

	attemptRevision, err := view.state.Revision.Uint64()
	if err != nil || attemptRevision == 0 || attemptRevision > math.MaxInt64 {
		return zero, managed.ErrUnavailable
	}

	if err = recoveryCheckHeadroom(target.Revision, attemptRevision, 1, after); err != nil {
		return zero, err
	}

	requestDigest, err := attemptHash(managed.RemovalRecoveryProfile+".activate", q)
	if err != nil {
		return zero, err
	}

	facts := managed.RemovalRecoveryActivation{Key: key, AttemptRef: view.ref, Profile: managed.RemovalRecoveryProfile, RetryProfile: managed.ProviderStepRetryProfile, ProviderOperationID: view.descriptor.ProviderOperationID, AdmissionDigest: view.state.AdmissionDigest, BindingCount: managed.Decimal(strconv.Itoa(bindingCount)), StepCount: managed.Decimal(strconv.Itoa(len(selections))), OriginalSelectionDigest: selectionDigest, ActivatedAt: now.UTC()}

	factDigest, err := managed.RecoveryFactDigest(facts)
	if err != nil {
		return zero, err
	}

	receipt := managed.Receipt{ID: command.ID, Digest: requestDigest, Revision: target.Revision + 1, LeaseEpoch: target.LeaseEpoch, AcceptedAt: now.UTC(), LeaseUntil: target.LeaseUntil}
	record := managed.RecoveryRecord[managed.RemovalRecoveryActivation]{SchemaVersion: "1", Kind: managed.RecoveryActivationRecord, Facts: facts, Digest: factDigest, Acceptance: managed.RecoveryAcceptance{OperationID: command.ID, RequestDigest: requestDigest, Receipt: managed.ReceiptWire(receipt)}}

	recordKey, err := recoveryFactKey(key, record.Kind, record.Acceptance.OperationID, record.Facts)
	if err != nil {
		return zero, err
	}

	activation, err := managed.RecoveryAssociationFor(recordKey, record)
	if err != nil {
		return zero, err
	}

	if _, err = managed.EncodeRecoveryRecord(record); err != nil {
		return zero, err
	}

	view.state.SchemaVersion = "2"
	view.state.Revision = managed.Decimal(strconv.FormatUint(attemptRevision+1, 10))
	state := recoveryAttemptState{AttemptState: view.state, Activation: activation}

	stateBytes, err := json.Marshal(state)
	if err != nil {
		return zero, err
	}

	if len(stateBytes) > managed.MaxAttemptFact {
		return zero, managed.ErrCapacity
	}

	return recoverySnapshot(recoveryActivationPlan{Profile: record, State: state, Selections: selections})
}

// recoveryProofBindingHeld rechecks local identity and time after host verification.
// Native proof authentication alone cannot satisfy these held authority checks.
func recoveryProofBindingHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, facts managed.RemovalRevalidationFacts, now time.Time) error {
	binding, err := recoveryBindingHeld(ctx, tx, target, view, facts.Binding.Key)
	if err != nil {
		return err
	}

	if binding != facts.Binding {
		return managed.ErrAuthority
	}

	intent, err := recoveryReadFact[managed.RecoveryNativeIntent](ctx, tx, facts.Binding.Key.AttemptKey, facts.Intent)
	if err != nil {
		return err
	}

	if err = recoveryIntentMatches(intent, facts.Intent, binding, "revalidate", facts.NativeRequestID, facts.NativeRequestDigest); err != nil {
		return err
	}

	if !managed.RecoveryProofFresh(facts, now) {
		return managed.ErrBlocked
	}

	return nil
}
