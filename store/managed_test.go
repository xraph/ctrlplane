package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/store/memory"
	"github.com/xraph/ctrlplane/store/postgres"
)

func openManagedPG(t *testing.T, dsn string) *postgres.Store {
	t.Helper()

	d := pgdriver.New()
	if err := d.Open(t.Context(), dsn); err != nil {
		t.Fatal(err)
	}

	db, err := grove.Open(d)
	if err != nil {
		t.Fatal(err)
	}

	s := postgres.New(db)

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})

	return s
}
func managedPG(t *testing.T) (*postgres.Store, string) {
	t.Helper()

	dsn := os.Getenv("CTRLPLANE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("CTRLPLANE_TEST_POSTGRES_DSN unset")
	}

	root := openManagedPG(t, dsn)

	name := "managed_" + id.New(id.PrefixTenant).String()
	if _, err := pgdriver.Unwrap(root.DB()).Exec(t.Context(), "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if _, err := pgdriver.Unwrap(root.DB()).Exec(context.Background(), "DROP DATABASE "+name); err != nil {
			t.Error(err)
		}
	})

	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}

	parsed.Path = "/" + name

	s := openManagedPG(t, parsed.String())
	if err = s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}

	return s, parsed.String()
}
func managedIdentity() managed.Identity {
	iid := id.New(id.PrefixInstance)
	spec := []byte(`{"image":"sha256:fixture"}`)

	return managed.Identity{ProvisionOperationID: id.New(managed.PrefixOperation), Key: managed.Key{TenantID: id.New(id.PrefixTenant).String(), InstanceID: iid}, Physical: managed.Physical{Provider: "docker", Placement: "daemon/configuration-digest", Project: "cp-" + iid.String(), Generation: iid.String()}, Manifest: managed.Manifest{Version: 1, Spec: spec, SpecDigest: managed.Digest(spec), Members: []managed.Member{{Name: "a", Service: "api", Installation: "installation", Namespace: "namespace", Queue: "queue", Build: "v1", ArtifactDigest: managed.Digest([]byte("image")), ConfigurationDigest: managed.Digest([]byte("config"))}, {Name: "b", Service: "sidecar", Installation: "installation", Namespace: "other", Queue: "queue", Build: "v2", ArtifactDigest: managed.Digest([]byte("image2")), ConfigurationDigest: managed.Digest([]byte("config2"))}}}}
}

type managedFixture struct {
	t *testing.T
	s managed.Store
	k managed.Key
}

