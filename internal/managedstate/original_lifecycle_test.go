package managedstate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

func originalParseFixture(t *testing.T) (managed.OriginalLifecycleRecord, []byte) {
	t.Helper()

	stamp := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	digest := managed.Digest([]byte("fixture"))
	process := managed.OriginalProcessFacts{Installation: "installation", Namespace: "namespace", Build: "build", Queue: "queue", RuntimeID: "runtime", InstanceID: "instance", HostID: "host", IdentityVersion: "1", ArtifactDigest: digest, ConfigurationDigest: digest, ConfigurationVersion: "config", EnrollmentEvidenceDigest: digest, ProbePolicyID: "policy", ProbePolicyVersion: "1", MaximumProofValidityNS: "1000000000", VerifierID: "verifier", Descriptor: managed.StartupBindingRef{BindingID: "binding", BindingGeneration: "1", BindingDigest: digest}, ProducerVerifier: "producer"}
	native := managed.OriginalNativeAcceptance{Operation: "query_runtime.register", RequestID: "request", RequestDigest: digest, CommandDigest: digest, ResponseVersion: "1", DeliveryID: "delivery", AcceptedAt: stamp}
	operation := id.New(managed.PrefixOperation)
	material := managed.OriginalLifecycleMaterial{Command: managed.OriginalCommand{Key: managed.Key{TenantID: "tenant", InstanceID: id.New(id.PrefixInstance)}, ID: operation, Revision: "1", LeaseEpoch: "1", Owner: "owner", ReservationID: id.New(managed.PrefixReservation)}, Kind: managed.OriginalRegistration, TargetIdentityDigest: digest, Registration: &managed.OriginalRegistrationFacts{Process: process, Native: native, Member: "member", Role: managed.Polling, StoredCommandDigest: digest, BindingVersion: "1", BindingState: "active", RemovalEpoch: "0", CreatedAt: stamp, ChangedAt: stamp}, NativeReceipt: []byte("fixture"), NativeReceiptDigest: digest}

	reference, err := managed.OriginalReference(material)
	if err != nil {
		t.Fatal(err)
	}

	value := managed.OriginalLifecycleRecord{Material: material, Reference: reference, Receipt: managed.OriginalLocalReceipt{ID: operation, Digest: digest, Revision: "2", LeaseEpoch: "1", AcceptedAt: stamp, LeaseUntil: stamp.Add(time.Minute)}}

	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = managed.ParseOriginalLifecycleRecord(raw); err != nil {
		t.Fatal(err)
	}

	return value, raw
}

