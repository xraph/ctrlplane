package managedstate

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/xraph/ctrlplane/managed"
)

type effectBindingWitness struct {
	facts        managed.ProviderEffectBindingFacts
	produceErr   error
	verifyErr    error
	produceCalls int
	verifyCalls  int
	mutate       bool
}

func (w *effectBindingWitness) RecoverProviderEffectBinding(context.Context, managed.ProviderEffectOriginal) (managed.ProviderEffectBindingFacts, error) {
	w.produceCalls++

	return w.facts, w.produceErr
}

func (w *effectBindingWitness) VerifyProviderEffectBinding(_ context.Context, _ managed.ProviderEffectOriginal, facts managed.ProviderEffectBindingFacts) error {
	w.verifyCalls++
	if w.mutate {
		facts.Command[0] = 'x'
		facts.Attestation[0] = 'x'
	}

	return w.verifyErr
}

func effectBindingFixture(t *testing.T) (*recoveryLeafFixture, managed.ProviderEffectBindingRecord, *ProviderEffectBindingRepository, *effectBindingWitness) {
	t.Helper()
	fixture, key, rootRef, _, now := retryOriginalFixture(t)

	root, err := recoveryReadFact[managed.ProviderStepRetryRoot](t.Context(), fixture, key, rootRef)
	if err != nil {
		t.Fatal(err)
	}

	original := managed.ProviderEffectOriginal{Key: managed.ProviderEffectBindingKey{AttemptKey: key, Step: root.Facts.Step, OriginalCommandID: root.Facts.OriginalPermission.CommandID}, AttemptRef: root.Facts.AttemptRef, Permission: root.Facts.OriginalPermission, Receipt: root.Facts.OriginalPermissionReceipt}
	identity := managed.RecoveryProducerIdentity{ProducerID: "fixture", Profile: "fixture.v1", Version: "1", VerifierID: "fixture-verifier"}

	command, err := managed.EncodeProviderEffectCommand(managed.ProviderEffectCommand{SchemaVersion: "1", Profile: identity.Profile, BackendAuthority: "authenticated-fixture", ResourceKind: "container", ResourceID: original.Permission.ResourceID, Incarnation: "authenticated-creation", Method: "remove", Options: []managed.ProviderEffectOption{}})
	if err != nil {
		t.Fatal(err)
	}

	facts := managed.ProviderEffectBindingFacts{Original: original, Producer: identity, Command: command, Attestation: []byte("fixture retained custody")}

	facts.ProviderCommandDigest, err = managed.ProviderEffectCommandDigest(command)
	if err != nil {
		t.Fatal(err)
	}

	record := managed.ProviderEffectBindingRecord{SchemaVersion: "1", Kind: managed.ProviderEffectBindingKind, Facts: facts, RequestDigest: strings.Repeat("e", 64), Acceptance: recoveryTestRecord(t, managed.RecoveryRootRecord, root.Facts, now).Acceptance}

	record.Digest, err = managed.ProviderEffectBindingDigest(facts)
	if err != nil {
		t.Fatal(err)
	}

	record.Acceptance.RequestDigest, err = managed.ProviderEffectBindingAcceptanceDigest(record.RequestDigest, record.Digest)
	if err != nil {
		t.Fatal(err)
	}

	record.Acceptance.Receipt.Digest = record.Acceptance.RequestDigest
	witness := &effectBindingWitness{facts: facts}

	repository, err := New(&recoveryBackendWitness{}).WithProviderEffectBindings(managed.ProviderEffectBindingHost{Producer: witness, Verifier: witness, Identity: identity, Provider: "docker", BackendAuthority: "authenticated-fixture"})
	if err != nil {
		t.Fatal(err)
	}

	return fixture, record, repository, witness
}

func TestProviderEffectConfiguredHostHasNoDockerDefault(t *testing.T) {
	_, _, repository, witness := effectBindingFixture(t)

	var absent *effectBindingWitness

	for _, change := range []func(*managed.ProviderEffectBindingHost){
		func(h *managed.ProviderEffectBindingHost) { h.Producer = nil },
		func(h *managed.ProviderEffectBindingHost) { h.Producer = absent },
		func(h *managed.ProviderEffectBindingHost) { h.Verifier = absent },
		func(h *managed.ProviderEffectBindingHost) { h.BackendAuthority = "" },
		func(h *managed.ProviderEffectBindingHost) { h.Identity.Version = "0" },
	} {
		host := repository.host
		change(&host)

		if _, err := repository.repository.WithProviderEffectBindings(host); err == nil {
			t.Fatal("incomplete host accepted")
		}
	}

	if witness.produceCalls != 0 || witness.verifyCalls != 0 {
		t.Fatal("configuration invoked producer")
	}
	// This fixture deliberately supplies its own producer. Core installs no Docker
	// transport or authority implementation through either configuration constructor.
	if repository.repository.recoveryHost != nil {
		t.Fatal("effect binding enabled complete recovery")
	}
}

