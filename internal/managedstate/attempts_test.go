package managedstate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

type attemptReadFixture struct {
	ControllerTransaction

	rows                   map[[3]string][]byte
	receipts               map[attemptReceiptKey]managed.Receipt
	rowReads, receiptReads int
	afterRead              func()
}

func (f *attemptReadFixture) ReadRecord(_ context.Context, kind, scope, key string) ([]byte, error) {
	f.rowReads++
	if f.afterRead != nil {
		f.afterRead()
	}

	raw, ok := f.rows[[3]string{kind, scope, key}]
	if !ok {
		return nil, managed.ErrNotFound
	}

	return raw, nil
}
func (f *attemptReadFixture) ReadReceipt(_ context.Context, key managed.Key, operation id.ID, digest string) (managed.Receipt, error) {
	f.receiptReads++

	receipt, ok := f.receipts[attemptReceiptKey{key, operation, digest}]
	if !ok {
		return managed.Receipt{}, managed.ErrConflict
	}

	return receipt, nil
}
func newAttemptReadView(f *attemptReadFixture) *attemptCheckedReads {
	return &attemptCheckedReads{ControllerTransaction: f, rows: map[[3]string][]byte{}, receipts: map[attemptReceiptKey]managed.Receipt{}}
}
func TestAttemptCheckedReadBoundaries(t *testing.T) {
	ctx := t.Context()
	f := &attemptReadFixture{rows: map[[3]string][]byte{}, receipts: map[attemptReceiptKey]managed.Receipt{}}
	v := newAttemptReadView(f)
	selected := [3]string{"removal_command", "a", "bc"}
	f.rows[selected] = []byte("first")

	raw, err := v.ReadRecord(ctx, selected[0], selected[1], selected[2])
	if err != nil {
		t.Fatal(err)
	}

	raw[0] = 'X'

	if string(f.rows[selected]) != "first" {
		t.Fatal("cache miss returned backend-owned bytes")
	}

	hit, err := v.ReadRecord(ctx, selected[0], selected[1], selected[2])
	if err != nil || string(hit) != "first" || f.rowReads != 1 {
		t.Fatal("immutable bytes not owned", err)
	}

	hit[0] = 'Y'

	for _, key := range [][3]string{{"removal_command", "ab", "c"}, {"removal_fence", "a", "bc"}, {"removal_command", "a", "bd"}} {
		f.rows[key] = []byte("distinct")

		got, e := v.ReadRecord(ctx, key[0], key[1], key[2])
		if e != nil || string(got) != "distinct" {
			t.Fatal("row tuple collision", e)
		}
	}

	for _, kind := range []string{"removal_provider_slot", "step", "removal_attempt_state", "removal_attempt_current", managed.QuiescenceCurrent} {
		key := [3]string{kind, "scope", "key"}

		f.rows[key] = []byte("before")
		if _, err = v.ReadRecord(ctx, key[0], key[1], key[2]); err != nil {
			t.Fatal(err)
		}

		f.rows[key] = []byte("after")

		got, e := v.ReadRecord(ctx, key[0], key[1], key[2])
		if e != nil || string(got) != "after" {
			t.Fatal("mutable row cached", kind, e)
		}
	}

	if _, err = v.ReadRecord(ctx, "removal_fence", "missing", "key"); !errors.Is(err, managed.ErrNotFound) {
		t.Fatal(err)
	}

	f.rows[[3]string{"removal_fence", "missing", "key"}] = []byte("inserted")

	if got, e := v.ReadRecord(ctx, "removal_fence", "missing", "key"); e != nil || string(got) != "inserted" {
		t.Fatal("absence cached", e)
	}

	key := managed.Key{TenantID: "tenant", InstanceID: id.New(id.PrefixInstance)}
	operation := id.New(managed.PrefixOperation)
	digest := managed.Digest([]byte("receipt"))
	receipt := managed.Receipt{ID: operation, Digest: digest, Revision: 1}

	f.receipts[attemptReceiptKey{key, operation, digest}] = receipt
	if got, e := v.ReadReceipt(ctx, key, operation, digest); e != nil || got != receipt {
		t.Fatal(e)
	}

	if got, e := v.ReadReceipt(ctx, key, operation, digest); e != nil || got != receipt || f.receiptReads != 1 {
		t.Fatal("receipt repeat", e)
	}

	if _, err = v.ReadReceipt(ctx, key, operation, managed.Digest([]byte("wrong"))); !errors.Is(err, managed.ErrConflict) {
		t.Fatal("wrong digest cache hit", err)
	}

	for _, which := range []string{"tenant", "target", "operation"} {
		other := key
		op := operation

		switch which {
		case "tenant":
			other.TenantID = "other"
		case "target":
			other.InstanceID = id.New(id.PrefixInstance)
		case "operation":
			op = id.New(managed.PrefixOperation)
		}

		expected := receipt
		expected.ID = op
		expected.Revision = 2

		f.receipts[attemptReceiptKey{other, op, digest}] = expected
		if got, e := v.ReadReceipt(ctx, other, op, digest); e != nil || got != expected {
			t.Fatal("receipt tuple collision", which, e)
		}
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()

	if _, err = v.ReadRecord(canceled, selected[0], selected[1], selected[2]); !errors.Is(err, context.Canceled) {
		t.Fatal("row hit ignored cancellation")
	}

	if _, err = v.ReadReceipt(canceled, key, operation, digest); !errors.Is(err, context.Canceled) {
		t.Fatal("receipt hit ignored cancellation")
	}

	if err = v.WriteRecord(ctx, "removal_command", "a", "bc", []byte("write"), false); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("write passed checked view")
	}

	if _, err = v.Atomic(ctx, key, operation, digest, func(*managed.Target, time.Time) (managed.Receipt, error) {
		t.Fatal("callback executed through checked view")

		return managed.Receipt{}, nil
	}); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("Atomic passed checked view")
	}

	if _, err = v.Create(ctx, &managed.Target{}); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("Create passed checked view")
	}

	if err = v.ReserveRows(ctx, managed.Identity{}, nil); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("ReserveRows passed checked view")
	}

	if err = v.ProjectRows(ctx, managed.Identity{}, managed.ActionReceipt{}, false); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("ProjectRows passed checked view")
	}

	f.rows[selected] = []byte("next-view")
	if got, e := newAttemptReadView(f).ReadRecord(ctx, selected[0], selected[1], selected[2]); e != nil || string(got) != "next-view" {
		t.Fatal("new checked view reused old row", e)
	}
}