func TestAttemptParsedOriginalBoundaries(t *testing.T) {
	expected, raw := originalParseFixture(t)
	view := newAttemptCheckedReads(nil)
	parse := func(kind, scope, key string, data []byte) managed.OriginalLifecycleRecord {
		t.Helper()

		value, err := view.parsedOriginal(t.Context(), kind, scope, key, data)
		if err != nil {
			t.Fatal(err)
		}

		return value
	}
	first := parse(managed.OriginalRegistration, "a", "bc", raw)
	first.Material.NativeReceipt[0] = 'X'
	first.Material.Registration.Process.RuntimeID = "changed"

	second := parse(managed.OriginalRegistration, "a", "bc", raw)
	if !reflect.DeepEqual(second, expected) || len(view.originals) != 1 {
		t.Fatal("parse hit borrowed caller state")
	}

	second.Material.NativeReceipt[0] = 'Y'
	second.Material.Registration.Member = "other"

	if !reflect.DeepEqual(parse(managed.OriginalRegistration, "a", "bc", raw), expected) {
		t.Fatal("cache hit aliases retained value")
	}

	for _, key := range [][3]string{{managed.OriginalDrain, "a", "bc"}, {managed.OriginalRegistration, "ab", "c"}, {managed.OriginalRegistration, "a", "bd"}} {
		parse(key[0], key[1], key[2], raw)
	}

	if len(view.originals) != 4 {
		t.Fatal("identity components collided")
	}

	changed := expected
	changed.Receipt.Revision = "3"

	changedRaw, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}

	if got := parse(managed.OriginalRegistration, "a", "bc", changedRaw); got.Receipt.Revision != "3" || len(view.originals) != 5 {
		t.Fatal("content digest omitted")
	}

	for _, bad := range [][]byte{nil, []byte(`null`), bytes.Replace(raw, []byte(`"native_receipt":"Zml4dHVyZQ=="`), []byte(`"native_receipt":null`), 1), append([]byte(" "), raw...), bytes.Replace(raw, []byte(`"material":`), []byte(`"unknown":1,"material":`), 1), bytes.Replace(raw, []byte(`"material":`), []byte(`"material":null,"material":`), 1), bytes.Replace(raw, []byte(`"native_receipt":`), []byte(`"native_receipt":null,"native_receipt":`), 1)} {
		if _, err = view.parsedOriginal(t.Context(), managed.OriginalRegistration, "a", "bc", bad); err == nil {
			t.Fatal("malformed original accepted")
		}
	}

	if len(view.originals) != 5 {
		t.Fatal("failed parse retained")
	}

	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err = view.parsedOriginal(canceled, managed.OriginalRegistration, "a", "bc", raw); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled hit", err)
	}

	if _, err = view.parsedOriginal(t.Context(), managed.OriginalRegistration, "a", "bc", make([]byte, managed.MaxOriginalLifecycleRecord+1)); !errors.Is(err, managed.ErrCapacity) {
		t.Fatal("raw bound", err)
	}

	for i := len(view.originals); i < 64; i++ {
		parse(managed.OriginalRegistration, "scope", strconv.Itoa(i), raw)
	}

	retained := view.originalBytes

	parse(managed.OriginalRegistration, "overflow", "key", raw)

	if len(view.originals) != 64 || view.originalBytes != retained {
		t.Fatal("entry bound exceeded")
	}

	if _, err = view.parsedOriginal(t.Context(), managed.OriginalRegistration, "overflow", "key", []byte(`{}`)); err == nil {
		t.Fatal("full cache bypassed parser")
	}

	bounded := newAttemptCheckedReads(nil)

	bounded.originalBytes = 64 * managed.MaxOriginalLifecycleRecord
	if _, err = bounded.parsedOriginal(t.Context(), managed.OriginalRegistration, "a", "bc", raw); err != nil || len(bounded.originals) != 0 {
		t.Fatal("byte bound fallback", err)
	}

	fresh := newAttemptCheckedReads(nil)
	if _, err = fresh.parsedOriginal(t.Context(), managed.OriginalRegistration, "a", "bc", []byte(`{}`)); err == nil || len(fresh.originals) != 0 {
		t.Fatal("parse survived next session")
	}

	drain := expected
	drain.Material.Drain = &managed.OriginalDrainFacts{OperationID: "operation"}
	copyValue := cloneOriginal(drain)

	copyValue.Material.Drain.OperationID = "changed"
	if drain.Material.Drain.OperationID != "operation" {
		t.Fatal("drain clone aliases input")
	}
}

func TestOriginalReadCancellationAfterTransport(t *testing.T) {
	value, raw := originalParseFixture(t)

	scope, err := originalScope(value.Material.Command.Key)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	key := managed.OriginalLifecycleKey{Key: value.Material.Command.Key, Kind: managed.OriginalRegistration, OperationID: value.Material.Command.ID}

	backend := &attemptReadFixture{rows: map[[3]string][]byte{{key.Kind, scope, key.OperationID.String()}: raw}, afterRead: cancel}
	if _, _, err = readOriginal(ctx, newAttemptCheckedReads(backend), key, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("transport cancellation", err)
	}
}

type currentBindingBatchFixture struct {
	ControllerTransaction

	calls     int
	requested []ControllerRecordKey
	change    func([]ControllerRecordResult) []ControllerRecordResult
}

