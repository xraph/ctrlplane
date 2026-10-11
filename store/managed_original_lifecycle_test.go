package store_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/store/postgres"
)

func originalCommand(t *testing.T, s managed.Store, k managed.Key) managed.OriginalCommand {
	t.Helper()

	target, err := s.ReadManagedTarget(t.Context(), k)
	if err != nil {
		t.Fatal(err)
	}

	return managed.OriginalCommand{Key: k, ID: id.New(managed.PrefixOperation), Revision: managed.Decimal(strconv.FormatUint(target.Revision, 10)), LeaseEpoch: managed.Decimal(strconv.FormatUint(target.LeaseEpoch, 10)), Owner: target.LeaseOwner}
}

func originalSetup(t *testing.T, s managed.ControllerStore) (managed.OriginalRegistrationAcceptanceRequest, managed.OriginalRegistrationAcceptanceRequest) {
	t.Helper()
	identity := *controllerAction(t).NewTarget
	// Two members of the same process use one immutable startup descriptor and role.
	first := identity.Manifest.Members[0]
	identity.Manifest.Members[1] = first
	identity.Manifest.Members[1].Name = "sibling"
	identity.Manifest.Members[1].Service = "sidecar"

	spec, err := managed.ParseProvisionSpec(identity.Manifest)
	if err != nil {
		t.Fatal(err)
	}

	spec.Services[1].Startup = spec.Services[0].Startup

	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}

	identity.Manifest.Spec, err = json.Marshal(managed.ManagedSpec{Profile: "ctrlplane.managed.v1", RuntimeRoles: []managed.RuntimeRole{managed.Polling, managed.Polling}, Provision: raw})
	if err != nil {
		t.Fatal(err)
	}

	identity.Manifest.SpecDigest = managed.Digest(identity.Manifest.Spec)

	target, err := s.CreateManagedTarget(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: managed.Command{Key: identity.Key, ID: id.New(managed.PrefixOperation), Revision: target.Revision, Owner: "original-test"}, Duration: managed.MaxLease})
	if err != nil {
		t.Fatal(err)
	}

	provisionCommand := originalCommand(t, s, identity.Key)
	provisionCommand.ReservationID = id.New(managed.PrefixReservation)

	provisionLocal, err := provisionCommand.LocalCommand()
	if err != nil {
		t.Fatal(err)
	}

	if _, err = s.IssueManagedProvision(t.Context(), provisionLocal); err != nil {
		t.Fatal(err)
	}

	provisionCommand = originalCommand(t, s, identity.Key)
	provisionCommand.ReservationID = id.New(managed.PrefixReservation)

	provisionLocal, err = provisionCommand.LocalCommand()
	if err != nil {
		t.Fatal(err)
	}

	if _, err = s.BindManagedResources(t.Context(), managed.ResourcesRequest{Command: provisionLocal, OperationID: identity.ProvisionOperationID, Receipt: []byte("provisioned"), Resources: map[string]string{"network": "network", "api": "api", "sidecar": "sidecar"}}); err != nil {
		t.Fatal(err)
	}

	makeRequest := func(member string) managed.OriginalRegistrationAcceptanceRequest {
		c := originalCommand(t, s, identity.Key)
		c.ReservationID = id.New(managed.PrefixReservation)

		local, err := c.LocalCommand()
		if err != nil {
			t.Fatal(err)
		}

		registration := managed.Registration{ID: c.ReservationID, Member: member, RuntimeID: "runtime", InstanceID: "native-instance", HostID: "host", RequestID: "request-" + member, Command: []byte("stored-command-" + member)}

		registration.CommandDigest = managed.Digest(registration.Command)
		if _, err = s.ReserveManagedRegistration(t.Context(), managed.ReserveRequest{Command: local, Registration: registration}); err != nil {
			t.Fatal(err)
		}

		c = originalCommand(t, s, identity.Key)
		c.ReservationID = registration.ID

		local, err = c.LocalCommand()
		if err != nil {
			t.Fatal(err)
		}

		if _, err = s.IssueManagedRegistration(t.Context(), managed.ReservationRequest{Command: local, ReservationID: registration.ID}); err != nil {
			t.Fatal(err)
		}

		c = originalCommand(t, s, identity.Key)
		c.ReservationID = registration.ID
		accepted := time.Now().UTC().Add(-time.Second)
		receipt := []byte("synthetic full native export storage fixture")
		process := managed.OriginalProcessFacts{Installation: first.Installation, Namespace: first.Namespace, Build: first.Build, Queue: first.Queue, RuntimeID: registration.RuntimeID, InstanceID: registration.InstanceID, HostID: registration.HostID, IdentityVersion: "1", ArtifactDigest: first.ArtifactDigest, ConfigurationDigest: first.ConfigurationDigest, ConfigurationVersion: "config-v1", EnrollmentEvidenceDigest: managed.Digest([]byte("enrollment")), ProbePolicyID: "policy", ProbePolicyVersion: "1", MaximumProofValidityNS: "60000000000", VerifierID: "native-verifier", Descriptor: spec.Services[0].Startup, ProducerVerifier: "trusted-descriptor-verifier"}

		return managed.OriginalRegistrationAcceptanceRequest{Command: c, Facts: managed.OriginalRegistrationFacts{Process: process, Native: managed.OriginalNativeAcceptance{Operation: "query_runtime.register", RequestID: registration.RequestID, RequestDigest: managed.Digest([]byte("native-request")), CommandDigest: managed.Digest([]byte("operator-command")), ResponseVersion: "1", DeliveryID: "delivery-" + member, AcceptedAt: accepted}, Member: member, Role: managed.Polling, StoredCommandDigest: registration.CommandDigest, BindingVersion: "1", BindingState: "active", RemovalEpoch: "0", CreatedAt: accepted, ChangedAt: accepted}, NativeReceipt: receipt, NativeReceiptDigest: managed.Digest(receipt)}
	}
	a := makeRequest(first.Name)
	b := makeRequest("sibling")
	reservationID := a.Command.ReservationID
	a.Command = originalCommand(t, s, identity.Key)
	a.Command.ReservationID = reservationID

	return a, b
}

