package managedstate

import (
	"context"
	"encoding/json"

	"github.com/xraph/ctrlplane/managed"
)

var _ managed.ProviderInvocationCommitsStore = (*Repository)(nil)

// ReadManagedProviderInvocationCommits authenticates the marker-selected set and
// each exact native acceptance under one held snapshot, without native callbacks.
func (r *Repository) ReadManagedProviderInvocationCommits(ctx context.Context, key managed.ProviderInvocationKey) ([]managed.RecoveryRecord[managed.ProviderNativeCommitFacts], error) {
	if r == nil || r.backend == nil {
		return nil, managed.ErrUnsupported
	}

	if _, err := invocationLocatorKey(key); err != nil {
		return nil, err
	}

	var out []managed.RecoveryRecord[managed.ProviderNativeCommitFacts]

	err := r.controller(ctx, "attempt:"+key.AttemptKey.InstanceID.String(), []managed.Key{key.AttemptKey.Key}, func(tx ControllerTransaction) error {
		target, readErr := tx.Read(ctx, key.AttemptKey.Key)
		if readErr != nil {
			return readErr
		}

		view, _, readErr := readRecoveryAttemptInspection(ctx, tx, target, key.AttemptKey)
		if readErr != nil {
			return readErr
		}

		if _, readErr = attemptProviderIndex(target, view, key.Step); readErr != nil {
			return readErr
		}

		selected, readErr := invocationStatusSelectionHeld(ctx, tx, view, key)
		if readErr != nil {
			return readErr
		}

		refs, readErr := invocationMarkerMembersHeld(ctx, tx, selected)
		if readErr != nil {
			return readErr
		}

		if len(refs) == 0 || len(refs) > managed.MaxRecoveryBindings || len(view.descriptor.ReservationIDs) > managed.MaxRecoveryBindings {
			return managed.ErrCapacity
		}

		out = make([]managed.RecoveryRecord[managed.ProviderNativeCommitFacts], 0, len(refs))

		for _, rid := range view.descriptor.ReservationIDs {
			reservation, memberErr := reservation(target, rid)
			if memberErr != nil {
				return memberErr
			}

			if reservation.Phase == managed.Revoked {
				continue
			}

			if len(out) >= len(refs) {
				return managed.ErrUnavailable
			}

			record, memberErr := recoveryReadFact[managed.ProviderNativeCommitFacts](ctx, tx, key.AttemptKey, refs[len(out)])
			if memberErr != nil {
				return memberErr
			}

			binding, memberErr := recoveryBindingInspectionHeld(ctx, tx, target, view, attemptBinding(view, rid))
			if memberErr != nil {
				return memberErr
			}

			if record.Facts.Invocation != key || record.Facts.Binding != binding {
				return managed.ErrUnavailable
			}
			// Use the same complete historical inspection as the existing exact reader:
			// independent receipt/event, accepted native intent/request, exact proof and
			// permission association. No present-day proof can replace an accepted member.
			request := managed.ProviderNativeCommitRequest{Command: managed.RecoveryCommand{ID: record.Acceptance.OperationID}, Facts: record.Facts}

			accepted, memberErr := invocationNativeCommitReplayHeld(ctx, tx, view, request, record.Acceptance.RequestDigest)
			if memberErr != nil {
				return memberErr
			}

			out = append(out, accepted)
		}

		if len(out) != len(refs) {
			return managed.ErrUnavailable
		}

		raw, readErr := json.Marshal(out)
		if readErr != nil {
			return readErr
		}

		if len(raw) > managed.MaxRecoveryLeafSet {
			return managed.ErrCapacity
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}