func (f *currentBindingBatchFixture) ReadImmutableRecords(_ context.Context, keys []ControllerRecordKey) ([]ControllerRecordResult, error) {
	f.calls++
	f.requested = append(f.requested, keys...)

	rows := make([]ControllerRecordResult, len(keys))
	for i, k := range keys {
		rows[i] = ControllerRecordResult{ControllerRecordKey: k, Found: true, Size: 3, Data: []byte("row")}
	}

	if f.change != nil {
		rows = f.change(rows)
	}

	return rows, nil
}

func bindingPrefetchFixture(t *testing.T, count int) (*currentBindingBatchFixture, *attemptCheckedReads, attemptView) {
	t.Helper()

	f := &currentBindingBatchFixture{}
	reads := newAttemptCheckedReads(f)

	v := attemptView{scope: managed.Digest([]byte("scope")), descriptor: managed.AttemptDescriptor{Key: managed.Key{TenantID: "tenant", InstanceID: id.New(id.PrefixInstance)}, Generation: "3"}, ref: managed.AttemptRef{AttemptID: id.New(managed.PrefixOperation), Generation: "3"}}
	for range count {
		rid := id.New(managed.PrefixReservation)
		v.descriptor.ReservationIDs = append(v.descriptor.ReservationIDs, rid)

		slot, err := attemptSlot(attemptBinding(v, rid), managed.AttemptBegin)
		if err != nil {
			t.Fatal(err)
		}

		reads.rows[[3]string{"removal_command", v.scope, slot}] = []byte("command")
	}

	return f, reads, v
}

func TestAttemptCurrentBindingPrefetchBoundsAndOwnership(t *testing.T) {
	f, reads, v := bindingPrefetchFixture(t, 64)
	for range 2 {
		if err := reads.prefetchCurrentBindings(t.Context(), v); err != nil {
			t.Fatal(err)
		}
	}

	if f.calls != 1 || len(f.requested) != 64 {
		t.Fatal("not one separate bounded batch", f.calls, len(f.requested))
	}

	selected := f.requested[0]

	raw, err := reads.ReadRecord(t.Context(), selected.Kind, selected.Scope, selected.Key)
	if err != nil {
		t.Fatal(err)
	}

	raw[0] = 'X'

	again, err := reads.ReadRecord(t.Context(), selected.Kind, selected.Scope, selected.Key)
	if err != nil || string(again) != "row" {
		t.Fatal("caller changed retained bytes", err)
	}

	v.descriptor.ReservationIDs = append(v.descriptor.ReservationIDs, id.New(managed.PrefixReservation))
	if err = reads.prefetchCurrentBindings(t.Context(), v); !errors.Is(err, managed.ErrUnavailable) || f.calls != 1 {
		t.Fatal("unbounded transport", err)
	}
}

func TestAttemptCurrentBindingPrefetchRefusals(t *testing.T) {
	for _, name := range []string{"missing-row", "duplicate", "extra", "wrong-scope", "wrong-key", "wrong-kind", "missing-required", "oversize", "size-mismatch", "unexpected-original", "unexpected-unissued", "cancel"} {
		t.Run(name, func(t *testing.T) {
			f, reads, v := bindingPrefetchFixture(t, 2)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			want := managed.ErrUnavailable

			if name == "unexpected-original" {
				v.descriptor.Generation = "1"
			}

			if name == "unexpected-unissued" {
				clear(reads.rows)
			}

			if name == "oversize" {
				want = managed.ErrCapacity
			}

			if name == "cancel" {
				want = context.Canceled
			}

			f.change = func(rows []ControllerRecordResult) []ControllerRecordResult {
				switch name {
				case "missing-row":
					return rows[:1]
				case "duplicate":
					rows[1] = rows[0]
				case "extra":
					return append(rows, rows[0])
				case "wrong-scope":
					rows[0].Scope = managed.Digest([]byte("wrong"))
				case "wrong-key":
					rows[0].Key = managed.Digest([]byte("wrong"))
				case "wrong-kind":
					rows[0].Kind = "removal_command"
				case "missing-required":
					rows[0].Found = false
					rows[0].Size = 0
					rows[0].Data = nil
				case "oversize", "unexpected-original", "unexpected-unissued":
					rows[0].Size = managed.MaxAttemptCurrentBinding + 1
					rows[0].Data = bytes.Repeat([]byte("x"), rows[0].Size)
				case "size-mismatch":
					rows[0].Size++
				case "cancel":
					cancel()
				}

				return rows
			}

			err := reads.prefetchCurrentBindings(ctx, v)
			if !errors.Is(err, want) {
				t.Fatal("wrong refusal", err, want)
			}

			for k := range reads.rows {
				if k[0] == "removal_current_binding" {
					t.Fatal("failed batch installed a value")
				}
			}

			if len(reads.missing) != 0 {
				t.Fatal("failed batch installed absence")
			}
		})
	}
}