func TestProviderEffectMissingHistoricalEvidenceRefuses(t *testing.T) {
	_, record, repository, witness := effectBindingFixture(t)
	for _, refusal := range []error{managed.ErrUnsupported, managed.ErrUnavailable} {
		witness.produceErr = refusal
		if _, err := repository.recoverProviderEffect(t.Context(), record.Facts.Original); !errors.Is(err, refusal) {
			t.Fatal("missing history changed into success", err)
		}
	}

	if witness.verifyCalls != 0 {
		t.Fatal("missing evidence reached verifier")
	}
}

func TestProviderEffectRejectsSubstitutionBeforeAcceptingVerifier(t *testing.T) {
	for name, change := range map[string]func(*managed.ProviderEffectBindingFacts){
		"producer": func(f *managed.ProviderEffectBindingFacts) { f.Producer.ProducerID = "other" },
		"verifier": func(f *managed.ProviderEffectBindingFacts) { f.Producer.VerifierID = "other" },
		"original": func(f *managed.ProviderEffectBindingFacts) { f.Original.Permission.Physical.Generation = "other" },
		"receipt":  func(f *managed.ProviderEffectBindingFacts) { f.Original.Receipt.Digest = strings.Repeat("f", 64) },
		"command":  func(f *managed.ProviderEffectBindingFacts) { f.ProviderCommandDigest = strings.Repeat("f", 64) },
		"backend": func(f *managed.ProviderEffectBindingFacts) {
			command, err := managed.DecodeProviderEffectCommand(f.Command)
			if err != nil {
				t.Fatal(err)
			}

			command.BackendAuthority = "other"

			f.Command, err = managed.EncodeProviderEffectCommand(command)
			if err != nil {
				t.Fatal(err)
			}

			f.ProviderCommandDigest, err = managed.ProviderEffectCommandDigest(f.Command)
			if err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, record, repository, witness := effectBindingFixture(t)
			change(&witness.facts)

			if _, err := repository.recoverProviderEffect(t.Context(), record.Facts.Original); err == nil {
				t.Fatal("substitution accepted")
			}

			if witness.verifyCalls != 0 {
				t.Fatal("substitution reached verifier")
			}
		})
	}
}

func TestProviderEffectVerifierFailureAndSnapshots(t *testing.T) {
	_, record, repository, witness := effectBindingFixture(t)

	witness.verifyErr = managed.ErrAuthority
	if _, err := repository.recoverProviderEffect(t.Context(), record.Facts.Original); !errors.Is(err, managed.ErrAuthority) {
		t.Fatal(err)
	}

	witness.verifyErr = nil
	witness.mutate = true

	out, err := repository.recoverProviderEffect(t.Context(), record.Facts.Original)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(out, record.Facts) {
		t.Fatal("verifier mutated retained facts")
	}

	witness.facts.Command[0] = 'x'
	if out.Command[0] == 'x' {
		t.Fatal("producer retained mutable output")
	}
}

func retainEffectFixture(t *testing.T, fixture *recoveryLeafFixture, record managed.ProviderEffectBindingRecord) {
	t.Helper()

	if err := providerEffectInsertHeld(t.Context(), fixture, record); err != nil {
		t.Fatal(err)
	}

	receipt, err := record.Acceptance.Receipt.Value()
	if err != nil {
		t.Fatal(err)
	}

	fixture.receipts[attemptReceiptKey{record.Facts.Original.Key.Key, receipt.ID, receipt.Digest}] = receipt
}

func TestProviderEffectExactReadAndRootRequireBothReceipts(t *testing.T) {
	fixture, record, _, _ := effectBindingFixture(t)
	if _, err := providerEffectRootHeld(t.Context(), fixture, record.Facts.Original); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("missing history became root", err)
	}

	retainEffectFixture(t, fixture, record)

	root, err := providerEffectRootHeld(t.Context(), fixture, record.Facts.Original)
	if err != nil {
		t.Fatal(err)
	}

	if root.Incarnation != "authenticated-creation" || root.Incarnation == root.OriginalPermission.Physical.Generation || root.ProviderCommandDigest != record.Facts.ProviderCommandDigest || !reflect.DeepEqual(root.OriginalPermission, record.Facts.Original.Permission) {
		t.Fatal("root invented authority or changed original")
	}

	for _, wire := range []managed.WireReceipt{record.Facts.Original.Receipt, record.Acceptance.Receipt} {
		receipt, receiptErr := wire.Value()
		if receiptErr != nil {
			t.Fatal(receiptErr)
		}

		key := attemptReceiptKey{record.Facts.Original.Key.Key, receipt.ID, receipt.Digest}
		delete(fixture.receipts, key)

		if _, err = providerEffectReadHeld(t.Context(), fixture, record.Facts.Original); !errors.Is(err, managed.ErrUnavailable) {
			t.Fatal("missing receipt accepted", err)
		}

		receipt.Revision++

		fixture.receipts[key] = receipt
		if _, err = providerEffectReadHeld(t.Context(), fixture, record.Facts.Original); !errors.Is(err, managed.ErrUnavailable) {
			t.Fatal("changed receipt accepted", err)
		}

		receipt.Revision--
		fixture.receipts[key] = receipt
	}

	before := fixture.reads
	if _, err = providerEffectReadHeld(t.Context(), fixture, record.Facts.Original); err != nil {
		t.Fatal(err)
	}

	if fixture.reads-before != 3 {
		t.Fatal("effect reader did not use exact row and two receipts")
	}

	changed := record.Facts.Original

	changed.Permission.Physical.Generation = "same-name-other-custody"
	if _, err = providerEffectReadHeld(t.Context(), fixture, changed); !errors.Is(err, managed.ErrAuthority) {
		t.Fatal("original substitution accepted", err)
	}
}

