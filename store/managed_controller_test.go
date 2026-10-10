package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/managedservice"
	"github.com/xraph/ctrlplane/store/memory"
	"github.com/xraph/ctrlplane/store/postgres"
)

func controllerStore(t *testing.T, backend string) managedservice.Authority {
	t.Helper()

	if backend == "memory" {
		return memory.New()
	}

	s, _ := managedPG(t)

	return s
}

func controllerAction(t *testing.T) managed.ActionSubmission {
	t.Helper()

	identity := managedIdentity()

	owner := id.New(id.PrefixWorkload)
	for n := range identity.Manifest.Members {
		identity.Manifest.Members[n].OwnerID = owner
	}

	provision := managed.ProvisionSpec{Version: "1", Services: []managed.ProvisionService{
		{Name: "api", Image: "example/api@sha256:" + strings.Repeat("a", 64), Role: "main", CPUMillis: "100", MemoryMiB: "16", PIDLimit: "16", Startup: managed.StartupBindingRef{BindingID: "api-binding", BindingGeneration: "1", BindingDigest: strings.Repeat("b", 64)}},
		{Name: "sidecar", Image: "example/sidecar@sha256:" + strings.Repeat("c", 64), Role: "sidecar", CPUMillis: "100", MemoryMiB: "16", PIDLimit: "16", Startup: managed.StartupBindingRef{BindingID: "query-binding", BindingGeneration: "1", BindingDigest: strings.Repeat("d", 64)}},
	}}

	raw, err := json.Marshal(provision)
	if err != nil {
		t.Fatal(err)
	}

	spec, err := json.Marshal(managed.ManagedSpec{Profile: "ctrlplane.managed.v1", RuntimeRoles: []managed.RuntimeRole{managed.Polling, managed.RetainedQuery}, Provision: raw})
	if err != nil {
		t.Fatal(err)
	}

	identity.Manifest.Spec = spec
	identity.Manifest.SpecDigest = managed.Digest(spec)
	q := managed.ActionSubmission{Command: managed.Command{Key: identity.Key, ID: id.New(managed.PrefixOperation)}, Kind: managed.ActionDeploy, Installation: "installation", Authority: "authority", ApplicationID: "app", OrganizationID: "org", Actor: managed.AuditActor{Subject: "verified-operator"}, OriginalCommand: []byte(`{"request":"deploy"}`), NewTarget: &identity, ReleaseID: id.New(id.PrefixRelease), OccurredAt: time.Now().UTC().Add(-time.Second)}
	signControllerAction(t, &q)

	return q
}

func signControllerAction(t *testing.T, q *managed.ActionSubmission) {
	t.Helper()

	q.OriginalDigest = managed.Digest(q.OriginalCommand)

	var err error

	q.CanonicalDigest, err = managed.ActionDigest(*q)
	if err != nil {
		t.Fatal(err)
	}
}

func acceptedController(t *testing.T, s managedservice.Authority) (managed.ActionSubmission, *managedFixture, managed.ProjectionRequest) {
	t.Helper()

	q := controllerAction(t)
	if _, err := s.SubmitManagedAction(t.Context(), q); err != nil {
		t.Fatal(err)
	}

	f := &managedFixture{t: t, s: s, k: q.Key}
	f.must(s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: f.command(), Duration: managed.MaxLease}))
	projection := managed.ProjectionRequest{Command: f.command(), ActionID: q.ID, Stage: managed.ProjectionPending}

	return q, f, projection
}

func auditQuery(q managed.ActionSubmission) managed.AuditPageRequest {
	return managed.AuditPageRequest{Installation: q.Installation, Authority: q.Authority, Scope: managed.AuditScope{Kind: "tenant", TenantID: q.TenantID}, Limit: 1}
}

