package managedstate

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/xraph/ctrlplane/managed"
)

// ProviderEffectBindingRepository exposes evidence retention without issuing capability.
// It shares the repository backend and cannot satisfy complete removal recovery.
type ProviderEffectBindingRepository struct {
	repository *Repository
	host       managed.ProviderEffectBindingHost
}

// WithProviderEffectBindings fixes the trusted producer and verifier for this view.
// No default provider is installed, including for Docker targets.
func (r *Repository) WithProviderEffectBindings(host managed.ProviderEffectBindingHost) (*ProviderEffectBindingRepository, error) {
	if r == nil || r.backend == nil || !token(host.Provider) || !token(host.BackendAuthority) {
		return nil, managed.ErrUnsupported
	}
	// Transaction views retain their caller's authority lock across this method.
	// Host callbacks require a root backend that opens and closes its own transactions.
	if _, held := r.backend.(ControllerTransaction); held {
		return nil, managed.ErrUnsupported
	}

	for _, capability := range []any{host.Producer, host.Verifier} {
		if capability == nil {
			return nil, managed.ErrUnsupported
		}

		value := reflect.ValueOf(capability)
		switch value.Kind() {
		case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
			if value.IsNil() {
				return nil, managed.ErrUnsupported
			}
		}
	}

	version, err := host.Identity.Version.Uint64()
	if err != nil || version == 0 || version > 1<<63-1 || !token(host.Identity.ProducerID) || !token(host.Identity.Profile) || !token(host.Identity.VerifierID) {
		return nil, managed.ErrInvalid
	}

	return &ProviderEffectBindingRepository{repository: r, host: host}, nil
}

func providerEffectKey(key managed.ProviderEffectBindingKey) (string, string, error) {
	if _, err := retryStepKey(key.AttemptKey, key.Step); err != nil {
		return "", "", err
	}

	if key.OriginalCommandID.Prefix() != managed.PrefixOperation {
		return "", "", managed.ErrInvalid
	}

	scope, err := attemptScope(key.Key)
	if err != nil {
		return "", "", err
	}

	selected, err := attemptHash(managed.ProviderEffectBindingProfile+".key", key)

	return scope, selected, err
}

// providerEffectOriginalHeld uses the checked attempt reader and exact original slot.
// A saved permission without effect evidence is still an issued, possibly effective call.
func providerEffectOriginalHeld(ctx context.Context, tx ControllerTransaction, target *managed.Target, key managed.ProviderEffectBindingKey) (managed.ProviderEffectOriginal, error) {
	var zero managed.ProviderEffectOriginal

	view, _, err := readRecoveryAttemptInspection(ctx, tx, target, key.AttemptKey)
	if err != nil {
		return zero, err
	}

	if view.recoveryActivation != nil {
		pending, _, pendingErr := guardedOriginalPendingHeld(ctx, tx, target, view, key.Step)
		if pendingErr == nil {
			if pending.Original.Key != key {
				return zero, managed.ErrAuthority
			}

			return pending.Original, nil
		}
		// Adopted original slots still use their unchanged legacy permission.
		stepKey, keyErr := retryStepKey(key.AttemptKey, key.Step)
		if keyErr != nil {
			return zero, keyErr
		}

		fixed, readErr := attemptRead[recoveryFixedSelection](ctx, tx, recoveryFixedSelectionKind, view.scope, stepKey, managed.MaxStepRetryPointer)
		if readErr != nil {
			return zero, readErr
		}

		if fixed.Disposition != "original" {
			return zero, pendingErr
		}
	}

	steps, err := attemptSteps(target, view)
	if err != nil {
		return zero, err
	}

	for index, step := range steps {
		if step != key.Step {
			continue
		}

		record, association, readErr := attemptProviderRead(ctx, tx, target, view, step, index)
		if readErr != nil {
			return zero, readErr
		}

		if record.Permission.CommandID != key.OriginalCommandID {
			return zero, managed.ErrUnavailable
		}

		original := managed.ProviderEffectOriginal{Key: key, AttemptRef: view.ref, Permission: managed.ProviderPermissionWire(record.Permission), Receipt: association.Receipt}
		if readErr = managed.ValidateProviderEffectOriginal(original); readErr != nil {
			return zero, readErr
		}

		return original, nil
	}

	return zero, managed.ErrUnavailable
}