func originalDrain(t *testing.T, s managed.ControllerStore, a managed.OriginalRegistrationAcceptanceRequest) managed.OriginalDrainAcceptanceRequest {
	t.Helper()
	c := originalCommand(t, s, a.Command.Key)
	c.ReservationID = a.Command.ReservationID
	accepted := time.Now().UTC().Add(-time.Second)
	raw := []byte("synthetic full native drain export")

	return managed.OriginalDrainAcceptanceRequest{Command: c, Facts: managed.OriginalDrainFacts{Process: a.Facts.Process, Native: managed.OriginalNativeAcceptance{Operation: "worker.drain.request", RequestID: "drain-request", RequestDigest: managed.Digest([]byte("drain-native-request")), CommandDigest: managed.Digest([]byte("drain-operator-command")), ResponseVersion: "1", DeliveryID: "drain-delivery", AcceptedAt: accepted}, OperationID: "drain-operation", Deadline: time.Now().UTC().Add(time.Minute), ProcessAccepted: true, AdmissionClosed: true, ObservedAt: accepted}, NativeReceipt: raw, NativeReceiptDigest: managed.Digest(raw)}
}

func acceptOriginalPair(t *testing.T, s managed.ControllerStore) (managed.OriginalRegistrationAcceptanceRequest, managed.OriginalRegistrationAcceptanceRequest) {
	t.Helper()

	a, b := originalSetup(t, s)
	if _, err := s.AcceptManagedOriginalRegistration(t.Context(), a); err != nil {
		t.Fatal(err)
	}

	rid := b.Command.ReservationID
	b.Command = originalCommand(t, s, b.Command.Key)

	b.Command.ReservationID = rid
	if _, err := s.AcceptManagedOriginalRegistration(t.Context(), b); err != nil {
		t.Fatal(err)
	}

	return a, b
}