func newManagedFixture(t *testing.T, s managed.Store) *managedFixture {
	t.Helper()

	i := managedIdentity()
	if _, err := s.CreateManagedTarget(t.Context(), i); err != nil {
		t.Fatal(err)
	}

	f := &managedFixture{t: t, s: s, k: i.Key}
	f.must(s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: f.command(), Duration: managed.MaxLease}))
	f.must(s.IssueManagedProvision(t.Context(), f.command()))
	f.must(s.BindManagedResources(t.Context(), managed.ResourcesRequest{Command: f.command(), OperationID: i.ProvisionOperationID, Receipt: []byte("provision-settled"), Resources: map[string]string{"network": "network-id", "api": "container-id"}}))

	return f
}
func (f *managedFixture) target() *managed.Target {
	f.t.Helper()

	v, err := f.s.ReadManagedTarget(f.t.Context(), f.k)
	if err != nil {
		f.t.Fatal(err)
	}

	return v
}
func (f *managedFixture) command() managed.Command {
	f.t.Helper()
	v := f.target()

	return managed.Command{Key: f.k, ID: id.New(managed.PrefixOperation), Revision: v.Revision, LeaseEpoch: v.LeaseEpoch, Owner: "controller"}
}
func (f *managedFixture) must(r managed.Receipt, err error) managed.Receipt {
	f.t.Helper()

	if err != nil {
		f.t.Fatal(err)
	}

	return r
}
func (f *managedFixture) registration(member, runtime string) managed.Registration {
	f.t.Helper()

	body := []byte(runtime + member)

	return managed.Registration{ID: id.New(managed.PrefixReservation), Member: member, RuntimeID: runtime, InstanceID: f.k.InstanceID.String(), HostID: "host", RequestID: id.New(managed.PrefixOperation).String(), Command: body, CommandDigest: managed.Digest(body)}
}
func (f *managedFixture) reserve(member, runtime string) managed.Registration {
	f.t.Helper()
	v := f.registration(member, runtime)
	f.must(f.s.ReserveManagedRegistration(f.t.Context(), managed.ReserveRequest{Command: f.command(), Registration: v}))

	return v
}
func (f *managedFixture) accepted(member, runtime string) managed.Registration {
	f.t.Helper()
	v := f.reserve(member, runtime)
	f.must(f.s.IssueManagedRegistration(f.t.Context(), managed.ReservationRequest{Command: f.command(), ReservationID: v.ID}))
	f.must(f.s.AcceptManagedRegistration(f.t.Context(), managed.AcceptanceRequest{ReservationRequest: managed.ReservationRequest{Command: f.command(), ReservationID: v.ID}, Receipt: []byte("accepted-" + member)}))

	return v
}
func (f *managedFixture) closeBinding(v managed.Registration) {
	f.t.Helper()

	proof := []byte("fence-" + v.Member)
	f.must(f.s.RecordManagedFence(f.t.Context(), managed.FenceRequest{ReservationRequest: managed.ReservationRequest{Command: f.command(), ReservationID: v.ID}, Fence: managed.Fence{CompatibilityEvidence: []byte("retirement-and-writer-floor"), Epoch: 1, Evidence: proof, Digest: managed.Digest(proof), ValidUntil: time.Now().Add(time.Hour), Survivors: []string{"off-instance"}}}))

	d := managed.Drain{OperationID: "drain-" + v.RuntimeID, Deadline: time.Now().Add(time.Hour), Evidence: []byte("drain-accept")}
	for _, old := range f.target().Reservations {
		if old.RuntimeID == v.RuntimeID && old.Drain != nil {
			return
		}
	}

	started := f.must(f.s.RecordManagedDrain(f.t.Context(), managed.DrainRequest{ReservationRequest: managed.ReservationRequest{Command: f.command(), ReservationID: v.ID}, Drain: d}))
	d.Complete = true
	d.Quiescent = true
	d.CompletedAt = started.AcceptedAt
	d.Evidence = []byte("drain-done")
	f.must(f.s.RecordManagedDrain(f.t.Context(), managed.DrainRequest{ReservationRequest: managed.ReservationRequest{Command: f.command(), ReservationID: v.ID}, Drain: d}))
}
func (f *managedFixture) admit() {
	f.t.Helper()
	f.must(f.s.AdmitManagedRemoval(f.t.Context(), managed.AdmissionRequest{Command: f.command(), OperationID: id.New(managed.PrefixOperation), Verifier: "provider-verifier"}))
}
func (f *managedFixture) settle(disposition managed.Disposition) {
	f.t.Helper()
	d := f.target().Deletion
	f.must(f.s.SettleManagedDeletion(f.t.Context(), managed.SettlementRequest{Command: f.command(), Settlement: managed.Settlement{OperationID: d.OperationID, FenceDigest: d.FenceDigest, Disposition: disposition, Verifier: d.Verifier, Evidence: []byte("provider-result"), SettledAt: d.IssuedAt}}))
}
func TestManagedStoreConformance(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s managed.Store
			if backend == "memory" {
				s = memory.New()
			} else {
				p, _ := managedPG(t)
				s = p
			}

			t.Run("ownership_copies_and_pages", func(t *testing.T) {
				i := managedIdentity()

				saved, err := s.CreateManagedTarget(t.Context(), i)
				if err != nil {
					t.Fatal(err)
				}

				i.Manifest.Spec[0] = 'x'
				saved.Manifest.Members[0].Build = "changed"

				got, err := s.ReadManagedTarget(t.Context(), i.Key)
				if err != nil || got.Manifest.Members[0].Build != "v1" || got.Manifest.Spec[0] != '{' {
					t.Fatalf("aliased: %v", err)
				}

				i.Manifest.SpecDigest = managed.Digest(i.Manifest.Spec)

				_, err = s.CreateManagedTarget(t.Context(), i)
				if !errors.Is(err, managed.ErrConflict) {
					t.Fatalf("changed identity: %v", err)
				}

				other := managedIdentity()

				other.Physical = got.Physical
				if _, err = s.CreateManagedTarget(t.Context(), other); !errors.Is(err, managed.ErrConflict) {
					t.Fatalf("duplicate physical identity: %v", err)
				}

				foreign := got.Key

				foreign.TenantID = "foreign"
				if _, err = s.InspectManagedProtection(t.Context(), foreign); !errors.Is(err, managed.ErrAuthority) {
					t.Fatalf("foreign identity: %v", err)
				}

				absent := managedIdentity().Key
				if p, err := s.InspectManagedProtection(t.Context(), absent); err != nil || p.Protected {
					t.Fatalf("absence: %+v %v", p, err)
				}

				for range 4 {
					j := managedIdentity()

					j.TenantID = got.TenantID
					if _, err = s.CreateManagedTarget(t.Context(), j); err != nil {
						t.Fatal(err)
					}
				}

				cursor := ""
				seen := map[string]bool{}

				for {
					page, err := s.ListManagedTargets(t.Context(), managed.PageRequest{TenantID: got.TenantID, Limit: 2, Cursor: cursor})
					if err != nil {
						t.Fatal(err)
					}

					for _, v := range page.Items {
						if seen[v.InstanceID.String()] {
							t.Fatal("duplicate page item")
						}

						seen[v.InstanceID.String()] = true
					}

					cursor = page.NextCursor
					if cursor == "" {
						break
					}

					if _, err = s.ListManagedTargets(t.Context(), managed.PageRequest{TenantID: "foreign", Cursor: cursor}); !errors.Is(err, managed.ErrInvalid) {
						t.Fatal("cursor scope bypass")
					}
				}

				if len(seen) != 5 {
					t.Fatalf("pages lost records: %d", len(seen))
				}
			})
			t.Run("seal_race_and_unknown_obligation", func(t *testing.T) {
				f := newManagedFixture(t, s)
				v := f.reserve("a", "incarnation")
				c := f.command()
				seal := c
				seal.ID = id.New(managed.PrefixOperation)
				start := make(chan struct{})
				errs := make(chan error, 2)

				go func() {
					<-start

					_, err := s.IssueManagedRegistration(t.Context(), managed.ReservationRequest{Command: c, ReservationID: v.ID})
					errs <- err
				}()
				go func() { <-start; _, err := s.SealManagedTarget(t.Context(), seal); errs <- err }()

				close(start)

				a, b := <-errs, <-errs
				if (a == nil) == (b == nil) {
					t.Fatalf("one CAS winner required: %v %v", a, b)
				}

				st := f.target()
				if st.Phase == managed.Open {
					f.must(s.SealManagedTarget(t.Context(), f.command()))
				}

				if f.target().Reservations[0].Phase == managed.MayHaveIssued {
					f.must(s.ObserveManagedUnknown(t.Context(), managed.ReservationRequest{Command: f.command(), ReservationID: v.ID}))

					if _, err := s.AdmitManagedRemoval(t.Context(), managed.AdmissionRequest{Command: f.command(), OperationID: id.New(managed.PrefixOperation), Verifier: "verifier"}); !errors.Is(err, managed.ErrBlocked) {
						t.Fatalf("unknown admitted: %v", err)
					}

					f.must(s.AcceptManagedRegistration(t.Context(), managed.AcceptanceRequest{ReservationRequest: managed.ReservationRequest{Command: f.command(), ReservationID: v.ID}, Receipt: []byte("late-accepted")}))
				} else if _, err := s.IssueManagedRegistration(t.Context(), managed.ReservationRequest{Command: f.command(), ReservationID: v.ID}); !errors.Is(err, managed.ErrBlocked) {
					t.Fatalf("sealed issuance: %v", err)
				}
			})
			t.Run("retirement_tombstone_and_inert_replay", func(t *testing.T) {
				f := newManagedFixture(t, s)
				v := f.accepted("a", "incarnation")
				w := f.accepted("b", "incarnation")
				seal := f.command()
				receipt := f.must(s.SealManagedTarget(t.Context(), seal))
				f.closeBinding(v)

				if _, err := s.AdmitManagedRemoval(t.Context(), managed.AdmissionRequest{Command: f.command(), OperationID: id.New(managed.PrefixOperation), Verifier: "v"}); !errors.Is(err, managed.ErrBlocked) {
					t.Fatalf("partial admitted: %v", err)
				}

				f.closeBinding(w)
				f.admit()
				f.must(s.IssueManagedDeletion(t.Context(), f.command()))

				if _, err := s.RevokeManagedDeletion(t.Context(), f.command()); !errors.Is(err, managed.ErrBlocked) {
					t.Fatalf("issued relabeled unissued: %v", err)
				}

				f.settle(managed.Removed)

				for _, v := range []managed.Registration{v, w} {
					f.must(s.FinishManagedBinding(t.Context(), managed.AcceptanceRequest{ReservationRequest: managed.ReservationRequest{Command: f.command(), ReservationID: v.ID}, Receipt: []byte("finished-" + v.Member)}))
				}

				f.must(s.TombstoneManagedTarget(t.Context(), f.command()))

				replay := f.must(s.SealManagedTarget(t.Context(), seal))
				if !reflect.DeepEqual(receipt, replay) || f.target().Phase != managed.Deleted {
					t.Fatal("replay changed result/state")
				}

				changed := seal

				changed.Owner = "changed"
				if _, err := s.SealManagedTarget(t.Context(), changed); !errors.Is(err, managed.ErrConflict) {
					t.Fatalf("changed accepted replay: %v", err)
				}

				if p, err := s.InspectManagedProtection(t.Context(), f.k); err != nil || !p.Protected || p.Phase != managed.Deleted {
					t.Fatalf("tombstone protection: %+v %v", p, err)
				}

				if pending, err := s.HasManagedObligations(t.Context(), f.k.TenantID); err != nil || pending {
					t.Fatalf("completed obligations: %v %v", pending, err)
				}
			})
			t.Run("abort_and_takeover", func(t *testing.T) {
				f := newManagedFixture(t, s)
				v := f.accepted("a", "incarnation")
				f.must(s.SealManagedTarget(t.Context(), f.command()))
				f.closeBinding(v)
				f.admit()
				stale := f.command()
				lease := f.command()
				f.must(s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: lease, Duration: managed.MaxLease}))

				if _, err := s.IssueManagedDeletion(t.Context(), stale); !errors.Is(err, managed.ErrConflict) {
					t.Fatalf("stale writer: %v", err)
				}

				f.must(s.RevokeManagedDeletion(t.Context(), f.command()))
				d := f.target().Deletion
				proof := managed.AbortProof{FenceDigest: f.target().Reservations[0].Fence.Digest, VerifiedAt: d.Settlement.SettledAt, ValidUntil: time.Now().Add(time.Hour), Evidence: []byte("fresh-proof"), Receipt: []byte("abort-accepted")}
				q := managed.AbortRequest{ReservationRequest: managed.ReservationRequest{Command: f.command(), ReservationID: v.ID}, Proof: proof}
				saved := f.must(s.AbortManagedBinding(t.Context(), q))
				f.must(s.SetManagedDesired(t.Context(), managed.DesiredRequest{Command: f.command(), Mode: managed.Active}))

				if _, err := s.ReserveManagedRegistration(t.Context(), managed.ReserveRequest{Command: f.command(), Registration: f.registration("a", "new-runtime")}); !errors.Is(err, managed.ErrBlocked) {
					t.Fatalf("abort reopened enrollment: %v", err)
				}

				replay := f.must(s.AbortManagedBinding(t.Context(), q))
				if !reflect.DeepEqual(saved, replay) {
					t.Fatal("abort replay changed")
				}
			})
			t.Run("capacity_closes_without_dropping_obligations", func(t *testing.T) {
				f := newManagedFixture(t, s)

				var bindings []managed.Registration
				for n := range managed.MaxReservations {
					bindings = append(bindings, f.accepted("a", fmt.Sprintf("incarnation-%d", n)))
				}

				if _, err := s.ReserveManagedRegistration(t.Context(), managed.ReserveRequest{Command: f.command(), Registration: f.registration("a", "overflow")}); !errors.Is(err, managed.ErrCapacity) {
					t.Fatalf("capacity: %v", err)
				}

				f.must(s.SealManagedTarget(t.Context(), f.command()))

				for _, v := range bindings {
					f.closeBinding(v)
				}

				f.admit()
				f.must(s.IssueManagedDeletion(t.Context(), f.command()))
				f.settle(managed.Removed)

				for _, v := range bindings {
					f.must(s.FinishManagedBinding(t.Context(), managed.AcceptanceRequest{ReservationRequest: managed.ReservationRequest{Command: f.command(), ReservationID: v.ID}, Receipt: []byte("finished")}))
				}

				f.must(s.TombstoneManagedTarget(t.Context(), f.command()))

				if len(f.target().Reservations) != managed.MaxReservations {
					t.Fatal("obligations dropped")
				}
			})
		})
	}
}