func TestAttemptInternalMaximumCodecs(t *testing.T) {
	maximum := managed.Decimal("9223372036854775807")
	digest := strings.Repeat("a", 64)
	token := strings.Repeat("x", 256)
	stamp := time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC)
	key := managed.Key{TenantID: token, InstanceID: id.New(id.PrefixInstance)}
	ref := managed.AttemptRef{AttemptID: id.New(managed.PrefixOperation), Generation: "1", DescriptorDigest: digest}
	operation := id.New(managed.PrefixOperation)
	receipt := managed.Receipt{ID: operation, Digest: digest, Revision: math.MaxInt64, LeaseEpoch: math.MaxInt64, AcceptedAt: stamp, LeaseUntil: stamp}
	root := attemptRoot{SchemaVersion: "1", Key: key, AttemptRef: ref, OriginalOperationKey: managed.OperationKey{Key: key, ActionID: id.New(managed.PrefixOperation)}, InitialOperationID: operation, AcceptedAt: stamp}
	slot := attemptProviderSlot{SchemaVersion: "1", AttemptRef: ref, ProviderOperationID: id.New(managed.PrefixOperation), Index: "32", PermissionDigest: digest, Receipt: managed.ReceiptWire(receipt), PositiveDigest: digest, LastUncertaintyDigest: digest}
	event := attemptEvent{SchemaVersion: "1", AttemptRef: ref, Kind: "attempt_witness", RequestDigest: digest, FactKind: "removal_binding_settlement", FactKey: digest, FactDigest: digest, Receipt: receipt}

	state := managed.AttemptState{BindingSlots: bytes.Repeat([]byte{attemptBeginBit | attemptFenceBit | attemptFinishCommandBit | attemptFinishBit}, 64), ProviderSlots: bytes.Repeat([]byte{attemptPermissionBit | attemptPositiveBit | attemptUncertaintyBit}, 33), SchemaVersion: "1", AttemptRef: ref, Revision: maximum, Phase: "removed", AdmissionDigest: digest, ProviderIssuedAt: &stamp, SettlementDigest: digest, TerminalDigest: digest}
	nondeleted := state
	nondeleted.BindingSlots = bytes.Repeat([]byte{attemptBeginBit | attemptFenceBit | attemptAbortCommandBit | attemptAbortBit | attemptWitnessBit}, 64)
	nondeleted.ProviderSlots = make([]byte, 33)
	nondeleted.ProviderSlots[0] = attemptPermissionBit | attemptFailureBit | attemptUncertaintyBit
	nondeleted.Phase = "settled"
	nondeleted.RevocationDigest = digest

	nondeleted.TerminalDigest = ""
	for _, test := range []struct {
		name  string
		value any
		cap   int
	}{{"root", root, managed.MaxAttemptPointer}, {"current", attemptCurrent{"1", key, ref, "1"}, managed.MaxAttemptPointer}, {"identity", attemptIdentity{"1", operation, "provider", key, ref}, managed.MaxAttemptPointer}, {"event", event, managed.MaxAttemptFact}, {"provider_slot_positive_uncertainty", slot, managed.MaxAttemptAssociation}, {"state64_33_removed", state, managed.MaxAttemptFact}, {"state64_33_nondeleted", nondeleted, managed.MaxAttemptFact}} {
		t.Run(test.name, func(t *testing.T) {
			raw, err := json.Marshal(test.value)
			if err != nil {
				t.Fatal(err)
			}

			if len(raw) > test.cap {
				t.Fatalf("actual %d exceeds cap%d", len(raw), test.cap)
			}

			decoded := reflect.New(reflect.TypeOf(test.value))
			if err = json.Unmarshal(raw, decoded.Interface()); err != nil {
				t.Fatal(err)
			}

			again, err := json.Marshal(decoded.Elem().Interface())
			if err != nil || !bytes.Equal(raw, again) {
				t.Fatal("accepted codec bytes changed", err)
			}

			var tree any
			if err = json.Unmarshal(raw, &tree); err != nil {
				t.Fatal(err)
			}

			var check func(any)

			check = func(v any) {
				switch x := v.(type) {
				case float64:
					t.Fatal("numeric new nested counter")
				case map[string]any:
					for _, child := range x {
						check(child)
					}
				case []any:
					for _, child := range x {
						check(child)
					}
				}
			}
			check(tree)
			t.Logf("actual maximum codec bytes=%d cap=%d", len(raw), test.cap)
		})
	}

	slot.FailureDigest = slot.PositiveDigest
	slot.PositiveDigest = ""

	raw, err := json.Marshal(slot)
	if err != nil || len(raw) > managed.MaxAttemptAssociation {
		t.Fatal("failure association maximum", len(raw), err)
	}

	t.Logf("actual maximum failure+uncertainty provider slot=%d", len(raw))
}