func TestManagedControllerAcceptance(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := controllerStore(t, backend)
			q := controllerAction(t)

			first, err := s.SubmitManagedAction(t.Context(), q)
			if err != nil {
				t.Fatal(err)
			}

			second, err := s.SubmitManagedAction(t.Context(), q)
			if err != nil || !reflect.DeepEqual(first, second) {
				t.Fatalf("replay: %v", err)
			}

			changed := q
			changed.Actor.Subject = "another-subject"
			signControllerAction(t, &changed)

			if _, err = s.SubmitManagedAction(t.Context(), changed); !errors.Is(err, managed.ErrConflict) {
				t.Fatalf("changed content: %v", err)
			}

			page, err := s.ListManagedAuditIntents(t.Context(), auditQuery(q))
			if err != nil || len(page.Items) != 1 {
				t.Fatalf("atomic intent: %+v %v", page, err)
			}

			f := &managedFixture{t: t, s: s, k: q.Key}
			f.must(s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: f.command(), Duration: managed.MaxLease}))

			pause := q
			pause.Command = f.command()
			pause.Kind = managed.ActionPause
			pause.NewTarget = nil
			pause.ReleaseID = id.Nil
			pause.ExpectedDesiredRevision = f.target().DesiredRevision
			signControllerAction(t, &pause)

			receipt, err := s.SubmitManagedAction(t.Context(), pause)
			if err != nil || f.target().Desired != managed.Paused {
				t.Fatalf("pause: %v", err)
			}

			replay, err := s.SubmitManagedAction(t.Context(), pause)
			if err != nil || !reflect.DeepEqual(receipt, replay) {
				t.Fatalf("stale accepted replay: %v", err)
			}

			stale := pause
			stale.ID = id.New(managed.PrefixOperation)
			signControllerAction(t, &stale)

			if _, err = s.SubmitManagedAction(t.Context(), stale); !errors.Is(err, managed.ErrConflict) {
				t.Fatalf("stale new action: %v", err)
			}

			unsupported := pause
			unsupported.ID = id.New(managed.PrefixOperation)
			unsupported.Kind = managed.ActionContinue
			signControllerAction(t, &unsupported)

			if _, err = s.SubmitManagedAction(t.Context(), unsupported); !errors.Is(err, managed.ErrUnsupported) {
				t.Fatalf("continuation: %v", err)
			}
		})
	}
}

func TestManagedControllerJournalCannotIssue(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := controllerStore(t, backend)
			q, f, projection := acceptedController(t, s)
			step := managed.ProviderStepAuthorization{Command: f.command(), Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: q.Key, ActionID: q.ID}, ProviderOperationID: q.NewTarget.ProvisionOperationID, Kind: managed.StepCreateNetwork}}

			advance := managed.OperationAdvance{Command: f.command(), ActionID: q.ID, ExpectedOperationRevision: 1, Observation: managed.OperationObservation{Kind: "completed", ObservedAt: time.Now().UTC().Add(-time.Second)}}
			if _, err := s.AdvanceManagedOperation(t.Context(), advance); err != nil {
				t.Fatal(err)
			}

			if _, err := s.AuthorizeManagedProviderStep(t.Context(), step); err == nil {
				t.Fatal("journal-only issue accepted")
			}

			if f.target().Provision.Issued {
				t.Fatal("failed permission changed A")
			}

			if _, err := s.ProjectManagedRows(t.Context(), projection); err != nil {
				t.Fatal(err)
			}

			step.Command = f.command()

			permission, err := s.AuthorizeManagedProviderStep(t.Context(), step)
			if err != nil || !permission.NewlyIssued || !f.target().Provision.Issued {
				t.Fatalf("issue: %+v %v", permission, err)
			}

			replay, err := s.AuthorizeManagedProviderStep(t.Context(), step)
			if err != nil || replay.NewlyIssued || !reflect.DeepEqual(replay.Permission, permission.Permission) {
				t.Fatalf("issue replay: %+v %v", replay, err)
			}

			next := step
			next.Command = f.command()
			next.Step.Kind = managed.StepCreateContainer

			next.Step.Service = "api"
			if _, err = s.AuthorizeManagedProviderStep(t.Context(), next); err == nil {
				t.Fatal("missing predecessor result accepted")
			}

			result := managed.ProviderStepResult{Permission: permission.Permission, Outcome: managed.PortTerminal, ResourceID: "network-id", Evidence: []byte("actual-definitive-response"), ObservedAt: controllerClock(t, s)}
			if err = s.RecordManagedProviderStepResult(t.Context(), result); err != nil {
				t.Fatal(err)
			}

			if _, err = s.AuthorizeManagedProviderStep(t.Context(), next); err != nil {
				t.Fatal(err)
			}

			result.ResourceID = "foreign-id"
			if err = s.RecordManagedProviderStepResult(t.Context(), result); !errors.Is(err, managed.ErrConflict) {
				t.Fatalf("result mutation: %v", err)
			}

			f.must(s.SealManagedTarget(t.Context(), f.command()))

			denied := next
			denied.Command = f.command()

			denied.Step.Service = "sidecar"
			if _, err = s.AuthorizeManagedProviderStep(t.Context(), denied); err == nil {
				t.Fatal("sealed target issued")
			}
		})
	}
}