func TestManagedPostgresAtomicRollbackAndConnections(t *testing.T) {
	s, dsn := managedPG(t)
	peer := openManagedPG(t, dsn)
	f := newManagedFixture(t, s)
	c := f.command()
	other := c
	other.ID = id.New(managed.PrefixOperation)
	start := make(chan struct{})

	var wg sync.WaitGroup

	errs := make(chan error, 2)

	for n, store := range []managed.Store{s, peer} {
		wg.Go(func() {
			<-start

			command := c
			if n == 1 {
				command = other
			}

			_, err := store.SetManagedDesired(t.Context(), managed.DesiredRequest{Command: command, Mode: managed.Paused})
			errs <- err
		})
	}

	close(start)
	wg.Wait()

	a, b := <-errs, <-errs
	if (a == nil) == (b == nil) {
		t.Fatalf("independent connections lost CAS: %v %v", a, b)
	}

	provisionIdentity := managedIdentity()
	if _, err := s.CreateManagedTarget(t.Context(), provisionIdentity); err != nil {
		t.Fatal(err)
	}

	provision := &managedFixture{t: t, s: s, k: provisionIdentity.Key}
	provision.must(s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: provision.command(), Duration: managed.MaxLease}))
	provisionCommand := provision.command()
	provisionBefore := provision.target()

	localIdentity := managedIdentity()
	if _, err := s.CreateManagedTarget(t.Context(), localIdentity); err != nil {
		t.Fatal(err)
	}

	local := &managedFixture{t: t, s: s, k: localIdentity.Key}
	local.must(s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: local.command(), Duration: managed.MaxLease}))
	local.must(s.SealManagedTarget(t.Context(), local.command()))
	localCommand := local.command()
	localBefore := local.target()
	pg := pgdriver.Unwrap(s.DB())

	_, err := pg.Exec(t.Context(), `CREATE FUNCTION reject_managed_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected receipt failure'; END $$;
 CREATE TRIGGER reject_receipt BEFORE INSERT ON cp_managed_commands FOR EACH ROW EXECUTE FUNCTION reject_managed_receipt()`)
	if err != nil {
		t.Fatal(err)
	}

	before := f.target()
	command := f.command()

	_, err = s.SetManagedDesired(t.Context(), managed.DesiredRequest{Command: command, Mode: managed.Retiring})
	if err == nil {
		t.Fatal("trigger did not refuse")
	}

	after := f.target()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("parent committed without receipt")
	}

	if _, err = s.IssueManagedProvision(t.Context(), provisionCommand); err == nil {
		t.Fatal("provision issue committed without receipt")
	}

	if !reflect.DeepEqual(provisionBefore, provision.target()) {
		t.Fatal("provision issue escaped rollback")
	}

	if _, err = s.TombstoneManagedUnissuedTarget(t.Context(), localCommand); err == nil {
		t.Fatal("local tombstone committed without receipt")
	}

	if !reflect.DeepEqual(localBefore, local.target()) {
		t.Fatal("local tombstone escaped rollback")
	}

	if _, err = pg.Exec(t.Context(), "DROP TRIGGER reject_receipt ON cp_managed_commands"); err != nil {
		t.Fatal(err)
	}

	f.must(s.SetManagedDesired(t.Context(), managed.DesiredRequest{Command: command, Mode: managed.Retiring}))
	provision.must(s.IssueManagedProvision(t.Context(), provisionCommand))
	local.must(s.TombstoneManagedUnissuedTarget(t.Context(), localCommand))

	fresh := openManagedPG(t, dsn)
	if p, err := fresh.InspectManagedProtection(t.Context(), f.k); err != nil || !p.Protected {
		t.Fatalf("reconnect lost protection: %+v %v", p, err)
	}
	// The managed target has no mutable instance or tenant row. Its authority survives both absences.
	if _, err = pg.Exec(t.Context(), "DROP TABLE cp_managed_commands"); err != nil {
		t.Fatal(err)
	}

	if _, err = s.SetManagedDesired(t.Context(), managed.DesiredRequest{Command: f.command(), Mode: managed.Active}); err == nil {
		t.Fatal("missing receipt schema accepted")
	}

	if _, err = pg.Exec(t.Context(), "DROP TABLE cp_managed_targets"); err != nil {
		t.Fatal(err)
	}

	if _, err = s.InspectManagedProtection(t.Context(), f.k); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatalf("missing schema treated as absence: %v", err)
	}
}

