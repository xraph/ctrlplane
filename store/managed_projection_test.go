package store_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/managedservice"
	"github.com/xraph/ctrlplane/store"
	"github.com/xraph/ctrlplane/store/memory"
	"github.com/xraph/ctrlplane/workload"
)

func TestManagedProjectionReservedRows(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			authority := controllerStore(t, backend)
			s := authority.(store.Store)
			q, f, request := acceptedController(t, authority)

			owner := q.NewTarget.Manifest.Members[0].OwnerID
			if _, err := s.GetByID(t.Context(), q.TenantID, q.InstanceID); !errors.Is(err, ctrlplane.ErrNotFound) {
				t.Fatalf("instance visible before projection: %v", err)
			}

			if _, err := s.GetWorkloadByID(t.Context(), q.TenantID, owner); !errors.Is(err, ctrlplane.ErrNotFound) {
				t.Fatalf("owner visible before projection: %v", err)
			}

			receipt, err := authority.ProjectManagedRows(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}

			inst, err := s.GetByID(t.Context(), q.TenantID, q.InstanceID)
			if err != nil {
				t.Fatal(err)
			}

			release, err := s.GetRelease(t.Context(), q.TenantID, q.ReleaseID)
			if err != nil {
				t.Fatal(err)
			}

			if inst.State != "provisioning" || release.Active || len(release.Services) != 2 || len(inst.Services) != 2 || len(inst.Services[0].Env) != 0 {
				t.Fatal("projection invented success or private configuration")
			}

			binding, err := authority.ReadManagedRelease(t.Context(), managed.ReleaseKey{TenantID: q.TenantID, ReleaseID: q.ReleaseID})
			if err != nil || !reflect.DeepEqual(binding.Manifest, q.NewTarget.Manifest) {
				t.Fatalf("startup/role association lost: %v", err)
			}

			replay, err := authority.ProjectManagedRows(t.Context(), request)
			if err != nil || !reflect.DeepEqual(receipt, replay) {
				t.Fatalf("projection replay: %v", err)
			}

			if f.target().Provision.Issued {
				t.Fatal("projection granted issue")
			}

			if _, err = authority.ReadManagedRelease(t.Context(), managed.ReleaseKey{TenantID: "foreign", ReleaseID: q.ReleaseID}); !errors.Is(err, managed.ErrNotFound) {
				t.Fatalf("foreign release read: %v", err)
			}

			if err = s.Delete(t.Context(), q.TenantID, q.InstanceID); err != nil {
				t.Fatal(err)
			}

			if _, err = authority.ProjectManagedRows(t.Context(), request); err == nil {
				t.Fatal("orphan projection replay accepted")
			}

			if _, err = s.GetByID(t.Context(), q.TenantID, q.InstanceID); !errors.Is(err, ctrlplane.ErrNotFound) {
				t.Fatal("replay resurrected row")
			}
		})
	}
}

func TestManagedProjectionForeignCollision(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			authority := controllerStore(t, backend)
			s := authority.(store.Store)
			q := controllerAction(t)

			foreign := &instance.Instance{Entity: ctrlplane.Entity{ID: q.InstanceID}, TenantID: "foreign", Name: "foreign", Slug: q.InstanceID.String(), ProviderName: "docker", State: "provisioning"}
			if err := s.Insert(t.Context(), foreign); err != nil {
				t.Fatal(err)
			}

			if _, err := authority.SubmitManagedAction(t.Context(), q); !errors.Is(err, managed.ErrConflict) {
				t.Fatalf("adopted instance: %v", err)
			}

			q = controllerAction(t)
			owner := q.NewTarget.Manifest.Members[0].OwnerID
			existing := workload.NewWorkload()
			existing.ID = owner
			existing.TenantID = q.TenantID
			existing.Name = "generic"

			existing.Slug = owner.String()
			if err := s.InsertWorkload(t.Context(), existing); err != nil {
				t.Fatal(err)
			}

			if _, err := authority.SubmitManagedAction(t.Context(), q); !errors.Is(err, managed.ErrConflict) {
				t.Fatalf("adopted generic owner: %v", err)
			}

			if _, err := authority.ReadManagedTarget(t.Context(), q.Key); !errors.Is(err, managed.ErrNotFound) {
				t.Fatalf("failed reservation leaked target: %v", err)
			}
		})
	}
}

