package managed

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
)

func recoveryCodecRecord[T any](t *testing.T, facts T) RecoveryRecord[T] {
	t.Helper()

	digest, err := RecoveryFactDigest(facts)
	if err != nil {
		t.Fatal(err)
	}

	operation := id.New(PrefixOperation)
	request := Digest([]byte("request"))
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

	return RecoveryRecord[T]{SchemaVersion: "1", Kind: recoveryFactKind(facts), Facts: facts, Digest: digest, Acceptance: RecoveryAcceptance{OperationID: operation, RequestDigest: request, Receipt: WireReceipt{ID: operation, Digest: request, Revision: "9223372036854775807", LeaseEpoch: "9223372036854775807", AcceptedAt: now, LeaseUntil: now.Add(time.Minute)}}}
}

func TestRecoveryRecordStrictCodec(t *testing.T) {
	record := recoveryCodecRecord(t, RemovalRecoveryActivation{Profile: RemovalRecoveryProfile})

	raw, err := EncodeRecoveryRecord(record)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := DecodeRecoveryRecord[RemovalRecoveryActivation](raw)
	if err != nil || decoded.Digest != record.Digest {
		t.Fatal("canonical record refused", err)
	}

	cases := [][]byte{
		append(bytes.Clone(raw), '\n'),
		append(bytes.Clone(raw), []byte("{}")...),
		append([]byte(" "), raw...),
		bytes.Replace(raw, []byte(`"schema_version":"1"`), []byte(`"schema_version":"1","schema_version":"1"`), 1),
		bytes.Replace(raw, []byte(`"schema_version":"1"`), []byte(`"unknown":true,"schema_version":"1"`), 1),
		bytes.Replace(raw, []byte(`"revision":"9223372036854775807"`), []byte(`"revision":"9223372036854775808"`), 1),
		bytes.Replace(raw, []byte(`"lease_epoch":"9223372036854775807"`), []byte(`"lease_epoch":"01"`), 1),
	}
	for i, candidate := range cases {
		if _, decodeErr := DecodeRecoveryRecord[RemovalRecoveryActivation](candidate); decodeErr == nil {
			t.Fatalf("case %d accepted noncanonical or invalid bytes", i)
		}
	}
}

func TestRecoveryNativeRawBoundsAreIndependent(t *testing.T) {
	export := RecoveryNativeExport{Raw: bytes.Repeat([]byte{255}, MaxRecoveryExport), Attestation: bytes.Repeat([]byte{254}, MaxRecoveryAttestation), ProducerID: "producer", ProducerProfile: "fixture.v1", ProducerVersion: "1", VerifierID: "verifier"}
	export.RawDigest = Digest(export.Raw)

	export.AttestationDigest = Digest(export.Attestation)
	if err := ValidateRecoveryNativeExport(export); err != nil {
		t.Fatal(err)
	}

	record := recoveryCodecRecord(t, RemovalRevalidationFacts{NativeMaterialDigest: strings.Repeat("a", 64), NativeCurrentDigest: strings.Repeat("b", 64), NativeReceiptDigest: strings.Repeat("c", 64), Export: export})

	raw, err := EncodeRecoveryRecord(record)
	if err != nil {
		t.Fatal(err)
	}

	if len(raw) < 10928 || len(raw) > MaxRemovalRevalidation {
		t.Fatal("actual JSON omitted base64 expansion or exceeded leaf cap", len(raw))
	}

	decoded, err := DecodeRecoveryRecord[RemovalRevalidationFacts](raw)
	if err != nil || decoded.Facts.NativeMaterialDigest == decoded.Facts.Export.RawDigest {
		t.Fatal("native and raw digest domains conflated", err)
	}

	export.Raw = append(export.Raw, 1)

	export.RawDigest = Digest(export.Raw)
	if err = ValidateRecoveryNativeExport(export); !errors.Is(err, ErrCapacity) {
		t.Fatal("raw cap+1 accepted", err)
	}

	export.Raw = export.Raw[:MaxRecoveryExport]
	export.RawDigest = Digest(export.Raw)
	export.Attestation = append(export.Attestation, 1)

	export.AttestationDigest = Digest(export.Attestation)
	if err = ValidateRecoveryNativeExport(export); !errors.Is(err, ErrCapacity) {
		t.Fatal("attestation cap+1 accepted", err)
	}
}

