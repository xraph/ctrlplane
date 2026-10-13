package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

func discoveryPause(t *testing.T, s *Store, original managed.ActionSubmission) managed.ActionSubmission {
	t.Helper()

	target, err := s.ReadManagedTarget(t.Context(), original.Key)
	if err != nil {
		t.Fatal(err)
	}

	q := original
	q.Command = projectionCommand(t, s, target.Key)
	q.Kind = managed.ActionPause
	q.NewTarget = nil
	q.ReleaseID = id.Nil
	q.ExpectedDesiredRevision = target.DesiredRevision
	q.OriginalCommand = []byte("pause " + q.ID.String())
	q.OccurredAt = time.Now().UTC().Add(-time.Second)
	signProjectionAction(t, &q)

	return q
}

func TestManagedDiscoveryMemoryDesiredCannotHideOlderActions(t *testing.T) {
	s, action, _ := projectionFixture(t)

	pause := discoveryPause(t, s, action)
	if _, err := s.SubmitManagedAction(t.Context(), pause); err != nil {
		t.Fatal(err)
	}

	head, err := s.ReadManagedWorkHead(t.Context(), action.Key)
	if err != nil || head.Provisioning.ID != action.ID || head.Desired.ID != pause.ID || head.LastOrdinal != "2" {
		t.Fatal(head, err)
	}

	first, err := s.ListManagedUnresolvedWork(t.Context(), managed.WorkPageRequest{Key: action.Key, Limit: 1})
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != action.ID || first.NextCursor == "" {
		t.Fatal(first, err)
	}
	// A later acceptance cannot extend an already fetched page window.
	later := discoveryPause(t, s, action)
	if _, err = s.SubmitManagedAction(t.Context(), later); err != nil {
		t.Fatal(err)
	}

	second, err := s.ListManagedUnresolvedWork(t.Context(), managed.WorkPageRequest{Key: action.Key, Cursor: first.NextCursor, Limit: 1})
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != pause.ID || second.NextCursor != "" {
		t.Fatal(second, err)
	}

	if _, err = s.SubmitManagedAction(t.Context(), action); err != nil {
		t.Fatal(err)
	}

	after, err := s.ReadManagedWorkHead(t.Context(), action.Key)
	if err != nil || after.Desired.ID != later.ID || after.LastOrdinal != "3" {
		t.Fatal("replay rewound discovery", after, err)
	}

	all, err := s.ListManagedUnresolvedWork(t.Context(), managed.WorkPageRequest{Key: action.Key, Limit: 32})
	if err != nil || len(all.Items) != 3 {
		t.Fatal(all, err)
	}
}

type discoveryFailureBackend struct {
	managedBackend

	fail string
}
type discoveryFailureTx struct {
	managedstate.ControllerTransaction

	fail string
}

func (b discoveryFailureBackend) ControllerAtomic(ctx context.Context, serial string, keys []managed.Key, fn func(managedstate.ControllerTransaction) error) error {
	return b.managedBackend.ControllerAtomic(ctx, serial, keys, func(tx managedstate.ControllerTransaction) error {
		return fn(discoveryFailureTx{ControllerTransaction: tx, fail: b.fail})
	})
}
func (t discoveryFailureTx) WriteRecord(ctx context.Context, kind, scope, key string, raw []byte, insert bool) error {
	if err := t.ControllerTransaction.WriteRecord(ctx, kind, scope, key, raw, insert); err != nil {
		return err
	}

	if kind == t.fail {
		return managed.ErrUnavailable
	}

	return nil
}

func TestManagedDiscoveryMemoryAcceptanceRollback(t *testing.T) {
	for _, kind := range []string{"audit", "work_index", "work_lookup", "work_head"} {
		t.Run(kind, func(t *testing.T) {
			s := New()
			action := projectionAction(t)

			r := managedstate.New(discoveryFailureBackend{managedBackend: managedBackend{store: s}, fail: kind})
			if _, err := r.SubmitManagedAction(t.Context(), action); err == nil {
				t.Fatal("partial acceptance succeeded")
			}

			if len(s.managedTargets) != 0 || len(s.managedControllerRecords) != 0 {
				t.Fatal("acceptance/audit/discovery escaped rollback")
			}
		})
	}
}