func TestAttemptCurrentBindingPrefetchFreshAbsence(t *testing.T) {
	f, reads, v := bindingPrefetchFixture(t, 2)
	clear(reads.rows)
	f.change = func(rows []ControllerRecordResult) []ControllerRecordResult {
		for i := range rows {
			rows[i].Found = false
			rows[i].Size = 0
			rows[i].Data = nil
		}

		return rows
	}

	if err := reads.prefetchCurrentBindings(t.Context(), v); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if err := reads.prefetchCurrentBindings(t.Context(), v); err != nil {
			t.Fatal(err)
		}
	}

	if f.calls != 1 || len(reads.missing) != 2 {
		t.Fatal("missing own slots not retained in this read")
	}

	f.change = nil

	fresh := newAttemptCheckedReads(f)
	if err := fresh.prefetchCurrentBindings(t.Context(), v); !errors.Is(err, managed.ErrUnavailable) || f.calls != 2 {
		t.Fatal("new session reused old absence", err)
	}
}

func TestAttemptCurrentBindingPrefetchCanceledBeforeTransport(t *testing.T) {
	f, reads, v := bindingPrefetchFixture(t, 2)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := reads.prefetchCurrentBindings(ctx, v); !errors.Is(err, context.Canceled) || f.calls != 0 {
		t.Fatal("canceled validation reached transport", err)
	}
}

func currentBindingParseInput(t *testing.T) (managed.AttemptCurrentBindingObservation, []byte) {
	t.Helper()

	stamp := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	digest := managed.Digest([]byte("binding"))
	value := managed.AttemptCurrentBindingObservation{SchemaVersion: "1", Key: managed.AttemptBindingKey{AttemptKey: managed.AttemptKey{Key: managed.Key{TenantID: "tenant", InstanceID: id.New(id.PrefixInstance)}, AttemptID: id.New(managed.PrefixOperation), Generation: "3"}, ReservationID: id.New(managed.PrefixReservation)}, DescriptorDigest: digest, BindingReference: managed.AttemptBindingReference{OriginalRegistrationOperationID: id.New(managed.PrefixOperation)}, BindingVersion: "10", RemovalEpoch: "4", State: "active", ObservedAt: stamp, ValidUntil: stamp.Add(time.Minute), VerifierID: "verifier", Evidence: []byte("proof"), EvidenceDigest: managed.Digest([]byte("proof"))}

	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}

	return value, raw
}

