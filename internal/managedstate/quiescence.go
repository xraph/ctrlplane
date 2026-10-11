package managedstate

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/xraph/ctrlplane/managed"
)

func quiescenceAssociation(ctx context.Context, tx ControllerTransaction, t *managed.Target, q managed.QuiescenceAcceptanceRequest) error {
	ref := q.Observation.Reference
	if q.Command.Key != t.Key || q.Command.ReservationID != ref.ReservationID {
		return managed.ErrAuthority
	}

	reg, _, err := readOriginal(ctx, tx, managed.OriginalLifecycleKey{Key: t.Key, Kind: managed.OriginalRegistration, OperationID: ref.RegistrationOperationID}, t)
	if err != nil {
		return err
	}

	drain, _, err := readOriginal(ctx, tx, managed.OriginalLifecycleKey{Key: t.Key, Kind: managed.OriginalDrain, OperationID: ref.DrainOperationID}, t)
	if err != nil {
		return err
	}

	expected, err := managed.NewQuiescenceReference(reg, drain)
	if err != nil {
		return err
	}

	if expected != ref || q.Observation.VerifierID != ref.Process.ProducerVerifier || q.Observation.ObservedAt.Before(drain.Material.Drain.ObservedAt) {
		return managed.ErrAuthority
	}

	return nil
}

func quiescenceReceipt(ctx context.Context, tx ControllerTransaction, q managed.QuiescenceAcceptanceRequest, missing error) (managed.Receipt, error) {
	c, err := q.Command.LocalCommand()
	if err != nil {
		return managed.Receipt{}, err
	}

	fingerprint, err := quiescenceFingerprint(q)
	if err != nil {
		return managed.Receipt{}, err
	}

	receipt, err := tx.ReadReceipt(ctx, c.Key, c.ID, fingerprint)
	if errors.Is(err, managed.ErrNotFound) {
		return managed.Receipt{}, missing
	}

	return receipt, err
}

// readQuiescence validates one immutable row without recursively resolving its current index.
func readQuiescence(ctx context.Context, tx ControllerTransaction, k managed.QuiescenceKey, t *managed.Target) (managed.QuiescenceRecord, managed.Receipt, error) {
	var empty managed.QuiescenceRecord

	scope, err := managed.QuiescenceScope(k.Key, k.ReservationID)
	if err != nil {
		return empty, managed.Receipt{}, err
	}

	raw, err := tx.ReadRecord(ctx, managed.QuiescenceHistory, scope, k.OperationID.String())
	if err != nil {
		return empty, managed.Receipt{}, err
	}

	record, err := managed.ParseQuiescenceRecord(raw)
	if err != nil {
		return empty, managed.Receipt{}, managed.ErrUnavailable
	}

	c := record.Request.Command
	if c.Key != k.Key || c.ReservationID != k.ReservationID || c.ID != k.OperationID {
		return empty, managed.Receipt{}, managed.ErrAuthority
	}

	if err = quiescenceAssociation(ctx, tx, t, record.Request); err != nil {
		return empty, managed.Receipt{}, err
	}

	receipt, err := quiescenceReceipt(ctx, tx, record.Request, managed.ErrUnavailable)
	if err != nil {
		return empty, managed.Receipt{}, err
	}

	if managed.OriginalReceipt(receipt) != record.Receipt {
		return empty, managed.Receipt{}, managed.ErrUnavailable
	}

	return record, receipt, nil
}

func readQuiescenceCurrent(ctx context.Context, tx ControllerTransaction, k managed.QuiescenceKey, t *managed.Target) (managed.QuiescenceIndex, managed.QuiescenceRecord, error) {
	var empty managed.QuiescenceRecord

	scope, err := managed.QuiescenceScope(k.Key, k.ReservationID)
	if err != nil {
		return managed.QuiescenceIndex{}, empty, err
	}

	raw, err := tx.ReadRecord(ctx, managed.QuiescenceCurrent, scope, "current")
	if err != nil {
		return managed.QuiescenceIndex{}, empty, err
	}

	index, err := managed.ParseQuiescenceIndex(raw)
	if err != nil || index.ReservationID != k.ReservationID {
		return index, empty, managed.ErrUnavailable
	}

	k.OperationID = index.OperationID

	record, _, err := readQuiescence(ctx, tx, k, t)
	if err != nil {
		return index, empty, managed.ErrUnavailable
	}

	digest, err := managed.QuiescenceRecordDigest(record)
	if err != nil || digest != index.RecordDigest || !record.Request.Observation.ObservedAt.Equal(index.ObservedAt) {
		return index, empty, managed.ErrUnavailable
	}

	return index, record, nil
}