// providerEffectReadHeld is the exact reader for later typed activation. Its caller
// supplies an original derived from checked retained slots in the same transaction.
// Missing evidence refuses; this helper never changes a selection to unissued.
func providerEffectReadHeld(ctx context.Context, tx ControllerTransaction, original managed.ProviderEffectOriginal) (managed.ProviderEffectBindingRecord, error) {
	var zero managed.ProviderEffectBindingRecord
	if err := managed.ValidateProviderEffectOriginal(original); err != nil {
		return zero, err
	}

	scope, selected, err := providerEffectKey(original.Key)
	if err != nil {
		return zero, err
	}

	raw, err := tx.ReadRecord(ctx, managed.ProviderEffectBindingKind, scope, selected)
	if errors.Is(err, managed.ErrNotFound) {
		return zero, managed.ErrUnavailable
	}

	if err != nil {
		return zero, err
	}

	record, err := managed.DecodeProviderEffectBinding(raw)
	if err != nil {
		return zero, err
	}

	if !managed.ProviderEffectOriginalEqual(record.Facts.Original, original) {
		return zero, managed.ErrAuthority
	}

	for _, wire := range []managed.WireReceipt{original.Receipt, record.Acceptance.Receipt} {
		receipt, receiptErr := wire.Value()
		if receiptErr != nil {
			return zero, receiptErr
		}

		if receiptErr = attemptReceiptCheck(ctx, tx, original.Key.Key, receipt); receiptErr != nil {
			return zero, receiptErr
		}
	}

	return record, nil
}

// ReadProviderEffectBinding inspects retained evidence and returns no provider grant.
func (r *Repository) ReadProviderEffectBinding(ctx context.Context, key managed.ProviderEffectBindingKey) (managed.ProviderEffectBindingRecord, error) {
	var out managed.ProviderEffectBindingRecord
	if _, _, err := providerEffectKey(key); err != nil {
		return out, err
	}

	err := r.controller(ctx, "attempt:"+key.InstanceID.String(), []managed.Key{key.Key}, func(tx ControllerTransaction) error {
		target, err := tx.Read(ctx, key.Key)
		if err != nil {
			return err
		}

		original, err := providerEffectOriginalHeld(ctx, tx, target, key)
		if err != nil {
			return err
		}

		out, err = providerEffectReadHeld(ctx, tx, original)

		return err
	})
	if err != nil {
		return managed.ProviderEffectBindingRecord{}, err
	}

	return out, nil
}

func (r *ProviderEffectBindingRepository) recoverProviderEffect(ctx context.Context, original managed.ProviderEffectOriginal) (managed.ProviderEffectBindingFacts, error) {
	var zero managed.ProviderEffectBindingFacts
	if r == nil || r.repository == nil {
		return zero, managed.ErrUnsupported
	}

	if err := managed.ValidateProviderEffectOriginal(original); err != nil {
		return zero, err
	}

	if original.Permission.Physical.Provider != r.host.Provider {
		return zero, managed.ErrAuthority
	}

	input, err := recoverySnapshot(original)
	if err != nil {
		return zero, err
	}

	facts, err := r.host.Producer.RecoverProviderEffectBinding(ctx, input)
	if err != nil {
		return zero, err
	}

	facts, err = recoverySnapshot(facts)
	if err != nil {
		return zero, err
	}

	if err = managed.ValidateProviderEffectBindingFacts(facts); err != nil {
		return zero, err
	}

	command, err := managed.DecodeProviderEffectCommand(facts.Command)
	if err != nil {
		return zero, err
	}

	if !managed.ProviderEffectOriginalEqual(facts.Original, original) || facts.Producer != r.host.Identity || command.BackendAuthority != r.host.BackendAuthority {
		return zero, managed.ErrAuthority
	}

	verification, err := recoverySnapshot(facts)
	if err != nil {
		return zero, err
	}

	if err = r.host.Verifier.VerifyProviderEffectBinding(ctx, original, verification); err != nil {
		return zero, err
	}

	return facts, nil
}