func TestAttemptReserveRequiresCheckedView(t *testing.T) {
	if err := attemptReserve(nil, attemptView{state: managed.AttemptState{BindingSlots: bytes.Repeat([]byte{127}, 64), ProviderSlots: bytes.Repeat([]byte{15}, 33)}}, 1, false); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("unchecked markers became reserve authority", err)
	}
}

type attemptBatchFixture struct {
	*attemptReadFixture

	recordCalls  int
	receiptCalls int
	recordRows   []ControllerRecordResult
	receiptRows  []ControllerReceiptResult
	batchErr     error
	after        func()
}

func (f *attemptBatchFixture) ReadImmutableRecords(context.Context, []ControllerRecordKey) ([]ControllerRecordResult, error) {
	f.recordCalls++
	if f.after != nil {
		f.after()
	}

	return f.recordRows, f.batchErr
}
func (f *attemptBatchFixture) ReadImmutableReceipts(context.Context, managed.Key, []id.ID) ([]ControllerReceiptResult, error) {
	f.receiptCalls++
	if f.after != nil {
		f.after()
	}

	return f.receiptRows, f.batchErr
}
func batchAttemptReadView(f *attemptBatchFixture) *attemptCheckedReads {
	return &attemptCheckedReads{ControllerTransaction: f, rows: map[[3]string][]byte{}, missing: map[[3]string]bool{}, receipts: map[attemptReceiptKey]managed.Receipt{}}
}
func TestAttemptBatchCompletenessAndMutationBoundaries(t *testing.T) {
	ctx := t.Context()
	selected := ControllerRecordKey{"removal_command", managed.Digest([]byte("scope")), managed.Digest([]byte("slot"))}
	second := selected
	second.Key = managed.Digest([]byte("second"))
	base := &attemptReadFixture{rows: map[[3]string][]byte{}, receipts: map[attemptReceiptKey]managed.Receipt{}}
	f := &attemptBatchFixture{attemptReadFixture: base}
	valid := ControllerRecordResult{ControllerRecordKey: selected, Found: true, Size: 3, Data: []byte("row")}
	another := ControllerRecordResult{ControllerRecordKey: second, Found: true, Size: 3, Data: []byte("two")}
	unexpected := another
	unexpected.Key = managed.Digest([]byte("unexpected"))
	malformed := another

	malformed.Size = 4
	for _, rows := range [][]ControllerRecordResult{{valid}, {valid, valid}, {valid, unexpected}, {valid, malformed}} {
		f.recordRows = rows

		v := batchAttemptReadView(f)
		if err := v.prefetchRecords(ctx, []ControllerRecordKey{selected, second}); !errors.Is(err, managed.ErrUnavailable) || len(v.rows) != 0 || len(v.missing) != 0 {
			t.Fatal("partial batch installed", err)
		}
	}

	f.recordRows = []ControllerRecordResult{valid, another}
	f.batchErr = managed.ErrAuthority

	v := batchAttemptReadView(f)
	if err := v.prefetchRecords(ctx, []ControllerRecordKey{selected, second}); !errors.Is(err, managed.ErrAuthority) || len(v.rows) != 0 {
		t.Fatal("batch error installed rows", err)
	}

	f.batchErr = nil
	f.recordRows = []ControllerRecordResult{{ControllerRecordKey: selected}}

	before := batchAttemptReadView(f)
	if err := before.prefetchRecords(ctx, []ControllerRecordKey{selected}); err != nil {
		t.Fatal(err)
	}

	if _, err := before.ReadRecord(ctx, selected.Kind, selected.Scope, selected.Key); !errors.Is(err, managed.ErrNotFound) || base.rowReads != 0 {
		t.Fatal("native exact absence lost", err)
	}

	base.rows[[3]string{selected.Kind, selected.Scope, selected.Key}] = []byte("row")
	f.recordRows = []ControllerRecordResult{valid}

	after := batchAttemptReadView(f)
	if err := after.prefetchRecords(ctx, []ControllerRecordKey{selected}); err != nil {
		t.Fatal(err)
	}

	raw, err := after.ReadRecord(ctx, selected.Kind, selected.Scope, selected.Key)
	if err != nil || string(raw) != "row" {
		t.Fatal("fresh post-write view reused absence", err)
	}

	raw[0] = 'X'

	if string(f.recordRows[0].Data) != "row" {
		t.Fatal("batch bytes alias caller")
	}

	for _, kind := range []string{managed.OriginalRegistration, "removal_attempt_event"} {
		missing := ControllerRecordKey{kind, selected.Scope, id.New(managed.PrefixOperation).String()}
		f.recordRows = []ControllerRecordResult{{ControllerRecordKey: missing}}

		view := batchAttemptReadView(f)
		if err = view.prefetchRecords(ctx, []ControllerRecordKey{missing}); err != nil {
			t.Fatal(err)
		}

		count := base.rowReads

		for range 2 {
			_, _ = view.ReadRecord(ctx, missing.Kind, missing.Scope, missing.Key)
		}

		if base.rowReads != count+2 || len(view.missing) != 0 {
			t.Fatal("mandatory missing row cached")
		}
	}

	canceled, cancel := context.WithCancel(ctx)
	f.after = cancel
	f.recordRows = []ControllerRecordResult{valid}

	view := batchAttemptReadView(f)
	if err = view.prefetchRecords(canceled, []ControllerRecordKey{selected}); !errors.Is(err, context.Canceled) || len(view.rows) != 0 {
		t.Fatal("canceled batch installed", err)
	}

	f.after = nil
	key := managed.Key{TenantID: "tenant", InstanceID: id.New(id.PrefixInstance)}
	op := id.New(managed.PrefixOperation)
	other := id.New(managed.PrefixOperation)
	receipt := managed.Receipt{ID: op, Digest: managed.Digest([]byte("exact")), Revision: 1}
	first := ControllerReceiptResult{OperationID: op, Found: true, Receipt: receipt}

	next := ControllerReceiptResult{OperationID: other}
	for _, rows := range [][]ControllerReceiptResult{{first}, {first, first}, {first, {OperationID: other, Found: true, Receipt: receipt}}} {
		f.receiptRows = rows

		view = batchAttemptReadView(f)
		if err = view.prefetchReceipts(ctx, key, []id.ID{op, other}); !errors.Is(err, managed.ErrUnavailable) || len(view.receipts) != 0 {
			t.Fatal("partial receipt batch installed", err)
		}
	}

	f.receiptRows = []ControllerReceiptResult{first, next}

	view = batchAttemptReadView(f)
	if err = view.prefetchReceipts(ctx, key, []id.ID{op, other}); err != nil {
		t.Fatal(err)
	}

	if got, e := view.ReadReceipt(ctx, key, op, receipt.Digest); e != nil || got != receipt {
		t.Fatal("prefetched receipt unavailable", e)
	}

	if _, e := view.ReadReceipt(ctx, key, op, managed.Digest([]byte("wrong"))); !errors.Is(e, managed.ErrConflict) {
		t.Fatal("actual digest replaced expected fingerprint", e)
	}

	count := base.receiptReads

	for range 2 {
		_, _ = view.ReadReceipt(ctx, key, other, receipt.Digest)
	}

	if base.receiptReads != count+2 {
		t.Fatal("receipt absence cached")
	}

	if _, err = view.ReadImmutableRecords(ctx, []ControllerRecordKey{selected}); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("view batch bypass", err)
	}

	if _, err = view.ReadImmutableReceipts(ctx, key, []id.ID{op}); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("view receipt batch bypass", err)
	}
}