func TestManagedControllerAccessAudit(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := controllerStore(t, backend)
			q := managed.AccessAuditRequest{AttemptID: id.New(managed.PrefixOperation), Installation: "installation", Authority: "authority", ApplicationID: "app", OrganizationID: "org", Actor: managed.AuditActor{Unauthenticated: true}, Scope: managed.AuditScope{Kind: "denial", DenialScope: "configured-denial"}, Action: "instances.list", ResourceKind: "instance", ResourceID: "collection", Outcome: "denied", Reason: "unauthenticated", OccurredAt: time.Now().UTC().Add(-time.Second)}

			first, err := s.RecordManagedAccessAudit(t.Context(), q)
			if err != nil {
				t.Fatal(err)
			}

			retry, err := s.RecordManagedAccessAudit(t.Context(), q)
			if err != nil || first != retry {
				t.Fatalf("access replay: %v", err)
			}

			changed := q

			changed.ResourceID = "foreign-row"
			if _, err = s.RecordManagedAccessAudit(t.Context(), changed); !errors.Is(err, managed.ErrConflict) {
				t.Fatalf("access conflict: %v", err)
			}

			query := managed.AuditPageRequest{Installation: q.Installation, Authority: q.Authority, Scope: q.Scope, Limit: 1}

			page, err := s.ListManagedAuditIntents(t.Context(), query)
			if err != nil || len(page.Items) != 1 {
				t.Fatalf("access intent: %v", err)
			}

			raw, err := json.Marshal(page.Items[0])
			if err != nil {
				t.Fatal(err)
			}

			ack := managed.AuditAcknowledgment{Intent: page.Items[0], Receipt: managed.AuditSinkReceipt{IntentID: q.AttemptID, IntentDigest: managed.Digest(raw), MappingVersion: "1", SourceKey: q.AttemptID.String(), SourceDigest: first.Digest, Installation: q.Installation, Authority: q.Authority, ApplicationID: q.ApplicationID, OrganizationID: q.OrganizationID, NormalizedFingerprint: managed.Digest([]byte("normalized-fixture")), SinkReceiptID: "sink-receipt", Receipt: []byte("complete-sink-receipt"), AcceptedAt: time.Now().UTC()}}
			wrong := ack

			wrong.Receipt.Authority = "other"
			if err = s.AcknowledgeManagedAudit(t.Context(), wrong); err == nil {
				t.Fatal("wrong authority acknowledgment accepted")
			}

			if err = s.AcknowledgeManagedAudit(t.Context(), ack); err != nil {
				t.Fatal(err)
			}

			if err = s.AcknowledgeManagedAudit(t.Context(), ack); err != nil {
				t.Fatal(err)
			}

			page, err = s.ListManagedAuditIntents(t.Context(), query)
			if err != nil || len(page.Items) != 0 {
				t.Fatalf("ack persistence: %v", err)
			}

			for _, scope := range []managed.AuditScope{{Kind: "tenant"}, {Kind: "global", TenantID: "foreign"}, {Kind: "denial"}} {
				invalid := q
				invalid.AttemptID = id.New(managed.PrefixOperation)

				invalid.Scope = scope
				if _, err = s.RecordManagedAccessAudit(t.Context(), invalid); err == nil {
					t.Fatal("blank or inconsistent scope accepted")
				}
			}
		})
	}
}

func TestManagedControllerContinuation(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := controllerStore(t, backend)
			target := controllerAction(t).Key
			second := controllerAction(t).Key
			key := managed.ControllerKey{Installation: "installation", Authority: "authority", Name: "sweep", Scope: managed.AuditScope{Kind: "global"}}
			q := managed.ContinuationAdvance{Key: key, ID: id.New(managed.PrefixOperation), Owner: "one", Duration: managed.MaxLease, Pending: []managed.Key{second}, InFlight: &target}

			first, err := s.AdvanceManagedContinuation(t.Context(), q)
			if err != nil {
				t.Fatal(err)
			}

			recovered, err := s.ReadManagedContinuation(t.Context(), key)
			if err != nil || !reflect.DeepEqual(first, recovered) {
				t.Fatalf("recover pending: %v", err)
			}

			retry, err := s.AdvanceManagedContinuation(t.Context(), q)
			if err != nil || !reflect.DeepEqual(first, retry) {
				t.Fatalf("cursor replay: %v", err)
			}

			other := q
			other.ID = id.New(managed.PrefixOperation)
			other.Owner = "two"
			other.ExpectedRevision = first.Revision

			other.LeaseEpoch = first.LeaseEpoch
			if _, err = s.AdvanceManagedContinuation(t.Context(), other); !errors.Is(err, managed.ErrConflict) {
				t.Fatalf("competing cursor: %v", err)
			}

			other.Owner = "one"
			other.Pending = nil

			other.InFlight = &second
			if _, err = s.AdvanceManagedContinuation(t.Context(), other); err != nil {
				t.Fatalf("later-page progress: %v", err)
			}

			other.ID = id.New(managed.PrefixOperation)

			other.Pending = make([]managed.Key, managed.MaxControllerPage+1)
			if _, err = s.AdvanceManagedContinuation(t.Context(), other); !errors.Is(err, managed.ErrInvalid) {
				t.Fatalf("bound: %v", err)
			}
		})
	}
}

// cancellationProbe fails a particular authority checkpoint without changing production code.
type cancellationProbe struct{ calls, fail int }

func (c *cancellationProbe) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *cancellationProbe) Done() <-chan struct{}       { return nil }
func (c *cancellationProbe) Value(any) any               { return nil }
func (c *cancellationProbe) Err() error {
	c.calls++
	if c.calls >= c.fail {
		return context.Canceled
	}

	return nil
}