func quiescenceReplayCurrent(ctx context.Context, tx ControllerTransaction, k managed.QuiescenceKey, t *managed.Target, record managed.QuiescenceRecord) error {
	index, _, err := readQuiescenceCurrent(ctx, tx, k, t)
	if err != nil {
		return err
	}

	observed := record.Request.Observation.ObservedAt
	if index.ObservedAt.Before(observed) || index.ObservedAt.Equal(observed) && index.OperationID != k.OperationID {
		return managed.ErrUnavailable
	}

	return nil
}

// AcceptManagedQuiescence retains a fresh terminal observation without changing the original drain.
func (r *Repository) AcceptManagedQuiescence(ctx context.Context, q managed.QuiescenceAcceptanceRequest) (managed.Receipt, error) {
	var out managed.Receipt

	c, err := q.Command.LocalCommand()
	if err != nil {
		return out, err
	}

	q.Observation, err = q.Observation.Normalize()
	if err != nil {
		return out, err
	}

	scope, err := managed.QuiescenceScope(c.Key, q.Command.ReservationID)
	if err != nil {
		return out, err
	}

	k := managed.QuiescenceKey{Key: c.Key, ReservationID: q.Command.ReservationID, OperationID: c.ID}

	err = r.controller(ctx, "quiescence:"+scope, []managed.Key{c.Key}, func(tx ControllerTransaction) error {
		target, err := tx.Read(ctx, c.Key)
		if err != nil {
			return err
		}

		_, rowErr := tx.ReadRecord(ctx, managed.QuiescenceHistory, scope, c.ID.String())
		if rowErr == nil {
			saved, receipt, err := readQuiescence(ctx, tx, k, target)
			if err != nil {
				return err
			}

			if !reflect.DeepEqual(saved.Request, q) {
				return managed.ErrConflict
			}

			if err = quiescenceReplayCurrent(ctx, tx, k, target, saved); err != nil {
				return err
			}

			out = receipt

			return nil
		}

		if !controllerAbsent(rowErr) {
			return rowErr
		}
		// A saved receipt without its observation is lost history, not an insert retry.
		if _, err = quiescenceReceipt(ctx, tx, q, managed.ErrNotFound); !errors.Is(err, managed.ErrNotFound) {
			if err == nil {
				return managed.ErrUnavailable
			}

			return err
		}

		if _, err = controllerParent(ctx, tx, c); err != nil {
			return err
		}

		if err = quiescenceAssociation(ctx, tx, target, q); err != nil {
			return err
		}

		index, _, indexErr := readQuiescenceCurrent(ctx, tx, k, target)

		first := controllerAbsent(indexErr)
		switch {
		case first:
			// Existence only. Proof selection and closure always use exact reservation keys.
			history, err := tx.ListRecords(ctx, managed.QuiescenceHistory, scope, "", 1)
			if err != nil {
				return err
			}

			if len(history) != 0 {
				return managed.ErrUnavailable
			}
		case indexErr != nil:
			return indexErr
		case !q.Observation.ObservedAt.After(index.ObservedAt):
			return managed.ErrConflict
		}

		var verified time.Time

		out, err = New(tx).apply(ctx, c, managed.QuiescenceHistory, q, true, func(t *managed.Target, now time.Time) error {
			if e := originalDrainFreshEligibility(t, q.Command.ReservationID); e != nil {
				return e
			}

			v, err := reservation(t, q.Command.ReservationID)
			if err != nil {
				return err
			}

			if v.Phase != managed.Accepted || v.Abort != nil {
				return managed.ErrBlocked
			}

			if q.Observation.ObservedAt.After(now) || !now.Before(q.Observation.ValidUntil) {
				return managed.ErrBlocked
			}

			verified = now.UTC()

			return nil
		})
		if err != nil {
			return err
		}

		record := managed.QuiescenceRecord{Request: q, VerifiedAt: verified, Receipt: managed.OriginalReceipt(out)}

		raw, err := record.Bytes()
		if err != nil {
			return err
		}

		digest, err := managed.QuiescenceRecordDigest(record)
		if err != nil {
			return err
		}

		if err = tx.WriteRecord(ctx, managed.QuiescenceHistory, scope, c.ID.String(), raw, true); err != nil {
			return err
		}

		current := managed.QuiescenceIndex{Schema: "ctrlplane.quiescence-current.v1", ReservationID: k.ReservationID, OperationID: c.ID, ObservedAt: q.Observation.ObservedAt, RecordDigest: digest}

		raw, err = current.Bytes()
		if err != nil {
			return err
		}

		return tx.WriteRecord(ctx, managed.QuiescenceCurrent, scope, "current", raw, first)
	})
	if err != nil {
		return managed.Receipt{}, err
	}

	return out, nil
}