func TestManagedProjectionPostgresAtomicFailures(t *testing.T) {
	for _, kind := range []string{"release", "projection", "target_projection"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := managedPG(t)
			q, _, request := acceptedController(t, s)
			injectControllerFailure(t, s, kind)

			if _, err := s.ProjectManagedRows(t.Context(), request); err == nil {
				t.Fatal("injected projection failure accepted")
			}

			if _, err := s.GetByID(t.Context(), q.TenantID, q.InstanceID); !errors.Is(err, ctrlplane.ErrNotFound) {
				t.Fatalf("partial instance: %v", err)
			}

			if _, err := s.GetRelease(t.Context(), q.TenantID, q.ReleaseID); !errors.Is(err, ctrlplane.ErrNotFound) {
				t.Fatalf("partial release: %v", err)
			}

			if _, err := s.GetWorkloadByID(t.Context(), q.TenantID, q.NewTarget.Manifest.Members[0].OwnerID); !errors.Is(err, ctrlplane.ErrNotFound) {
				t.Fatalf("partial owner: %v", err)
			}

			clearControllerFailure(t, s)

			if _, err := s.ProjectManagedRows(t.Context(), request); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestManagedProjectionMemoryAtomicCancellation(t *testing.T) {
	failures := 0
	success := false

	for checkpoint := 1; checkpoint <= 40; checkpoint++ {
		s := memory.New()
		q, _, request := acceptedController(t, s)

		probe := &cancellationProbe{fail: checkpoint}
		if _, err := s.ProjectManagedRows(probe, request); err == nil {
			success = true

			break
		}

		failures++

		if _, err := s.GetByID(t.Context(), q.TenantID, q.InstanceID); !errors.Is(err, ctrlplane.ErrNotFound) {
			t.Fatalf("checkpoint %d partial instance: %v", checkpoint, err)
		}

		if _, err := s.GetRelease(t.Context(), q.TenantID, q.ReleaseID); !errors.Is(err, ctrlplane.ErrNotFound) {
			t.Fatalf("checkpoint %d partial release: %v", checkpoint, err)
		}

		if _, err := s.ProjectManagedRows(t.Context(), request); err != nil {
			t.Fatalf("checkpoint %d retry: %v", checkpoint, err)
		}
	}

	if failures < 10 || !success {
		t.Fatalf("projection checkpoints incomplete: %d %v", failures, success)
	}
}

func TestManagedControllerStrictSpec(t *testing.T) {
	q := controllerAction(t)
	m := q.NewTarget.Manifest

	parsed, err := managed.ParseManagedSpec(m)
	if err != nil {
		t.Fatal(err)
	}

	if parsed.RuntimeRoles[0] != managed.Polling || parsed.RuntimeRoles[1] != managed.RetainedQuery {
		t.Fatal("member roles reordered")
	}

	original := string(m.Spec)

	cases := map[string]string{
		"missing_profile":   strings.Replace(original, `"profile":"ctrlplane.managed.v1",`, "", 1),
		"duplicate_profile": strings.Replace(original, `"profile":`, `"profile":"ctrlplane.managed.v1","profile":`, 1),
		"unknown_field":     strings.Replace(original, `"profile":`, `"extra":1,"profile":`, 1),
		"unknown_role":      strings.Replace(original, `"polling"`, `"worker"`, 1),
		"null_role":         strings.Replace(original, `"polling"`, `null`, 1),
		"null_provision":    `{"profile":"ctrlplane.managed.v1","runtime_roles":["polling","retained_query"],"provision":null}`,
		"mutable_binding":   strings.Replace(original, `"binding_generation":"1"`, `"binding_generation":"latest"`, 1),
		"leading_zero":      strings.Replace(original, `"cpu_millis":"100"`, `"cpu_millis":"0100"`, 1),
		"overflow":          strings.Replace(original, `"cpu_millis":"100"`, `"cpu_millis":"9223372036854775807"`, 1),
		"caller_env":        strings.Replace(original, `"version":"1",`, `"version":"1","env":{"secret":"forbidden"},`, 1),
		"too_big":           original + strings.Repeat(" ", managed.MaxEvidence),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			invalid := m
			invalid.Spec = []byte(raw)

			invalid.SpecDigest = managed.Digest(invalid.Spec)
			if _, err := managed.ParseProvisionSpec(invalid); err == nil {
				t.Fatal("invalid spec accepted")
			}
		})
	}

	legacy := managedIdentity()

	s := memory.New()
	if _, err := s.CreateManagedTarget(t.Context(), legacy); err != nil {
		t.Fatal(err)
	}

	read, err := s.ReadManagedTarget(t.Context(), legacy.Key)
	if err != nil || string(read.Manifest.Spec) != string(legacy.Manifest.Spec) {
		t.Fatal("legacy bytes changed")
	}

	if _, err = managed.ParseManagedSpec(read.Manifest); err == nil {
		t.Fatal("legacy profile invented")
	}
}