func TestAttemptImmutableManifestParse(t *testing.T) {
	binding := managed.StartupBindingRef{BindingID: "binding", BindingGeneration: "1", BindingDigest: strings.Repeat("a", 64)}
	provision := managed.ProvisionSpec{Version: "1", Services: []managed.ProvisionService{{Name: "api", Image: "x@sha256:" + strings.Repeat("b", 64), Role: "main", CPUMillis: "1", MemoryMiB: "1", PIDLimit: "1", Startup: binding}}}

	raw, err := json.Marshal(provision)
	if err != nil {
		t.Fatal(err)
	}

	encoded, err := json.Marshal(managed.ManagedSpec{Profile: "ctrlplane.managed.v1", RuntimeRoles: []managed.RuntimeRole{managed.Polling}, Provision: raw})
	if err != nil {
		t.Fatal(err)
	}

	target := &managed.Target{Identity: managed.Identity{Key: managed.Key{TenantID: "tenant", InstanceID: id.New(id.PrefixInstance)}, Manifest: managed.Manifest{Version: 1, Members: []managed.Member{{Name: "worker", Service: "api"}}, Spec: encoded, SpecDigest: managed.Digest(encoded)}}}
	view := newAttemptReadView(&attemptReadFixture{})

	spec, roles, err := view.parsedManifest(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}

	retained := bytes.Clone(view.manifestIdentity.Manifest.Spec)
	retainedService := &view.manifestSpec.Services[0]
	spec.Services[0].Name = "changed"
	roles.RuntimeRoles[0] = managed.RetainedQuery
	roles.Provision[0] = '!'

	spec, roles, err = view.parsedManifest(t.Context(), target)
	if &view.manifestSpec.Services[0] != retainedService {
		t.Fatal("successful parse was rebuilt instead of reused")
	}

	if err != nil || spec.Services[0].Name != "api" || roles.RuntimeRoles[0] != managed.Polling || roles.Provision[0] != '{' || !bytes.Equal(retained, view.manifestIdentity.Manifest.Spec) {
		t.Fatal("parsed cache ownership/hit", err)
	}

	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	if _, _, err = view.parsedManifest(canceled, target); !errors.Is(err, context.Canceled) {
		t.Fatal("cache hit ignored cancellation", err)
	}

	other := *target

	other.InstanceID = id.New(id.PrefixInstance)
	if _, _, err = view.parsedManifest(t.Context(), &other); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("different identity reused parse", err)
	}

	for name, mutate := range map[string]func(*managed.Identity){
		"tenant":    func(i *managed.Identity) { i.TenantID = "other" },
		"provision": func(i *managed.Identity) { i.ProvisionOperationID = id.New(managed.PrefixOperation) },
		"physical":  func(i *managed.Identity) { i.Physical.Provider = "other" },
		"version":   func(i *managed.Identity) { i.Manifest.Version++ },
		"digest":    func(i *managed.Identity) { i.Manifest.SpecDigest = strings.Repeat("c", 64) },
		"member":    func(i *managed.Identity) { i.Manifest.Members[0].OwnerID = id.New(id.PrefixInstance) },
	} {
		t.Run(name, func(t *testing.T) {
			different := *target
			different.Manifest.Members = slices.Clone(target.Manifest.Members)
			mutate(&different.Identity)

			if _, _, e := view.parsedManifest(t.Context(), &different); !errors.Is(e, managed.ErrUnavailable) {
				t.Fatal("changed identity component reused cache", e)
			}
		})
	}

	first := target.Manifest.Spec[0]

	target.Manifest.Spec[0] = '!'
	if _, _, err = view.parsedManifest(t.Context(), target); !errors.Is(err, managed.ErrUnavailable) || view.manifestIdentity.Manifest.Spec[0] != first {
		t.Fatal("input bytes aliased cache", err)
	}

	target.Manifest.Spec[0] = first

	target.Manifest.Spec = append(bytes.Clone(encoded), ' ')
	if _, _, err = view.parsedManifest(t.Context(), target); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("same-target changed bytes reused parse", err)
	}

	fresh := newAttemptReadView(&attemptReadFixture{})
	if _, _, err = fresh.parsedManifest(t.Context(), target); err == nil || fresh.manifestIdentity != nil {
		t.Fatal("malformed fresh input cached", err)
	}

	target.Manifest.Spec = bytes.Clone(encoded)
	if _, _, err = fresh.parsedManifest(t.Context(), target); err != nil {
		t.Fatal("failure cached instead of valid later input", err)
	}

	target.Manifest.Members[0].Service = "other"
	if _, _, err = view.parsedManifest(t.Context(), target); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("changed member reused parse", err)
	}

	target.Manifest.Members[0].Service = "api"
	rid := id.New(managed.PrefixReservation)
	command := []byte("native")
	target.Reservations = []managed.Reservation{{Registration: managed.Registration{ID: rid, Member: "worker", RuntimeID: "runtime", InstanceID: "instance", HostID: "host", RequestID: "request", Command: command, CommandDigest: managed.Digest(command)}}}

	material := managed.OriginalLifecycleMaterial{Command: managed.OriginalCommand{ReservationID: rid}, Registration: &managed.OriginalRegistrationFacts{Member: "worker", Role: managed.Polling, StoredCommandDigest: managed.Digest(command), Native: managed.OriginalNativeAcceptance{RequestID: "request"}, Process: managed.OriginalProcessFacts{RuntimeID: "runtime", InstanceID: "instance", HostID: "host", Descriptor: binding}}}
	if err = originalRegistrationMatch(t.Context(), view, target, material); err != nil {
		t.Fatal("valid association", err)
	}

	material.Registration.Process.Descriptor.BindingID = "wrong"
	if err = originalRegistrationMatch(t.Context(), view, target, material); !errors.Is(err, managed.ErrAuthority) {
		t.Fatal("later association bypassed on hit", err)
	}

	if err = originalRegistrationMatch(t.Context(), nil, target, material); !errors.Is(err, managed.ErrAuthority) {
		t.Fatal("ordinary association changed", err)
	}

	target.Manifest.Spec = []byte("malformed")

	material.Registration.Member = "wrong"
	if err = originalRegistrationMatch(t.Context(), view, target, material); !errors.Is(err, managed.ErrAuthority) {
		t.Fatal("binding error ordering changed", err)
	}

	if err = originalRegistrationMatch(t.Context(), nil, target, material); !errors.Is(err, managed.ErrAuthority) {
		t.Fatal("ordinary error ordering changed", err)
	}
}