func TestManagedDiscoveryMemoryMissingAssociationIsNotBackfilled(t *testing.T) {
	s, action, _ := projectionFixture(t)
	for bucket := range s.managedControllerRecords {
		if len(bucket) >= 11 && bucket[:11] == "work_index:" {
			delete(s.managedControllerRecords, bucket)
		}
	}

	before := projectionSnapshot(t, s)
	if _, err := s.ListManagedUnresolvedWork(t.Context(), managed.WorkPageRequest{Key: action.Key, Limit: 1}); err == nil {
		t.Fatal("missing association accepted")
	}

	if _, err := s.SubmitManagedAction(t.Context(), action); err == nil {
		t.Fatal("replay backfilled missing association")
	}

	if before != projectionSnapshot(t, s) {
		t.Fatal("read/replay repaired unknown history")
	}
}

func discoveryRegistration(t *testing.T) (*Store, managed.ActionSubmission, managed.OriginalRegistrationAcceptanceRequest, managed.PhaseCommandRequest) {
	t.Helper()
	seed, original := memoryOriginal(t)

	seedTarget, err := seed.ReadManagedTarget(t.Context(), original.Command.Key)
	if err != nil {
		t.Fatal(err)
	}

	action := projectionAction(t)
	action.Key = seedTarget.Key
	action.NewTarget = &seedTarget.Identity
	signProjectionAction(t, &action)

	s := New()
	if _, err = s.SubmitManagedAction(t.Context(), action); err != nil {
		t.Fatal(err)
	}

	projection := projectionLease(t, s, action.Key, action.ID)
	if _, err = s.ProjectManagedRows(t.Context(), projection); err != nil {
		t.Fatal(err)
	}

	if _, err = s.IssueManagedProvision(t.Context(), projectionCommand(t, s, action.Key)); err != nil {
		t.Fatal(err)
	}

	if _, err = s.BindManagedResources(t.Context(), managed.ResourcesRequest{Command: projectionCommand(t, s, action.Key), OperationID: seedTarget.ProvisionOperationID, Receipt: []byte("provisioned"), Resources: map[string]string{"network": "network", "api": "api", "sidecar": "sidecar"}}); err != nil {
		t.Fatal(err)
	}

	registration := seedTarget.Reservations[0].Registration
	if _, err = s.ReserveManagedRegistration(t.Context(), managed.ReserveRequest{Command: projectionCommand(t, s, action.Key), Registration: registration}); err != nil {
		t.Fatal(err)
	}

	command := projectionCommand(t, s, action.Key)
	binding := managed.RoleBinding{Target: managed.IdentityWire(seedTarget.Identity), MemberIndex: "0", MemberName: registration.Member, Role: managed.Polling, ReservationID: registration.ID, ReservationVersion: "1", RuntimeID: registration.RuntimeID, InstanceID: registration.InstanceID, HostID: registration.HostID, NativeBuild: original.Facts.Process.Build, ConfigurationDigest: original.Facts.Process.ConfigurationDigest, EnrollmentEvidence: []byte("enrollment")}
	native := []byte("native-original-registration")
	q := managed.PhaseCommandRequest{Schema: managed.DiscoverySchema, Parent: managed.PhaseParent{Key: command.Key, ID: command.ID, Revision: managed.Decimal(strconv.FormatUint(command.Revision, 10)), LeaseEpoch: managed.Decimal(strconv.FormatUint(command.LeaseEpoch, 10)), Owner: command.Owner}, ActionID: action.ID, Kind: "registration", Process: &managed.ProcessPhaseCommand{Request: managed.LifecycleRequest{Command: managed.PortCommand{SchemaVersion: "1", OperationID: id.New(managed.PrefixOperation), RequestID: registration.RequestID, Original: registration.Command, OriginalDigest: registration.CommandDigest, Native: native, NativeDigest: managed.Digest(native)}, Binding: binding}, Process: original.Facts.Process, Scope: managed.MachineScope{Installation: action.Installation, Application: action.ApplicationID, Environment: "test", TenantID: action.TenantID, Action: "workers.register", ResourceID: action.InstanceID.String()}}, Audit: managed.AccessAuditRequest{AttemptID: id.New(managed.PrefixOperation), Installation: action.Installation, Authority: action.Authority, ApplicationID: action.ApplicationID, OrganizationID: action.OrganizationID, Actor: action.Actor, Scope: managed.AuditScope{Kind: "tenant", TenantID: action.TenantID}, Action: "managed.reconcile", ResourceKind: "instance", ResourceID: action.InstanceID.String(), Outcome: "allowed", Reason: "authorized", OccurredAt: time.Now().UTC().Add(-time.Second)}}
	q.Process.Native = managed.PhaseNativeIdentity{Operation: original.Facts.Native.Operation, RequestID: original.Facts.Native.RequestID, RequestDigest: original.Facts.Native.RequestDigest, CommandDigest: original.Facts.Native.CommandDigest}

	return s, action, original, q
}

