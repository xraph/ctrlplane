package memory

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

func memoryOriginalCommand(t *testing.T, s *Store, k managed.Key, rid id.ID) managed.OriginalCommand {
	t.Helper()

	target, err := s.ReadManagedTarget(t.Context(), k)
	if err != nil {
		t.Fatal(err)
	}

	return managed.OriginalCommand{Key: k, ID: id.New(managed.PrefixOperation), Revision: managed.Decimal(strconv.FormatUint(target.Revision, 10)), LeaseEpoch: managed.Decimal(strconv.FormatUint(target.LeaseEpoch, 10)), Owner: target.LeaseOwner, ReservationID: rid}
}
func memoryOriginal(t *testing.T) (*Store, managed.OriginalRegistrationAcceptanceRequest) {
	t.Helper()

	s := New()
	identity := *projectionAction(t).NewTarget

	target, err := s.CreateManagedTarget(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: managed.Command{Key: identity.Key, ID: id.New(managed.PrefixOperation), Revision: target.Revision, Owner: "original"}, Duration: managed.MaxLease}); err != nil {
		t.Fatal(err)
	}

	provisionCommand := memoryOriginalCommand(t, s, identity.Key, id.New(managed.PrefixReservation))
	provisionCommand.ReservationID = id.New(managed.PrefixReservation)

	provisionLocal, err := provisionCommand.LocalCommand()
	if err != nil {
		t.Fatal(err)
	}

	if _, err = s.IssueManagedProvision(t.Context(), provisionLocal); err != nil {
		t.Fatal(err)
	}

	provisionCommand = memoryOriginalCommand(t, s, identity.Key, id.New(managed.PrefixReservation))
	provisionCommand.ReservationID = id.New(managed.PrefixReservation)

	provisionLocal, err = provisionCommand.LocalCommand()
	if err != nil {
		t.Fatal(err)
	}

	if _, err = s.BindManagedResources(t.Context(), managed.ResourcesRequest{Command: provisionLocal, OperationID: identity.ProvisionOperationID, Receipt: []byte("provisioned"), Resources: map[string]string{"network": "network", "api": "api", "sidecar": "sidecar"}}); err != nil {
		t.Fatal(err)
	}

	rid := id.New(managed.PrefixReservation)
	c := memoryOriginalCommand(t, s, identity.Key, rid)

	local, err := c.LocalCommand()
	if err != nil {
		t.Fatal(err)
	}

	reg := managed.Registration{ID: rid, Member: identity.Manifest.Members[0].Name, RuntimeID: "runtime", InstanceID: "instance", HostID: "host", RequestID: "request", Command: []byte("command"), CommandDigest: managed.Digest([]byte("command"))}
	if _, err = s.ReserveManagedRegistration(t.Context(), managed.ReserveRequest{Command: local, Registration: reg}); err != nil {
		t.Fatal(err)
	}

	c = memoryOriginalCommand(t, s, identity.Key, rid)

	local, err = c.LocalCommand()
	if err != nil {
		t.Fatal(err)
	}

	if _, err = s.IssueManagedRegistration(t.Context(), managed.ReservationRequest{Command: local, ReservationID: rid}); err != nil {
		t.Fatal(err)
	}

	c = memoryOriginalCommand(t, s, identity.Key, rid)
	member := identity.Manifest.Members[0]

	spec, err := managed.ParseProvisionSpec(identity.Manifest)
	if err != nil {
		t.Fatal(err)
	}

	stamp := time.Now().UTC().Add(-time.Second)
	facts := managed.OriginalRegistrationFacts{Process: managed.OriginalProcessFacts{Installation: member.Installation, Namespace: member.Namespace, Build: member.Build, Queue: member.Queue, RuntimeID: reg.RuntimeID, InstanceID: reg.InstanceID, HostID: reg.HostID, IdentityVersion: "1", ArtifactDigest: member.ArtifactDigest, ConfigurationDigest: member.ConfigurationDigest, ConfigurationVersion: "version", EnrollmentEvidenceDigest: managed.Digest([]byte("enrollment")), ProbePolicyID: "policy", ProbePolicyVersion: "1", MaximumProofValidityNS: "1000000000", VerifierID: "verifier", Descriptor: spec.Services[0].Startup, ProducerVerifier: "producer"}, Native: managed.OriginalNativeAcceptance{Operation: "query_runtime.register", RequestID: reg.RequestID, RequestDigest: managed.Digest([]byte("native")), CommandDigest: managed.Digest([]byte("operator")), ResponseVersion: "1", DeliveryID: "delivery", AcceptedAt: stamp}, Member: reg.Member, Role: managed.Polling, StoredCommandDigest: reg.CommandDigest, BindingVersion: "1", BindingState: "active", RemovalEpoch: "0", CreatedAt: stamp, ChangedAt: stamp}

	return s, managed.OriginalRegistrationAcceptanceRequest{Command: c, Facts: facts, NativeReceipt: []byte("synthetic"), NativeReceiptDigest: managed.Digest([]byte("synthetic"))}
}