func TestManagedProtectionUnsupported(t *testing.T) {
	if _, err := managed.RequireProtection(struct{}{}); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal(err)
	}
}

// TestManagedPostgresRestart uses the same fixture database before and after an external restart.
func TestManagedPostgresRestart(t *testing.T) {
	phase := os.Getenv("CTRLPLANE_MANAGED_RESTART")
	path := os.Getenv("CTRLPLANE_MANAGED_RESTART_STATE")

	if phase == "" {
		t.Skip("external restart phase unset")
	}

	if path == "" {
		t.Fatal("restart state path required")
	}

	dsn := os.Getenv("CTRLPLANE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("restart DSN required")
	}

	s := openManagedPG(t, dsn)

	type persisted struct {
		Key     managed.Key            `json:"key"`
		Command managed.ReserveRequest `json:"command"`
		Receipt managed.Receipt        `json:"receipt"`
	}

	if phase == "seed" {
		if err := s.Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}

		f := newManagedFixture(t, s)
		q := managed.ReserveRequest{Command: f.command(), Registration: f.registration("a", "restart-incarnation")}
		receipt := f.must(s.ReserveManagedRegistration(t.Context(), q))
		f.must(s.IssueManagedRegistration(t.Context(), managed.ReservationRequest{Command: f.command(), ReservationID: q.Registration.ID}))
		f.must(s.SealManagedTarget(t.Context(), f.command()))

		b, err := json.Marshal(persisted{f.k, q, receipt})
		if err != nil {
			t.Fatal(err)
		}

		if err = os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}

		return
	}

	if phase != "verify" {
		t.Fatal("invalid restart phase")
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var p persisted
	if err = json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}

	st, err := s.ReadManagedTarget(t.Context(), p.Key)
	if err != nil || st.Phase != managed.Sealed || st.Reservations[0].Phase != managed.MayHaveIssued {
		t.Fatalf("restart obligation: %+v %v", st, err)
	}

	got, err := s.ReserveManagedRegistration(t.Context(), p.Command)
	if err != nil || !reflect.DeepEqual(got, p.Receipt) {
		t.Fatalf("restart replay: %+v %v", got, err)
	}
}