func TestManagedControllerEncodedBounds(t *testing.T) {
	q := controllerAction(t)
	maximum := strings.Repeat("x", 256)
	q.Installation = maximum
	q.Authority = maximum
	q.ApplicationID = maximum
	q.OrganizationID = maximum
	q.Actor.Subject = maximum
	q.TenantID = maximum
	q.NewTarget.TenantID = maximum
	q.NewTarget.Physical = managed.Physical{Provider: maximum, Placement: maximum, Project: maximum, Generation: maximum}
	owner := q.NewTarget.Manifest.Members[0].OwnerID

	members := make([]managed.Member, managed.MaxMembers)
	for n := range members {
		members[n] = managed.Member{Name: strings.Repeat("n", 254) + fmt.Sprintf("%02d", n), Service: maximum, OwnerID: owner, Installation: maximum, Namespace: strings.Repeat("s", 254) + fmt.Sprintf("%02d", n), Queue: maximum, Build: maximum, ArtifactDigest: strings.Repeat("a", 64), ConfigurationDigest: strings.Repeat("b", 64)}
	}

	q.NewTarget.Manifest.Members = members

	envelope := managed.ManagedSpec{Profile: "ctrlplane.managed.v1", RuntimeRoles: make([]managed.RuntimeRole, managed.MaxMembers), Provision: json.RawMessage(`{}`)}
	for n := range envelope.RuntimeRoles {
		envelope.RuntimeRoles[n] = managed.RetainedQuery
	}

	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}

	if len(raw) != 611 {
		t.Fatalf("32-role envelope: %d", len(raw))
	}

	provision := managed.ProvisionSpec{Version: "1"}

	for n := range 4 {
		name := maximum
		if n > 0 {
			name = fmt.Sprintf("service%d", n)
		}

		role := "sidecar"
		if n == 0 {
			role = "main"
		}

		provision.Services = append(provision.Services, managed.ProvisionService{Name: name, Image: strings.Repeat("i", 184) + "@sha256:" + strings.Repeat("a", 64), Role: role, CPUMillis: "9223372036854", MemoryMiB: "8796093022207", PIDLimit: "9223372036854775807", Startup: managed.StartupBindingRef{BindingID: maximum, BindingGeneration: "9223372036854775807", BindingDigest: strings.Repeat("b", 64)}})
	}

	encode := func() {
		p, marshalErr := json.Marshal(provision)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}

		envelope.Provision = p

		raw, marshalErr = json.Marshal(envelope)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
	}
	encode()

	if len(raw) > managed.MaxEvidence {
		provision.Services[3].Startup.BindingID = provision.Services[3].Startup.BindingID[:256-(len(raw)-managed.MaxEvidence)]

		encode()
	}
	// Grow additional valid service names until the exact total Spec boundary.
	for n := 1; n < len(provision.Services) && len(raw) < managed.MaxEvidence; n++ {
		extra := min(256-len(provision.Services[n].Name), managed.MaxEvidence-len(raw))
		provision.Services[n].Name += strings.Repeat("n", extra)

		encode()
	}

	if len(raw) != managed.MaxEvidence {
		t.Fatalf("maximum valid Spec fixture: %d", len(raw))
	}

	q.NewTarget.Manifest.Spec = raw
	q.NewTarget.Manifest.SpecDigest = managed.Digest(raw)
	q.OriginalCommand = []byte(strings.Repeat("x", managed.MaxEvidence))
	signControllerAction(t, &q)

	if _, err = managed.ParseProvisionSpec(q.NewTarget.Manifest); err != nil {
		t.Fatal(err)
	}

	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := controllerStore(t, backend)

			receipt, acceptErr := s.SubmitManagedAction(t.Context(), q)
			if acceptErr != nil {
				t.Fatal(acceptErr)
			}

			f := &managedFixture{t: t, s: s, k: q.Key}
			f.must(s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: f.command(), Duration: managed.MaxLease}))
			command := managed.PortCommand{SchemaVersion: "1", OperationID: id.New(managed.PrefixOperation), RequestID: maximum, Original: q.OriginalCommand, OriginalDigest: managed.Digest(q.OriginalCommand), Native: q.OriginalCommand, NativeDigest: managed.Digest(q.OriginalCommand)}

			completed, advanceErr := s.AdvanceManagedOperation(t.Context(), managed.OperationAdvance{Command: f.command(), ActionID: q.ID, ExpectedOperationRevision: 1, Observation: managed.OperationObservation{Kind: "completed", Command: &command, Evidence: q.OriginalCommand, Diagnostic: maximum, ObservedAt: controllerClock(t, s)}})
			if advanceErr != nil {
				t.Fatal(advanceErr)
			}

			encoded, marshalErr := json.Marshal(completed)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}

			t.Logf("accepted maximum fields plus completion: %d bytes", len(encoded))

			if len(encoded)+100 > managed.MaxControllerRecord {
				t.Fatal("terminal history envelope exhausted")
			}

			replay, replayErr := s.SubmitManagedAction(t.Context(), q)
			if replayErr != nil || !reflect.DeepEqual(receipt, replay) {
				t.Fatalf("maximum replay: %v", replayErr)
			}

			tooBig := q
			tooBig.ID = id.New(managed.PrefixOperation)

			tooBig.OriginalCommand = append(append([]byte(nil), q.OriginalCommand...), 'x')
			signControllerAction(t, &tooBig)

			if _, acceptErr = s.SubmitManagedAction(t.Context(), tooBig); acceptErr == nil {
				t.Fatal("over-limit command accepted")
			}

			key := managed.ControllerKey{Installation: maximum, Authority: maximum, Name: maximum, Scope: managed.AuditScope{Kind: "tenant", TenantID: maximum}}

			pending := make([]managed.Key, managed.MaxControllerPage)
			for n := range pending {
				pending[n] = managed.Key{TenantID: maximum, InstanceID: id.New(id.PrefixInstance)}
			}

			inFlight := managed.Key{TenantID: maximum, InstanceID: id.New(id.PrefixInstance)}
			cursorRaw, _ := json.Marshal([2]string{maximum, inFlight.InstanceID.String()})
			advance := managed.ContinuationAdvance{Key: key, ID: id.New(managed.PrefixOperation), Owner: maximum, Duration: managed.MaxLease, Pending: pending, InFlight: &inFlight, Cursor: base64.RawURLEncoding.EncodeToString(cursorRaw)}

			continuation, cursorErr := s.AdvanceManagedContinuation(t.Context(), advance)
			if cursorErr != nil {
				t.Fatal(cursorErr)
			}

			encoded, marshalErr = json.Marshal(continuation)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}

			t.Logf("accepted maximum continuation: %d bytes", len(encoded))

			if len(encoded)+100 > managed.MaxContinuation {
				t.Fatal("cursor receipt capacity exhausted")
			}

			advance.ID = id.New(managed.PrefixOperation)

			advance.Pending = append(advance.Pending, managed.Key{TenantID: maximum, InstanceID: id.New(id.PrefixInstance)})
			if _, cursorErr = s.AdvanceManagedContinuation(t.Context(), advance); cursorErr == nil {
				t.Fatal("65 pending keys accepted")
			}
		})
	}
}

