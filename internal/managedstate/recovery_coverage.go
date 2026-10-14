package managedstate

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"time"

	"github.com/xraph/ctrlplane/managed"
)

const recoveryCoverageSelectionKind = "removal_coverage_selection"

// The sponsor lives outside the coverage content hash. Its retained intent names
// Coverage, and its original event/receipt authenticates this acyclic association.
type recoveryCoverageSelection struct {
	SchemaVersion managed.Decimal             `json:"schema_version"`
	AttemptRef    managed.AttemptRef          `json:"attempt_ref"`
	Coverage      managed.RecoveryAssociation `json:"coverage"`
	Sponsor       managed.RecoveryAssociation `json:"sponsor"`
	Command       managed.RecoveryCommand     `json:"command"`
}

type recoveryCoveragePlan struct {
	facts    managed.EffectiveRemovalCoverage
	bindings []managed.EffectiveRemovalBinding
}

// recoveryCoverageDisposition refuses a mixture of deletion and nondeletion, as
// well as any unresolved step. Cutoff alone never supplies a terminal disposition.
func recoveryCoverageDisposition(steps []managed.EffectiveProviderStep) (managed.Disposition, error) {
	if len(steps) == 0 || len(steps) > managed.MaxRecoverySteps {
		return "", managed.ErrCapacity
	}

	positive := 0

	seen := make(map[string]bool, len(steps))
	for _, step := range steps {
		if !digest(step.StepDigest) || !digest(step.SelectionDigest) || seen[step.StepDigest] {
			return "", managed.ErrUnavailable
		}

		seen[step.StepDigest] = true
		switch step.Disposition {
		case managed.EffectiveStepUnissued:
			if step.PermissionDigest != "" || step.TerminalDigest != "" || step.ClosureDigest != "" {
				return "", managed.ErrUnavailable
			}
		case managed.EffectiveStepRemoved:
			if !digest(step.PermissionDigest) || !digest(step.TerminalDigest) || step.ClosureDigest != "" {
				return "", managed.ErrUnavailable
			}

			positive++
		case managed.EffectiveStepClosed:
			if !digest(step.PermissionDigest) || !digest(step.TerminalDigest) || !digest(step.ClosureDigest) {
				return "", managed.ErrUnavailable
			}
		default:
			return "", managed.ErrBlocked
		}
	}

	if positive == len(steps) {
		return managed.Removed, nil
	}

	if positive != 0 {
		return "", managed.ErrBlocked
	}

	return managed.SettledWithoutDeletion, nil
}

func recoverySelectedCoverage(stepDigest string, selected retrySelected) (managed.EffectiveProviderStep, error) {
	out := managed.EffectiveProviderStep{StepDigest: stepDigest, SelectionDigest: selected.current.Digest}

	permissionDigest, err := recoveryPermissionDigest(selected.permission.Facts.Permission)
	if err != nil {
		return out, err
	}

	out.PermissionDigest = permissionDigest

	if selected.result != nil {
		if selected.closure != nil || selected.failure != nil {
			return out, managed.ErrUnavailable
		}

		facts := selected.result.Facts
		wire := managed.WireProviderStepRecord{Permission: selected.permission.Facts.Permission, Result: &facts.Result}

		decoded, decodeErr := wire.Value()
		if decodeErr != nil {
			return out, decodeErr
		}

		result := decoded.Result
		if result == nil || facts.Key != selected.permission.Facts.Key || facts.RootDigest != selected.root.Digest || facts.PermissionDigest != permissionDigest || !reflect.DeepEqual(facts.Result.Permission, selected.permission.Facts.Permission) || !evidence(result.Evidence) || result.ObservedAt.Before(result.Permission.IssuedAt) || result.ObservedAt.After(selected.result.Acceptance.Receipt.AcceptedAt) {
			return out, managed.ErrUnavailable
		}

		if result.Outcome == managed.PortUnavailable {
			return out, managed.ErrBlocked
		}

		if result.Outcome != managed.PortTerminal || result.ResourceID == "" || result.ResourceID != selected.root.Facts.ResourceID || result.ResourceID != result.Permission.ResourceID || selected.root.Facts.Incarnation == "" {
			return out, managed.ErrUnavailable
		}

		if (selected.current.Ordinal != "0" || invocationGuardedOriginal(selected)) && selected.marker == nil {
			return out, managed.ErrUnavailable
		}

		out.Disposition, out.TerminalDigest = managed.EffectiveStepRemoved, selected.result.Digest

		return out, nil
	}

	if selected.failure == nil || selected.closure == nil {
		return out, managed.ErrBlocked
	}

	if err = retryClosedFailure(*selected.failure, *selected.closure); err != nil {
		return out, err
	}

	if selected.failure.Facts.Key != selected.permission.Facts.Key || selected.closure.Facts.Key != selected.permission.Facts.Key || selected.failure.Facts.RootDigest != selected.root.Digest || selected.closure.Facts.RootDigest != selected.root.Digest || selected.failure.Facts.PermissionDigest != permissionDigest || selected.closure.Facts.PermissionDigest != permissionDigest || selected.closure.Facts.FailureDigest != selected.failure.Digest {
		return out, managed.ErrUnavailable
	}

	if selected.marker != nil {
		if err = retryMarkerBeforeClosure(*selected.marker, *selected.closure); err != nil {
			return out, err
		}
	}

	out.Disposition, out.TerminalDigest, out.ClosureDigest = managed.EffectiveStepClosed, selected.failure.Digest, selected.closure.Digest

	return out, nil
}

