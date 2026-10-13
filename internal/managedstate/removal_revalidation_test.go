package managedstate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

type recoveryLeafFixture struct {
	ControllerTransaction

	rows      map[[3]string][]byte
	receipts  map[attemptReceiptKey]managed.Receipt
	reads     int
	targetKey managed.Key
	writes    int
}

func (fixture *recoveryLeafFixture) ReadRecord(_ context.Context, kind, scope, key string) ([]byte, error) {
	fixture.reads++

	raw, found := fixture.rows[[3]string{kind, scope, key}]
	if !found {
		return nil, managed.ErrNotFound
	}

	return bytes.Clone(raw), nil
}

func (fixture *recoveryLeafFixture) WriteRecord(_ context.Context, kind, scope, key string, raw []byte, insert bool) error {
	selected := [3]string{kind, scope, key}

	_, found := fixture.rows[selected]
	if insert && found {
		return managed.ErrConflict
	}

	if !insert && !found {
		return managed.ErrNotFound
	}

	fixture.writes++
	fixture.rows[selected] = bytes.Clone(raw)

	return nil
}

func (fixture *recoveryLeafFixture) ReadReceipt(_ context.Context, key managed.Key, operation id.ID, digest string) (managed.Receipt, error) {
	fixture.reads++

	receipt, found := fixture.receipts[attemptReceiptKey{key, operation, digest}]
	if !found {
		return managed.Receipt{}, managed.ErrNotFound
	}

	return receipt, nil
}

func recoveryFixture() (*recoveryLeafFixture, managed.AttemptKey, time.Time) {
	return &recoveryLeafFixture{rows: map[[3]string][]byte{}, receipts: map[attemptReceiptKey]managed.Receipt{}}, managed.AttemptKey{Key: managed.Key{TenantID: "tenant", InstanceID: id.New(id.PrefixInstance)}, AttemptID: id.New(managed.PrefixOperation), Generation: "1"}, time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
}

func recoveryTestRecord[T any](t *testing.T, kind managed.RecoveryRecordKind, facts T, now time.Time) managed.RecoveryRecord[T] {
	t.Helper()

	hash, err := managed.RecoveryFactDigest(facts)
	if err != nil {
		t.Fatal(err)
	}

	operation := id.New(managed.PrefixOperation)
	request := managed.Digest([]byte(operation.String()))
	receipt := managed.Receipt{ID: operation, Digest: request, Revision: 2, LeaseEpoch: 1, AcceptedAt: now, LeaseUntil: now.Add(time.Minute)}

	return managed.RecoveryRecord[T]{SchemaVersion: "1", Kind: kind, Facts: facts, Digest: hash, Acceptance: managed.RecoveryAcceptance{OperationID: operation, RequestDigest: request, Receipt: managed.ReceiptWire(receipt)}}
}

func recoveryTestStore[T any](t *testing.T, fixture *recoveryLeafFixture, k managed.AttemptKey, record managed.RecoveryRecord[T]) managed.RecoveryAssociation {
	t.Helper()

	reference, err := recoveryWriteFact(t.Context(), fixture, k, record)
	if err != nil {
		t.Fatal(err)
	}

	receipt, err := record.Acceptance.Receipt.Value()
	if err != nil {
		t.Fatal(err)
	}

	fixture.receipts[attemptReceiptKey{k.Key, receipt.ID, receipt.Digest}] = receipt

	return reference
}

func recoveryTestProof(t *testing.T, key managed.AttemptKey, now time.Time) managed.RecoveryRecord[managed.RemovalRevalidationFacts] {
	t.Helper()

	export := managed.RecoveryNativeExport{Raw: []byte("compact"), RawDigest: managed.Digest([]byte("compact")), Attestation: []byte("attestation"), AttestationDigest: managed.Digest([]byte("attestation")), ProducerID: "producer", ProducerProfile: "fixture.v1", ProducerVersion: "1", VerifierID: "verifier"}
	facts := managed.RemovalRevalidationFacts{Binding: managed.RemovalRecoveryBinding{Key: managed.AttemptBindingKey{AttemptKey: key, ReservationID: id.New(managed.PrefixReservation)}, DescriptorDigest: strings.Repeat("a", 64)}, GuardID: "guard", GuardRootDigest: strings.Repeat("b", 64), SourceObservedAt: now.Add(-time.Second), NativeAcceptedAt: now, ValidUntil: now.Add(time.Minute), Export: export}

	return recoveryTestRecord(t, managed.RecoveryProofRecord, facts, now)
}