// ReadManagedQuiescence resolves exact history and checks its current association without expiry checks.
func (r *Repository) ReadManagedQuiescence(ctx context.Context, k managed.QuiescenceKey) (managed.QuiescenceRecord, error) {
	var out managed.QuiescenceRecord

	scope, err := managed.QuiescenceScope(k.Key, k.ReservationID)
	if err != nil {
		return out, err
	}

	if k.OperationID.Prefix() != managed.PrefixOperation || len(k.OperationID.String()) > 64 {
		return out, managed.ErrInvalid
	}

	err = r.controller(ctx, "quiescence:"+scope, []managed.Key{k.Key}, func(tx ControllerTransaction) error {
		target, err := tx.Read(ctx, k.Key)
		if err != nil {
			return err
		}

		out, _, err = readQuiescence(ctx, tx, k, target)
		if err != nil {
			return err
		}

		return quiescenceReplayCurrent(ctx, tx, k, target, out)
	})
	if err != nil {
		return managed.QuiescenceRecord{}, err
	}

	return out, nil
}

// closureAuthority acquires controller authority before the parent, or reuses the held transaction.
// Backends without this optional capability retain only the successful A drain predicate.
func (r *Repository) closureAuthority(ctx context.Context, k managed.Key, fn func(*Repository) (managed.Receipt, error)) (managed.Receipt, error) {
	if _, held := r.backend.(ControllerTransaction); held {
		return fn(r)
	}

	if _, supported := r.backend.(ControllerBackend); !supported {
		return fn(r)
	}

	var out managed.Receipt

	err := r.controller(ctx, "closure:"+k.InstanceID.String(), []managed.Key{k}, func(tx ControllerTransaction) error {
		var err error

		out, err = fn(New(tx))

		return err
	})
	if err != nil {
		return managed.Receipt{}, err
	}

	return out, nil
}

func laterQuiescence(ctx context.Context, tx ControllerTransaction, t *managed.Target, v managed.Reservation, now time.Time) error {
	if tx == nil {
		return managed.ErrBlocked
	}

	_, record, err := readQuiescenceCurrent(ctx, tx, managed.QuiescenceKey{Key: t.Key, ReservationID: v.ID}, t)
	if errors.Is(err, managed.ErrNotFound) {
		return managed.ErrBlocked
	}

	if err != nil {
		return err
	}

	o := record.Request.Observation
	if o.ObservedAt.After(now) || !now.Before(o.ValidUntil) {
		return managed.ErrBlocked
	}

	return nil
}

func quiescenceFingerprint(q managed.QuiescenceAcceptanceRequest) (string, error) {
	raw, err := json.Marshal(struct {
		Kind  string `json:"kind"`
		Input any    `json:"input"`
	}{managed.QuiescenceHistory, q})
	if err != nil {
		return "", err
	}

	return managed.Digest(raw), nil
}