func TestManagedControllerMemoryAtomicCancellation(t *testing.T) {
	failures := 0
	success := false

	for checkpoint := 1; checkpoint <= 24; checkpoint++ {
		s := memory.New()
		q := controllerAction(t)
		probe := &cancellationProbe{fail: checkpoint}

		_, err := s.SubmitManagedAction(probe, q)
		if err == nil {
			success = true

			break
		}

		failures++

		if _, err = s.ReadManagedTarget(t.Context(), q.Key); !errors.Is(err, managed.ErrNotFound) {
			t.Fatalf("checkpoint %d leaked target: %v", checkpoint, err)
		}

		page, err := s.ListManagedAuditIntents(t.Context(), auditQuery(q))
		if err != nil || len(page.Items) != 0 {
			t.Fatalf("checkpoint %d leaked audit: %v", checkpoint, err)
		}

		if _, err = s.SubmitManagedAction(t.Context(), q); err != nil {
			t.Fatalf("checkpoint %d retry: %v", checkpoint, err)
		}
	}

	if failures < 7 || !success {
		t.Fatalf("did not cover acceptance checkpoints: failures=%d success=%v", failures, success)
	}
}

func injectControllerFailure(t *testing.T, s *postgres.Store, kind string) {
	t.Helper()

	pg := pgdriver.Unwrap(s.DB())

	query := fmt.Sprintf(`CREATE FUNCTION fail_controller_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='%s' THEN RAISE EXCEPTION 'injected controller failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_controller BEFORE INSERT ON cp_managed_controller_records FOR EACH ROW EXECUTE FUNCTION fail_controller_write();`, kind)
	if _, err := pg.Exec(t.Context(), query); err != nil {
		t.Fatal(err)
	}
}
func clearControllerFailure(t *testing.T, s *postgres.Store) {
	t.Helper()

	if _, err := pgdriver.Unwrap(s.DB()).Exec(t.Context(), "DROP TRIGGER fail_controller ON cp_managed_controller_records; DROP FUNCTION fail_controller_write()"); err != nil {
		t.Fatal(err)
	}
}

func TestManagedControllerPostgresAtomicFailures(t *testing.T) {
	for _, kind := range []string{"operation", "action", "audit"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := managedPG(t)
			q := controllerAction(t)
			injectControllerFailure(t, s, kind)

			if _, err := s.SubmitManagedAction(t.Context(), q); err == nil {
				t.Fatal("injected failure accepted")
			}

			if _, err := s.ReadManagedTarget(t.Context(), q.Key); !errors.Is(err, managed.ErrNotFound) {
				t.Fatalf("partial target: %v", err)
			}

			clearControllerFailure(t, s)

			if _, err := s.SubmitManagedAction(t.Context(), q); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func controllerClock(t *testing.T, s managedservice.Authority) time.Time {
	t.Helper()

	if pg, ok := s.(*postgres.Store); ok {
		var micros int64
		if err := pgdriver.Unwrap(pg.DB()).NewRaw("SELECT (extract(epoch FROM clock_timestamp())*1000000)::bigint").Scan(t.Context(), &micros); err != nil {
			t.Fatal(err)
		}

		return time.UnixMicro(micros).UTC()
	}

	return time.Now().UTC()
}

func TestManagedControllerConcurrentAAndAcceptance(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s, peer managedservice.Authority
			if backend == "memory" {
				s = memory.New()
				peer = s
			} else {
				pg, dsn := managedPG(t)
				s = pg
				peer = openManagedPG(t, dsn)
			}

			q, f, _ := acceptedController(t, s)
			before := f.target()
			command := f.command()
			pause := q
			pause.Command = command
			pause.Kind = managed.ActionPause
			pause.NewTarget = nil
			pause.ReleaseID = id.Nil
			pause.ExpectedDesiredRevision = before.DesiredRevision
			signControllerAction(t, &pause)

			direct := command
			direct.ID = id.New(managed.PrefixOperation)
			start := make(chan struct{})
			results := make(chan error, 2)

			go func() { <-start; _, err := s.SubmitManagedAction(t.Context(), pause); results <- err }()
			go func() {
				<-start

				_, err := peer.SetManagedDesired(t.Context(), managed.DesiredRequest{Command: direct, Mode: managed.Retiring})
				results <- err
			}()

			close(start)

			one, two := <-results, <-results
			if (one == nil) == (two == nil) {
				t.Fatalf("expected exactly one CAS winner: %v %v", one, two)
			}

			if one != nil && !errors.Is(one, managed.ErrConflict) || two != nil && !errors.Is(two, managed.ErrConflict) {
				t.Fatalf("unexpected refusal: %v %v", one, two)
			}

			if f.target().Revision != before.Revision+1 {
				t.Fatal("overlapping backend changed target twice")
			}
		})
	}
}