// RetainProviderEffectBinding authenticates outside locks, then inserts one immutable
// evidence row and its parent receipt atomically. Parent and attempt counters stay fixed.
// It issues no permission, transport, marker, retry selection, or activation.
func (r *ProviderEffectBindingRepository) RetainProviderEffectBinding(ctx context.Context, q managed.ProviderEffectBindingRequest) (managed.ProviderEffectBindingRecord, error) {
	var out managed.ProviderEffectBindingRecord
	if r == nil || r.repository == nil {
		return out, managed.ErrUnsupported
	}

	if !validAttemptCommand(q.Command) || q.Command.Key != q.Original.Key {
		return out, managed.ErrInvalid
	}

	scope, selected, err := providerEffectKey(q.Original)
	if err != nil {
		return out, err
	}

	fingerprint, err := attemptHash(managed.ProviderEffectBindingProfile+".retain", q)
	if err != nil {
		return out, err
	}

	var (
		original managed.ProviderEffectOriginal
		replay   bool
	)

	inspect := func(tx ControllerTransaction) error {
		target, readErr := tx.Read(ctx, q.Command.Key)
		if readErr != nil {
			return readErr
		}

		original, readErr = providerEffectOriginalHeld(ctx, tx, target, q.Original)
		if readErr != nil {
			return readErr
		}

		if _, readErr = tx.ReadRecord(ctx, managed.ProviderEffectBindingKind, scope, selected); errors.Is(readErr, managed.ErrNotFound) {
			return nil
		}

		if readErr != nil {
			return readErr
		}

		out, readErr = providerEffectReadHeld(ctx, tx, original)
		if readErr != nil {
			return readErr
		}

		if out.Acceptance.OperationID != q.Command.ID || out.RequestDigest != fingerprint {
			return managed.ErrConflict
		}

		replay = true

		return nil
	}

	serial := "attempt:" + q.Original.InstanceID.String()
	if err = r.repository.controller(ctx, serial, []managed.Key{q.Command.Key}, inspect); err != nil {
		return managed.ProviderEffectBindingRecord{}, err
	}

	if replay {
		return out, nil
	}

	facts, err := r.recoverProviderEffect(ctx, original)
	if err != nil {
		return managed.ProviderEffectBindingRecord{}, err
	}

	factDigest, err := managed.ProviderEffectBindingDigest(facts)
	if err != nil {
		return managed.ProviderEffectBindingRecord{}, err
	}

	acceptanceDigest, err := managed.ProviderEffectBindingAcceptanceDigest(fingerprint, factDigest)
	if err != nil {
		return managed.ProviderEffectBindingRecord{}, err
	}

	err = r.repository.controller(ctx, serial, []managed.Key{q.Command.Key}, func(tx ControllerTransaction) error {
		// Recheck a concurrent identical retention before opening a new operation.
		if inspectErr := inspect(tx); inspectErr != nil || replay {
			return inspectErr
		}

		if !managed.ProviderEffectOriginalEqual(facts.Original, original) {
			return managed.ErrConflict
		}

		fresh := false

		_, atomicErr := tx.Atomic(ctx, q.Command.Key, q.Command.ID, acceptanceDigest, func(current *managed.Target, now time.Time) (managed.Receipt, error) {
			if current.Revision != q.Command.Revision || current.LeaseEpoch != q.Command.LeaseEpoch || current.LeaseOwner != q.Command.Owner || !now.Before(current.LeaseUntil) {
				return managed.Receipt{}, managed.ErrConflict
			}

			if localErr := removalLocalIDAvailable(ctx, tx, q.Command.ID); localErr != nil {
				return managed.Receipt{}, localErr
			}

			held, heldErr := providerEffectOriginalHeld(ctx, tx, current, q.Original)
			if heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			if !managed.ProviderEffectOriginalEqual(held, facts.Original) {
				return managed.Receipt{}, managed.ErrConflict
			}

			receipt := managed.Receipt{ID: q.Command.ID, Digest: acceptanceDigest, Revision: current.Revision, LeaseEpoch: current.LeaseEpoch, AcceptedAt: now.UTC(), LeaseUntil: current.LeaseUntil}

			out = managed.ProviderEffectBindingRecord{SchemaVersion: "1", Kind: managed.ProviderEffectBindingKind, Facts: facts, Digest: factDigest, RequestDigest: fingerprint, Acceptance: managed.RecoveryAcceptance{OperationID: q.Command.ID, RequestDigest: acceptanceDigest, Receipt: managed.ReceiptWire(receipt)}}
			if heldErr = providerEffectInsertHeld(ctx, tx, out); heldErr != nil {
				return managed.Receipt{}, heldErr
			}

			fresh = true

			return receipt, nil
		})
		if atomicErr != nil {
			return atomicErr
		}

		if !fresh {
			return managed.ErrUnavailable
		}

		return nil
	})
	if err != nil {
		return managed.ProviderEffectBindingRecord{}, err
	}

	return out, nil
}

// providerEffectInsertHeld validates every byte before the single immutable insert.
// The surrounding authority transaction must persist its acceptance receipt atomically.
func providerEffectInsertHeld(ctx context.Context, tx ControllerTransaction, record managed.ProviderEffectBindingRecord) error {
	raw, err := managed.EncodeProviderEffectBinding(record)
	if err != nil {
		return err
	}

	scope, selected, err := providerEffectKey(record.Facts.Original.Key)
	if err != nil {
		return err
	}

	return tx.WriteRecord(ctx, managed.ProviderEffectBindingKind, scope, selected, raw, true)
}

// providerEffectRootHeld prepares fixed root facts from retained authenticated evidence.
// Activation must still atomically materialize all selections and its schema fence.
func providerEffectRootHeld(ctx context.Context, tx ControllerTransaction, original managed.ProviderEffectOriginal) (managed.ProviderStepRetryRoot, error) {
	var zero managed.ProviderStepRetryRoot

	record, err := providerEffectReadHeld(ctx, tx, original)
	if err != nil {
		return zero, err
	}

	command, err := managed.DecodeProviderEffectCommand(record.Facts.Command)
	if err != nil {
		return zero, err
	}

	return managed.ProviderStepRetryRoot{AttemptRef: original.AttemptRef, Step: original.Key.Step, Profile: managed.ProviderStepRetryProfile, SpecDigest: original.Permission.SpecDigest, ResourceID: command.ResourceID, Incarnation: command.Incarnation, ProviderCommandDigest: record.Facts.ProviderCommandDigest, OriginalPermission: original.Permission, OriginalPermissionReceipt: original.Receipt}, nil
}