var _ managedservice.Authority = (*memory.Store)(nil)
var _ context.Context = (*cancellationProbe)(nil)

func TestManagedControllerRoleBinding(t *testing.T) {
	q := controllerAction(t)
	target := managed.Target{Identity: *q.NewTarget}
	registration := managed.Registration{ID: id.New(managed.PrefixReservation), Member: target.Manifest.Members[0].Name, RuntimeID: "runtime", InstanceID: "native", HostID: "host"}
	target.Reservations = []managed.Reservation{{Registration: registration}}

	binding := managed.RoleBinding{Target: managed.IdentityWire(target.Identity), MemberIndex: "0", MemberName: registration.Member, Role: managed.Polling, ReservationID: registration.ID, ReservationVersion: "1", RuntimeID: registration.RuntimeID, InstanceID: registration.InstanceID, HostID: registration.HostID, NativeBuild: target.Manifest.Members[0].Build, ConfigurationDigest: target.Manifest.Members[0].ConfigurationDigest, EnrollmentEvidence: []byte("accepted-native-enrollment")}
	if err := managed.ValidateRoleBinding(target, binding); err != nil {
		t.Fatal(err)
	}

	wrong := binding

	wrong.Role = managed.RetainedQuery
	if err := managed.ValidateRoleBinding(target, wrong); !errors.Is(err, managed.ErrAuthority) {
		t.Fatalf("role substitution: %v", err)
	}

	other := registration
	other.ID = id.New(managed.PrefixReservation)
	other.Member = target.Manifest.Members[1].Name
	other.InstanceID = "other-native"

	target.Reservations = append(target.Reservations, managed.Reservation{Registration: other})
	if err := managed.ValidateRoleBinding(target, binding); !errors.Is(err, managed.ErrConflict) {
		t.Fatalf("shared runtime contradictory roles: %v", err)
	}

	target.Reservations[1].RuntimeID = "query-runtime"
	if err := managed.ValidateRoleBinding(target, binding); err != nil {
		t.Fatalf("separate accounted runtime: %v", err)
	}

	binding.ReservationVersion = "01"
	if err := managed.ValidateRoleBinding(target, binding); !errors.Is(err, managed.ErrInvalid) {
		t.Fatalf("coerced reservation version: %v", err)
	}
}