func TestManagedOriginalLifecycle(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := controllerStore(t, backend)
			a, b := acceptOriginalPair(t, s)
			d := originalDrain(t, s, a)

			receipt, err := s.RecordManagedOriginalDrain(t.Context(), d)
			if err != nil {
				t.Fatal(err)
			}

			key := managed.OriginalLifecycleKey{Key: a.Command.Key, Kind: managed.OriginalDrain, OperationID: d.Command.ID}

			record, err := s.ReadManagedOriginalLifecycle(t.Context(), key)
			if err != nil {
				t.Fatal(err)
			}

			if record.Receipt != managed.OriginalReceipt(receipt) {
				t.Fatal("different local receipt")
			}

			target, err := s.ReadManagedTarget(t.Context(), a.Command.Key)
			if err != nil {
				t.Fatal(err)
			}

			for _, v := range target.Reservations {
				if v.Drain == nil || v.Drain.Complete || v.Drain.Quiescent || !v.Drain.CompletedAt.IsZero() {
					t.Fatal("acceptance became terminal")
				}
			}

			record.Material.NativeReceipt[0] ^= 1

			again, err := s.ReadManagedOriginalLifecycle(t.Context(), key)
			if err != nil || !bytes.Equal(again.Material.NativeReceipt, d.NativeReceipt) {
				t.Fatal("read was not defensive", err)
			}

			successor := originalCommand(t, s, a.Command.Key)
			successor.ReservationID = a.Command.ReservationID

			leaseCommand, e := successor.LocalCommand()
			if e != nil {
				t.Fatal(e)
			}

			if _, e = s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: leaseCommand, Duration: time.Nanosecond}); e != nil {
				t.Fatal(e)
			}

			successor = originalCommand(t, s, a.Command.Key)
			successor.ReservationID = a.Command.ReservationID
			successor.Owner = "successor"

			leaseCommand, e = successor.LocalCommand()
			if e != nil {
				t.Fatal(e)
			}

			if _, e = s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: leaseCommand, Duration: managed.MaxLease}); e != nil {
				t.Fatal(e)
			}

			replay, err := s.RecordManagedOriginalDrain(t.Context(), d)
			if err != nil || replay != receipt {
				t.Fatal("exact replay after takeover", err)
			}

			if _, err = s.AcceptManagedOriginalRegistration(t.Context(), a); err != nil {
				t.Fatal("old registration replay", err)
			}

			if _, err = s.AcceptManagedOriginalRegistration(t.Context(), b); err != nil {
				t.Fatal("sibling replay", err)
			}

			changed := d

			changed.Facts.Native.DeliveryID = "changed"
			if _, err = s.RecordManagedOriginalDrain(t.Context(), changed); !errors.Is(err, managed.ErrConflict) {
				t.Fatal("altered replay", err)
			}

			changed = d
			changed.Command = originalCommand(t, s, a.Command.Key)

			changed.Command.ReservationID = a.Command.ReservationID
			if _, err = s.RecordManagedOriginalDrain(t.Context(), changed); !errors.Is(err, managed.ErrBlocked) {
				t.Fatal("later backfill", err)
			}

			before, err := s.ReadManagedTarget(t.Context(), a.Command.Key)
			if err != nil {
				t.Fatal(err)
			}

			local := managed.Command{Key: before.Key, ID: id.New(managed.PrefixOperation), Revision: before.Revision, LeaseEpoch: before.LeaseEpoch, Owner: before.LeaseOwner}
			if _, err = s.SealManagedTarget(t.Context(), local); err != nil {
				t.Fatal(err)
			}

			sealed, err := s.ReadManagedTarget(t.Context(), a.Command.Key)
			if err != nil {
				t.Fatal(err)
			}

			local.ID = id.New(managed.PrefixOperation)

			local.Revision = sealed.Revision
			if _, err = s.AdmitManagedRemoval(t.Context(), managed.AdmissionRequest{Command: local, OperationID: id.New(managed.PrefixOperation), Verifier: "verifier"}); err == nil {
				t.Fatal("acceptance-only drain granted removal")
			}

			wrong := key

			wrong.TenantID = "other-tenant"
			if _, err = s.ReadManagedOriginalLifecycle(t.Context(), wrong); err == nil {
				t.Fatal("cross tenant")
			}
		})
	}
}