func TestManagedProvisionSealObligation(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s managed.Store
			if backend == "memory" {
				s = memory.New()
			} else {
				p, _ := managedPG(t)
				s = p
			}

			for _, issued := range []bool{false, true} {
				i := managedIdentity()
				if _, err := s.CreateManagedTarget(t.Context(), i); err != nil {
					t.Fatal(err)
				}

				f := &managedFixture{t: t, s: s, k: i.Key}
				f.must(s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: f.command(), Duration: managed.MaxLease}))

				q := f.command()
				if issued {
					f.must(s.IssueManagedProvision(t.Context(), q))
				}

				f.must(s.SealManagedTarget(t.Context(), f.command()))

				if !issued {
					if _, err := s.IssueManagedProvision(t.Context(), q); !errors.Is(err, managed.ErrConflict) {
						t.Fatalf("stale provision permission: %v", err)
					}

					if _, err := s.IssueManagedProvision(t.Context(), f.command()); !errors.Is(err, managed.ErrBlocked) {
						t.Fatalf("sealed provision: %v", err)
					}

					f.admit()

					continue
				}

				if _, err := s.AdmitManagedRemoval(t.Context(), managed.AdmissionRequest{Command: f.command(), OperationID: id.New(managed.PrefixOperation), Verifier: "verifier"}); !errors.Is(err, managed.ErrBlocked) {
					t.Fatalf("in-flight creation admitted: %v", err)
				}

				f.must(s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: f.command(), Duration: managed.MaxLease}))
				f.must(s.IssueManagedProvision(t.Context(), q)) // Immutable receipt recovery is not another issue permission.
				f.must(s.BindManagedResources(t.Context(), managed.ResourcesRequest{Command: f.command(), OperationID: i.ProvisionOperationID, Resources: map[string]string{"network": "late-network", "api": "late-container", "init": "late-init"}, Receipt: []byte("exact-create-settled")}))
				f.admit()
			}
		})
	}
}