// Hold the controller clock behind Atomic's fresh clock without sleeps. Both
// timestamps are valid, but only the latter belongs to the accepted mutation.
type discoveryEarlierClockBackend struct{ managedBackend }

func (b discoveryEarlierClockBackend) ControllerAtomic(ctx context.Context, serial string, keys []managed.Key, change func(managedstate.ControllerTransaction) error) error {
	return b.managedBackend.ControllerAtomic(ctx, serial, keys, func(tx managedstate.ControllerTransaction) error {
		return change(discoveryEarlierClockTransaction{ControllerTransaction: tx, clock: tx.Now().Add(-time.Millisecond)})
	})
}

type discoveryEarlierClockTransaction struct {
	managedstate.ControllerTransaction

	clock time.Time
}

func (t discoveryEarlierClockTransaction) Now() time.Time { return t.clock }

func TestManagedDiscoveryMemoryRegistrationAtomicOriginalRetention(t *testing.T) {
	s, action, _, q := discoveryRegistration(t)

	r := managedstate.New(discoveryEarlierClockBackend{managedBackend{store: s}})

	grant, err := r.RetainManagedPhaseCommand(t.Context(), q)
	if err != nil || !grant.NewlyRetained {
		t.Fatal(grant, err)
	}

	if grant.Record.AuditReceipt.AcceptedAt != grant.Record.Acceptance.AcceptedAt {
		t.Fatal("phase audit used a different acceptance clock")
	}

	target, err := s.ReadManagedTarget(t.Context(), action.Key)
	if err != nil || target.Reservations[0].Phase != managed.MayHaveIssued {
		t.Fatal(target, err)
	}

	page, err := s.ListManagedUnresolvedWork(t.Context(), managed.WorkPageRequest{Key: action.Key, Limit: 32})
	if err != nil || len(page.Items) != 2 || page.Items[1] != grant.Record.Work {
		t.Fatal(page, err)
	}

	q.Process.Request.Command.Native[0] = '!'

	read, err := s.ReadManagedPhaseCommand(t.Context(), grant.Record.Work)
	if err != nil || read.Request.Process.Request.Command.Native[0] == '!' {
		t.Fatal("retained bytes aliased caller", err)
	}

	replay, err := s.RetainManagedPhaseCommand(t.Context(), read.Request)
	if err != nil || replay.NewlyRetained || !reflect.DeepEqual(replay.Record, read) {
		t.Fatal(replay, err)
	}

	if _, err = s.RetainManagedPhaseCommand(t.Context(), q); err == nil {
		t.Fatal("changed native request accepted")
	}
}