type originalFailureBackend struct {
	managedBackend

	failure string
}
type originalFailureTransaction struct {
	managedstate.ControllerTransaction

	failure string
}

func (b originalFailureBackend) ControllerAtomic(ctx context.Context, serial string, keys []managed.Key, change func(managedstate.ControllerTransaction) error) error {
	return b.managedBackend.ControllerAtomic(ctx, serial, keys, func(tx managedstate.ControllerTransaction) error {
		err := change(originalFailureTransaction{tx, b.failure})
		if err != nil {
			return err
		}

		if b.failure == "commit" {
			return errors.New("injected commit failure")
		}

		return nil
	})
}
func (tx originalFailureTransaction) Atomic(ctx context.Context, k managed.Key, operation id.ID, digest string, change func(*managed.Target, time.Time) (managed.Receipt, error)) (managed.Receipt, error) {
	receipt, err := tx.ControllerTransaction.Atomic(ctx, k, operation, digest, change)
	if err == nil && tx.failure == "after-a-receipt" {
		return managed.Receipt{}, errors.New("injected after A reference and local receipt")
	}

	return receipt, err
}
func (tx originalFailureTransaction) WriteRecord(ctx context.Context, kind, scope, key string, raw []byte, insert bool) error {
	if tx.failure == "before-row" {
		return errors.New("injected row failure")
	}

	if err := tx.ControllerTransaction.WriteRecord(ctx, kind, scope, key, raw, insert); err != nil {
		return err
	}

	if tx.failure == "after-row" {
		return errors.New("injected after row")
	}

	return nil
}
func memoryOriginalDrain(t *testing.T, s *Store, q managed.OriginalRegistrationAcceptanceRequest) managed.OriginalDrainAcceptanceRequest {
	t.Helper()

	if _, err := s.AcceptManagedOriginalRegistration(t.Context(), q); err != nil {
		t.Fatal(err)
	}

	c := memoryOriginalCommand(t, s, q.Command.Key, q.Command.ReservationID)
	n := q.Facts.Native
	n.Operation = "worker.drain.request"

	return managed.OriginalDrainAcceptanceRequest{Command: c, Facts: managed.OriginalDrainFacts{Process: q.Facts.Process, Native: n, OperationID: "drain", Deadline: time.Now().UTC().Add(time.Minute), ProcessAccepted: true, AdmissionClosed: true, ObservedAt: n.AcceptedAt}, NativeReceipt: q.NativeReceipt, NativeReceiptDigest: q.NativeReceiptDigest}
}
func TestManagedOriginalMemoryAtomicRollback(t *testing.T) {
	for _, kind := range []string{managed.OriginalRegistration, managed.OriginalDrain} {
		t.Run(kind, func(t *testing.T) {
			for _, failure := range []string{"after-a-receipt", "before-row", "after-row", "commit"} {
				t.Run(failure, func(t *testing.T) {
					s, q := memoryOriginal(t)

					var d managed.OriginalDrainAcceptanceRequest
					if kind == managed.OriginalDrain {
						d = memoryOriginalDrain(t, s, q)
					}

					before := projectionSnapshot(t, s)
					repo := managedstate.New(originalFailureBackend{managedBackend: managedBackend{store: s}, failure: failure})

					var err error
					if kind == managed.OriginalRegistration {
						_, err = repo.AcceptManagedOriginalRegistration(t.Context(), q)
					} else {
						_, err = repo.RecordManagedOriginalDrain(t.Context(), d)
					}

					if err == nil {
						t.Fatal("injected failure accepted")
					}

					if after := projectionSnapshot(t, s); after != before {
						t.Fatal("rollback left persisted changes")
					}

					if kind == managed.OriginalRegistration {
						_, err = s.AcceptManagedOriginalRegistration(t.Context(), q)
					} else {
						_, err = s.RecordManagedOriginalDrain(t.Context(), d)
					}

					if err != nil {
						t.Fatal("clean retry after rollback", err)
					}
				})
			}
		})
	}
}
func TestManagedOriginalMemoryCorruption(t *testing.T) {
	for _, mode := range []string{"missing-row", "corrupt-row", "missing-reference", "corrupt-reference", "missing-receipt", "missing-receipt-fresh-cas", "corrupt-receipt", "identity", "descriptor", "reservation-command"} {
		t.Run(mode, func(t *testing.T) {
			s, q := memoryOriginal(t)
			if _, err := s.AcceptManagedOriginalRegistration(t.Context(), q); err != nil {
				t.Fatal(err)
			}

			scopeRaw, err := json.Marshal(q.Command.Key)
			if err != nil {
				t.Fatal(err)
			}

			bucket := managed.OriginalRegistration + ":" + managed.Digest(scopeRaw)
			rowKey := q.Command.ID.String()
			receiptKey := q.Command.InstanceID.String() + ":" + rowKey
			target := s.managedTargets[q.Command.InstanceID.String()]

			switch mode {
			case "missing-row":
				delete(s.managedControllerRecords[bucket], rowKey)
			case "corrupt-row":
				s.managedControllerRecords[bucket][rowKey] = []byte("{}")
			case "missing-reference":
				target.Reservations[0].Receipt = nil
			case "corrupt-reference":
				target.Reservations[0].Receipt = []byte("corrupt")
			case "missing-receipt":
				delete(s.managedReceipts, receiptKey)
			case "missing-receipt-fresh-cas":
				delete(s.managedReceipts, receiptKey)

				revision, err := q.Command.Revision.Uint64()
				if err != nil {
					t.Fatal(err)
				}

				target.Revision = revision
			case "corrupt-receipt":
				r := s.managedReceipts[receiptKey]
				r.Revision++
				s.managedReceipts[receiptKey] = r
			case "identity":
				target.Physical.Generation = "changed"
			case "descriptor":
				target.Manifest.Spec[0] ^= 1
			case "reservation-command":
				target.Reservations[0].Command = []byte("changed")
			}

			before := projectionSnapshot(t, s)

			key := managed.OriginalLifecycleKey{Key: q.Command.Key, Kind: managed.OriginalRegistration, OperationID: q.Command.ID}
			if _, err = s.ReadManagedOriginalLifecycle(t.Context(), key); err == nil {
				t.Fatal("corruption read accepted")
			}

			if _, err = s.AcceptManagedOriginalRegistration(t.Context(), q); err == nil {
				t.Fatal("corruption replay accepted")
			}

			if after := projectionSnapshot(t, s); after != before {
				t.Fatal("corrupt history repaired")
			}
		})
	}
}
func TestManagedOriginalMemoryLegacyBackfill(t *testing.T) {
	s, q := memoryOriginal(t)

	local, err := q.Command.LocalCommand()
	if err != nil {
		t.Fatal(err)
	}

	if _, err = s.AcceptManagedRegistration(t.Context(), managed.AcceptanceRequest{ReservationRequest: managed.ReservationRequest{Command: local, ReservationID: q.Command.ReservationID}, Receipt: []byte("legacy opaque")}); err != nil {
		t.Fatal(err)
	}

	before, err := s.ReadManagedTarget(t.Context(), q.Command.Key)
	if err != nil {
		t.Fatal(err)
	}

	q.Command = memoryOriginalCommand(t, s, q.Command.Key, q.Command.ReservationID)
	if _, err = s.AcceptManagedOriginalRegistration(t.Context(), q); !errors.Is(err, managed.ErrBlocked) {
		t.Fatal("legacy backfill", err)
	}

	after, err := s.ReadManagedTarget(t.Context(), q.Command.Key)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("legacy refusal mutated state", err)
	}
}