func TestManagedLateAcceptanceAndTerminalDrain(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s managed.Store
			if backend == "memory" {
				s = memory.New()
			} else {
				p, _ := managedPG(t)
				s = p
			}

			f := newManagedFixture(t, s)
			v := f.reserve("a", "late-runtime")
			issued := managed.ReservationRequest{Command: f.command(), ReservationID: v.ID}
			f.must(s.IssueManagedRegistration(t.Context(), issued))
			f.must(s.SealManagedTarget(t.Context(), f.command()))
			f.must(s.ObserveManagedUnknown(t.Context(), managed.ReservationRequest{Command: f.command(), ReservationID: v.ID}))

			for range 2 {
				if _, err := s.AdmitManagedRemoval(t.Context(), managed.AdmissionRequest{Command: f.command(), OperationID: id.New(managed.PrefixOperation), Verifier: "verifier"}); !errors.Is(err, managed.ErrBlocked) {
					t.Fatalf("unknown admitted: %v", err)
				}
			}

			accept := managed.AcceptanceRequest{ReservationRequest: managed.ReservationRequest{Command: f.command(), ReservationID: v.ID}, Receipt: []byte("original-evidence")}
			accepted := f.must(s.AcceptManagedRegistration(t.Context(), accept))
			f.must(s.SetManagedDesired(t.Context(), managed.DesiredRequest{Command: f.command(), Mode: managed.Paused}))

			replay := f.must(s.AcceptManagedRegistration(t.Context(), accept))
			if !reflect.DeepEqual(accepted, replay) {
				t.Fatal("accepted reply drifted")
			}

			accept.Receipt[0] = 'X'
			if _, err := s.AcceptManagedRegistration(t.Context(), accept); !errors.Is(err, managed.ErrConflict) {
				t.Fatalf("changed receipt accepted: %v", err)
			}

			if f.target().Reservations[0].Receipt[0] != 'o' {
				t.Fatal("receipt aliases caller")
			}

			d := managed.Drain{OperationID: "original-drain", Deadline: time.Now().Add(time.Hour), Evidence: []byte("accepted-drain")}
			started := f.must(s.RecordManagedDrain(t.Context(), managed.DrainRequest{ReservationRequest: managed.ReservationRequest{Command: f.command(), ReservationID: v.ID}, Drain: d}))
			original := d

			d.Deadline = d.Deadline.Add(time.Hour)
			if _, err := s.RecordManagedDrain(t.Context(), managed.DrainRequest{ReservationRequest: managed.ReservationRequest{Command: f.command(), ReservationID: v.ID}, Drain: d}); !errors.Is(err, managed.ErrConflict) {
				t.Fatalf("deadline refreshed: %v", err)
			}

			d = original
			d.Complete = true
			d.CompletedAt = started.AcceptedAt
			d.Evidence = []byte("incomplete-terminal-result")
			f.must(s.RecordManagedDrain(t.Context(), managed.DrainRequest{ReservationRequest: managed.ReservationRequest{Command: f.command(), ReservationID: v.ID}, Drain: d}))

			d.Quiescent = true
			if _, err := s.RecordManagedDrain(t.Context(), managed.DrainRequest{ReservationRequest: managed.ReservationRequest{Command: f.command(), ReservationID: v.ID}, Drain: d}); !errors.Is(err, managed.ErrConflict) {
				t.Fatalf("terminal result changed: %v", err)
			}

			replacement := managedIdentity()

			replacement.TenantID = f.k.TenantID
			if _, err := s.AllocateManagedReplacement(t.Context(), managed.ReplacementRequest{Command: f.command(), Replacement: replacement}); !errors.Is(err, managed.ErrBlocked) {
				t.Fatalf("paused replacement: %v", err)
			}

			f.must(s.SetManagedDesired(t.Context(), managed.DesiredRequest{Command: f.command(), Mode: managed.Active}))
			allocation := managed.ReplacementRequest{Command: f.command(), Replacement: replacement}
			f.must(s.AllocateManagedReplacement(t.Context(), allocation))

			if f.target().ReplacementOperationID != allocation.ID {
				t.Fatal("replacement operation identity missing")
			}

			if pending, err := s.HasManagedObligations(t.Context(), f.k.TenantID); err != nil || !pending {
				t.Fatalf("orphaned obligation lost: %v %v", pending, err)
			}

			cursor := ""
			found := false

			for {
				page, err := s.ListManagedControllerTargets(t.Context(), managed.ControllerPageRequest{Cursor: cursor, Limit: 1})
				if err != nil {
					t.Fatal(err)
				}

				for _, v := range page.Items {
					if v.Key == f.k {
						found = true
					}
				}

				cursor = page.NextCursor
				if cursor == "" {
					break
				}
			}

			if !found {
				t.Fatal("controller enumeration needs absent tenant metadata")
			}
		})
	}
}