func TestAttemptParsedCurrentBindingBoundaries(t *testing.T) {
	value, raw := currentBindingParseInput(t)
	backend := &attemptReadFixture{rows: map[[3]string][]byte{}}
	reads := newAttemptCheckedReads(backend)
	key := [3]string{"removal_current_binding", managed.Digest([]byte("scope")), managed.Digest([]byte("slot"))}
	reads.rows[key] = raw

	first, digest, err := reads.parsedCurrentBinding(t.Context(), key[1], key[2])
	if err != nil || !reflect.DeepEqual(first, value) || digest == "" || len(reads.currentBindings) != 1 {
		t.Fatal("strict cache miss", err)
	}

	first.Evidence[0] = 'X'

	second, again, err := reads.parsedCurrentBinding(t.Context(), key[1], key[2])
	if err != nil || again != digest || !reflect.DeepEqual(second, value) {
		t.Fatal("miss exposed retained evidence", err)
	}

	second.Evidence[0] = 'Y'

	third, _, err := reads.parsedCurrentBinding(t.Context(), key[1], key[2])
	if err != nil || !reflect.DeepEqual(third, value) {
		t.Fatal("hit exposed retained evidence", err)
	}

	changed := value
	changed.BindingVersion = "11"

	changedRaw, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}

	reads.rows[key] = changedRaw

	got, changedDigest, err := reads.parsedCurrentBinding(t.Context(), key[1], key[2])
	if err != nil || got.BindingVersion != "11" || changedDigest == digest || len(reads.currentBindings) != 2 {
		t.Fatal("changed bytes reused", err)
	}

	for _, other := range [][3]string{{key[0], managed.Digest([]byte("other scope")), key[2]}, {key[0], key[1], managed.Digest([]byte("other slot"))}} {
		reads.rows[other] = raw
		if _, _, err = reads.parsedCurrentBinding(t.Context(), other[1], other[2]); err != nil {
			t.Fatal(err)
		}
	}

	if len(reads.currentBindings) != 4 {
		t.Fatal("scope/slot keys aliased")
	}

	for name, bad := range map[string][]byte{"null": []byte("null"), "duplicate": append([]byte(`{"schema_version":"1",`), raw[1:]...), "unknown": []byte(`{"unknown":1}`), "noncanonical": append(bytes.Clone(raw), ' ')} {
		reads.rows[key] = bad
		if _, _, err = reads.parsedCurrentBinding(t.Context(), key[1], key[2]); !errors.Is(err, managed.ErrUnavailable) || len(reads.currentBindings) != 4 {
			t.Fatal("malformed retained", name, err)
		}
	}

	reads.rows[key] = bytes.Repeat([]byte("x"), managed.MaxAttemptCurrentBinding+1)
	if _, _, err = reads.parsedCurrentBinding(t.Context(), key[1], key[2]); !errors.Is(err, managed.ErrCapacity) {
		t.Fatal("raw cap", err)
	}

	reads.rows[key] = raw
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, _, err = reads.parsedCurrentBinding(ctx, key[1], key[2]); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled cache hit", err)
	}

	backend.rows[key] = changedRaw
	fresh := newAttemptCheckedReads(backend)

	got, _, err = fresh.parsedCurrentBinding(t.Context(), key[1], key[2])
	if err != nil || got.BindingVersion != "11" || len(fresh.currentBindings) != 1 {
		t.Fatal("fresh read reused prior session", err)
	}
}

func TestAttemptParsedCurrentBindingFiniteFallback(t *testing.T) {
	_, raw := currentBindingParseInput(t)
	reads := newAttemptCheckedReads(&attemptReadFixture{})

	scope := managed.Digest([]byte("scope"))
	for i := range 65 {
		slot := managed.Digest([]byte(strconv.Itoa(i)))

		reads.rows[[3]string{"removal_current_binding", scope, slot}] = raw
		if _, _, err := reads.parsedCurrentBinding(t.Context(), scope, slot); err != nil {
			t.Fatal(err)
		}
	}

	if len(reads.currentBindings) != 64 || reads.currentBindingBytes != 64*len(raw) {
		t.Fatal("entry bound changed")
	}

	bounded := newAttemptCheckedReads(&attemptReadFixture{})
	bounded.currentBindingBytes = 64 * managed.MaxAttemptCurrentBinding
	slot := managed.Digest([]byte("slot"))

	bounded.rows[[3]string{"removal_current_binding", scope, slot}] = raw
	if _, _, err := bounded.parsedCurrentBinding(t.Context(), scope, slot); err != nil || len(bounded.currentBindings) != 0 {
		t.Fatal("byte bound must decode without retention", err)
	}
}