func TestManagedDiscoveryMemoryPhaseRollbackAtEveryRecord(t *testing.T) {
	for _, kind := range []string{"audit", "work_index", "work_lookup", "work_head", "phase_command", "phase_selection"} {
		t.Run(kind, func(t *testing.T) {
			s, _, _, q := discoveryRegistration(t)
			before := projectionSnapshot(t, s)

			r := managedstate.New(discoveryFailureBackend{managedBackend: managedBackend{store: s}, fail: kind})
			if _, err := r.RetainManagedPhaseCommand(t.Context(), q); err == nil {
				t.Fatal("failed phase accepted")
			}

			if before != projectionSnapshot(t, s) {
				t.Fatal("partial issue/audit/original/discovery published")
			}
		})
	}
}

func TestManagedDiscoveryMemoryIndependentReceiptRejectsRewrittenOriginal(t *testing.T) {
	s, _, _, q := discoveryRegistration(t)

	grant, err := s.RetainManagedPhaseCommand(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}

	for bucket, rows := range s.managedControllerRecords {
		if len(bucket) < 14 || bucket[:14] != "phase_command:" {
			continue
		}

		raw := rows[q.Parent.ID.String()]

		record, decodeErr := managed.DecodePhaseCommandRecord(raw)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}

		record.Request.Process.Request.Command.Native = []byte("rewritten")
		record.Request.Process.Request.Command.NativeDigest = managed.Digest([]byte("rewritten"))

		record.RequestDigest, err = managed.PhaseCommandDigest(record.Request)
		if err != nil {
			t.Fatal(err)
		}

		record.Work.Digest = record.RequestDigest

		body, encodeErr := managed.EncodePhaseCommandRecord(record)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}

		rows[q.Parent.ID.String()] = body
		// Rewrite every row-owned reference, leaving the independent parent receipt untouched.
		refBytes, refErr := managed.EncodeWorkReference(record.Work)
		if refErr != nil {
			t.Fatal(refErr)
		}

		for otherBucket, otherRows := range s.managedControllerRecords {
			if strings.HasPrefix(otherBucket, "work_index:") || strings.HasPrefix(otherBucket, "work_lookup:") || strings.HasPrefix(otherBucket, "phase_selection:") {
				for key, value := range otherRows {
					ref, readErr := managed.DecodeWorkReference(value)
					if readErr == nil && ref.ID == q.Parent.ID {
						otherRows[key] = bytes.Clone(refBytes)
					}
				}
			}
		}

		if _, err = s.ReadManagedPhaseCommand(t.Context(), record.Work); !errors.Is(err, managed.ErrConflict) {
			t.Fatal("independent parent receipt did not reject rewritten original", err)
		}
	}

	if _, err = s.ReadManagedPhaseCommand(t.Context(), grant.Record.Work); err == nil || errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func discoveryNextPhase(t *testing.T, s *Store, prior managed.PhaseCommandRequest, kind string) managed.PhaseCommandRequest {
	t.Helper()

	q, err := managed.ClonePhaseCommandRequest(prior)
	if err != nil {
		t.Fatal(err)
	}

	command := projectionCommand(t, s, q.Parent.Key)
	q.Parent = managed.PhaseParent{Key: command.Key, ID: command.ID, Revision: managed.Decimal(strconv.FormatUint(command.Revision, 10)), LeaseEpoch: managed.Decimal(strconv.FormatUint(command.LeaseEpoch, 10)), Owner: command.Owner}
	q.Kind = kind
	q.Audit.AttemptID = id.New(managed.PrefixOperation)
	q.Audit.OccurredAt = time.Now().UTC().Add(-time.Second)
	q.Process.Scope.Action = "workers." + kind
	raw := []byte(kind + " original")

	q.Process.Request.Command = managed.PortCommand{SchemaVersion: "1", OperationID: id.New(managed.PrefixOperation), RequestID: kind, Original: raw, OriginalDigest: managed.Digest(raw), Native: raw, NativeDigest: managed.Digest(raw)}
	if kind == "drain" {
		q.Process.DrainOperation = "permanent-drain"
		q.Process.Deadline = time.Now().UTC().Add(time.Minute)
	}

	q.Process.Native = managed.PhaseNativeIdentity{}
	if kind == "drain" {
		q.Process.Native = managed.PhaseNativeIdentity{Operation: "worker.drain.request", RequestID: kind, RequestDigest: managed.Digest([]byte("native-domain:" + kind)), CommandDigest: managed.Digest([]byte("command-domain:" + kind))}
	}

	return q
}