func TestAttemptImmutableEventAndDigestCaches(t *testing.T) {
	ctx := t.Context()
	scope := strings.Repeat("a", 64)
	op := id.New(managed.PrefixOperation)
	digest := strings.Repeat("b", 64)
	stamp := time.Now().UTC()
	event := attemptEvent{SchemaVersion: "1", AttemptRef: managed.AttemptRef{AttemptID: id.New(managed.PrefixOperation), Generation: "1", DescriptorDigest: digest}, Kind: "attempt_recovery", RequestDigest: digest, FactDigest: digest, Receipt: managed.Receipt{ID: op, Digest: digest, Revision: 1, LeaseEpoch: 1, AcceptedAt: stamp, LeaseUntil: stamp.Add(time.Minute)}}

	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}

	f := &attemptReadFixture{rows: map[[3]string][]byte{}, receipts: map[attemptReceiptKey]managed.Receipt{}}
	view := newAttemptReadView(f)
	key := [3]string{"removal_attempt_event", scope, op.String()}
	f.rows[key] = raw

	got, err := view.eventValue(ctx, scope, op.String())
	if err != nil || got != event || len(view.events) != 1 {
		t.Fatal("event decode", err)
	}

	got.Kind = "mutated caller"

	got, err = view.eventValue(ctx, scope, op.String())
	if err != nil || got != event || len(view.events) != 1 {
		t.Fatal("event hit ownership", err)
	}

	for _, tuple := range [][3]string{{"removal_attempt_event", "other", op.String()}, {"removal_attempt_event", scope, id.New(managed.PrefixOperation).String()}} {
		f.rows[tuple] = raw
		if _, err = view.eventValue(ctx, tuple[1], tuple[2]); err != nil {
			t.Fatal(err)
		}
	}

	if len(view.events) != 3 {
		t.Fatal("event tuple collision")
	}

	changed := event
	changed.Kind = "different"

	changedRaw, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}

	view.rows[key] = changedRaw
	if got, err = view.eventValue(ctx, scope, op.String()); err != nil || got.Kind != "different" || len(view.events) != 4 {
		t.Fatal("changed actual bytes reused stale decode", err)
	}

	view.rows[key] = []byte(`{"bad":true}`)

	before := len(view.events)
	if _, err = view.eventValue(ctx, scope, op.String()); err == nil || len(view.events) != before {
		t.Fatal("parse failure cached", err)
	}

	view.rows[key] = raw
	if got, err = view.eventValue(ctx, scope, op.String()); err != nil || got != event {
		t.Fatal("failure hid valid input", err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()

	if _, err = view.eventValue(canceled, scope, op.String()); !errors.Is(err, context.Canceled) {
		t.Fatal("event hit canceled", err)
	}

	fact := []byte(`{"field":"value"}`)
	for _, kind := range []string{"removal_command", "removal_fence", "removal_abort", "removal_finish", "removal_binding_settlement"} {
		expected, e := attemptStoredFactDigest(kind, fact)
		if e != nil {
			t.Fatal(e)
		}

		for range 2 {
			got, e := view.storedFactDigest(ctx, kind, scope, "slot", fact)
			if e != nil || got != expected {
				t.Fatal("fact digest bytes/domain changed", e)
			}
		}
	}

	if len(view.factDigests) != 5 {
		t.Fatal("fact domain collision")
	}

	for _, tuple := range [][3]string{{"removal_command", "other", "slot"}, {"removal_command", scope, "other"}} {
		if _, err = view.storedFactDigest(ctx, tuple[0], tuple[1], tuple[2], fact); err != nil {
			t.Fatal(err)
		}
	}

	if len(view.factDigests) != 7 {
		t.Fatal("fact tuple collision")
	}

	if _, err = view.storedFactDigest(ctx, "removal_command", scope, "slot", []byte(`{"field":"changed"}`)); err != nil || len(view.factDigests) != 8 {
		t.Fatal("fact bytes collision", err)
	}

	before = len(view.factDigests)
	if _, err = view.storedFactDigest(ctx, "removal_command", scope, "slot", []byte(`{`)); err == nil || len(view.factDigests) != before {
		t.Fatal("hash failure cached", err)
	}

	if _, err = view.storedFactDigest(ctx, "removal_command", scope, "slot", fact); err != nil {
		t.Fatal("failure hid valid hash", err)
	}

	if _, err = view.storedFactDigest(canceled, "removal_command", scope, "slot", fact); !errors.Is(err, context.Canceled) {
		t.Fatal("digest hit canceled", err)
	}

	if _, err = view.storedFactDigest(ctx, "removal_attempt_state", scope, "slot", fact); err != nil || len(view.factDigests) != before {
		t.Fatal("mutable fact cached", err)
	}
	// Fill only bounded value maps. Existing raw row storage is unchanged.
	for n := len(view.events); n < 512; n++ {
		view.events[attemptImmutableValueKey{Kind: "removal_attempt_event", Scope: scope, Key: strconv.Itoa(n), BytesDigest: digest}] = event
	}

	for n := len(view.factDigests); n < 512; n++ {
		view.factDigests[attemptImmutableValueKey{Kind: "removal_command", Scope: scope, Key: strconv.Itoa(n), BytesDigest: digest}] = digest
	}

	extra := id.New(managed.PrefixOperation).String()

	f.rows[[3]string{"removal_attempt_event", scope, extra}] = raw
	if _, err = view.eventValue(ctx, scope, extra); err != nil || len(view.events) != 512 {
		t.Fatal("event full-map fallback", err)
	}

	if _, err = view.storedFactDigest(ctx, "removal_command", scope, extra, fact); err != nil || len(view.factDigests) != 512 {
		t.Fatal("digest full-map fallback", err)
	}

	f.rows[key] = changedRaw

	fresh := newAttemptReadView(f)
	if got, err = fresh.eventValue(ctx, scope, op.String()); err != nil || got.Kind != "different" {
		t.Fatal("fresh view retained prior value", err)
	}
	// Receipt/fact semantic checks execute even after canonical decode hits.
	f.rows[key] = raw
	fresh = newAttemptReadView(f)
	target := managed.Key{TenantID: "tenant", InstanceID: id.New(id.PrefixInstance)}
	checked := attemptView{scope: scope, ref: event.AttemptRef, descriptor: managed.AttemptDescriptor{Key: target}}

	f.receipts[attemptReceiptKey{target, op, digest}] = event.Receipt
	if _, err = attemptEventRead(ctx, fresh, checked, op); err != nil {
		t.Fatal(err)
	}

	checked.ref.DescriptorDigest = strings.Repeat("c", 64)
	if _, err = attemptEventRead(ctx, fresh, checked, op); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("event association bypassed on hit", err)
	}

	checked.ref = event.AttemptRef
	wrong := event
	wrong.RequestDigest = strings.Repeat("d", 64)
	wrong.Receipt.Digest = wrong.RequestDigest
	wrong.FactDigest = wrong.RequestDigest

	fresh.rows[key], err = json.Marshal(wrong)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = attemptEventRead(ctx, fresh, checked, op); !errors.Is(err, managed.ErrConflict) {
		t.Fatal("wrong receipt fingerprint bypassed", err)
	}

	installContext, installCancel := context.WithCancel(ctx)
	installFixture := &attemptReadFixture{rows: map[[3]string][]byte{key: raw}, afterRead: installCancel}

	installView := newAttemptReadView(installFixture)
	if _, e := installView.eventValue(installContext, scope, op.String()); !errors.Is(e, context.Canceled) || len(installView.events) != 0 {
		t.Fatal("canceled decode installed", e)
	}

	t.Logf("512 event values shallow=%d key shallow=%d;512 digest values shallow=%d; map buckets and owned strings excluded", 512*reflect.TypeFor[attemptEvent]().Size(), 512*reflect.TypeFor[attemptImmutableValueKey]().Size(), 512*reflect.TypeFor[string]().Size())
}