func TestManagedControllerWireIntegers(t *testing.T) {
	q := controllerAction(t)
	q.NewTarget.Manifest.Version = ^uint64(0)
	identity := managed.IdentityWire(*q.NewTarget)

	restored, err := identity.Value()
	if err != nil || !reflect.DeepEqual(restored, *q.NewTarget) {
		t.Fatalf("identity round trip: %v", err)
	}

	fence := managed.Fence{Epoch: 9007199254740993, Evidence: []byte("original-fence"), CompatibilityEvidence: []byte("original-compatibility"), Digest: strings.Repeat("a", 64), Survivors: []string{"off-instance"}}
	fenceWire := managed.FenceWire(fence)

	fenceRestored, err := fenceWire.Value()
	if err != nil || !reflect.DeepEqual(fence, fenceRestored) {
		t.Fatalf("fence round trip: %v", err)
	}

	receipt := managed.Receipt{ID: q.ID, Digest: q.CanonicalDigest, Revision: 1<<63 - 1, LeaseEpoch: 9007199254740993}
	wireReceipt := managed.ReceiptWire(receipt)

	restoredReceipt, err := wireReceipt.Value()
	if err != nil || !reflect.DeepEqual(receipt, restoredReceipt) {
		t.Fatalf("receipt round trip: %v", err)
	}

	permission := managed.ProviderStepPermission{Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: q.Key, ActionID: q.ID}, ProviderOperationID: q.NewTarget.ProvisionOperationID, Kind: managed.StepCreateNetwork}, CommandID: q.ID, Digest: q.CanonicalDigest, SpecDigest: q.NewTarget.Manifest.SpecDigest, CreateProfile: "ctrlplane.managed.create.v1", Physical: q.NewTarget.Physical, IssuedAt: q.OccurredAt, Seal: 9007199254740993, Revision: 1<<63 - 1}
	step := managed.ProviderStepRecord{Permission: permission, Result: &managed.ProviderStepResult{Permission: permission, Evidence: []byte("original-result"), Outcome: managed.PortTerminal, ResourceID: "network", ObservedAt: q.OccurredAt}}
	wireStep := managed.ProviderStepWire(step)

	restoredStep, err := wireStep.Value()
	if err != nil || !reflect.DeepEqual(step, restoredStep) {
		t.Fatalf("step round trip: %v", err)
	}

	payload := struct {
		Identity managed.WireIdentity           `json:"identity"`
		Fence    managed.WireFence              `json:"fence"`
		Receipt  managed.WireReceipt            `json:"receipt"`
		Step     managed.WireProviderStepRecord `json:"step"`
	}{identity, fenceWire, wireReceipt, wireStep}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	var decoded any

	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()

	if err = decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}

	var assertNoNumber func(any)

	assertNoNumber = func(value any) {
		switch v := value.(type) {
		case json.Number:
			t.Fatalf("numeric wire integer: %v", v)
		case map[string]any:
			for _, item := range v {
				assertNoNumber(item)
			}
		case []any:
			for _, item := range v {
				assertNoNumber(item)
			}
		}
	}
	assertNoNumber(decoded)

	if !strings.Contains(string(raw), `"18446744073709551615"`) || !strings.Contains(string(raw), `"9007199254740993"`) {
		t.Fatal("large counter lost")
	}

	for _, bad := range []managed.Decimal{"", "01", "+1", "-1", "1.0", " 1", "18446744073709551616"} {
		identity.Manifest.Version = bad
		if _, err = identity.Value(); err == nil {
			t.Fatalf("version accepted %q", bad)
		}

		fenceWire.Epoch = bad
		if _, err = fenceWire.Value(); err == nil {
			t.Fatalf("epoch accepted %q", bad)
		}

		wireReceipt.Revision = bad
		if _, err = wireReceipt.Value(); err == nil {
			t.Fatalf("receipt accepted %q", bad)
		}

		wireStep.Permission.Seal = bad
		if _, err = wireStep.Value(); err == nil {
			t.Fatalf("permission accepted %q", bad)
		}
	}

	for _, invalid := range []managed.Decimal{"0", "9223372036854775808", "18446744073709551615"} {
		invalidReceipt := managed.ReceiptWire(receipt)
		invalidReceipt.Revision = invalid

		if _, err = invalidReceipt.Value(); !errors.Is(err, managed.ErrInvalid) {
			t.Fatalf("invalid stored revision accepted: %q %v", invalid, err)
		}

		invalidPermission := managed.ProviderPermissionWire(permission)
		invalidPermission.Revision = invalid

		if _, err = invalidPermission.Value(); !errors.Is(err, managed.ErrInvalid) {
			t.Fatalf("invalid permission revision accepted: %q %v", invalid, err)
		}
	}

	invalidEpoch := managed.ReceiptWire(receipt)
	invalidEpoch.LeaseEpoch = "9223372036854775808"

	if _, err = invalidEpoch.Value(); !errors.Is(err, managed.ErrInvalid) {
		t.Fatalf("signed lease limit: %v", err)
	}

	invalidSeal := managed.ProviderPermissionWire(permission)
	invalidSeal.Seal = "9223372036854775808"

	if _, err = invalidSeal.Value(); !errors.Is(err, managed.ErrInvalid) {
		t.Fatalf("signed seal limit: %v", err)
	}

	fence.Epoch = ^uint64(0)
	if recovered, conversionErr := managed.FenceWire(fence).Value(); conversionErr != nil || recovered.Epoch != fence.Epoch {
		t.Fatalf("unsigned fence domain narrowed: %v", conversionErr)
	}

	fence.Epoch = 0
	if _, conversionErr := managed.FenceWire(fence).Value(); !errors.Is(conversionErr, managed.ErrInvalid) {
		t.Fatalf("zero fence epoch accepted: %v", conversionErr)
	}

	var malformed managed.WireReceipt
	if err = json.Unmarshal([]byte(`{"revision":9007199254740993,"lease_epoch":"1"}`), &malformed); err == nil {
		t.Fatal("numeric wire accepted")
	}

	for _, value := range []string{`null`, `""`, `"1e3"`, `"01"`, `"-1"`, `"+1"`, `"1.0"`, `"18446744073709551616"`} {
		var wire managed.WireReceipt

		decodeErr := json.Unmarshal([]byte(`{"revision":`+value+`,"lease_epoch":"1"}`), &wire)
		if decodeErr == nil {
			_, decodeErr = wire.Value()
		}

		if decodeErr == nil {
			t.Fatalf("malformed JSON decimal accepted: %s", value)
		}
	}
}

