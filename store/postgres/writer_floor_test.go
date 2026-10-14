package postgres

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/xraph/grove/driver"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

func pgFloorRequest() managed.WriterFloorActivation {
	return managed.WriterFloorActivation{Schema: managed.WriterFloorSchema, AuthorityID: "floor-test", MinimumProtocol: "1", Epoch: "1", ActivationID: id.New(managed.PrefixOperation)}
}

// This fixture uses the database owner for corruption injection. It does not qualify
// separate production credentials, deployment admission or transport custody.
func pgFloorFixture(t *testing.T) (*Store, *WriterFloorAdmin, *WriterFloorReader) {
	t.Helper()

	if os.Getenv("CTRLPLANE_WRITER_FLOOR_REQUIRED") == "1" && os.Getenv("CTRLPLANE_TEST_POSTGRES_DSN") == "" {
		t.Fatal("CTRLPLANE_TEST_POSTGRES_DSN required for writer floor qualification")
	}

	s, _ := newProjectionPG(t)

	a, err := NewWriterFloorAdmin(s.DB(), "floor-test")
	if err != nil {
		t.Fatal(err)
	}

	if err = a.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}

	r, err := NewWriterFloorReader(s.DB(), "floor-test")
	if err != nil {
		t.Fatal(err)
	}

	return s, a, r
}

func pgFloorNext(first managed.WriterFloor) managed.WriterFloorActivation {
	q := first.Activation()
	q.Epoch = "2"
	q.MinimumProtocol = "2"
	q.ActivationID = id.New(managed.PrefixOperation)
	q.ExpectedDigest = first.Digest

	return q
}

func pgFloorExec(t *testing.T, s *Store, sql string, args ...any) {
	t.Helper()

	if _, err := s.pg.NewRaw(sql, args...).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func pgFloorCounts(t *testing.T, s *Store) (int, int) {
	t.Helper()

	var current, history int
	if err := s.pg.NewRaw("SELECT count(*) FROM public.cp_writer_floor_current").Scan(t.Context(), &current); err != nil {
		t.Fatal(err)
	}

	if err := s.pg.NewRaw("SELECT count(*) FROM public.cp_writer_floor_history").Scan(t.Context(), &history); err != nil {
		t.Fatal(err)
	}

	return current, history
}

func TestWriterFloorPostgresConcurrentCASReplay(t *testing.T) {
	_, a, r := pgFloorFixture(t)

	first, err := a.Activate(t.Context(), pgFloorRequest())
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errorsOut := make(chan error, 2)

	var group sync.WaitGroup

	for range 2 {
		q := pgFloorNext(first)

		group.Go(func() { <-start; _, activateErr := a.Activate(t.Context(), q); errorsOut <- activateErr })
	}

	close(start)
	group.Wait()
	close(errorsOut)

	success, conflict := 0, 0

	for activateErr := range errorsOut {
		switch {
		case activateErr == nil:
			success++
		case errors.Is(activateErr, managed.ErrConflict):
			conflict++
		default:
			t.Fatal(activateErr)
		}
	}

	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}

	current, err := r.ReadManagedWriterFloor(t.Context())
	if err != nil || current.Epoch != "2" {
		t.Fatal(current, err)
	}

	replayed, err := a.Activate(t.Context(), first.Activation())
	if err != nil || replayed != first {
		t.Fatal(replayed, err)
	}

	after, err := r.ReadManagedWriterFloor(t.Context())
	if err != nil || after != current {
		t.Fatal("historical replay moved head", err)
	}
}

func TestWriterFloorPostgresSecondWriteFailureRollsBackHistory(t *testing.T) {
	for _, installed := range []bool{false, true} {
		t.Run(map[bool]string{false: "install", true: "advance"}[installed], func(t *testing.T) {
			s, a, r := pgFloorFixture(t)
			q := pgFloorRequest()

			var first managed.WriterFloor

			if installed {
				var err error

				first, err = a.Activate(t.Context(), q)
				if err != nil {
					t.Fatal(err)
				}

				q = pgFloorNext(first)
			}

			pgFloorExec(t, s, `CREATE FUNCTION public.floor_test_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected current failure'; END $$;
CREATE TRIGGER floor_test_fail BEFORE INSERT OR UPDATE ON public.cp_writer_floor_current FOR EACH STATEMENT EXECUTE FUNCTION public.floor_test_fail()`)

			if _, err := a.Activate(t.Context(), q); err == nil {
				t.Fatal("injected persistence failure accepted")
			}

			current, history := pgFloorCounts(t, s)

			want := 0
			if installed {
				want = 1
			}

			if current != want || history != want {
				t.Fatal("history leaked across rollback", current, history)
			}

			if installed {
				got, err := r.ReadManagedWriterFloor(t.Context())
				if err != nil || got != first {
					t.Fatal(got, err)
				}
			}
		})
	}
}

