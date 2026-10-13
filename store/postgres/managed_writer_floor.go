package postgres

import (
	"context"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

// NewWithWriterFloorAdmission selects protected managed-mutation admission before
// publishing the store. New retains legacy behavior. Protected deployments must
// exclude legacy constructors/binaries through their host admission and credentials.
// Missing protected tables or SELECT grants refuse; this constructor installs neither.
func NewWithWriterFloorAdmission(db *grove.DB, authority string) (*Store, error) {
	if _, err := NewWriterFloorReader(db, authority); err != nil {
		return nil, err
	}

	s := New(db)
	s.writerFloorAuthority = authority

	return s, nil
}

// The whole protected namespace is locked before controller/target/ordinary locks,
// including while it is empty. Activation takes the exclusive form before its
// authority lock, so first installation cannot race a zero-floor transaction.
func writerFloorMutationLock(ctx context.Context, tx *pgdriver.PgTx, exclusive bool) error {
	query := "SELECT pg_advisory_xact_lock_shared(hashtextextended($1,0))"
	if exclusive {
		query = "SELECT pg_advisory_xact_lock(hashtextextended($1,0))"
	}

	_, err := tx.NewRaw(query, "ctrlplane-writer-floor-mutation-admission").Exec(ctx)

	return err
}

// Admission is private to one transaction. Its lock lasts through commit/rollback;
// no result is cached across transactions, and no request supplies a protocol.
type writerFloorMutationAdmission struct {
	tx        *pgdriver.PgTx
	authority string
	refusal   error
}

func (a *writerFloorMutationAdmission) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if a == nil || a.tx == nil {
		return managed.ErrUnavailable
	}

	return a.refusal
}

func (b managedBackend) writerFloorAdmissionFor(ctx context.Context, tx *pgdriver.PgTx) (*writerFloorMutationAdmission, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if b.store == nil || tx == nil {
		return nil, managed.ErrUnavailable
	}

	if b.tx != nil {
		if b.floorAdmission == nil || b.floorAdmission.tx != tx || b.floorAdmission.authority != b.store.writerFloorAuthority {
			return nil, managed.ErrUnavailable
		}

		return b.floorAdmission, nil
	}

	admission := &writerFloorMutationAdmission{tx: tx, authority: b.store.writerFloorAuthority}
	if admission.authority == "" {
		return admission, nil
	}

	if err := writerFloorMutationLock(ctx, tx, false); err != nil {
		return nil, err
	}
	// Two bounded authority keys distinguish pristine storage from configured and
	// mixed namespaces. Exact payload reads remain capped by the approved floor reader.
	var authorities []struct {
		Authority string `grove:"authority_id"`
	}
	if err := tx.NewRaw("SELECT CASE WHEN octet_length(authority_id) BETWEEN 1 AND 256 THEN authority_id ELSE '' END AS authority_id FROM public.cp_writer_floor_current ORDER BY cp_writer_floor_current.authority_id LIMIT 2").Scan(ctx, &authorities); err != nil {
		return nil, err
	}

	if len(authorities) == 0 {
		var retained bool
		if err := tx.NewRaw("SELECT EXISTS(SELECT 1 FROM public.cp_writer_floor_history)").Scan(ctx, &retained); err != nil {
			return nil, err
		}

		if retained {
			admission.refusal = managed.ErrWriterFloorCorrupt
		}

		return admission, nil
	}

	if len(authorities) != 1 {
		admission.refusal = managed.ErrUnsupported

		return admission, nil
	}

	if authorities[0].Authority != admission.authority {
		admission.refusal = managed.ErrAuthority

		return admission, nil
	}

	admission.refusal = managedstate.CheckWriterFloorMutation(ctx, writerFloorSnapshot{tx: tx, authority: admission.authority}, admission.authority)

	return admission, nil
}

func (t managedControllerTransaction) checkManagedMutationFloor(ctx context.Context) error {
	if t.tx == nil || t.floorAdmission == nil || t.floorAdmission.tx != t.tx || t.store == nil || t.floorAdmission.authority != t.store.writerFloorAuthority {
		return managed.ErrUnavailable
	}

	return t.floorAdmission.check(ctx)
}
