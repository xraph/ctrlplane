package managedstate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

// AcceptManagedOriginalRegistration atomically retains the original native export and A receipt.
func (r *Repository) AcceptManagedOriginalRegistration(ctx context.Context, q managed.OriginalRegistrationAcceptanceRequest) (managed.Receipt, error) {
	return r.acceptOriginal(ctx, managed.OriginalLifecycleMaterial{Command: q.Command, Kind: managed.OriginalRegistration, Registration: &q.Facts, NativeReceipt: q.NativeReceipt, NativeReceiptDigest: q.NativeReceiptDigest})
}

// RecordManagedOriginalDrain retains process acceptance without asserting terminal success.
func (r *Repository) RecordManagedOriginalDrain(ctx context.Context, q managed.OriginalDrainAcceptanceRequest) (managed.Receipt, error) {
	return r.acceptOriginal(ctx, managed.OriginalLifecycleMaterial{Command: q.Command, Kind: managed.OriginalDrain, Drain: &q.Facts, NativeReceipt: q.NativeReceipt, NativeReceiptDigest: q.NativeReceiptDigest})
}

func originalScope(k managed.Key) (string, error) { return controllerFingerprint(k) }

func originalARequest(m managed.OriginalLifecycleMaterial, reference []byte) (managed.Command, string, any, error) {
	c, err := m.Command.LocalCommand()
	if err != nil {
		return c, "", nil, err
	}

	selected := managed.ReservationRequest{Command: c, ReservationID: m.Command.ReservationID}
	if m.Kind == managed.OriginalRegistration {
		return c, "accept", managed.AcceptanceRequest{ReservationRequest: selected, Receipt: reference}, nil
	}

	if m.Drain == nil {
		return c, "", nil, managed.ErrInvalid
	}

	return c, "drain", managed.DrainRequest{ReservationRequest: selected, Drain: managed.Drain{OperationID: m.Drain.OperationID, Deadline: m.Drain.Deadline, Evidence: reference}}, nil
}

// originalAReceipt reads exact immutable A history in the held transaction.
// Missing receipts cannot mutate the parent or repair lost history.
func originalAReceipt(ctx context.Context, tx ControllerTransaction, m managed.OriginalLifecycleMaterial, reference []byte, missing error) (managed.Receipt, error) {
	c, kind, input, err := originalARequest(m, reference)
	if err != nil {
		return managed.Receipt{}, err
	}

	raw, err := json.Marshal(struct {
		Kind  string `json:"kind"`
		Input any    `json:"input"`
	}{kind, input})
	if err != nil {
		return managed.Receipt{}, err
	}

	receipt, err := tx.ReadReceipt(ctx, c.Key, c.ID, managed.Digest(raw))
	if errors.Is(err, managed.ErrNotFound) {
		return managed.Receipt{}, missing
	}

	return receipt, err
}

func originalRegistrationMatch(ctx context.Context, tx ControllerTransaction, t *managed.Target, m managed.OriginalLifecycleMaterial) error {
	v, err := reservation(t, m.Command.ReservationID)
	if err != nil {
		return err
	}

	f := m.Registration
	if f == nil || f.Member != v.Member || f.StoredCommandDigest != v.CommandDigest || managed.Digest(v.Command) != v.CommandDigest || f.Native.RequestID != v.RequestID || f.Process.RuntimeID != v.RuntimeID || f.Process.InstanceID != v.InstanceID || f.Process.HostID != v.HostID {
		return managed.ErrAuthority
	}

	var (
		spec  managed.ProvisionSpec
		roles managed.ManagedSpec
	)
	if checked, ok := tx.(*attemptCheckedReads); ok {
		spec, roles, err = checked.parsedManifest(ctx, t)
	} else {
		spec, err = managed.ParseProvisionSpec(t.Manifest)
		if err != nil {
			return err
		}

		roles, err = managed.ParseManagedSpec(t.Manifest)
	}

	if err != nil {
		return err
	}

	for _, other := range t.Reservations {
		if other.RuntimeID != v.RuntimeID {
			continue
		}

		for n, member := range t.Manifest.Members {
			if other.Member == member.Name && roles.RuntimeRoles[n] != f.Role {
				return managed.ErrConflict
			}
		}
	}

	for n, member := range t.Manifest.Members {
		if member.Name != v.Member {
			continue
		}

		p := f.Process
		if roles.RuntimeRoles[n] != f.Role || p.Installation != member.Installation || p.Namespace != member.Namespace || p.Build != member.Build || p.Queue != member.Queue || p.ArtifactDigest != member.ArtifactDigest || p.ConfigurationDigest != member.ConfigurationDigest {
			return managed.ErrAuthority
		}

		for _, service := range spec.Services {
			if service.Name == member.Service {
				if service.Startup != p.Descriptor {
					return managed.ErrAuthority
				}

				return nil
			}
		}
	}

	return managed.ErrAuthority
}