func TestManagedPostgresPhysicalOwnershipRace(t *testing.T) {
	s, dsn := managedPG(t)
	peer := openManagedPG(t, dsn)
	a, b := managedIdentity(), managedIdentity()
	b.Physical = a.Physical
	start := make(chan struct{})
	errs := make(chan error, 2)

	go func() { <-start; _, err := s.CreateManagedTarget(t.Context(), a); errs <- err }()
	go func() { <-start; _, err := peer.CreateManagedTarget(t.Context(), b); errs <- err }()

	close(start)

	first, second := <-errs, <-errs
	if first == nil {
		first, second = second, first
	}

	if !errors.Is(first, managed.ErrConflict) || second != nil {
		t.Fatalf("unique ownership race: %v %v", first, second)
	}
}

func TestManagedProvisionSealRace(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s, peer managed.Store
			if backend == "memory" {
				s = memory.New()
				peer = s
			} else {
				p, dsn := managedPG(t)
				s = p
				peer = openManagedPG(t, dsn)
			}

			i := managedIdentity()
			if _, err := s.CreateManagedTarget(t.Context(), i); err != nil {
				t.Fatal(err)
			}

			f := &managedFixture{t: t, s: s, k: i.Key}
			f.must(s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: f.command(), Duration: managed.MaxLease}))
			issue, seal := f.command(), f.command()
			start := make(chan struct{})
			errs := make(chan error, 2)

			go func() { <-start; _, err := s.IssueManagedProvision(t.Context(), issue); errs <- err }()
			go func() { <-start; _, err := peer.SealManagedTarget(t.Context(), seal); errs <- err }()

			close(start)

			a, b := <-errs, <-errs
			if a == nil {
				a, b = b, a
			}

			if !errors.Is(a, managed.ErrConflict) || b != nil {
				t.Fatalf("provision/seal race: %v %v", a, b)
			}

			st := f.target()
			if st.Provision.Issued {
				f.must(s.SealManagedTarget(t.Context(), f.command()))

				if _, err := s.AdmitManagedRemoval(t.Context(), managed.AdmissionRequest{Command: f.command(), OperationID: id.New(managed.PrefixOperation), Verifier: "verifier"}); !errors.Is(err, managed.ErrBlocked) {
					t.Fatalf("issued create lost after seal: %v", err)
				}
			} else if st.Phase != managed.Sealed || !st.Provision.Revoked {
				t.Fatal("seal failed to revoke unissued provision")
			}
		})
	}
}