func TestManagedOriginalLeafRefusals(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := controllerStore(t, backend)
			a, _ := originalSetup(t, s)

			before, err := s.ReadManagedTarget(t.Context(), a.Command.Key)
			if err != nil {
				t.Fatal(err)
			}

			tests := map[string]func(*managed.OriginalRegistrationAcceptanceRequest){
				"descriptor": func(q *managed.OriginalRegistrationAcceptanceRequest) {
					q.Facts.Process.Descriptor.BindingDigest = managed.Digest([]byte("wrong"))
				},
				"descriptor-version": func(q *managed.OriginalRegistrationAcceptanceRequest) {
					q.Facts.Process.Descriptor.BindingGeneration = "2"
				},
				"binding-version":  func(q *managed.OriginalRegistrationAcceptanceRequest) { q.Facts.BindingVersion = "2" },
				"identity-version": func(q *managed.OriginalRegistrationAcceptanceRequest) { q.Facts.Process.IdentityVersion = "2" },
				"decimal":          func(q *managed.OriginalRegistrationAcceptanceRequest) { q.Command.Revision = "01" },
				"overflow":         func(q *managed.OriginalRegistrationAcceptanceRequest) { q.Command.LeaseEpoch = "18446744073709551615" },
				"member":           func(q *managed.OriginalRegistrationAcceptanceRequest) { q.Facts.Member = "wrong" },
				"role":             func(q *managed.OriginalRegistrationAcceptanceRequest) { q.Facts.Role = managed.RetainedQuery },
				"runtime":          func(q *managed.OriginalRegistrationAcceptanceRequest) { q.Facts.Process.RuntimeID = "wrong" },
				"host":             func(q *managed.OriginalRegistrationAcceptanceRequest) { q.Facts.Process.HostID = "wrong" },
				"request":          func(q *managed.OriginalRegistrationAcceptanceRequest) { q.Facts.Native.RequestID = "wrong" },
				"artifact": func(q *managed.OriginalRegistrationAcceptanceRequest) {
					q.Facts.Process.ArtifactDigest = managed.Digest([]byte("wrong"))
				},
				"configuration": func(q *managed.OriginalRegistrationAcceptanceRequest) {
					q.Facts.Process.ConfigurationDigest = managed.Digest([]byte("wrong"))
				},
				"raw-digest": func(q *managed.OriginalRegistrationAcceptanceRequest) {
					q.NativeReceiptDigest = q.Facts.Native.RequestDigest
				},
				"command-domain": func(q *managed.OriginalRegistrationAcceptanceRequest) {
					q.Facts.StoredCommandDigest = q.Facts.Native.CommandDigest
				},
				"raw-overflow": func(q *managed.OriginalRegistrationAcceptanceRequest) {
					q.NativeReceipt = bytes.Repeat([]byte{1}, 4097)
					q.NativeReceiptDigest = managed.Digest(q.NativeReceipt)
				},
				"token-overflow": func(q *managed.OriginalRegistrationAcceptanceRequest) {
					q.Facts.Native.DeliveryID = strings.Repeat("x", 257)
				},
				"native-future": func(q *managed.OriginalRegistrationAcceptanceRequest) {
					q.Facts.Native.AcceptedAt = time.Now().Add(time.Hour)
				},
			}
			for name, mutate := range tests {
				t.Run(name, func(t *testing.T) {
					q := a
					mutate(&q)

					if _, err := s.AcceptManagedOriginalRegistration(t.Context(), q); err == nil {
						t.Fatal("accepted invalid leaf")
					}
				})
			}

			after, err := s.ReadManagedTarget(t.Context(), a.Command.Key)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("refusal mutated parent", err)
			}

			if _, err = s.AcceptManagedOriginalRegistration(t.Context(), a); err != nil {
				t.Fatal(err)
			}

			d := originalDrain(t, s, a)
			if _, err = s.RecordManagedOriginalDrain(t.Context(), d); err == nil {
				t.Fatal("unaccepted sibling permitted process drain")
			}
		})
	}
}

func TestManagedOriginalSiblingConsistency(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := controllerStore(t, backend)

			a, b := originalSetup(t, s)
			if _, err := s.AcceptManagedOriginalRegistration(t.Context(), a); err != nil {
				t.Fatal(err)
			}

			rid := b.Command.ReservationID
			b.Command = originalCommand(t, s, b.Command.Key)
			b.Command.ReservationID = rid

			b.Facts.Process.ConfigurationVersion = "different-native-version"
			if _, err := s.AcceptManagedOriginalRegistration(t.Context(), b); err != nil {
				t.Fatal(err)
			}

			d := originalDrain(t, s, a)
			if _, err := s.RecordManagedOriginalDrain(t.Context(), d); !errors.Is(err, managed.ErrAuthority) {
				t.Fatal("sibling process mismatch", err)
			}
		})
	}
}