// recoveryCoverageStepsHeld resolves only the fixed descriptor set and each selected
// invocation's bounded certificate. It never lists or recursively walks retry history.
func recoveryCoverageStepsHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, acceptedAt time.Time) ([]managed.EffectiveProviderStep, error) {
	if !view.validated || view.recoveryActivation == nil || view.state.RevocationDigest == "" {
		return nil, managed.ErrBlocked
	}

	steps, err := attemptSteps(target, view)
	if err != nil {
		return nil, err
	}

	if err = recoveryActivationSlots(target, view, len(steps)); err != nil {
		return nil, err
	}

	out := make([]managed.EffectiveProviderStep, 0, len(steps))
	for index, step := range steps {
		stepDigest, keyErr := providerStepID(step)
		if keyErr != nil {
			return nil, keyErr
		}

		stepKey, keyErr := retryStepKey(attemptKey(view), step)
		if keyErr != nil {
			return nil, keyErr
		}

		fixed, readErr := attemptRead[recoveryFixedSelection](ctx, tx, recoveryFixedSelectionKind, view.scope, stepKey, managed.MaxStepRetryPointer)
		if readErr != nil {
			return nil, readErr
		}

		if fixed.SchemaVersion != "1" || fixed.AttemptRef != view.ref || fixed.Activation != *view.recoveryActivation || fixed.Step != step {
			return nil, managed.ErrUnavailable
		}

		guarded := false

		if fixed.Disposition == "unissued" {
			origin, originErr := guardedOriginalSelectionHeld(ctx, tx, view, step)
			if originErr == nil {
				if origin.Root == nil {
					return nil, managed.ErrBlocked
				}

				guarded = true
			} else if !errors.Is(originErr, managed.ErrNotFound) {
				return nil, originErr
			}
		}

		if fixed.Disposition == "unissued" && !guarded {
			if fixed.Root != nil || view.state.ProviderSlots[index] != 0 {
				return nil, managed.ErrUnavailable
			}
			// The original slot reader independently refuses hidden original rows.
			if _, _, readErr = attemptProviderRead(ctx, tx, target, view, step, index); readErr != nil {
				return nil, readErr
			}

			for _, kind := range []string{retryCurrentKind, retryPreviousKind} {
				if _, readErr = tx.ReadRecord(ctx, kind, view.scope, stepKey); !errors.Is(readErr, managed.ErrNotFound) {
					if readErr != nil {
						return nil, readErr
					}

					return nil, managed.ErrUnavailable
				}
			}

			selectionDigest, hashErr := attemptHash("ctrlplane.managed.recovery-fixed-selection.v1", fixed)
			if hashErr != nil {
				return nil, hashErr
			}

			out = append(out, managed.EffectiveProviderStep{Disposition: managed.EffectiveStepUnissued, StepDigest: stepDigest, SelectionDigest: selectionDigest})

			continue
		}

		if !guarded && (fixed.Disposition != "original" || fixed.Root == nil || view.state.ProviderSlots[index]&attemptPermissionBit == 0) {
			return nil, managed.ErrUnavailable
		}

		selected, readErr := invocationSelectedHeld(ctx, tx, view, step)
		if readErr != nil {
			return nil, readErr
		}

		if readErr = invocationStatusMatchesHeld(ctx, tx, view, selected); readErr != nil {
			return nil, readErr
		}

		if selected.result != nil && selected.result.Acceptance.Receipt.AcceptedAt.After(acceptedAt) || selected.closure != nil && selected.closure.Acceptance.Receipt.AcceptedAt.After(acceptedAt) {
			return nil, managed.ErrUnavailable
		}

		covered, readErr := recoverySelectedCoverage(stepDigest, selected)
		if readErr != nil {
			return nil, readErr
		}

		out = append(out, covered)
	}

	return out, nil
}