func (f *attemptBatchFixture) ReadHeldRecords(ctx context.Context, keys []ControllerRecordKey) ([]ControllerRecordResult, error) {
	return f.ReadImmutableRecords(ctx, keys)
}

func TestAttemptHeldBatchTransportRefusal(t *testing.T) {
	selected := ControllerRecordKey{"step", "tenant", managed.Digest([]byte("slot"))}
	second := selected
	second.Key = managed.Digest([]byte("second"))
	valid := ControllerRecordResult{ControllerRecordKey: selected, Found: true, Size: 3, Data: []byte("row")}
	another := ControllerRecordResult{ControllerRecordKey: second, Found: true, Size: 3, Data: []byte("two")}
	malformed := another
	malformed.Size++
	oversized := another
	oversized.Size = 262145
	oversized.Data = nil
	unexpected := another
	unexpected.Scope = "other-tenant"
	absent := another

	absent.Found = false
	for _, rows := range [][]ControllerRecordResult{{valid}, {valid, valid}, {valid, malformed}, {valid, oversized}, {valid, unexpected}, {valid, absent}} {
		f := &attemptBatchFixture{attemptReadFixture: &attemptReadFixture{}, recordRows: rows}

		view := newAttemptCheckedReads(f)
		if err := view.prefetchHeldRecords(t.Context(), []ControllerRecordKey{selected, second}); !errors.Is(err, managed.ErrUnavailable) || len(view.heldRows) != 0 || len(view.heldMissing) != 0 {
			t.Fatal("invalid held batch installed", err)
		}
	}

	f := &attemptBatchFixture{attemptReadFixture: &attemptReadFixture{}, recordRows: []ControllerRecordResult{valid}}

	view := newAttemptCheckedReads(f)
	if err := view.prefetchHeldRecords(t.Context(), []ControllerRecordKey{selected}); err != nil {
		t.Fatal(err)
	}

	if _, err := view.ReadHeldRecords(t.Context(), []ControllerRecordKey{selected}); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("batch wrapper bypass", err)
	}

	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := view.ReadRecord(canceled, selected.Kind, selected.Scope, selected.Key); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type attemptHeldCountingFixture struct {
	*attemptReadFixture

	batches [][]ControllerRecordKey
}