func TestManagedOriginalEncodedLimits(t *testing.T) {
	s := controllerStore(t, "memory")
	a, _ := originalSetup(t, s)
	maxToken := strings.Repeat("x", 256)
	maxDigest := strings.Repeat("a", 64)
	maxCounter := managed.Decimal(strconv.FormatInt(math.MaxInt64, 10))
	stamp := time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC)
	p := &a.Facts.Process
	p.Installation = maxToken
	p.Namespace = maxToken
	p.Build = maxToken
	p.Queue = maxToken
	p.RuntimeID = maxToken
	p.InstanceID = maxToken
	p.HostID = maxToken
	p.ConfigurationVersion = maxToken
	p.ProbePolicyID = maxToken
	p.VerifierID = maxToken
	p.ProducerVerifier = maxToken
	p.Descriptor.BindingID = maxToken
	p.ArtifactDigest = maxDigest
	p.ConfigurationDigest = maxDigest
	p.EnrollmentEvidenceDigest = maxDigest
	p.Descriptor.BindingDigest = maxDigest
	p.Descriptor.BindingGeneration = maxCounter
	p.ProbePolicyVersion = maxCounter
	p.MaximumProofValidityNS = maxCounter
	a.Facts.Role = managed.RetainedQuery
	a.Facts.Member = maxToken
	a.Facts.Native.RequestID = maxToken
	a.Facts.Native.DeliveryID = maxToken
	a.Facts.Native.RequestDigest = maxDigest
	a.Facts.Native.CommandDigest = maxDigest
	a.Facts.Native.AcceptedAt = stamp
	a.Facts.CreatedAt = stamp
	a.Facts.ChangedAt = stamp
	a.Command.TenantID = maxToken
	a.Command.Owner = maxToken
	a.Command.Revision = "9223372036854775806"
	a.Command.LeaseEpoch = maxCounter
	a.NativeReceipt = bytes.Repeat([]byte{255}, 4096)
	a.NativeReceiptDigest = managed.Digest(a.NativeReceipt)

	m := managed.OriginalLifecycleMaterial{Command: a.Command, Kind: managed.OriginalRegistration, TargetIdentityDigest: maxDigest, Registration: &a.Facts, NativeReceipt: a.NativeReceipt, NativeReceiptDigest: a.NativeReceiptDigest}
	for _, kind := range []string{managed.OriginalRegistration, managed.OriginalDrain} {
		if kind == managed.OriginalDrain {
			m.Kind = kind
			m.Registration = nil
			m.Drain = &managed.OriginalDrainFacts{Process: *p, Native: a.Facts.Native, OperationID: maxToken, Deadline: stamp, ProcessAccepted: true, AdmissionClosed: true, ObservedAt: stamp.Add(-time.Nanosecond)}
			m.Drain.Native.Operation = "worker.drain.request"
			m.Drain.Native.AcceptedAt = stamp.Add(-time.Nanosecond)
		}

		ref, err := managed.OriginalReference(m)
		if err != nil {
			t.Fatal(err)
		}

		r := managed.OriginalLifecycleRecord{Material: m, Reference: ref, Receipt: managed.OriginalLocalReceipt{ID: m.Command.ID, Digest: maxDigest, Revision: maxCounter, LeaseEpoch: maxCounter, AcceptedAt: stamp, LeaseUntil: stamp}}

		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}

		reference, err := ref.Bytes()
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("actual maximal %s row=%d reference=%d raw=%d", kind, len(raw), len(reference), len(m.NativeReceipt))

		if len(raw) > 32768 || len(reference) > 1024 {
			t.Fatal("maximum combination exceeds frozen caps")
		}

		if _, err = managed.ParseOriginalLifecycleRecord(raw); err != nil {
			t.Fatal(err)
		}

		for _, bad := range [][]byte{append([]byte(" "), raw...), bytes.Replace(raw, []byte(`"revision":"9223372036854775806"`), []byte(`"revision":9223372036854775806`), 1), bytes.Replace(raw, []byte(`"kind":"`+kind+`"`), []byte(`"kind":null`), 1), bytes.Replace(raw, []byte(`"material":`), []byte(`"unknown":`), 1)} {
			if _, err = managed.ParseOriginalLifecycleRecord(bad); err == nil {
				t.Fatal("noncanonical record accepted")
			}
		}

		if _, err = managed.ParseOriginalLifecycleRecord(bytes.Repeat([]byte{' '}, 32769)); !errors.Is(err, managed.ErrCapacity) {
			t.Fatal(err)
		}

		if _, err = managed.ParseOriginalReference(bytes.Repeat([]byte{' '}, 1025)); !errors.Is(err, managed.ErrCapacity) {
			t.Fatal(err)
		}

		for _, bad := range [][]byte{append([]byte(" "), reference...), bytes.Replace(reference, []byte(`"schema":`), []byte(`"unknown":`), 1), bytes.Replace(reference, []byte(`"kind":`), []byte(`"schema":"ctrlplane.original-reference.v1","kind":`), 1), append(reference, []byte("null")...), bytes.Replace(reference, []byte(`"kind":"`+kind+`"`), []byte(`"kind":null`), 1)} {
			if _, err = managed.ParseOriginalReference(bad); err == nil {
				t.Fatal("noncanonical reference accepted")
			}
		}
	}
}