func TestWriterFloorPostgresCallbackFailureAndCancellationRollback(t *testing.T) {
	for _, cancelAfterWrite := range []bool{false, true} {
		s, a, _ := pgFloorFixture(t)
		ctx, cancel := context.WithCancel(t.Context())
		err := a.backend.WriterFloorAdminAtomic(ctx, func(tx managedstate.WriterFloorAdminTransaction) error {
			now, clockErr := tx.WriterFloorTime(ctx)
			if clockErr != nil {
				return clockErr
			}

			floor, buildErr := managed.NewWriterFloor(pgFloorRequest(), now)
			if buildErr != nil {
				return buildErr
			}

			raw, encodeErr := managed.EncodeWriterFloor(floor)
			if encodeErr != nil {
				return encodeErr
			}

			if writeErr := tx.InstallInitialWriterFloor(ctx, raw); writeErr != nil {
				return writeErr
			}

			if cancelAfterWrite {
				cancel()

				return nil
			}

			return managed.ErrUnavailable
		})

		cancel()

		want := managed.ErrUnavailable
		if cancelAfterWrite {
			want = context.Canceled
		}

		if !errors.Is(err, want) {
			t.Fatal(err)
		}

		current, history := pgFloorCounts(t, s)
		if current != 0 || history != 0 {
			t.Fatal("callback rollback leaked", current, history)
		}
	}
}

func TestWriterFloorPostgresIndexedCorruptionAndPristineRefusal(t *testing.T) {
	for _, damage := range []string{"missing_current", "bad_current", "epoch", "protocol", "digest", "bad_history"} {
		t.Run(damage, func(t *testing.T) {
			s, a, r := pgFloorFixture(t)
			if _, err := a.Activate(t.Context(), pgFloorRequest()); err != nil {
				t.Fatal(err)
			}

			switch damage {
			case "missing_current":
				pgFloorExec(t, s, "DELETE FROM public.cp_writer_floor_current")
			case "bad_current":
				pgFloorExec(t, s, "UPDATE public.cp_writer_floor_current SET data=$1", []byte("{}"))
			case "epoch":
				pgFloorExec(t, s, "UPDATE public.cp_writer_floor_current SET epoch=2")
			case "protocol":
				pgFloorExec(t, s, "UPDATE public.cp_writer_floor_current SET minimum_protocol=2")
			case "digest":
				pgFloorExec(t, s, "UPDATE public.cp_writer_floor_current SET digest=$1", strings.Repeat("f", 64))
			case "bad_history":
				pgFloorExec(t, s, "ALTER TABLE public.cp_writer_floor_history DISABLE TRIGGER cp_writer_floor_history_immutable")
				pgFloorExec(t, s, "UPDATE public.cp_writer_floor_history SET data=$1", []byte("{}"))
				pgFloorExec(t, s, "ALTER TABLE public.cp_writer_floor_history ENABLE TRIGGER cp_writer_floor_history_immutable")
			}

			if _, err := r.ReadManagedWriterFloor(t.Context()); err == nil {
				t.Fatal("damaged protected state accepted")
			}

			if _, err := a.Activate(t.Context(), pgFloorRequest()); err == nil {
				t.Fatal("nonpristine install accepted")
			}
		})
	}
}

func TestWriterFloorPostgresSQLBoundsUniquenessAndHistoryImmutability(t *testing.T) {
	s, a, r := pgFloorFixture(t)

	first, err := a.Activate(t.Context(), pgFloorRequest())
	if err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{
		"UPDATE public.cp_writer_floor_history SET data=data",
		"DELETE FROM public.cp_writer_floor_history",
		"TRUNCATE public.cp_writer_floor_history CASCADE",
	} {
		if _, err = s.pg.NewRaw(query).Exec(t.Context()); err == nil {
			t.Fatal("history mutation accepted", query)
		}
	}

	for _, data := range [][]byte{nil, bytes.Repeat([]byte("x"), managed.MaxWriterFloor+1)} {
		if _, err = s.pg.NewRaw("UPDATE public.cp_writer_floor_current SET data=$1", data).Exec(t.Context()); err == nil {
			t.Fatal("SQL payload bound bypass")
		}
	}

	for _, query := range []string{
		"INSERT INTO public.cp_writer_floor_history SELECT authority_id,$1,epoch,minimum_protocol,digest,data FROM public.cp_writer_floor_history",
		"INSERT INTO public.cp_writer_floor_history SELECT authority_id,activation_id,epoch+1,minimum_protocol,digest,data FROM public.cp_writer_floor_history WHERE activation_id=$1",
	} {
		activation := id.New(managed.PrefixOperation).String()
		if strings.Contains(query, "WHERE activation_id") {
			activation = first.ActivationID.String()
		}

		if _, err = s.pg.NewRaw(query, activation).Exec(t.Context()); err == nil {
			t.Fatal("history uniqueness bypass")
		}
	}

	got, err := r.ReadManagedWriterFloor(t.Context())
	if err != nil || got != first {
		t.Fatal(got, err)
	}
	// Owner-only corruption proves oversized bytes are withheld before scanning Data.
	pgFloorExec(t, s, "ALTER TABLE public.cp_writer_floor_current DROP CONSTRAINT cp_writer_floor_current_size")
	pgFloorExec(t, s, "UPDATE public.cp_writer_floor_current SET data=$1", bytes.Repeat([]byte("x"), managed.MaxWriterFloor+1))

	var row writerFloorRow
	if err = s.pg.NewRaw("SELECT "+writerFloorColumns+" FROM public.cp_writer_floor_current").Scan(t.Context(), &row); err != nil {
		t.Fatal(err)
	}

	if row.Size != managed.MaxWriterFloor+1 || len(row.Data) != 0 {
		t.Fatal("oversized bytes allocated", row.Size, len(row.Data))
	}

	if _, err = r.ReadManagedWriterFloor(t.Context()); !errors.Is(err, managed.ErrWriterFloorCorrupt) {
		t.Fatal(err)
	}
}

