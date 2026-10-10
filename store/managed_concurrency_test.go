package store_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/store/memory"
)

// managedPeers returns two independent PG clients or the same authoritative Memory store.
func managedPeers(t *testing.T, backend string) (managed.Store, managed.Store, func() time.Time) {
	t.Helper()

	if backend == "memory" {
		s := memory.New()

		return s, s, func() time.Time { return time.Now().UTC() }
	}

	s, dsn := managedPG(t)
	peer := openManagedPG(t, dsn)
	pg := pgdriver.Unwrap(peer.DB())

	var firstPID, secondPID int
	if err := pgdriver.Unwrap(s.DB()).NewRaw("SELECT pg_backend_pid()").Scan(t.Context(), &firstPID); err != nil {
		t.Fatal(err)
	}

	if err := pg.NewRaw("SELECT pg_backend_pid()").Scan(t.Context(), &secondPID); err != nil {
		t.Fatal(err)
	}

	if firstPID == secondPID {
		t.Fatal("takeover requires separate database connections")
	}

	t.Logf("separate PostgreSQL backends: %d and %d", firstPID, secondPID)

	return s, peer, func() time.Time {
		var micros int64
		if err := pg.NewRaw("SELECT (extract(epoch FROM clock_timestamp())*1000000)::bigint").Scan(t.Context(), &micros); err != nil {
			t.Fatal(err)
		}

		return time.UnixMicro(micros).UTC()
	}
}

func waitManagedExpiry(t *testing.T, clock func() time.Time, until time.Time) time.Time {
	t.Helper()

	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()

	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()

	for {
		if observed := clock(); observed.After(until) {
			return observed
		}

		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("authoritative lease expiry not observed")
		}
	}
}

func TestManagedDifferentOwnerTakeover(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s, peer, clock := managedPeers(t, backend)
			f := newManagedFixture(t, s)
			accepted := f.accepted("a", "runtime-a")
			drain := managed.Drain{OperationID: "original-drain", Deadline: clock().Add(time.Hour), Evidence: []byte("drain-accepted")}
			started := f.must(s.RecordManagedDrain(t.Context(), managed.DrainRequest{ReservationRequest: managed.ReservationRequest{Command: f.command(), ReservationID: accepted.ID}, Drain: drain}))
			pending := f.reserve("b", "runtime-b")
			issue := managed.ReservationRequest{Command: f.command(), ReservationID: pending.ID}
			issued := f.must(s.IssueManagedRegistration(t.Context(), issue))
			f.must(s.ObserveManagedUnknown(t.Context(), managed.ReservationRequest{Command: f.command(), ReservationID: pending.ID}))
			lease := f.must(s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: f.command(), Duration: 2 * time.Second}))
			before := f.target()
			stale := f.command()
			candidate := stale
			candidate.ID = id.New(managed.PrefixOperation)
			candidate.Owner = "controller-b"
			claim := managed.LeaseRequest{Command: candidate, Duration: managed.MaxLease}

			if observed := clock(); !observed.Before(lease.LeaseUntil) {
				t.Fatal("fixture missed the active lease window")
			}

			if _, err := peer.ClaimManagedLease(t.Context(), claim); !errors.Is(err, managed.ErrConflict) {
				t.Fatalf("owner B acquired active owner A lease: %v", err)
			}

			if !reflect.DeepEqual(before, f.target()) {
				t.Fatal("refused takeover changed obligations")
			}

			observed := waitManagedExpiry(t, clock, lease.LeaseUntil)
			taken := f.must(peer.ClaimManagedLease(t.Context(), claim))
			after := f.target()

			if after.LeaseOwner != candidate.Owner || after.LeaseEpoch != before.LeaseEpoch+1 || after.Revision != before.Revision+1 || !taken.AcceptedAt.After(lease.LeaseUntil) {
				t.Fatalf("invalid owner B takeover: %+v", taken)
			}

			t.Logf("owner A=%s owner B=%s expiry=%s observed=%s accepted=%s", stale.Owner, candidate.Owner, lease.LeaseUntil.Format(time.RFC3339Nano), observed.Format(time.RFC3339Nano), taken.AcceptedAt.Format(time.RFC3339Nano))

			preserved := *after
			preserved.LeaseOwner, preserved.LeaseEpoch, preserved.LeaseUntil, preserved.Revision = before.LeaseOwner, before.LeaseEpoch, before.LeaseUntil, before.Revision

			if !reflect.DeepEqual(before, &preserved) {
				t.Fatal("takeover changed physical identity, commands, deadlines or obligations")
			}

			if _, err := s.SealManagedTarget(t.Context(), stale); !errors.Is(err, managed.ErrConflict) {
				t.Fatalf("stale owner A mutation: %v", err)
			}

			if _, err := s.SealManagedTarget(t.Context(), f.command()); !errors.Is(err, managed.ErrConflict) {
				t.Fatalf("owner A bypassed ownership with fresh revision: %v", err)
			}

			if replay := f.must(peer.IssueManagedRegistration(t.Context(), issue)); !reflect.DeepEqual(replay, issued) {
				t.Fatal("owner A accepted receipt changed after takeover")
			}

			if !reflect.DeepEqual(after, f.target()) {
				t.Fatal("receipt recovery or stale mutation changed issued work")
			}

			commandB := func() managed.Command {
				command := f.command()
				command.Owner = candidate.Owner

				return command
			}

			f.must(peer.AcceptManagedRegistration(t.Context(), managed.AcceptanceRequest{ReservationRequest: managed.ReservationRequest{Command: commandB(), ReservationID: pending.ID}, Receipt: []byte("original-late-acceptance")}))

			drain.Complete, drain.Quiescent, drain.CompletedAt = true, true, started.AcceptedAt
			drain.Evidence = []byte("original-drain-completed")
			f.must(peer.RecordManagedDrain(t.Context(), managed.DrainRequest{ReservationRequest: managed.ReservationRequest{Command: commandB(), ReservationID: accepted.ID}, Drain: drain}))
			f.must(peer.SealManagedTarget(t.Context(), commandB()))

			for _, binding := range []managed.Registration{accepted, pending} {
				proof := []byte("fresh-fence-" + binding.Member)
				f.must(peer.RecordManagedFence(t.Context(), managed.FenceRequest{ReservationRequest: managed.ReservationRequest{Command: commandB(), ReservationID: binding.ID}, Fence: managed.Fence{CompatibilityEvidence: []byte("retirement-and-writer-floor"), Epoch: 1, Evidence: proof, Digest: managed.Digest(proof), ValidUntil: clock().Add(time.Hour), Survivors: []string{"off-instance"}}}))
			}

			pendingDrain := managed.Drain{OperationID: "pending-drain", Deadline: clock().Add(time.Hour), Evidence: []byte("drain-accepted")}
			pendingStarted := f.must(peer.RecordManagedDrain(t.Context(), managed.DrainRequest{ReservationRequest: managed.ReservationRequest{Command: commandB(), ReservationID: pending.ID}, Drain: pendingDrain}))
			pendingDrain.Complete, pendingDrain.Quiescent, pendingDrain.CompletedAt = true, true, pendingStarted.AcceptedAt
			f.must(peer.RecordManagedDrain(t.Context(), managed.DrainRequest{ReservationRequest: managed.ReservationRequest{Command: commandB(), ReservationID: pending.ID}, Drain: pendingDrain}))
			f.must(peer.AdmitManagedRemoval(t.Context(), managed.AdmissionRequest{Command: commandB(), OperationID: id.New(managed.PrefixOperation), Verifier: "verifier"}))
			final := f.target()

			if final.Phase != managed.Admitted || !reflect.DeepEqual(final.Identity, before.Identity) || len(final.Reservations) != 2 || !reflect.DeepEqual(final.Reservations[1].Registration, pending) || final.Reservations[1].Phase != managed.Accepted || !final.Reservations[0].Drain.Deadline.Equal(drain.Deadline) || final.Reservations[0].Drain.OperationID != drain.OperationID {
				t.Fatal("new owner did not reconcile the original complete obligation set")
			}
		})
	}
}