func TestManagedOriginalPostgresDeadlineLock(t *testing.T) {
	s, dsn := managedPG(t)
	a, _ := acceptOriginalPair(t, s)
	d := originalDrain(t, s, a)
	d.Facts.Deadline = time.Now().UTC().Add(700 * time.Millisecond)

	lock := pgdriver.New()
	if err := lock.Open(t.Context(), dsn); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = lock.Close() })

	tx, err := lock.BeginTxQuery(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = tx.Rollback() })

	var body []byte
	if err = tx.NewRaw("SELECT data FROM cp_managed_targets WHERE id=$1 FOR UPDATE", a.Command.InstanceID.String()).Scan(t.Context(), &body); err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)

	go func() { _, e := s.RecordManagedOriginalDrain(t.Context(), d); result <- e }()
	// Prove the real connection is blocked on the held database lock before release.
	deadline := time.Now().Add(3 * time.Second)

	for {
		var count int

		err = pgdriver.Unwrap(s.DB()).NewRaw("SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%cp_managed_targets%'").Scan(t.Context(), &count)
		if err != nil {
			t.Fatal(err)
		}

		if count > 0 {
			break
		}

		if time.Now().After(deadline) {
			t.Fatal("wrapper did not wait on held row lock")
		}

		time.Sleep(10 * time.Millisecond)
	}

	time.Sleep(time.Until(d.Facts.Deadline) + 50*time.Millisecond)

	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if err = <-result; !errors.Is(err, managed.ErrBlocked) {
		t.Fatal("post-lock expiry accepted", err)
	}

	target, err := s.ReadManagedTarget(t.Context(), a.Command.Key)
	if err != nil {
		t.Fatal(err)
	}

	for _, v := range target.Reservations {
		if v.Drain != nil {
			t.Fatal("expired acceptance persisted")
		}
	}
}