// recoveryCoverageBindingHeld pins an accepted native guard observation without
// claiming that its process is still live at the later closure callback.
func recoveryCoverageBindingHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, facts managed.EffectiveRemovalBinding, acceptedAt time.Time) error {
	binding, err := recoveryBindingInspectionHeld(ctx, tx, target, view, facts.Binding.Key)
	if err != nil {
		return err
	}

	if binding != facts.Binding || facts.Proof.Kind != managed.RecoveryProofRecord {
		return managed.ErrAuthority
	}

	proof, err := recoveryProofExactHeld(ctx, tx, target, view, facts.Proof)
	if err != nil {
		return err
	}

	if proof.Acceptance.Receipt.AcceptedAt.After(acceptedAt) || proof.Facts.Binding != binding || !token(proof.Facts.GuardID) || !digest(proof.Facts.GuardRootDigest) {
		return managed.ErrUnavailable
	}

	return nil
}

func prepareRecoveryCoverageHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, view attemptView, acceptedAt time.Time) (recoveryCoveragePlan, error) {
	var out recoveryCoveragePlan

	cutoff, err := recoveryCutoffHeld(ctx, tx, view)
	if err != nil {
		return out, err
	}

	if acceptedAt.Before(cutoff.Facts.CutoffAt) {
		return out, managed.ErrUnavailable
	}

	cutoffKey, err := recoveryFactKey(attemptKey(view), managed.RecoveryCutoffRecord, cutoff.Acceptance.OperationID, cutoff.Facts)
	if err != nil {
		return out, err
	}

	cutoffRef, err := managed.RecoveryAssociationFor(cutoffKey, cutoff)
	if err != nil {
		return out, err
	}

	steps, err := recoveryCoverageStepsHeld(ctx, tx, target, view, acceptedAt)
	if err != nil {
		return out, err
	}

	disposition, err := recoveryCoverageDisposition(steps)
	if err != nil {
		return out, err
	}

	for _, reservationID := range view.descriptor.ReservationIDs {
		reservation, readErr := reservation(target, reservationID)
		if readErr != nil {
			return out, readErr
		}

		if reservation.Phase == managed.Revoked {
			continue
		}

		binding, readErr := recoveryBindingHeld(ctx, tx, target, view, attemptBinding(view, reservationID))
		if readErr != nil {
			return out, readErr
		}

		current, _, readErr := recoveryReadCurrent(ctx, tx, binding)
		if readErr != nil {
			return out, readErr
		}

		facts := managed.EffectiveRemovalBinding{Binding: binding, Proof: current.Proof}
		if readErr = recoveryCoverageBindingHeld(ctx, tx, target, view, facts, acceptedAt); readErr != nil {
			return out, readErr
		}

		out.bindings = append(out.bindings, facts)
	}

	if len(out.bindings) == 0 || len(out.bindings) > managed.MaxRecoveryBindings {
		return out, managed.ErrCapacity
	}

	out.facts = managed.EffectiveRemovalCoverage{Key: attemptKey(view), AttemptRef: view.ref, ProviderOperationID: view.descriptor.ProviderOperationID, ResourcesDigest: view.descriptor.ResourcesDigest, AdmissionDigest: view.state.AdmissionDigest, Cutoff: cutoffRef, Disposition: disposition, Steps: steps, BindingCount: managed.Decimal(strconv.Itoa(len(out.bindings)))}

	return out, nil
}