func TestManagedDiscoveryMemoryActivationAndOriginalDrainRetention(t *testing.T) {
	s, action, original, registration := discoveryRegistration(t)
	if _, err := s.RetainManagedPhaseCommand(t.Context(), registration); err != nil {
		t.Fatal(err)
	}

	original.Command = memoryOriginalCommand(t, s, action.Key, original.Command.ReservationID)
	if _, err := s.AcceptManagedOriginalRegistration(t.Context(), original); err != nil {
		t.Fatal(err)
	}

	activation := discoveryNextPhase(t, s, registration, "activation")

	activationGrant, err := s.RetainManagedPhaseCommand(t.Context(), activation)
	if err != nil || !activationGrant.NewlyRetained {
		t.Fatal(activationGrant, err)
	}

	pause := discoveryPause(t, s, action)
	if _, err = s.SubmitManagedAction(t.Context(), pause); err != nil {
		t.Fatal(err)
	}
	// Desired changes cannot hide or replace an activation that may have been sent.
	if _, err = s.ReadManagedPhaseCommand(t.Context(), activationGrant.Record.Work); err != nil {
		t.Fatal(err)
	}

	if _, err = s.SealManagedTarget(t.Context(), projectionCommand(t, s, action.Key)); err != nil {
		t.Fatal(err)
	}

	drain := discoveryNextPhase(t, s, registration, "drain")
	drain.ActionID = pause.ID

	grant, err := s.RetainManagedPhaseCommand(t.Context(), drain)
	if err != nil || !grant.NewlyRetained {
		t.Fatal(grant, err)
	}

	replayed, err := s.RetainManagedPhaseCommand(t.Context(), drain)
	if err != nil || replayed.NewlyRetained {
		t.Fatal(replayed, err)
	}

	drain.Process.Deadline = drain.Process.Deadline.Add(time.Second)
	if _, err = s.RetainManagedPhaseCommand(t.Context(), drain); !errors.Is(err, managed.ErrConflict) {
		t.Fatal("original deadline changed", err)
	}

	newDrain := discoveryNextPhase(t, s, registration, "drain")

	newDrain.ActionID = pause.ID
	if _, err = s.RetainManagedPhaseCommand(t.Context(), newDrain); !errors.Is(err, managed.ErrConflict) {
		t.Fatal("new identity escaped unresolved drain", err)
	}
}

func TestManagedDiscoveryMemoryCanceledPhaseCannotRetain(t *testing.T) {
	s, _, _, q := discoveryRegistration(t)
	before := projectionSnapshot(t, s)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := s.RetainManagedPhaseCommand(ctx, q); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	if before != projectionSnapshot(t, s) {
		t.Fatal("canceled request changed authority")
	}
}

func discoveryAcceptedRegistration(t *testing.T) (*Store, managed.ActionSubmission, managed.PhaseCommandRequest) {
	t.Helper()

	s, action, original, q := discoveryRegistration(t)
	if _, err := s.RetainManagedPhaseCommand(t.Context(), q); err != nil {
		t.Fatal(err)
	}

	original.Command = memoryOriginalCommand(t, s, action.Key, original.Command.ReservationID)
	if _, err := s.AcceptManagedOriginalRegistration(t.Context(), original); err != nil {
		t.Fatal(err)
	}

	return s, action, q
}