func TestRecoveryRecordActualCap(t *testing.T) {
	record := recoveryCodecRecord(t, RecoveryNativeIntent{Profile: RemovalRecoveryProfile})

	raw, err := EncodeRecoveryRecord(record)
	if err != nil {
		t.Fatal(err)
	}

	// RequestID is plain ASCII, so each extra byte contributes exactly one JSON byte.
	record.Facts.RequestID = strings.Repeat("x", MaxRecoveryNativeIntent-len(raw))

	record.Digest, err = RecoveryFactDigest(record.Facts)
	if err != nil {
		t.Fatal(err)
	}

	raw, err = EncodeRecoveryRecord(record)
	if err != nil || len(raw) != MaxRecoveryNativeIntent {
		t.Fatal("joint encoded cap refused", len(raw), err)
	}

	record.Facts.RequestID += "x"

	record.Digest, err = RecoveryFactDigest(record.Facts)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = EncodeRecoveryRecord(record); !errors.Is(err, ErrCapacity) {
		t.Fatal("joint encoded cap+1 accepted", err)
	}
}

func TestRecoveryDigestExcludesLocalReceipt(t *testing.T) {
	record := recoveryCodecRecord(t, RemovalRecoveryActivation{Profile: RemovalRecoveryProfile})
	original := record.Digest
	record.Acceptance.Receipt.Revision = "2"

	raw, err := EncodeRecoveryRecord(record)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := DecodeRecoveryRecord[RemovalRecoveryActivation](raw)
	if err != nil || decoded.Digest != original {
		t.Fatal("local receipt entered the fact hash domain", err)
	}

	record.Facts.Profile += ".changed"
	if _, err = EncodeRecoveryRecord(record); !errors.Is(err, ErrInvalid) {
		t.Fatal("changed facts retained an old digest", err)
	}
}

func TestRecoveryFreshnessUsesCallbackTime(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

	proof := RemovalRevalidationFacts{SourceObservedAt: now.Add(-time.Second), NativeAcceptedAt: now, ValidUntil: now.Add(time.Second)}
	if !RecoveryProofFresh(proof, now) || RecoveryProofFresh(proof, proof.ValidUntil) || RecoveryProofFresh(proof, now.Add(-time.Second)) {
		t.Fatal("incorrect exact acceptance/expiry boundary")
	}
}

func TestRecoveryCurrentAndPreviousCaps(t *testing.T) {
	reference := RecoveryAssociation{Kind: RecoveryAuthorizationRecord, Key: strings.Repeat("a", 64), Digest: strings.Repeat("b", 64), OperationID: id.New(PrefixOperation), ReceiptDigest: strings.Repeat("c", 64)}
	current := ProviderStepRetryCurrent{SchemaVersion: "1", RootDigest: strings.Repeat("a", 64), CommandID: id.New(PrefixOperation), Ordinal: "9223372036854775807", Permission: reference, Authority: reference, PreviousDigest: strings.Repeat("b", 64), MarkerKey: strings.Repeat("c", 64), FailureKey: strings.Repeat("d", 64), ClosureKey: strings.Repeat("e", 64), ResultKey: strings.Repeat("f", 64), Digest: strings.Repeat("a", 64)}

	previous := ProviderStepRetryPrevious{SchemaVersion: "1", RootDigest: current.RootDigest, CommandID: current.CommandID, Ordinal: current.Ordinal, PermissionDigest: strings.Repeat("a", 64), Failure: reference, Closure: reference, Authority: reference, Digest: current.Digest}
	for _, value := range []any{current, previous} {
		raw, err := json.Marshal(value)
		if err != nil || len(raw) > MaxStepRetryPointer {
			t.Fatal("fixed pointer exceeds its joint maximum", len(raw), err)
		}
	}
}