func originalRegistrationsForDrain(ctx context.Context, tx ControllerTransaction, t *managed.Target, m managed.OriginalLifecycleMaterial) error {
	selected, err := reservation(t, m.Command.ReservationID)
	if err != nil {
		return err
	}

	if m.Drain == nil || selected.RuntimeID != m.Drain.Process.RuntimeID {
		return managed.ErrAuthority
	}

	count := 0

	for _, v := range t.Reservations {
		if v.RuntimeID != selected.RuntimeID {
			continue
		}

		if v.Phase != managed.Accepted {
			return managed.ErrBlocked
		}

		reference, err := managed.ParseOriginalReference(v.Receipt)
		if err != nil {
			return managed.ErrUnavailable
		}

		if reference.Kind != managed.OriginalRegistration {
			return managed.ErrAuthority
		}

		saved, _, err := readOriginal(ctx, tx, managed.OriginalLifecycleKey{Key: t.Key, Kind: reference.Kind, OperationID: reference.OperationID}, t)
		if err != nil {
			return err
		}

		if saved.Material.Command.ReservationID != v.ID || saved.Material.Registration == nil || saved.Material.Registration.Process != m.Drain.Process {
			return managed.ErrAuthority
		}

		count++
	}

	if count == 0 {
		return managed.ErrBlocked
	}

	return nil
}

func originalAssociation(ctx context.Context, tx ControllerTransaction, t *managed.Target, record managed.OriginalLifecycleRecord) error {
	m := record.Material

	identityDigest, err := managed.OriginalIdentityDigest(t.Identity)
	if err != nil {
		return err
	}

	if m.Command.Key != t.Key || m.TargetIdentityDigest != identityDigest {
		return managed.ErrAuthority
	}

	reference, err := record.Reference.Bytes()
	if err != nil {
		return err
	}

	v, err := reservation(t, m.Command.ReservationID)
	if err != nil {
		return err
	}

	switch m.Kind {
	case managed.OriginalRegistration:
		if err = originalRegistrationMatch(ctx, tx, t, m); err != nil {
			return err
		}

		if v.Phase != managed.Accepted || !bytes.Equal(v.Receipt, reference) {
			return managed.ErrUnavailable
		}
	case managed.OriginalDrain:
		if err = originalRegistrationsForDrain(ctx, tx, t, m); err != nil {
			return err
		}

		for _, other := range t.Reservations {
			if other.RuntimeID != m.Drain.Process.RuntimeID {
				continue
			}

			d := other.Drain
			if d == nil || d.OperationID != m.Drain.OperationID || !d.Deadline.Equal(m.Drain.Deadline) || d.Complete || d.Quiescent || !d.CompletedAt.IsZero() || !bytes.Equal(d.Evidence, reference) {
				return managed.ErrUnavailable
			}
		}
	default:
		return managed.ErrInvalid
	}

	return nil
}

func readOriginal(ctx context.Context, tx ControllerTransaction, k managed.OriginalLifecycleKey, t *managed.Target) (managed.OriginalLifecycleRecord, managed.Receipt, error) {
	var empty managed.OriginalLifecycleRecord

	scope, err := originalScope(k.Key)
	if err != nil {
		return empty, managed.Receipt{}, err
	}

	raw, err := tx.ReadRecord(ctx, k.Kind, scope, k.OperationID.String())
	if err != nil {
		return empty, managed.Receipt{}, err
	}

	record, err := managed.ParseOriginalLifecycleRecord(raw)
	if err != nil {
		return empty, managed.Receipt{}, managed.ErrUnavailable
	}

	if record.Material.Command.Key != k.Key || record.Material.Kind != k.Kind || record.Material.Command.ID != k.OperationID {
		return empty, managed.Receipt{}, managed.ErrAuthority
	}

	if err = originalAssociation(ctx, tx, t, record); err != nil {
		return empty, managed.Receipt{}, err
	}

	reference, err := record.Reference.Bytes()
	if err != nil {
		return empty, managed.Receipt{}, err
	}

	receipt, err := originalAReceipt(ctx, tx, record.Material, reference, managed.ErrUnavailable)
	if err != nil {
		return empty, managed.Receipt{}, err
	}

	if managed.OriginalReceipt(receipt) != record.Receipt {
		return empty, managed.Receipt{}, managed.ErrUnavailable
	}

	return record, receipt, nil
}