func TestManagedReservationSealRace(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s, peer, _ := managedPeers(t, backend)
			reserveWins, sealWins := 0, 0

			for attempt := range 8 {
				f := newManagedFixture(t, s)
				request := managed.ReserveRequest{Command: f.command(), Registration: f.registration("a", "racing-runtime")}
				seal := request.Command
				seal.ID = id.New(managed.PrefixOperation)
				start := make(chan struct{})
				reserveResult, sealResult := make(chan error, 1), make(chan error, 1)

				reserveCall := func() { <-start; _, err := s.ReserveManagedRegistration(t.Context(), request); reserveResult <- err }
				sealCall := func() { <-start; _, err := peer.SealManagedTarget(t.Context(), seal); sealResult <- err }

				if attempt%2 == 0 {
					go reserveCall()
					go sealCall()
				} else {
					go sealCall()
					go reserveCall()
				}

				close(start)

				reserveErr, sealErr := <-reserveResult, <-sealResult
				if reserveErr == nil {
					reserveWins++

					if !errors.Is(sealErr, managed.ErrConflict) {
						t.Fatalf("reserve winner needs seal CAS refusal: %v", sealErr)
					}

					f.must(peer.SealManagedTarget(t.Context(), f.command()))
					st := f.target()

					if st.Phase != managed.Sealed || len(st.Reservations) != 1 || st.Reservations[0].Phase != managed.Revoked || !reflect.DeepEqual(st.Reservations[0].Registration, request.Registration) {
						t.Fatal("winning reservation was dropped or remained issuable")
					}

					if _, err := s.IssueManagedRegistration(t.Context(), managed.ReservationRequest{Command: f.command(), ReservationID: request.Registration.ID}); !errors.Is(err, managed.ErrBlocked) {
						t.Fatalf("revoked reservation was issued: %v", err)
					}
				} else {
					sealWins++

					if sealErr != nil || !errors.Is(reserveErr, managed.ErrConflict) {
						t.Fatalf("seal winner needs reserve CAS refusal: reserve=%v seal=%v", reserveErr, sealErr)
					}

					st := f.target()
					if st.Phase != managed.Sealed || len(st.Reservations) != 0 {
						t.Fatal("reservation appended behind seal")
					}

					request.Command = f.command()
					if _, err := s.ReserveManagedRegistration(t.Context(), request); !errors.Is(err, managed.ErrBlocked) {
						t.Fatalf("fresh late reservation bypassed seal: %v", err)
					}

					if !reflect.DeepEqual(st, f.target()) {
						t.Fatal("late reservation changed sealed target")
					}
				}
			}

			t.Logf("barrier-started races: reserve won %d; seal won %d", reserveWins, sealWins)
		})
	}
}
