package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/store/postgres"
)

// releaseAfterStoreTime waits for an observed database lock and authoritative expiry.
func releaseAfterStoreTime(t *testing.T, s *postgres.Store, key managed.Key, until time.Time, call func() error) error {
	t.Helper()

	pg := pgdriver.Unwrap(s.DB())

	tx, err := pg.BeginTxQuery(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = tx.Rollback() }()

	var data []byte
	if err = tx.NewRaw("SELECT data FROM cp_managed_targets WHERE id=$1 FOR UPDATE", key.InstanceID.String()).Scan(t.Context(), &data); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- call() }()

	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()

	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()

	for {
		var blocked bool
		if err = pg.NewRaw(`SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'SELECT data FROM cp_managed_targets%')`).Scan(t.Context(), &blocked); err != nil {
			t.Fatal(err)
		}

		var micros int64
		if err = pg.NewRaw("SELECT (extract(epoch FROM clock_timestamp())*1000000)::bigint").Scan(t.Context(), &micros); err != nil {
			t.Fatal(err)
		}

		if blocked && time.UnixMicro(micros).After(until) {
			break
		}

		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("did not observe blocked transaction and store expiry")
		}
	}

	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}

	select {
	case err = <-done:
		return err
	case <-deadline.C:
		t.Fatal("blocked transition did not finish")

		return nil
	}
}
func TestManagedPostgresPostLockAuthority(t *testing.T) {
	s, dsn := managedPG(t)
	peer := openManagedPG(t, dsn)
	f := newManagedFixture(t, s)
	v := f.reserve("a", "incarnation")
	q := managed.LeaseRequest{Command: f.command(), Duration: 500 * time.Millisecond}
	r := f.must(s.ClaimManagedLease(t.Context(), q))
	issue := managed.ReservationRequest{Command: f.command(), ReservationID: v.ID}

	err := releaseAfterStoreTime(t, s, f.k, r.LeaseUntil, func() error {
		_, e := peer.IssueManagedRegistration(t.Context(), issue)

		return e
	})
	if !errors.Is(err, managed.ErrConflict) {
		t.Fatalf("expired lease accepted after lock: %v", err)
	}

	f.must(s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: f.command(), Duration: managed.MaxLease}))
	f.must(s.IssueManagedRegistration(t.Context(), managed.ReservationRequest{Command: f.command(), ReservationID: v.ID}))
	f.must(s.AcceptManagedRegistration(t.Context(), managed.AcceptanceRequest{ReservationRequest: managed.ReservationRequest{Command: f.command(), ReservationID: v.ID}, Receipt: []byte("accepted")}))
	f.must(s.SealManagedTarget(t.Context(), f.command()))
	f.closeBinding(v)
	f.admit()
	f.must(s.RevokeManagedDeletion(t.Context(), f.command()))
	st := f.target()
	proof := managed.AbortProof{FenceDigest: st.Reservations[0].Fence.Digest, VerifiedAt: st.Deletion.Settlement.SettledAt, ValidUntil: time.Now().Add(500 * time.Millisecond), Evidence: []byte("proof"), Receipt: []byte("abort")}
	abort := managed.AbortRequest{ReservationRequest: managed.ReservationRequest{Command: f.command(), ReservationID: v.ID}, Proof: proof}

	err = releaseAfterStoreTime(t, s, f.k, proof.ValidUntil, func() error {
		_, e := peer.AbortManagedBinding(t.Context(), abort)

		return e
	})
	if !errors.Is(err, managed.ErrInvalid) {
		t.Fatalf("expired proof accepted after lock: %v", err)
	}

	if f.target().Reservations[0].Abort != nil {
		t.Fatal("expired abort persisted")
	}
	// A wrong removal epoch/digest cannot replace the exact accepted fence.
	abort.Command = f.command()
	abort.Proof.ValidUntil = time.Now().Add(time.Hour)

	abort.Proof.FenceDigest = id.New(managed.PrefixOperation).String()
	if _, err = s.AbortManagedBinding(t.Context(), abort); !errors.Is(err, managed.ErrInvalid) {
		t.Fatalf("wrong fence accepted: %v", err)
	}
}