func TestRecoveryLeafRequiresExactAcceptanceAndReceipt(t *testing.T) {
	fixture, key, now := recoveryFixture()
	record := recoveryTestProof(t, key, now)

	reference := recoveryTestStore(t, fixture, key, record)
	if _, err := recoveryReadFact[managed.RemovalRevalidationFacts](t.Context(), fixture, key, reference); err != nil {
		t.Fatal(err)
	}

	scope, err := attemptScope(key.Key)
	if err != nil {
		t.Fatal(err)
	}

	acceptedKey := [3]string{recoveryAcceptanceKind, scope, reference.Key}
	saved := fixture.rows[acceptedKey]
	delete(fixture.rows, acceptedKey)

	if _, err = recoveryReadFact[managed.RemovalRevalidationFacts](t.Context(), fixture, key, reference); err == nil {
		t.Fatal("missing acceptance inferred from leaf presence")
	}

	fixture.rows[acceptedKey] = saved

	fixture.receipts = map[attemptReceiptKey]managed.Receipt{}
	if _, err = recoveryReadFact[managed.RemovalRevalidationFacts](t.Context(), fixture, key, reference); err == nil {
		t.Fatal("missing parent receipt inferred from acceptance")
	}
}

func TestRecoveryProofReplayDoesNotAdvanceCurrent(t *testing.T) {
	fixture, key, now := recoveryFixture()
	first := recoveryTestProof(t, key, now)

	accepted, fresh, err := recoveryAdvanceProof(t.Context(), fixture, "", first, now)
	if err != nil || !fresh {
		t.Fatal("fresh proof refused", err)
	}

	receipt, err := accepted.Acceptance.Receipt.Value()
	if err != nil {
		t.Fatal(err)
	}

	fixture.receipts[attemptReceiptKey{key.Key, receipt.ID, receipt.Digest}] = receipt

	current, _, err := recoveryReadCurrent(t.Context(), fixture, first.Facts.Binding)
	if err != nil {
		t.Fatal(err)
	}

	secondFacts := first.Facts
	secondFacts.SourceObservedAt = now.Add(time.Second)
	secondFacts.NativeAcceptedAt = now.Add(time.Second)
	secondFacts.ValidUntil = now.Add(2 * time.Minute)
	second := recoveryTestRecord(t, managed.RecoveryProofRecord, secondFacts, now.Add(time.Second))

	accepted, fresh, err = recoveryAdvanceProof(t.Context(), fixture, current.Digest, second, now.Add(time.Second))
	if err != nil || !fresh {
		t.Fatal("advance refused", err)
	}

	receipt, err = accepted.Acceptance.Receipt.Value()
	if err != nil {
		t.Fatal(err)
	}

	fixture.receipts[attemptReceiptKey{key.Key, receipt.ID, receipt.Digest}] = receipt
	writes := fixture.writes

	replayed, fresh, err := recoveryAdvanceProof(t.Context(), fixture, "obsolete-current", first, now.Add(time.Hour))
	if err != nil || fresh || replayed.Digest != first.Digest || fixture.writes != writes {
		t.Fatal("historical replay issued or changed selection", err)
	}

	_, selected, err := recoveryReadCurrent(t.Context(), fixture, first.Facts.Binding)
	if err != nil || selected.Digest != second.Digest {
		t.Fatal("replay rewound current", err)
	}
}

func TestRecoveryDanglingCurrentCannotBecomeFirstProof(t *testing.T) {
	fixture, key, now := recoveryFixture()
	record := recoveryTestProof(t, key, now)

	accepted, fresh, err := recoveryAdvanceProof(t.Context(), fixture, "", record, now)
	if err != nil || !fresh {
		t.Fatal(err)
	}

	scope, err := attemptScope(key.Key)
	if err != nil {
		t.Fatal(err)
	}

	selected, err := recoveryFactKey(key, accepted.Kind, accepted.Acceptance.OperationID, accepted.Facts)
	if err != nil {
		t.Fatal(err)
	}

	delete(fixture.rows, [3]string{string(accepted.Kind), scope, selected})

	next := recoveryTestRecord(t, managed.RecoveryProofRecord, record.Facts, now)
	if _, _, err = recoveryAdvanceProof(t.Context(), fixture, "", next, now); err == nil {
		t.Fatal("missing selected proof silently replaced")
	}
}

func TestRecoveryProofPointerRefusesCorruptJSON(t *testing.T) {
	fixture, key, now := recoveryFixture()
	record := recoveryTestProof(t, key, now)

	_, _, err := recoveryAdvanceProof(t.Context(), fixture, "", record, now)
	if err != nil {
		t.Fatal(err)
	}

	binding, err := recoveryBindingKey(record.Facts.Binding)
	if err != nil {
		t.Fatal(err)
	}

	scope, err := attemptScope(key.Key)
	if err != nil {
		t.Fatal(err)
	}

	fixture.rows[[3]string{recoveryCurrentKind, scope, binding}] = []byte(`{"schema_version":"1","unknown":true}`)
	if _, _, err = recoveryReadCurrent(t.Context(), fixture, record.Facts.Binding); err == nil {
		t.Fatal("corrupt pointer accepted")
	}
}