func TestManagedLocalUnissuedClosure(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s managed.Store
			if backend == "memory" {
				s = memory.New()
			} else {
				p, _ := managedPG(t)
				s = p
			}

			i := managedIdentity()
			if _, err := s.CreateManagedTarget(t.Context(), i); err != nil {
				t.Fatal(err)
			}

			f := &managedFixture{t: t, s: s, k: i.Key}
			f.must(s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: f.command(), Duration: managed.MaxLease}))

			stale := f.command()
			if _, err := s.TombstoneManagedUnissuedTarget(t.Context(), f.command()); !errors.Is(err, managed.ErrBlocked) {
				t.Fatalf("unsealed closure: %v", err)
			}

			f.must(s.SealManagedTarget(t.Context(), f.command()))
			closeCommand := f.command()

			receipt := f.must(s.TombstoneManagedUnissuedTarget(t.Context(), closeCommand))
			if got := f.must(s.TombstoneManagedUnissuedTarget(t.Context(), closeCommand)); !reflect.DeepEqual(got, receipt) {
				t.Fatal("local terminal replay changed")
			}

			changed := closeCommand

			changed.Owner = "other-controller"
			if _, err := s.TombstoneManagedUnissuedTarget(t.Context(), changed); !errors.Is(err, managed.ErrConflict) {
				t.Fatalf("changed local replay: %v", err)
			}

			if _, err := s.IssueManagedProvision(t.Context(), stale); !errors.Is(err, managed.ErrConflict) {
				t.Fatalf("late stale creation: %v", err)
			}

			if _, err := s.IssueManagedProvision(t.Context(), f.command()); !errors.Is(err, managed.ErrBlocked) {
				t.Fatalf("fresh creation after local terminal: %v", err)
			}

			st := f.target()
			if st.Phase != managed.Deleted || st.TerminalDisposition != managed.NoResourcesCreated || st.Deletion != nil || st.Provision.Issued || !st.Provision.Revoked {
				t.Fatalf("invalid local terminal: %+v", st)
			}

			if protection, err := s.InspectManagedProtection(t.Context(), f.k); err != nil || !protection.Protected {
				t.Fatalf("local tombstone unprotected: %+v %v", protection, err)
			}

			issued := newManagedFixture(t, s)
			issued.must(s.SealManagedTarget(t.Context(), issued.command()))

			if _, err := s.TombstoneManagedUnissuedTarget(t.Context(), issued.command()); !errors.Is(err, managed.ErrBlocked) {
				t.Fatalf("issued create treated as local revocation: %v", err)
			}
		})
	}
}