func TestManagedControllerPortBounds(t *testing.T) {
	q := controllerAction(t)
	command := managed.PortCommand{SchemaVersion: "1", OperationID: q.ID, RequestID: strings.Repeat("r", 256), Original: []byte(strings.Repeat("o", managed.MaxEvidence)), Native: []byte(strings.Repeat("n", managed.MaxEvidence))}
	command.OriginalDigest = managed.Digest(command.Original)

	command.NativeDigest = managed.Digest(command.Native)
	if err := command.Validate(); err != nil {
		t.Fatal(err)
	}

	changed := command

	changed.NativeDigest = command.OriginalDigest
	if err := changed.Validate(); err == nil {
		t.Fatal("translated digest replaced original domain")
	}

	changed = command
	changed.Original = append(append([]byte(nil), command.Original...), 'x')

	changed.OriginalDigest = managed.Digest(changed.Original)
	if err := changed.Validate(); err == nil {
		t.Fatal("oversized original accepted")
	}

	changed = command

	changed.SchemaVersion = "01"
	if err := changed.Validate(); err == nil {
		t.Fatal("noncanonical payload version")
	}

	maximum := strings.Repeat("x", 256)
	permission := managed.ProviderStepPermission{Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: managed.Key{TenantID: maximum, InstanceID: q.InstanceID}, ActionID: q.ID}, ProviderOperationID: q.NewTarget.ProvisionOperationID, Kind: managed.StepRemoveContainer, Service: maximum}, CommandID: q.ID, Digest: strings.Repeat("a", 64), SpecDigest: strings.Repeat("b", 64), CreateProfile: "ctrlplane.managed.create.v1", Physical: managed.Physical{Provider: maximum, Placement: maximum, Project: maximum, Generation: maximum}, Seal: 1<<63 - 1, FenceDigest: strings.Repeat("c", 64), ResourceID: maximum, Revision: 1<<63 - 1, IssuedAt: q.OccurredAt}
	record := managed.ProviderStepRecord{Permission: permission, Result: &managed.ProviderStepResult{Permission: permission, Outcome: managed.PortTerminal, ResourceID: maximum, Evidence: command.Original, Diagnostic: maximum, ObservedAt: q.OccurredAt}}

	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("maximum retained provider result: %d bytes", len(raw))

	if len(raw) > managed.MaxProviderStep {
		t.Fatal("maximum predecessor blocks completion")
	}

	wire := managed.ProviderStepWire(record)

	raw, err = json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("maximum provider wire result: %d bytes", len(raw))

	if len(raw) > managed.MaxProviderStep {
		t.Fatal("wire expansion exceeds result bound")
	}

	if restored, conversionErr := wire.Value(); conversionErr != nil || !reflect.DeepEqual(restored, record) {
		t.Fatalf("maximum wire result rejected or changed: %v", conversionErr)
	}

	wire.Result.Evidence = append(wire.Result.Evidence, 'x')
	if _, err = wire.Value(); !errors.Is(err, managed.ErrCapacity) {
		t.Fatalf("oversized wire proof: %v", err)
	}
}