func (f *attemptHeldCountingFixture) ReadHeldRecords(ctx context.Context, keys []ControllerRecordKey) ([]ControllerRecordResult, error) {
	if err := ValidateHeldRecordKeys(keys); err != nil {
		return nil, err
	}

	f.batches = append(f.batches, slices.Clone(keys))

	out := make([]ControllerRecordResult, 0, len(keys))
	for _, k := range keys {
		raw, found := f.rows[[3]string{k.Kind, k.Scope, k.Key}]
		out = append(out, ControllerRecordResult{ControllerRecordKey: k, Found: found, Size: len(raw), Data: bytes.Clone(raw)})
	}

	return out, ctx.Err()
}

func TestAttemptUnissuedProviderBatchBoundary(t *testing.T) {
	binding := managed.StartupBindingRef{BindingID: "binding", BindingGeneration: "1", BindingDigest: strings.Repeat("a", 64)}
	spec := managed.ProvisionSpec{Version: "1"}
	target := &managed.Target{Identity: managed.Identity{Key: managed.Key{TenantID: "tenant", InstanceID: id.New(id.PrefixInstance)}}}
	target.Resources = map[string]string{"network": "network-resource"}

	for i := range 32 {
		name := "service" + strconv.Itoa(i)

		role := "sidecar"
		if i == 0 {
			role = "main"
		}

		spec.Services = append(spec.Services, managed.ProvisionService{Name: name, Image: "x@sha256:" + strings.Repeat("b", 64), Role: role, CPUMillis: "1", MemoryMiB: "1", PIDLimit: "1", Startup: binding})
		target.Resources[name] = "resource" + strconv.Itoa(i)
	}

	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}

	encoded, err := json.Marshal(managed.ManagedSpec{Profile: "ctrlplane.managed.v1", RuntimeRoles: []managed.RuntimeRole{managed.Polling}, Provision: raw})
	if err != nil {
		t.Fatal(err)
	}

	target.Manifest = managed.Manifest{Version: 1, Spec: encoded, SpecDigest: managed.Digest(encoded), Members: []managed.Member{{Name: "worker", Service: "service0"}}}
	v := attemptView{scope: managed.Digest([]byte("scope")), descriptor: managed.AttemptDescriptor{ActionID: id.New(managed.PrefixOperation), ProviderOperationID: id.New(managed.PrefixOperation)}, state: managed.AttemptState{ProviderSlots: make([]byte, 33)}}

	steps, err := attemptSteps(target, v)
	if err != nil || len(steps) != 33 {
		t.Fatal("steps", len(steps), err)
	}

	f := &attemptHeldCountingFixture{attemptReadFixture: &attemptReadFixture{rows: map[[3]string][]byte{}}}

	reads := newAttemptCheckedReads(f)
	if err = reads.prefetchProviders(t.Context(), target, v); err != nil {
		t.Fatal(err)
	}

	if len(f.batches) != 2 || len(f.batches[0]) != 64 || len(f.batches[1]) != 35 {
		t.Fatal("99 selections must use two bounded reads", f.batches)
	}

	unique := map[ControllerRecordKey]bool{}

	for _, batch := range f.batches {
		for _, selected := range batch {
			if unique[selected] {
				t.Fatal("duplicate selection")
			}

			unique[selected] = true
		}
	}

	for i, step := range steps {
		if _, _, err = attemptProviderRead(t.Context(), reads, target, v, step, i); err != nil {
			t.Fatal(err)
		}
	}

	if f.rowReads != 0 {
		t.Fatal("presence escaped batch", f.rowReads)
	}

	if err = reads.prefetchProviders(t.Context(), target, v); err != nil || len(f.batches) != 2 {
		t.Fatal("held view refetched", err)
	}

	slot, err := providerStepID(steps[0])
	if err != nil {
		t.Fatal(err)
	}

	for _, selected := range []ControllerRecordKey{{"step", target.TenantID, slot}, {"removal_provider_slot", v.scope, slot}, {"step_failure", target.TenantID, slot}} {
		f.rows = map[[3]string][]byte{{selected.Kind, selected.Scope, selected.Key}: []byte(`{}`)}
		fresh := newAttemptCheckedReads(f)

		err = fresh.prefetchProviders(t.Context(), target, v)
		if err == nil {
			_, _, err = attemptProviderRead(t.Context(), fresh, target, v, steps[0], 0)
		}

		if !errors.Is(err, managed.ErrUnavailable) {
			t.Fatal("fresh validation changed unexpected zero-mask refusal", selected.Kind, err)
		}
	}
}