func TestWriterFloorPostgresSnapshotLockAndDisabledHeldCheck(t *testing.T) {
	s, a, _ := pgFloorFixture(t)
	if _, err := a.Activate(t.Context(), pgFloorRequestAboveCeiling()); err != nil {
		t.Fatal(err)
	}

	err := a.backend.WriterFloorSnapshot(t.Context(), func(snapshot managedstate.WriterFloorSnapshot) error {
		other, beginErr := s.pg.BeginTxQuery(t.Context(), &driver.TxOptions{IsolationLevel: driver.LevelReadCommitted})
		if beginErr != nil {
			return beginErr
		}
		defer func() { _ = other.Rollback() }()

		if _, execErr := other.NewRaw("SET LOCAL lock_timeout = '50ms'").Exec(t.Context()); execErr != nil {
			return execErr
		}

		lockErr := writerFloorLock(t.Context(), other, "floor-test", true)

		var state interface{ SQLState() string }
		if !errors.As(lockErr, &state) || state.SQLState() != "55P03" {
			t.Fatalf("shared snapshot did not block activation: %v", lockErr)
		}

		return managedstate.CheckWriterFloorMutation(t.Context(), snapshot, "floor-test")
	})
	if !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal("above-ceiling floor allowed mutation", err)
	}

	tx, err := s.pg.BeginTxQuery(t.Context(), &driver.TxOptions{IsolationLevel: driver.LevelReadCommitted})
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = tx.Rollback() }()

	held := managedControllerTransaction{managedBackend: managedBackend{store: s, tx: tx}}
	if err = held.checkWriterFloorMutationHeld(t.Context(), "floor-test"); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestWriterFloorPostgresNilHandleRefused(t *testing.T) {
	if _, err := NewWriterFloorReader(nil, "floor-test"); !errors.Is(err, managed.ErrInvalid) {
		t.Fatal(err)
	}

	if _, err := NewWriterFloorAdmin(nil, "floor-test"); !errors.Is(err, managed.ErrInvalid) {
		t.Fatal(err)
	}

	var admin *WriterFloorAdmin
	if _, err := admin.Activate(t.Context(), pgFloorRequest()); !errors.Is(err, managed.ErrAuthority) {
		t.Fatal(err)
	}
}

func TestWriterFloorPostgresCurrentRollbackCannotReuseEpoch(t *testing.T) {
	s, a, _ := pgFloorFixture(t)

	first, err := a.Activate(t.Context(), pgFloorRequest())
	if err != nil {
		t.Fatal(err)
	}

	second, err := a.Activate(t.Context(), pgFloorNext(first))
	if err != nil {
		t.Fatal(err)
	}

	pgFloorExec(t, s, `UPDATE public.cp_writer_floor_current c SET activation_id=h.activation_id,epoch=h.epoch,minimum_protocol=h.minimum_protocol,digest=h.digest,data=h.data FROM public.cp_writer_floor_history h WHERE c.authority_id=h.authority_id AND h.activation_id=$1`, first.ActivationID.String())

	if _, err = a.Activate(t.Context(), second.Activation()); !errors.Is(err, managed.ErrWriterFloorCorrupt) {
		t.Fatal(err)
	}

	if _, err = a.Activate(t.Context(), pgFloorNext(first)); !errors.Is(err, managed.ErrConflict) {
		t.Fatal(err)
	}

	current, history := pgFloorCounts(t, s)
	if current != 1 || history != 2 {
		t.Fatal("rollback attempted to rewrite history", current, history)
	}
}