func TestManagedProjectionOccupiedReservedOwner(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			authority := controllerStore(t, backend)
			s := authority.(store.Store)
			q, f, request := acceptedController(t, authority)

			operation, err := authority.ReadManagedOperation(t.Context(), managed.OperationKey{Key: q.Key, ActionID: q.ID})
			if err != nil {
				t.Fatal(err)
			}

			_, owner, _, err := managedstate.ProjectionEntities(*q.NewTarget, operation.Receipt, *q.NewTarget)
			if err != nil {
				t.Fatal(err)
			}

			owner.Name = "generic-owner"
			owner.Slug = "generic-owner"

			owner.Labels = map[string]string{"generic": "true"}
			if err = s.InsertWorkload(t.Context(), owner); err != nil {
				t.Fatal(err)
			}

			before := f.target()

			receipt, err := authority.ProjectManagedRows(t.Context(), request)
			if !errors.Is(err, managed.ErrConflict) || !receipt.ID.IsNil() {
				t.Fatalf("adopted occupied reservation: %v", err)
			}

			if !reflect.DeepEqual(before, f.target()) {
				t.Fatal("failed projection changed managed target")
			}

			if _, err = s.GetByID(t.Context(), q.TenantID, q.InstanceID); !errors.Is(err, ctrlplane.ErrNotFound) {
				t.Fatalf("partial instance: %v", err)
			}

			if _, err = s.GetRelease(t.Context(), q.TenantID, q.ReleaseID); !errors.Is(err, ctrlplane.ErrNotFound) {
				t.Fatalf("partial release: %v", err)
			}

			if _, err = authority.ReadManagedRelease(t.Context(), managed.ReleaseKey{TenantID: q.TenantID, ReleaseID: q.ReleaseID}); !errors.Is(err, managed.ErrNotFound) {
				t.Fatalf("partial association: %v", err)
			}

			recovered, err := s.GetWorkloadByID(t.Context(), q.TenantID, owner.ID)
			if err != nil || recovered.Name != owner.Name || recovered.Labels["generic"] != "true" {
				t.Fatalf("generic owner rewritten: %v", err)
			}
		})
	}
}