func TestManagedControllerCompleteRemovalParents(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := controllerStore(t, backend)

			q, f, projection := acceptedController(t, s)
			if _, err := s.ProjectManagedRows(t.Context(), projection); err != nil {
				t.Fatal(err)
			}

			f.must(s.IssueManagedProvision(t.Context(), f.command()))
			f.must(s.BindManagedResources(t.Context(), managed.ResourcesRequest{Command: f.command(), OperationID: q.NewTarget.ProvisionOperationID, Receipt: []byte("full provider fixture result"), Resources: map[string]string{"network": "network-id", "api": "api-id", "sidecar": "sidecar-id"}}))
			first := f.accepted("a", "runtime-polling")
			second := f.accepted("b", "runtime-query")
			f.must(s.SealManagedTarget(t.Context(), f.command()))
			f.closeBinding(first)

			if _, err := s.AdmitManagedRemoval(t.Context(), managed.AdmissionRequest{Command: f.command(), OperationID: id.New(managed.PrefixOperation), Verifier: "verifier"}); !errors.Is(err, managed.ErrBlocked) {
				t.Fatalf("query role skipped closure: %v", err)
			}

			f.closeBinding(second)
			f.admit()
			step := managed.ProviderStepAuthorization{Command: f.command(), Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: q.Key, ActionID: q.ID}, ProviderOperationID: f.target().Deletion.OperationID, Kind: managed.StepRemoveContainer, Service: "api"}}
			before := f.target()
			wrong := step

			wrong.Step.ProviderOperationID = id.New(managed.PrefixOperation)
			if _, err := s.AuthorizeManagedProviderStep(t.Context(), wrong); err == nil {
				t.Fatal("wrong attempt issued")
			}

			if !reflect.DeepEqual(before, f.target()) {
				t.Fatal("failed parent check changed A")
			}

			grant, err := s.AuthorizeManagedProviderStep(t.Context(), step)
			if err != nil || !grant.NewlyIssued || grant.Permission.FenceDigest != before.Deletion.FenceDigest || grant.Permission.ResourceID != "api-id" {
				t.Fatalf("full parent issue: %+v %v", grant, err)
			}
		})
	}
}

func TestManagedControllerAccessAtomicFailure(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := controllerStore(t, backend)
			q := managed.AccessAuditRequest{AttemptID: id.New(managed.PrefixOperation), Installation: "installation", Authority: "authority", ApplicationID: "app", OrganizationID: "org", Actor: managed.AuditActor{Subject: "verified"}, Scope: managed.AuditScope{Kind: "tenant", TenantID: "tenant"}, Action: "health.summary", ResourceKind: "health", ResourceID: "collection", Outcome: "allowed", Reason: "authorized", OccurredAt: time.Now().UTC().Add(-time.Second)}

			if pg, ok := s.(*postgres.Store); ok {
				injectControllerFailure(t, pg, "audit")

				if _, err := s.RecordManagedAccessAudit(t.Context(), q); err == nil {
					t.Fatal("outbox failure accepted")
				}

				clearControllerFailure(t, pg)
			} else {
				// Failure at the outbox write follows the staged access receipt.
				probe := &cancellationProbe{fail: 4}
				if _, err := s.RecordManagedAccessAudit(probe, q); err == nil {
					t.Fatal("outbox cancellation accepted")
				}
			}

			changed := q
			changed.Reason = "resource_unavailable"

			changed.Outcome = "unavailable"
			if _, err := s.RecordManagedAccessAudit(t.Context(), changed); err != nil {
				t.Fatalf("rolled-back receipt leaked: %v", err)
			}
		})
	}
}