func TestManagedDiscoveryMemoryReplacementDrainUsesAcceptedSuccessorKey(t *testing.T) {
	s, action, registration := discoveryAcceptedRegistration(t)
	replacement := projectionAction(t)
	replacement.Command = projectionCommand(t, s, action.Key)
	replacement.NewTarget.TenantID = action.TenantID

	target, err := s.ReadManagedTarget(t.Context(), action.Key)
	if err != nil {
		t.Fatal(err)
	}

	replacement.ExpectedDesiredRevision = target.DesiredRevision
	for n := range replacement.NewTarget.Manifest.Members {
		replacement.NewTarget.Manifest.Members[n].OwnerID = action.NewTarget.Manifest.Members[0].OwnerID
	}

	signProjectionAction(t, &replacement)

	if _, err = s.SubmitManagedAction(t.Context(), replacement); err != nil {
		t.Fatal(err)
	}

	if _, err = s.SealManagedTarget(t.Context(), projectionCommand(t, s, action.Key)); err != nil {
		t.Fatal(err)
	}

	drain := discoveryNextPhase(t, s, registration, "drain")
	drain.ActionID = replacement.ID

	grant, err := s.RetainManagedPhaseCommand(t.Context(), drain)
	if err != nil {
		t.Fatal(err)
	}

	if grant.Record.Work.Key != action.Key || grant.Record.Work.Operation.Key != replacement.NewTarget.Key {
		t.Fatal("replacement origin lost", grant.Record.Work)
	}

	if _, err = s.ReadManagedPhaseCommand(t.Context(), grant.Record.Work); err != nil {
		t.Fatal(err)
	}
}

func TestManagedDiscoveryMemoryFreshDrainRequiresSealAndCurrentDesire(t *testing.T) {
	s, action, registration := discoveryAcceptedRegistration(t)

	pause := discoveryPause(t, s, action)
	if _, err := s.SubmitManagedAction(t.Context(), pause); err != nil {
		t.Fatal(err)
	}

	drain := discoveryNextPhase(t, s, registration, "drain")

	drain.ActionID = pause.ID
	if _, err := s.RetainManagedPhaseCommand(t.Context(), drain); !errors.Is(err, managed.ErrBlocked) {
		t.Fatal("unsealed drain retained", err)
	}

	if _, err := s.SealManagedTarget(t.Context(), projectionCommand(t, s, action.Key)); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetManagedDesired(t.Context(), managed.DesiredRequest{Command: projectionCommand(t, s, action.Key), Mode: managed.Active}); err != nil {
		t.Fatal(err)
	}

	drain = discoveryNextPhase(t, s, registration, "drain")
	drain.ActionID = pause.ID

	before := projectionSnapshot(t, s)
	if _, err := s.RetainManagedPhaseCommand(t.Context(), drain); !errors.Is(err, managed.ErrBlocked) {
		t.Fatal("old pause granted fresh drain", err)
	}

	if before != projectionSnapshot(t, s) {
		t.Fatal("refused drain changed authority")
	}
}

func TestManagedDiscoveryMemoryActionRequiresExactAuditIntent(t *testing.T) {
	s, action, _ := projectionFixture(t)
	changed := false

	for bucket, rows := range s.managedControllerRecords {
		if !strings.HasPrefix(bucket, "audit:") {
			continue
		}

		raw, exists := rows[action.ID.String()]
		if !exists {
			continue
		}

		var intent managed.AuditIntent
		if err := json.Unmarshal(raw, &intent); err != nil {
			t.Fatal(err)
		}

		intent.Actor.Subject = "different-actor"
		intent.Outcome = "denied"

		body, err := json.Marshal(intent)
		if err != nil {
			t.Fatal(err)
		}

		rows[action.ID.String()] = body
		changed = true
	}

	if !changed {
		t.Fatal("fixture audit absent")
	}

	before := projectionSnapshot(t, s)
	if _, err := s.ReadManagedWorkHead(t.Context(), action.Key); err == nil {
		t.Fatal("rewritten audit trusted")
	}

	if _, err := s.SubmitManagedAction(t.Context(), action); err == nil {
		t.Fatal("rewritten audit replayed")
	}

	if before != projectionSnapshot(t, s) {
		t.Fatal("audit repaired")
	}
}
