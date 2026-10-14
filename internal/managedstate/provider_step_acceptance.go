package managedstate

import (
	"context"
	"errors"
	"math"

	"github.com/xraph/ctrlplane/managed"
)

// ReadManagedProviderStepAcceptance reads independent retained command history.
// It does not authorize, call a provider, refresh a lease or synthesize a receipt.
func (r *Repository) ReadManagedProviderStepAcceptance(ctx context.Context, q managed.ProviderStepAuthorization) (managed.ProviderStepAcceptance, error) {
	var out managed.ProviderStepAcceptance
	if r == nil || r.backend == nil {
		return out, managed.ErrUnsupported
	}

	if !validAttemptCommand(q.Command) || q.Revision >= math.MaxInt64 || q.Key != q.Step.Key {
		return out, managed.ErrInvalid
	}

	kind := "provider_step"

	var input any = q
	switch q.Step.Kind {
	case managed.StepCreateNetwork:
		kind, input = "issue_provision", q.Command
	case managed.StepCreateContainer, managed.StepStartContainer:
	default:
		return out, managed.ErrUnsupported
	}

	stepKey, err := providerStepID(q.Step)
	if err != nil {
		return out, err
	}

	fingerprint, err := controllerFingerprint(struct {
		Kind  string `json:"kind"`
		Input any    `json:"input"`
	}{kind, input})
	if err != nil {
		return out, err
	}

	err = r.controller(ctx, "step:"+stepKey, []managed.Key{q.Key}, func(tx ControllerTransaction) error {
		// ReadReceipt authenticates the exact parent, including tenant and target ID.
		// No current lease or revision is used to reconstruct historical acceptance.
		target, readErr := tx.Read(ctx, q.Key)
		if readErr != nil {
			return readErr
		}

		if target.Key != q.Key || target.ProvisionOperationID != q.Step.ProviderOperationID {
			return managed.ErrAuthority
		}

		record, readErr := attemptRead[managed.ProviderStepRecord](ctx, tx, "step", q.TenantID, stepKey, managed.MaxProviderStep)
		if readErr != nil {
			return readErr
		}

		if _, readErr = managed.ProviderStepWire(record).Value(); readErr != nil {
			return managed.ErrUnavailable
		}

		permission := record.Permission
		if permission.SpecDigest != target.Manifest.SpecDigest || permission.Physical != target.Physical {
			return managed.ErrUnavailable
		}

		if _, readErr = managed.ProviderPermissionWire(permission).Value(); readErr != nil {
			return managed.ErrUnavailable
		}

		expected, readErr := providerPermissionDigest(q, permission.SpecDigest, permission.Physical)
		if readErr != nil {
			return readErr
		}

		if permission.Step != q.Step || permission.CommandID != q.ID || permission.Digest != expected || permission.Revision != q.Revision+1 {
			return managed.ErrUnavailable
		}

		if (q.Step.Kind == managed.StepCreateNetwork || q.Step.Kind == managed.StepCreateContainer) && permission.ResourceID != "" {
			return managed.ErrUnavailable
		}

		if q.Step.Kind == managed.StepStartContainer {
			if !token(permission.ResourceID) {
				return managed.ErrUnavailable
			}
			// The start target comes from the original terminal create result, never from
			// today's resource projection or from a caller-selected resource ID.
			createStep := q.Step
			createStep.Kind = managed.StepCreateContainer

			createID, associationErr := providerStepID(createStep)
			if associationErr != nil {
				return associationErr
			}

			created, associationErr := attemptRead[managed.ProviderStepRecord](ctx, tx, "step", q.TenantID, createID, managed.MaxProviderStep)
			if associationErr != nil {
				return associationErr
			}

			if _, associationErr = managed.ProviderStepWire(created).Value(); associationErr != nil {
				return managed.ErrUnavailable
			}

			original := created.Permission
			if original.Step != createStep || original.SpecDigest != permission.SpecDigest || original.Physical != permission.Physical || original.ResourceID != "" || original.Revision >= permission.Revision || created.Result == nil || created.Result.Outcome != managed.PortTerminal || created.Result.ResourceID != permission.ResourceID {
				return managed.ErrUnavailable
			}
		}

		receipt, readErr := tx.ReadReceipt(ctx, q.Key, q.ID, fingerprint)
		if errors.Is(readErr, managed.ErrNotFound) || errors.Is(readErr, managed.ErrConflict) {
			return managed.ErrUnavailable
		}

		if readErr != nil {
			return readErr
		}

		if _, readErr = managed.ReceiptWire(receipt).Value(); readErr != nil {
			return managed.ErrUnavailable
		}

		if receipt.ID != q.ID || receipt.Digest != fingerprint || receipt.Revision != permission.Revision || receipt.LeaseEpoch != q.LeaseEpoch || !receipt.LeaseUntil.After(receipt.AcceptedAt) {
			return managed.ErrUnavailable
		}

		out = managed.ProviderStepAcceptance{Permission: permission, Receipt: receipt}

		return nil
	})
	if err != nil {
		return managed.ProviderStepAcceptance{}, err
	}

	return out, nil
}

var _ managed.ProviderStepAcceptanceStore = (*Repository)(nil)