func TestManagedControllerPostgresRestart(t *testing.T) {
	fixture := os.Getenv("CTRLPLANE_B21_FIXTURE_ID")

	label := os.Getenv("CTRLPLANE_B21_FIXTURE_LABEL")
	if fixture == "" || label == "" {
		t.Skip("owned B2.1 restart fixture not configured")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	inspect, err := exec.CommandContext(ctx, "docker", "inspect", fixture, "--format", "{{.Id}} {{index .Config.Labels \"ctrlplane.task\"}}").Output()
	if err != nil {
		t.Fatal(err)
	}

	if strings.TrimSpace(string(inspect)) != fixture+" "+strings.TrimPrefix(label, "ctrlplane.task=") {
		t.Fatal("restart fixture identity mismatch")
	}

	dsn := os.Getenv("CTRLPLANE_TEST_POSTGRES_DSN")

	s := openManagedPG(t, dsn)
	if err = s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}

	q, f, projection := acceptedController(t, s)

	projected, err := s.ProjectManagedRows(t.Context(), projection)
	if err != nil {
		t.Fatal(err)
	}

	before := f.target()

	journal, err := s.AdvanceManagedOperation(t.Context(), managed.OperationAdvance{Command: f.command(), ActionID: q.ID, ExpectedOperationRevision: 1, Observation: managed.OperationObservation{Kind: "retry", ObservedAt: controllerClock(t, s), RetryAt: controllerClock(t, s).Add(time.Minute), Diagnostic: "retry-fixture"}})
	if err != nil {
		t.Fatal(err)
	}

	key := managed.ControllerKey{Installation: q.Installation, Authority: q.Authority, Name: q.ID.String(), Scope: managed.AuditScope{Kind: "tenant", TenantID: q.TenantID}}

	cursor, err := s.AdvanceManagedContinuation(t.Context(), managed.ContinuationAdvance{Key: key, ID: id.New(managed.PrefixOperation), Owner: "restart-controller", Duration: managed.MaxLease, InFlight: &q.Key})
	if err != nil {
		t.Fatal(err)
	}

	var (
		systemBefore string
		startBefore  int64
	)

	pg := pgdriver.Unwrap(s.DB())
	if err = pg.NewRaw("SELECT system_identifier::text FROM pg_control_system()").Scan(t.Context(), &systemBefore); err != nil {
		t.Fatal(err)
	}

	if err = pg.NewRaw("SELECT (extract(epoch FROM pg_postmaster_start_time())*1000000)::bigint").Scan(t.Context(), &startBefore); err != nil {
		t.Fatal(err)
	}

	if out, err := exec.CommandContext(ctx, "docker", "stop", "--time", "10", fixture).CombinedOutput(); err != nil {
		t.Fatalf("owned stop: %s %v", out, err)
	}

	if out, err := exec.CommandContext(ctx, "docker", "start", fixture).CombinedOutput(); err != nil {
		t.Fatalf("owned start: %s %v", out, err)
	}

	portBytes, err := exec.CommandContext(ctx, "docker", "port", fixture, "5432/tcp").Output()
	if err != nil {
		t.Fatal(err)
	}

	address := strings.TrimSpace(string(portBytes))
	if !strings.HasPrefix(address, "127.0.0.1:") {
		t.Fatal("fixture rebound off loopback")
	}

	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}

	parsed.Host = address
	deadline := time.Now().Add(20 * time.Second)

	for {
		if err = exec.CommandContext(ctx, "docker", "exec", fixture, "pg_isready", "-U", "b21", "-d", "b21").Run(); err == nil {
			break
		}

		if time.Now().After(deadline) {
			t.Fatal("owned fixture did not recover")
		}

		time.Sleep(100 * time.Millisecond)
	}

	peer := openManagedPG(t, parsed.String())

	var (
		systemAfter string
		startAfter  int64
	)

	pg = pgdriver.Unwrap(peer.DB())
	if err = pg.NewRaw("SELECT system_identifier::text FROM pg_control_system()").Scan(t.Context(), &systemAfter); err != nil {
		t.Fatal(err)
	}

	if err = pg.NewRaw("SELECT (extract(epoch FROM pg_postmaster_start_time())*1000000)::bigint").Scan(t.Context(), &startAfter); err != nil {
		t.Fatal(err)
	}

	if systemBefore != systemAfter || startAfter <= startBefore {
		t.Fatal("persistent server restart not established")
	}

	t.Logf("persistent system identifier %s; postmaster %s -> %s", systemAfter, time.UnixMicro(startBefore).UTC().Format(time.RFC3339Nano), time.UnixMicro(startAfter).UTC().Format(time.RFC3339Nano))

	target, err := peer.ReadManagedTarget(t.Context(), q.Key)
	if err != nil || !reflect.DeepEqual(target, before) {
		t.Fatalf("target restart: %v", err)
	}

	replay, err := peer.SubmitManagedAction(t.Context(), q)
	if err != nil || !reflect.DeepEqual(replay, journal.Receipt) {
		t.Fatalf("action restart: %v", err)
	}

	operation, err := peer.ReadManagedOperation(t.Context(), journal.Key)
	if err != nil || !reflect.DeepEqual(operation, journal) {
		t.Fatalf("journal restart: %v", err)
	}

	recoveredCursor, err := peer.ReadManagedContinuation(t.Context(), key)
	if err != nil || !reflect.DeepEqual(recoveredCursor, cursor) {
		t.Fatalf("cursor restart: %v", err)
	}

	projectionReplay, err := peer.ProjectManagedRows(t.Context(), projection)
	if err != nil || !reflect.DeepEqual(projectionReplay, projected) {
		t.Fatalf("projection restart: %v", err)
	}

	page, err := peer.ListManagedAuditIntents(t.Context(), auditQuery(q))
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("audit restart: %v", err)
	}
	// Refresh only the task-owned private DSN file when Docker changes its port.
	if err = os.Setenv("CTRLPLANE_TEST_POSTGRES_DSN", parsed.String()); err != nil { //nolint:usetesting // The owned restart may rebind the fixture used by subsequent sequential tests.
		t.Fatal(err)
	}

	if privatePath := os.Getenv("CTRLPLANE_B21_FIXTURE_DSN_FILE"); privatePath != "" {
		if err = os.WriteFile(privatePath, []byte(parsed.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestManagedControllerReplacementAtomicity(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := controllerStore(t, backend)

			q, f, projection := acceptedController(t, s)
			if _, err := s.ProjectManagedRows(t.Context(), projection); err != nil {
				t.Fatal(err)
			}

			replacement := controllerAction(t)
			replacement.Command = f.command()
			replacement.NewTarget.TenantID = q.TenantID

			replacement.ExpectedDesiredRevision = f.target().DesiredRevision
			for n := range replacement.NewTarget.Manifest.Members {
				replacement.NewTarget.Manifest.Members[n].OwnerID = q.NewTarget.Manifest.Members[0].OwnerID
			}

			signControllerAction(t, &replacement)

			before := f.target()

			if pg, ok := s.(*postgres.Store); ok {
				injectControllerFailure(t, pg, "audit")

				failed, err := s.SubmitManagedAction(t.Context(), replacement)
				if err == nil || !failed.ID.IsNil() {
					t.Fatal("failed replacement returned acceptance")
				}

				clearControllerFailure(t, pg)
			} else {
				// Fail after nested A acceptance, while retaining the exact outer transaction.
				failed := false

				for checkpoint := 1; checkpoint < 40; checkpoint++ {
					probe := &cancellationProbe{fail: checkpoint}

					_, err := s.SubmitManagedAction(probe, replacement)
					if err == nil {
						break
					}

					failed = true

					if !reflect.DeepEqual(f.target(), before) {
						t.Fatalf("partial replacement at %d", checkpoint)
					}

					if _, err = s.ReadManagedTarget(t.Context(), replacement.NewTarget.Key); !errors.Is(err, managed.ErrNotFound) {
						t.Fatalf("partial target at %d: %v", checkpoint, err)
					}
				}

				if !failed {
					t.Fatal("no cancellation exercised")
				}
			}

			receipt, err := s.SubmitManagedAction(t.Context(), replacement)
			if err != nil {
				t.Fatal(err)
			}

			replay, err := s.SubmitManagedAction(t.Context(), replacement)
			if err != nil || !reflect.DeepEqual(receipt, replay) {
				t.Fatalf("replacement replay: %v", err)
			}

			if f.target().Replacement == nil || f.target().Replacement.Key != replacement.NewTarget.Key {
				t.Fatal("replacement not coupled")
			}

			another := controllerAction(t)
			another.Command = f.command()
			another.NewTarget.TenantID = q.TenantID
			another.ExpectedDesiredRevision = f.target().DesiredRevision
			signControllerAction(t, &another)

			if _, err = s.SubmitManagedAction(t.Context(), another); !errors.Is(err, managed.ErrConflict) {
				t.Fatalf("second unresolved replacement: %v", err)
			}

			next := &managedFixture{t: t, s: s, k: replacement.NewTarget.Key}
			next.must(s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: next.command(), Duration: managed.MaxLease}))

			if _, err = s.ProjectManagedRows(t.Context(), managed.ProjectionRequest{Command: next.command(), ActionID: replacement.ID, Stage: managed.ProjectionPending}); err != nil {
				t.Fatalf("managed owner reuse: %v", err)
			}
		})
	}
}