func TestAttemptSharedImmutableTransport(t *testing.T) {
	selected := ControllerRecordKey{"removal_current_binding", managed.Digest([]byte("scope")), managed.Digest([]byte("slot"))}
	op := id.New(managed.PrefixOperation)
	key := managed.Key{TenantID: "tenant", InstanceID: id.New(id.PrefixInstance)}
	receipt := managed.Receipt{ID: op, Digest: managed.Digest([]byte("receipt"))}
	f := &attemptBatchFixture{attemptReadFixture: &attemptReadFixture{}, recordRows: []ControllerRecordResult{{ControllerRecordKey: selected, Found: true, Size: 3, Data: []byte("row")}}, receiptRows: []ControllerReceiptResult{{OperationID: op, Found: true, Receipt: receipt}}}

	reads := newAttemptCheckedReads(f)
	for range 3 {
		if err := reads.prefetchRecords(t.Context(), []ControllerRecordKey{selected}); err != nil {
			t.Fatal(err)
		}

		if err := reads.prefetchReceipts(t.Context(), key, []id.ID{op}); err != nil {
			t.Fatal(err)
		}
	}

	if f.recordCalls != 1 || f.receiptCalls != 1 {
		t.Fatal("successful exact transport repeated", f.recordCalls, f.receiptCalls)
	}

	raw, err := reads.ReadRecord(t.Context(), selected.Kind, selected.Scope, selected.Key)
	if err != nil || string(raw) != "row" || f.rowReads != 0 {
		t.Fatal("prefetched current binding escaped transport", err)
	}
}

func TestAttemptHeldPresenceBeforeCapacity(t *testing.T) {
	for _, kind := range []string{"removal_provider_slot", "step_failure"} {
		for _, wanted := range []bool{false, true} {
			t.Run(kind+"/wanted="+strconv.FormatBool(wanted), func(t *testing.T) {
				selected := ControllerRecordKey{kind, managed.Digest([]byte("scope")), managed.Digest([]byte("slot"))}
				f := &attemptBatchFixture{attemptReadFixture: &attemptReadFixture{}, recordRows: []ControllerRecordResult{{ControllerRecordKey: selected, Found: true, Size: HeldRecordLimit(kind) + 1}}}
				reads := newAttemptCheckedReads(f)
				err := reads.prefetchSelectedRecords(t.Context(), []ControllerRecordKey{selected}, true, map[ControllerRecordKey]bool{selected: wanted})

				expected := managed.ErrUnavailable
				if wanted {
					expected = managed.ErrCapacity
				}

				if !errors.Is(err, expected) {
					t.Fatal("presence and capacity precedence", err)
				}

				if len(reads.heldRows) != 0 || len(reads.heldMissing) != 0 {
					t.Fatal("refused batch installed values")
				}
			})
		}
	}
}