func TestManagedOriginalPostgresCorruptionRollback(t *testing.T) {
	s, _ := managedPG(t)
	a, _ := acceptOriginalPair(t, s)
	d := originalDrain(t, s, a)
	pg := pgdriver.Unwrap(s.DB())

	originalPGRollback(t, s, a.Command.Key, d.Command.ID, func() (managed.Receipt, error) { return s.RecordManagedOriginalDrain(t.Context(), d) })

	receipt, err := s.RecordManagedOriginalDrain(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}

	key := managed.OriginalLifecycleKey{Key: a.Command.Key, Kind: managed.OriginalDrain, OperationID: d.Command.ID}
	// Save exact authority bytes and restore them only as explicit test corruption repair.
	var target, local, row []byte
	if err = pg.NewRaw("SELECT data::text::bytea FROM cp_managed_targets WHERE id=$1", a.Command.InstanceID.String()).Scan(t.Context(), &target); err != nil {
		t.Fatal(err)
	}

	if err = pg.NewRaw("SELECT receipt::text::bytea FROM cp_managed_commands WHERE operation_id=$1", d.Command.ID.String()).Scan(t.Context(), &local); err != nil {
		t.Fatal(err)
	}

	if err = pg.NewRaw("SELECT data FROM cp_managed_controller_records WHERE kind='original_drain' AND key=$1", d.Command.ID.String()).Scan(t.Context(), &row); err != nil {
		t.Fatal(err)
	}

	scopeRaw, err := json.Marshal(a.Command.Key)
	if err != nil {
		t.Fatal(err)
	}

	scope := managed.Digest(scopeRaw)

	mutations := map[string]string{
		"missing-row":     "DELETE FROM cp_managed_controller_records WHERE kind='original_drain' AND key=$1",
		"corrupt-row":     "UPDATE cp_managed_controller_records SET data='{}'::bytea WHERE kind='original_drain' AND key=$1",
		"missing-receipt": "DELETE FROM cp_managed_commands WHERE operation_id=$1",
		"corrupt-receipt": "UPDATE cp_managed_commands SET receipt=jsonb_set(receipt,'{revision}','999') WHERE operation_id=$1",
	}
	for name, query := range mutations {
		t.Run(name, func(t *testing.T) {
			if _, err := pg.Exec(t.Context(), query, d.Command.ID.String()); err != nil {
				t.Fatal(err)
			}

			if _, err := s.ReadManagedOriginalLifecycle(t.Context(), key); err == nil {
				t.Fatal("corruption read")
			}

			if _, err := s.RecordManagedOriginalDrain(t.Context(), d); err == nil {
				t.Fatal("corruption replay repaired")
			}

			if _, err := pg.Exec(t.Context(), "INSERT INTO cp_managed_controller_records(kind,scope,key,data) VALUES('original_drain',$1,$2,$3) ON CONFLICT(kind,scope,key) DO UPDATE SET data=excluded.data", scope, d.Command.ID.String(), row); err != nil {
				t.Fatal(err)
			}

			if _, err := pg.Exec(t.Context(), "INSERT INTO cp_managed_commands(target_id,operation_id,receipt) VALUES($1,$2,$3::jsonb) ON CONFLICT(target_id,operation_id) DO UPDATE SET receipt=excluded.receipt", a.Command.InstanceID.String(), d.Command.ID.String(), string(local)); err != nil {
				t.Fatal(err)
			}
		})
	}

	for _, path := range []string{"{reservations,0,drain,evidence}", "{reservations,1,receipt}", "{physical,generation}"} {
		t.Run("association-"+path, func(t *testing.T) {
			if _, err := pg.Exec(t.Context(), "UPDATE cp_managed_targets SET data=jsonb_set(data,$2::text[],'\"Y29ycnVwdA==\"') WHERE id=$1", a.Command.InstanceID.String(), path); err != nil {
				t.Fatal(err)
			}

			if _, err := s.ReadManagedOriginalLifecycle(t.Context(), key); err == nil {
				t.Fatal("broken association read")
			}

			if _, err := s.RecordManagedOriginalDrain(t.Context(), d); err == nil {
				t.Fatal("broken association repaired")
			}

			if _, err := pg.Exec(t.Context(), "UPDATE cp_managed_targets SET data=$2::jsonb WHERE id=$1", a.Command.InstanceID.String(), string(target)); err != nil {
				t.Fatal(err)
			}
		})
	}

	if _, err = pg.Exec(t.Context(), "INSERT INTO cp_managed_controller_records(kind,scope,key,data) VALUES('original_drain','overflow','overflow',$1)", bytes.Repeat([]byte{1}, 32769)); err == nil {
		t.Fatal("migration row overflow accepted")
	}

	if _, err = pg.Exec(t.Context(), "INSERT INTO cp_managed_controller_records(kind,scope,key,data) VALUES('unrelated','overflow','overflow',$1)", bytes.Repeat([]byte{1}, 32769)); err != nil {
		t.Fatal("constraint affected unrelated kinds", err)
	}

	replay, err := s.RecordManagedOriginalDrain(t.Context(), d)
	if err != nil || replay != receipt {
		t.Fatal("restored exact replay", err)
	}
}

var _ managed.ControllerStore = (*postgres.Store)(nil)