func (r *Repository) acceptOriginal(ctx context.Context, m managed.OriginalLifecycleMaterial) (managed.Receipt, error) {
	var out managed.Receipt

	c, err := m.Command.LocalCommand()
	if err != nil {
		return out, err
	}

	scope, err := originalScope(c.Key)
	if err != nil {
		return out, err
	}

	err = r.controller(ctx, "original:"+scope, []managed.Key{c.Key}, func(tx ControllerTransaction) error {
		target, err := tx.Read(ctx, c.Key)
		if err != nil {
			return err
		}

		m.TargetIdentityDigest, err = managed.OriginalIdentityDigest(target.Identity)
		if err != nil {
			return err
		}

		m, err = m.Normalize()
		if err != nil {
			return err
		}

		k := managed.OriginalLifecycleKey{Key: c.Key, Kind: m.Kind, OperationID: c.ID}
		// Determine row presence before validating fresh authority. Any existing row
		// must resolve completely; a broken association never falls through to insert.
		_, rowErr := tx.ReadRecord(ctx, k.Kind, scope, k.OperationID.String())
		if rowErr == nil {
			saved, receipt, err := readOriginal(ctx, tx, k, target)
			if err != nil {
				return err
			}

			if !reflect.DeepEqual(saved.Material, m) {
				return managed.ErrConflict
			}

			out = receipt

			return nil
		}

		if !controllerAbsent(rowErr) {
			return rowErr
		}

		if _, err = controllerParent(ctx, tx, c); err != nil {
			return err
		}

		v, err := reservation(target, m.Command.ReservationID)
		if err != nil {
			return err
		}

		if m.Kind == managed.OriginalRegistration {
			if v.Phase != managed.MayHaveIssued || len(v.Receipt) != 0 {
				return managed.ErrBlocked
			}

			if err = originalRegistrationMatch(ctx, tx, target, m); err != nil {
				return err
			}

			if m.Registration.Native.AcceptedAt.After(tx.Now()) {
				return managed.ErrInvalid
			}
		} else {
			if err = originalRegistrationsForDrain(ctx, tx, target, m); err != nil {
				return err
			}

			if err = originalDrainFreshEligibility(target, m.Command.ReservationID); err != nil {
				return err
			}

			if m.Drain.ObservedAt.After(tx.Now()) {
				return managed.ErrInvalid
			}

			for _, other := range target.Reservations {
				if other.RuntimeID == m.Drain.Process.RuntimeID && other.Drain != nil {
					return managed.ErrBlocked
				}
			}
		}

		reference, err := managed.OriginalReference(m)
		if err != nil {
			return err
		}

		raw, err := reference.Bytes()
		if err != nil {
			return err
		}

		if _, err = originalAReceipt(ctx, tx, m, raw, managed.ErrNotFound); !errors.Is(err, managed.ErrNotFound) {
			if err == nil {
				return managed.ErrUnavailable
			}

			return err
		}

		_, _, request, err := originalARequest(m, raw)
		if err != nil {
			return err
		}

		repo := New(tx)

		switch q := request.(type) {
		case managed.AcceptanceRequest:
			out, err = repo.AcceptManagedRegistration(ctx, q)
		case managed.DrainRequest:
			out, err = repo.RecordManagedDrain(ctx, q)
		default:
			return managed.ErrInvalid
		}

		if err != nil {
			return err
		}

		record := managed.OriginalLifecycleRecord{Material: m, Reference: reference, Receipt: managed.OriginalReceipt(out)}

		return controllerWrite(ctx, tx, m.Kind, scope, c.ID.String(), record, true, managed.MaxOriginalLifecycleRecord)
	})
	if err != nil {
		return managed.Receipt{}, err
	}

	return out, nil
}

// ReadManagedOriginalLifecycle resolves exact retained material, A association and local receipt.
func (r *Repository) ReadManagedOriginalLifecycle(ctx context.Context, k managed.OriginalLifecycleKey) (managed.OriginalLifecycleRecord, error) {
	var out managed.OriginalLifecycleRecord
	if !key(k.Key) || len(k.InstanceID.String()) > 64 || k.OperationID.Prefix() != managed.PrefixOperation || len(k.OperationID.String()) > 64 || (k.Kind != managed.OriginalRegistration && k.Kind != managed.OriginalDrain) {
		return out, managed.ErrInvalid
	}

	scope, err := originalScope(k.Key)
	if err != nil {
		return out, err
	}

	err = r.controller(ctx, "original:"+scope, []managed.Key{k.Key}, func(tx ControllerTransaction) error {
		target, err := tx.Read(ctx, k.Key)
		if err != nil {
			return err
		}

		out, _, err = readOriginal(ctx, tx, k, target)

		return err
	})
	if err != nil {
		return managed.OriginalLifecycleRecord{}, err
	}

	return out, nil
}

// originalDrainFreshEligibility checks only mutable eligibility under held authority.
// Historical association callers deliberately do not apply this current Abort veto.
func originalDrainFreshEligibility(t *managed.Target, rid id.ID) error {
	selected, err := reservation(t, rid)
	if err != nil {
		return err
	}

	for _, other := range t.Reservations {
		if other.RuntimeID == selected.RuntimeID && (other.Phase != managed.Accepted || other.Abort != nil) {
			return managed.ErrBlocked
		}
	}

	return nil
}