func TestManagedControllerProviderPermissionRollback(t *testing.T) {
	s, _ := managedPG(t)

	q, f, projection := acceptedController(t, s)
	if _, err := s.ProjectManagedRows(t.Context(), projection); err != nil {
		t.Fatal(err)
	}

	request := managed.ProviderStepAuthorization{Command: f.command(), Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: q.Key, ActionID: q.ID}, ProviderOperationID: q.NewTarget.ProvisionOperationID, Kind: managed.StepCreateNetwork}}
	before := f.target()

	injectControllerFailure(t, s, "step")

	grant, err := s.AuthorizeManagedProviderStep(t.Context(), request)
	if err == nil || grant.NewlyIssued || !grant.Permission.CommandID.IsNil() {
		t.Fatal("failed ledger exposed grant")
	}

	if !reflect.DeepEqual(before, f.target()) {
		t.Fatal("nested A issue committed before ledger")
	}

	clearControllerFailure(t, s)

	grant, err = s.AuthorizeManagedProviderStep(t.Context(), request)
	if err != nil || !grant.NewlyIssued {
		t.Fatalf("rolled-back permission replay: %v", err)
	}

	proof := []byte(strings.Repeat("x", managed.MaxEvidence))

	result := managed.ProviderStepResult{Permission: grant.Permission, Outcome: managed.PortUnavailable, Evidence: proof, Diagnostic: strings.Repeat("d", 256), ObservedAt: controllerClock(t, s)}
	if err = s.RecordManagedProviderStepResult(t.Context(), result); err != nil {
		t.Fatal(err)
	}

	result.Outcome = managed.PortTerminal
	result.ResourceID = strings.Repeat("r", 256)

	result.ObservedAt = controllerClock(t, s)
	if err = s.RecordManagedProviderStepResult(t.Context(), result); err != nil {
		t.Fatalf("maximum terminal after uncertainty: %v", err)
	}

	if err = s.RecordManagedProviderStepResult(t.Context(), result); err != nil {
		t.Fatalf("terminal replay: %v", err)
	}

	result.Evidence = append(result.Evidence, 'x')
	if err = s.RecordManagedProviderStepResult(t.Context(), result); !errors.Is(err, managed.ErrInvalid) {
		t.Fatalf("oversized proof: %v", err)
	}
}