func TestRecoveryTransportKeysStayFinite(t *testing.T) {
	key := ControllerRecordKey{Kind: string(managed.RecoveryProofRecord), Scope: strings.Repeat("a", 64), Key: strings.Repeat("b", 64)}
	if err := ValidateImmutableRecordKeys([]ControllerRecordKey{key}); err != nil {
		t.Fatal(err)
	}

	keys := make([]ControllerRecordKey, 65)
	for i := range keys {
		keys[i] = key
	}

	if err := ValidateImmutableRecordKeys(keys); !errors.Is(err, managed.ErrInvalid) {
		t.Fatal("unbounded exact batch accepted", err)
	}

	key.Kind = recoveryCurrentKind
	if err := ValidateImmutableRecordKeys([]ControllerRecordKey{key}); err == nil {
		t.Fatal("mutable pointer accepted by immutable batch")
	}

	if err := ValidateHeldRecordKeys([]ControllerRecordKey{key}); err != nil {
		t.Fatal("mutable pointer refused by held batch", err)
	}
}

func TestRecoveryLeafSetOwnsExportBytes(t *testing.T) {
	_, key, now := recoveryFixture()
	proof := recoveryTestProof(t, key, now)
	ref := managed.AttemptRef{AttemptID: key.AttemptID, Generation: key.Generation, DescriptorDigest: proof.Facts.Binding.DescriptorDigest}
	current := managed.RemovalRevalidationCurrent{BindingDigest: strings.Repeat("c", 64), Proof: managed.RecoveryAssociation{Digest: proof.Digest}}

	out, err := recoveryLeafSet(ref, []managed.RecoveryRecord[managed.RemovalRevalidationFacts]{proof}, []managed.RemovalRevalidationCurrent{current})
	if err != nil {
		t.Fatal(err)
	}

	out.Proofs[0].Facts.Export.Raw[0] = 'x'
	if string(proof.Facts.Export.Raw) != "compact" {
		t.Fatal("response aliases checked proof memory")
	}

	raw, err := json.Marshal(out)
	if err != nil || len(raw) > managed.MaxRecoveryLeafSet {
		t.Fatal("invalid response bytes", err)
	}
}

func (fixture *recoveryLeafFixture) Read(_ context.Context, key managed.Key) (*managed.Target, error) {
	if key != fixture.targetKey {
		return nil, managed.ErrAuthority
	}

	return &managed.Target{Identity: managed.Identity{Key: key}}, nil
}

func TestRecoveryNativeExactReadersPreserveSavedReceipt(t *testing.T) {
	fixture, key, now := recoveryFixture()
	fixture.targetKey = key.Key
	repository := New(fixture)
	proof := recoveryTestProof(t, key, now)
	intentFacts := managed.RecoveryNativeIntent{Binding: proof.Facts.Binding, Profile: managed.RemovalRecoveryProfile, Operation: "revalidate", RequestID: "native-request", RequestDigest: strings.Repeat("a", 64), Command: []byte("opaque native request"), CommandDigest: managed.Digest([]byte("opaque native request"))}
	intent := recoveryTestRecord(t, managed.RecoveryIntentRecord, intentFacts, now)
	intentRef := recoveryTestStore(t, fixture, key, intent)

	readIntent, err := repository.ReadManagedRecoveryNativeIntent(t.Context(), managed.RecoveryExactRequest{Key: key, Association: intentRef})
	if err != nil || readIntent.Acceptance != intent.Acceptance || !bytes.Equal(readIntent.Facts.Command, intent.Facts.Command) {
		t.Fatal("exact native request or original receipt changed", err)
	}

	commitFacts := managed.ProviderNativeCommitFacts{Binding: proof.Facts.Binding, Invocation: managed.ProviderInvocationKey{AttemptKey: key, CommandID: id.New(managed.PrefixOperation)}, Intent: intentRef, NativeRequestID: "bind-request", NativeRequestDigest: strings.Repeat("b", 64), NativeReceiptDigest: strings.Repeat("c", 64), Export: proof.Facts.Export}
	commit := recoveryTestRecord(t, managed.RecoveryCommitRecord, commitFacts, now)
	commitRef := recoveryTestStore(t, fixture, key, commit)

	readCommit, err := repository.ReadManagedProviderNativeCommit(t.Context(), managed.RecoveryExactRequest{Key: key, Association: commitRef})
	if err != nil || readCommit.Acceptance != commit.Acceptance || readCommit.Digest != commit.Digest {
		t.Fatal("exact native commit or original receipt changed", err)
	}

	writes := fixture.writes
	if _, err = repository.ReadManagedProviderNativeCommit(t.Context(), managed.RecoveryExactRequest{Key: key, Association: intentRef}); err == nil || fixture.writes != writes {
		t.Fatal("wrong typed exact association accepted or mutated storage", err)
	}
}

func TestRecoveryExactReadChecksExportShape(t *testing.T) {
	fixture, key, now := recoveryFixture()
	record := recoveryTestProof(t, key, now)
	record.Facts.Export.Raw = bytes.Repeat([]byte("x"), managed.MaxRecoveryExport+1)
	record.Facts.Export.RawDigest = managed.Digest(record.Facts.Export.Raw)

	var err error

	record.Digest, err = managed.RecoveryFactDigest(record.Facts)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = recoveryWriteFact(t.Context(), fixture, key, record); !errors.Is(err, managed.ErrCapacity) || fixture.writes != 0 {
		t.Fatal("export cap was enforced only after a write", err)
	}
}