func TestAttemptParsedCurrentBindingDoesNotGrantAuthority(t *testing.T) {
	value, raw := currentBindingParseInput(t)
	backend := &attemptReadFixture{rows: map[[3]string][]byte{}}
	reads := newAttemptCheckedReads(backend)
	v := attemptView{scope: managed.Digest([]byte("scope")), ref: managed.AttemptRef{AttemptID: value.Key.AttemptID, Generation: value.Key.Generation, DescriptorDigest: value.DescriptorDigest}, descriptor: managed.AttemptDescriptor{Key: value.Key.Key, Generation: value.Key.Generation, VerifierID: value.VerifierID}}
	command := managed.AttemptCommandRecord{Key: managed.AttemptCommandKey{AttemptBindingKey: value.Key, Kind: managed.AttemptBegin}, BindingReference: value.BindingReference, ExpectedNativeVersion: value.BindingVersion, IssuedAt: value.ObservedAt}

	slot, err := attemptSlot(value.Key, managed.AttemptBegin)
	if err != nil {
		t.Fatal(err)
	}

	reads.rows[[3]string{"removal_current_binding", v.scope, slot}] = raw

	_, command.CurrentBindingDigest, err = reads.parsedCurrentBinding(t.Context(), v.scope, slot)
	if err != nil {
		t.Fatal(err)
	}

	target := &managed.Target{Identity: managed.Identity{Key: value.Key.Key}}

	for range 2 {
		before := backend.rowReads
		if _, err = attemptCurrentBindingRead(t.Context(), reads, target, v, command); !errors.Is(err, managed.ErrNotFound) || backend.rowReads <= before {
			t.Fatal("cached parsing skipped missing original", err)
		}
	}

	wrong := command

	wrong.CurrentBindingDigest = managed.Digest([]byte("wrong"))
	if _, err = attemptCurrentBindingRead(t.Context(), reads, target, v, wrong); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("cached command digest granted", err)
	}

	wrong = command

	wrong.ExpectedNativeVersion = "99"
	if _, err = attemptCurrentBindingRead(t.Context(), reads, target, v, wrong); !errors.Is(err, managed.ErrInvalid) {
		t.Fatal("cached version granted", err)
	}

	wrong = command

	wrong.IssuedAt = value.ValidUntil
	if _, err = attemptCurrentBindingRead(t.Context(), reads, target, v, wrong); !errors.Is(err, managed.ErrInvalid) {
		t.Fatal("cached observation became fresh", err)
	}
}

func TestAttemptParsedCurrentBindingCancellationAndRawCap(t *testing.T) {
	_, raw := currentBindingParseInput(t)
	key := [3]string{"removal_current_binding", managed.Digest([]byte("scope")), managed.Digest([]byte("slot"))}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	backend := &attemptReadFixture{rows: map[[3]string][]byte{key: raw}, afterRead: cancel}

	reads := newAttemptCheckedReads(backend)
	if _, _, err := reads.parsedCurrentBinding(ctx, key[1], key[2]); !errors.Is(err, context.Canceled) || len(reads.currentBindings) != 0 {
		t.Fatal("cancellation after transport retained parsing", err)
	}

	oversize := bytes.Repeat([]byte("x"), managed.MaxAttemptCurrentBinding+1)
	reads.rows[key] = oversize

	reads.currentBindings = map[attemptImmutableValueKey]attemptParsedCurrentBinding{{key[0], key[1], key[2], managed.Digest(oversize)}: {}}
	if _, _, err := reads.parsedCurrentBinding(t.Context(), key[1], key[2]); !errors.Is(err, managed.ErrCapacity) {
		t.Fatal("cache hit bypassed raw cap", err)
	}
}