func TestManagedControllerNestedPortValidation(t *testing.T) {
	q := controllerAction(t)
	permission := managed.ProviderStepPermission{Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: q.Key, ActionID: q.ID}, ProviderOperationID: q.NewTarget.ProvisionOperationID, Kind: managed.StepRemoveContainer, Service: "api"}, CommandID: q.ID, Digest: q.CanonicalDigest, SpecDigest: q.NewTarget.Manifest.SpecDigest, CreateProfile: "ctrlplane.managed.create.v1", Physical: q.NewTarget.Physical, Seal: 1<<63 - 1, FenceDigest: strings.Repeat("f", 64), ResourceID: "container", IssuedAt: q.OccurredAt, Revision: 1<<63 - 1}
	record := managed.ProviderStepRecord{Permission: permission, Result: &managed.ProviderStepResult{Permission: permission, Outcome: managed.PortTerminal, ResourceID: "container", Evidence: []byte{0, 255, 1, 128}, ObservedAt: q.OccurredAt}}
	valid := managed.ProviderStepWire(record)
	historyRequest := managed.HistoricalSettlementRequest{Key: q.Key, SchemaVersion: "1", Installation: q.Installation, Authority: q.Authority, ManifestDigest: q.NewTarget.Manifest.SpecDigest, Seal: "9223372036854775807", AttemptID: q.ID, AttemptGeneration: "1", ProviderOperationID: q.NewTarget.ProvisionOperationID, FenceDigest: permission.FenceDigest}

	for _, outer := range []string{"execution", "history"} {
		validate := func(t *testing.T, wire managed.WireProviderStepRecord) error {
			t.Helper()

			if outer == "execution" {
				value := managed.ProviderExecutionResult{SchemaVersion: "1", Outcome: managed.PortTerminal, OperationID: q.NewTarget.ProvisionOperationID, Steps: []managed.WireProviderStepRecord{wire}}

				raw, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}

				var decoded managed.ProviderExecutionResult
				if err = json.Unmarshal(raw, &decoded); err != nil {
					t.Fatal(err)
				}

				return decoded.Validate()
			}

			value := managed.HistoricalSettlement{Request: historyRequest, Physical: q.NewTarget.Physical, Steps: []managed.WireProviderStepRecord{wire}, Receipt: managed.ReceiptWire(managed.Receipt{ID: q.ID, Digest: q.CanonicalDigest, Revision: 1, LeaseEpoch: 1})}

			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}

			var decoded managed.HistoricalSettlement
			if err = json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal(err)
			}

			return decoded.Validate()
		}
		t.Run(outer, func(t *testing.T) {
			if err := validate(t, valid); err != nil {
				t.Fatalf("valid baseline: %v", err)
			}

			tests := []struct {
				name   string
				change func(*managed.WireProviderStepRecord)
			}{
				{"permission_resource_257", func(w *managed.WireProviderStepRecord) { w.Permission.ResourceID = strings.Repeat("r", 257) }},
				{"result_resource_257", func(w *managed.WireProviderStepRecord) { w.Result.ResourceID = strings.Repeat("r", 257) }},
				{"physical_token_257", func(w *managed.WireProviderStepRecord) { w.Permission.Physical.Project = strings.Repeat("p", 257) }},
				{"tenant_token_257", func(w *managed.WireProviderStepRecord) { w.Permission.Step.TenantID = strings.Repeat("t", 257) }},
				{"service_257", func(w *managed.WireProviderStepRecord) { w.Permission.Step.Service = strings.Repeat("s", 257) }},
				{"digest_syntax", func(w *managed.WireProviderStepRecord) { w.Permission.Digest = strings.Repeat("g", 64) }},
				{"spec_digest_syntax", func(w *managed.WireProviderStepRecord) { w.Permission.SpecDigest = "invalid" }},
				{"fence_digest_syntax", func(w *managed.WireProviderStepRecord) { w.Permission.FenceDigest = "invalid" }},
				{"command_prefix", func(w *managed.WireProviderStepRecord) { w.Permission.CommandID = id.New(id.PrefixInstance) }},
				{"step_kind", func(w *managed.WireProviderStepRecord) { w.Permission.Step.Kind = "arbitrary" }},
				{"profile", func(w *managed.WireProviderStepRecord) { w.Permission.CreateProfile = "arbitrary" }},
				{"oversized_step", func(w *managed.WireProviderStepRecord) {
					w.Result.ResourceID = strings.Repeat("r", managed.MaxProviderStep+1)
				}},
				{"permission_copy", func(w *managed.WireProviderStepRecord) {
					w.Result.Permission.CommandID = id.New(managed.PrefixOperation)
				}},
				{"seal_domain", func(w *managed.WireProviderStepRecord) { w.Permission.Seal = "9223372036854775808" }},
				{"result_permission_token", func(w *managed.WireProviderStepRecord) {
					w.Result.Permission.Physical.Provider = strings.Repeat("p", 257)
				}},
			}
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					wire := managed.ProviderStepWire(record)
					test.change(&wire)

					if err := validate(t, wire); err == nil {
						t.Fatal("invalid nested payload accepted")
					}
				})
			}
		})
	}

	restored, err := valid.Value()
	if err != nil || !reflect.DeepEqual(restored, record) {
		t.Fatalf("opaque bytes or stored permission digest changed: %v", err)
	}

	request := historyRequest

	request.Seal = "9223372036854775808"
	if err = request.Validate(); !errors.Is(err, managed.ErrInvalid) {
		t.Fatalf("historical seal domain: %v", err)
	}

	command := managed.PortCommand{SchemaVersion: "1", OperationID: q.ID, RequestID: "request", Original: []byte("original"), Native: []byte("translated")}
	command.OriginalDigest = managed.Digest(command.Original)
	command.NativeDigest = managed.Digest(command.Native)
	member := q.NewTarget.Manifest.Members[0]

	binding := managed.RemovalBinding{LifecycleRequest: managed.LifecycleRequest{Command: command, Binding: managed.RoleBinding{Target: managed.IdentityWire(*q.NewTarget), MemberIndex: "0", MemberName: member.Name, Role: managed.Polling, ReservationID: id.New(managed.PrefixReservation), ReservationVersion: "1", RuntimeID: "runtime", InstanceID: "native", HostID: "host", NativeBuild: member.Build, ConfigurationDigest: member.ConfigurationDigest, EnrollmentEvidence: []byte("enrolled")}}, AttemptID: q.ID, AttemptGeneration: "1", ProviderOperationID: q.NewTarget.ProvisionOperationID, Seal: "9223372036854775807", Fence: managed.FenceWire(managed.Fence{Epoch: ^uint64(0), Evidence: []byte("fence"), CompatibilityEvidence: []byte("compatibility"), Digest: permission.FenceDigest}), SurvivorProof: []byte("survivors")}
	if err = binding.Validate(); err != nil {
		t.Fatalf("valid removal baseline: %v", err)
	}

	binding.Seal = "9223372036854775808"
	if err = binding.Validate(); !errors.Is(err, managed.ErrInvalid) {
		t.Fatalf("removal seal domain: %v", err)
	}
}