func originalPGRollback(t *testing.T, s *postgres.Store, key managed.Key, operation id.ID, invoke func() (managed.Receipt, error)) {
	t.Helper()

	pg := pgdriver.Unwrap(s.DB())

	for _, table := range []string{"cp_managed_targets", "cp_managed_commands", "cp_managed_controller_records"} {
		t.Run("rollback-"+table, func(t *testing.T) {
			if _, err := pg.Exec(t.Context(), "CREATE FUNCTION reject_original_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected original failure'; END $$"); err != nil {
				t.Fatal(err)
			}

			if _, err := pg.Exec(t.Context(), "CREATE TRIGGER reject_original BEFORE INSERT OR UPDATE ON "+table+" FOR EACH ROW EXECUTE FUNCTION reject_original_write()"); err != nil {
				t.Fatal(err)
			}

			before, err := s.ReadManagedTarget(t.Context(), key)
			if err != nil {
				t.Fatal(err)
			}

			if _, err = invoke(); err == nil {
				t.Fatal("injected failure accepted")
			}

			after, err := s.ReadManagedTarget(t.Context(), key)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("partial target write", err)
			}

			var count int
			if err = pg.NewRaw("SELECT count(*) FROM cp_managed_commands WHERE operation_id=$1", operation.String()).Scan(t.Context(), &count); err != nil || count != 0 {
				t.Fatal("partial receipt", count, err)
			}

			if err = pg.NewRaw("SELECT count(*) FROM cp_managed_controller_records WHERE key=$1", operation.String()).Scan(t.Context(), &count); err != nil || count != 0 {
				t.Fatal("partial row", count, err)
			}

			if _, err = pg.Exec(t.Context(), "DROP TRIGGER reject_original ON "+table); err != nil {
				t.Fatal(err)
			}

			if _, err = pg.Exec(t.Context(), "DROP FUNCTION reject_original_write()"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestManagedOriginalPostgresRegistrationRollback(t *testing.T) {
	s, _ := managedPG(t)
	a, _ := originalSetup(t, s)
	originalPGRollback(t, s, a.Command.Key, a.Command.ID, func() (managed.Receipt, error) { return s.AcceptManagedOriginalRegistration(t.Context(), a) })

	if _, err := s.AcceptManagedOriginalRegistration(t.Context(), a); err != nil {
		t.Fatal(err)
	}
}

func TestManagedOriginalExactRawBound(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := controllerStore(t, backend)
			a, b := originalSetup(t, s)
			a.NativeReceipt = bytes.Repeat([]byte{255}, managed.MaxOriginalNativeReceipt)

			a.NativeReceiptDigest = managed.Digest(a.NativeReceipt)
			if _, err := s.AcceptManagedOriginalRegistration(t.Context(), a); err != nil {
				t.Fatal(err)
			}

			rid := b.Command.ReservationID
			b.Command = originalCommand(t, s, b.Command.Key)

			b.Command.ReservationID = rid
			if _, err := s.AcceptManagedOriginalRegistration(t.Context(), b); err != nil {
				t.Fatal(err)
			}

			d := originalDrain(t, s, a)
			d.NativeReceipt = bytes.Repeat([]byte{254}, managed.MaxOriginalNativeReceipt)

			d.NativeReceiptDigest = managed.Digest(d.NativeReceipt)
			if _, err := s.RecordManagedOriginalDrain(t.Context(), d); err != nil {
				t.Fatal(err)
			}

			for _, q := range []struct {
				kind    string
				command managed.OriginalCommand
				raw     []byte
			}{{managed.OriginalRegistration, a.Command, a.NativeReceipt}, {managed.OriginalDrain, d.Command, d.NativeReceipt}} {
				record, err := s.ReadManagedOriginalLifecycle(t.Context(), managed.OriginalLifecycleKey{Key: q.command.Key, Kind: q.kind, OperationID: q.command.ID})
				if err != nil {
					t.Fatal(err)
				}

				if !bytes.Equal(record.Material.NativeReceipt, q.raw) {
					t.Fatal("exact 4096-byte raw export changed")
				}

				if q.kind == managed.OriginalDrain {
					handle, err := managed.OriginalDrainHandleDigest(record.Material.TargetIdentityDigest, d.Facts)
					if err != nil {
						t.Fatal(err)
					}

					for _, mutate := range []func(*managed.OriginalDrainFacts){func(f *managed.OriginalDrainFacts) { f.OperationID = "other" }, func(f *managed.OriginalDrainFacts) { f.Deadline = f.Deadline.Add(time.Nanosecond) }, func(f *managed.OriginalDrainFacts) { f.Process.RuntimeID = "other" }, func(f *managed.OriginalDrainFacts) { f.Process.HostID = "other" }, func(f *managed.OriginalDrainFacts) {
						f.Process.Descriptor.BindingDigest = managed.Digest([]byte("other"))
					}} {
						changed := d.Facts
						mutate(&changed)

						other, err := managed.OriginalDrainHandleDigest(record.Material.TargetIdentityDigest, changed)
						if err != nil || other == handle {
							t.Fatal("handle scope collision", err)
						}
					}

					other, err := managed.OriginalDrainHandleDigest(managed.Digest([]byte("other-target")), d.Facts)
					if err != nil || other == handle {
						t.Fatal("handle target collision", err)
					}

					ref := record.Reference
					if ref.MaterialDigest == ref.FactsDigest || ref.MaterialDigest == ref.NativeReceiptDigest || ref.FactsDigest == ref.TargetIdentityDigest {
						t.Fatal("digest domain collision")
					}
				}
			}
		})
	}
}