func TestProviderEffectInsertIsImmutableAndRefusesBeforeWrite(t *testing.T) {
	fixture, record, _, _ := effectBindingFixture(t)

	scope, key, err := providerEffectKey(record.Facts.Original.Key)
	if err != nil {
		t.Fatal(err)
	}

	selected := [3]string{managed.ProviderEffectBindingKind, scope, key}
	before := fixture.writes

	originalRows := make(map[[3]string][]byte, len(fixture.rows))
	for rowKey, row := range fixture.rows {
		originalRows[rowKey] = bytes.Clone(row)
	}

	bad := record

	bad.Facts.Attestation = bytes.Repeat([]byte{255}, managed.MaxProviderEffectAttestation+1)
	if err = providerEffectInsertHeld(t.Context(), fixture, bad); err == nil || fixture.writes != before {
		t.Fatal("invalid evidence wrote a row", err)
	}

	retainEffectFixture(t, fixture, record)

	for rowKey, row := range originalRows {
		if !bytes.Equal(row, fixture.rows[rowKey]) {
			t.Fatal("retention changed an original row")
		}
	}

	raw := bytes.Clone(fixture.rows[selected])
	if err = providerEffectInsertHeld(t.Context(), fixture, record); !errors.Is(err, managed.ErrConflict) {
		t.Fatal("immutable row overwritten", err)
	}

	if !bytes.Equal(raw, fixture.rows[selected]) {
		t.Fatal("duplicate insert changed retained bytes")
	}

	if ImmutableRecordLimit(managed.ProviderEffectBindingKind) != managed.MaxProviderEffectBinding {
		t.Fatal("finite batch cap missing")
	}

	if err = ValidateImmutableRecordKeys([]ControllerRecordKey{{Kind: managed.ProviderEffectBindingKind, Scope: scope, Key: key}}); err != nil {
		t.Fatal(err)
	}
}

func TestProviderEffectHostRejectsTransactionViewsBeforeCallbacks(t *testing.T) {
	fixture, _, repository, witness := effectBindingFixture(t)

	var absent *recoveryLeafFixture
	for _, backend := range []Backend{fixture, absent} {
		if _, err := New(backend).WithProviderEffectBindings(repository.host); !errors.Is(err, managed.ErrUnsupported) {
			t.Fatal("transaction-backed host accepted", err)
		}
	}

	if witness.produceCalls != 0 || witness.verifyCalls != 0 {
		t.Fatal("held configuration invoked host code")
	}
}

func TestProviderEffectRehashedRowCannotReplaceIndependentReceipt(t *testing.T) {
	fixture, record, _, _ := effectBindingFixture(t)
	retainEffectFixture(t, fixture, record)

	command, err := managed.DecodeProviderEffectCommand(record.Facts.Command)
	if err != nil {
		t.Fatal(err)
	}

	command.Incarnation = "invented-replacement"

	record.Facts.Command, err = managed.EncodeProviderEffectCommand(command)
	if err != nil {
		t.Fatal(err)
	}

	record.Facts.Attestation = []byte("replacement")

	record.Facts.ProviderCommandDigest, err = managed.ProviderEffectCommandDigest(record.Facts.Command)
	if err != nil {
		t.Fatal(err)
	}

	record.Digest, err = managed.ProviderEffectBindingDigest(record.Facts)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = managed.EncodeProviderEffectBinding(record); !errors.Is(err, managed.ErrInvalid) {
		t.Fatal("fact replacement kept old receipt", err)
	}
	// Recompute every self-contained field, including the row's copy of acceptance.
	// The separate backend receipt remains the one actually committed before tampering.
	record.Acceptance.RequestDigest, err = managed.ProviderEffectBindingAcceptanceDigest(record.RequestDigest, record.Digest)
	if err != nil {
		t.Fatal(err)
	}

	record.Acceptance.Receipt.Digest = record.Acceptance.RequestDigest

	raw, err := managed.EncodeProviderEffectBinding(record)
	if err != nil {
		t.Fatal(err)
	}

	scope, selected, err := providerEffectKey(record.Facts.Original.Key)
	if err != nil {
		t.Fatal(err)
	}

	fixture.rows[[3]string{managed.ProviderEffectBindingKind, scope, selected}] = raw
	if _, err = providerEffectReadHeld(t.Context(), fixture, record.Facts.Original); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("rewritten row authenticated itself", err)
	}
}