func TestManagedControllerPostLockPermission(t *testing.T) {
	s, dsn := managedPG(t)
	peer := openManagedPG(t, dsn)

	q, f, projection := acceptedController(t, s)
	if _, err := s.ProjectManagedRows(t.Context(), projection); err != nil {
		t.Fatal(err)
	}

	lease := f.must(s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: f.command(), Duration: 500 * time.Millisecond}))
	request := managed.ProviderStepAuthorization{Command: f.command(), Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: q.Key, ActionID: q.ID}, ProviderOperationID: q.NewTarget.ProvisionOperationID, Kind: managed.StepCreateNetwork}}

	err := releaseAfterStoreTime(t, s, q.Key, lease.LeaseUntil, func() error {
		_, issueErr := peer.AuthorizeManagedProviderStep(t.Context(), request)

		return issueErr
	})
	if !errors.Is(err, managed.ErrConflict) {
		t.Fatalf("expired lease after physical lock: %v", err)
	}

	if f.target().Provision.Issued {
		t.Fatal("expired permission issued")
	}

	if _, err = s.ReadManagedProviderStep(t.Context(), request.Step); !errors.Is(err, managed.ErrNotFound) {
		t.Fatalf("expired step retained: %v", err)
	}
}

func TestManagedControllerAuditPaging(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := controllerStore(t, backend)

			request := managed.AccessAuditRequest{Installation: "installation", Authority: "authority", ApplicationID: "app", OrganizationID: "org", Actor: managed.AuditActor{Subject: "operator"}, Scope: managed.AuditScope{Kind: "global"}, Action: "instances.list", ResourceKind: "instance", ResourceID: "collection", Outcome: "allowed", Reason: "authorized", OccurredAt: time.Now().UTC().Add(-time.Second)}
			for range 3 {
				request.AttemptID = id.New(managed.PrefixOperation)
				if _, err := s.RecordManagedAccessAudit(t.Context(), request); err != nil {
					t.Fatal(err)
				}
			}

			query := managed.AuditPageRequest{Installation: request.Installation, Authority: request.Authority, Scope: request.Scope, Limit: 1}

			page, err := s.ListManagedAuditIntents(t.Context(), query)
			if err != nil || len(page.Items) != 1 || page.NextCursor == "" {
				t.Fatalf("first page: %v", err)
			}

			first := page.Items[0]

			raw, err := json.Marshal(first)
			if err != nil {
				t.Fatal(err)
			}

			ack := managed.AuditAcknowledgment{Intent: first, Receipt: managed.AuditSinkReceipt{IntentID: first.ID, IntentDigest: managed.Digest(raw), MappingVersion: first.MappingVersion, SourceKey: first.SourceKey, SourceDigest: first.SourceDigest, Installation: first.Installation, Authority: first.Authority, ApplicationID: first.ApplicationID, OrganizationID: first.OrganizationID, NormalizedFingerprint: managed.Digest([]byte("normalized")), SinkReceiptID: "sink", Receipt: []byte("accepted"), AcceptedAt: controllerClock(t, s)}}
			if err = s.AcknowledgeManagedAudit(t.Context(), ack); err != nil {
				t.Fatal(err)
			}

			empty, err := s.ListManagedAuditIntents(t.Context(), query)
			if err != nil || len(empty.Items) != 0 || empty.NextCursor == "" {
				t.Fatalf("acknowledged first page starved cursor: %v", err)
			}

			query.Cursor = empty.NextCursor

			next, err := s.ListManagedAuditIntents(t.Context(), query)
			if err != nil || len(next.Items) != 1 || next.Items[0].ID == first.ID {
				t.Fatalf("later intent unreachable: %v", err)
			}

			query.Scope = managed.AuditScope{Kind: "tenant", TenantID: "foreign"}
			if _, err = s.ListManagedAuditIntents(t.Context(), query); !errors.Is(err, managed.ErrInvalid) {
				t.Fatalf("scope-transplanted cursor: %v", err)
			}

			collision := controllerAction(t)
			collision.ID = first.ID
			signControllerAction(t, &collision)

			if _, err = s.SubmitManagedAction(t.Context(), collision); !errors.Is(err, managed.ErrConflict) {
				t.Fatalf("source attempt reused across kinds: %v", err)
			}
		})
	}
}
