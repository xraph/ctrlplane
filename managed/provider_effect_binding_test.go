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

func effectCodecFixture(t *testing.T) ProviderEffectBindingRecord {
	t.Helper()

	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	key := AttemptKey{Key: Key{TenantID: "tenant", InstanceID: id.New(id.PrefixInstance)}, AttemptID: id.New(PrefixOperation), Generation: "1"}
	step := ProviderStepKey{OperationKey: OperationKey{Key: key.Key, ActionID: id.New(PrefixOperation)}, ProviderOperationID: id.New(PrefixOperation), Kind: StepRemoveContainer, Service: "api"}
	permission := ProviderStepPermission{Step: step, CommandID: id.New(PrefixOperation), Digest: strings.Repeat("a", 64), SpecDigest: strings.Repeat("b", 64), CreateProfile: "ctrlplane.managed.create.v1", Physical: Physical{Provider: "fixture", Placement: "local", Project: "project", Generation: "reserved-name"}, Seal: 1, FenceDigest: strings.Repeat("c", 64), ResourceID: "exact-resource", IssuedAt: now, Revision: 1}
	original := ProviderEffectOriginal{Key: ProviderEffectBindingKey{AttemptKey: key, Step: step, OriginalCommandID: permission.CommandID}, AttemptRef: AttemptRef{AttemptID: key.AttemptID, Generation: key.Generation, DescriptorDigest: strings.Repeat("d", 64)}, Permission: ProviderPermissionWire(permission), Receipt: ReceiptWire(Receipt{ID: permission.CommandID, Digest: permission.Digest, Revision: 1, LeaseEpoch: 1, AcceptedAt: now, LeaseUntil: now.Add(time.Minute)})}

	command, err := EncodeProviderEffectCommand(ProviderEffectCommand{SchemaVersion: "1", Profile: "fixture.v1", BackendAuthority: "authenticated-backend", ResourceKind: "container", ResourceID: permission.ResourceID, Incarnation: "creation-attestation", Method: "remove", Options: []ProviderEffectOption{{Name: "force", Value: "true"}, {Name: "remove_volumes", Value: "true"}}})
	if err != nil {
		t.Fatal(err)
	}

	facts := ProviderEffectBindingFacts{Original: original, Producer: RecoveryProducerIdentity{ProducerID: "producer", Profile: "fixture.v1", Version: "1", VerifierID: "verifier"}, Command: command, Attestation: []byte("fixture custody witness")}

	facts.ProviderCommandDigest, err = ProviderEffectCommandDigest(command)
	if err != nil {
		t.Fatal(err)
	}

	operation := id.New(PrefixOperation)
	receipt := Receipt{ID: operation, Digest: strings.Repeat("e", 64), Revision: 1, LeaseEpoch: 1, AcceptedAt: now, LeaseUntil: now.Add(time.Minute)}
	record := ProviderEffectBindingRecord{SchemaVersion: "1", Kind: ProviderEffectBindingKind, Facts: facts, RequestDigest: strings.Repeat("e", 64), Acceptance: RecoveryAcceptance{OperationID: operation, RequestDigest: receipt.Digest, Receipt: ReceiptWire(receipt)}}

	record.Digest, err = ProviderEffectBindingDigest(facts)
	if err != nil {
		t.Fatal(err)
	}

	effectCodecBindAcceptance(t, &record)

	return record
}

func effectCodecBindAcceptance(t *testing.T, record *ProviderEffectBindingRecord) {
	t.Helper()

	digest, err := ProviderEffectBindingAcceptanceDigest(record.RequestDigest, record.Digest)
	if err != nil {
		t.Fatal(err)
	}

	record.Acceptance.RequestDigest = digest
	record.Acceptance.Receipt.Digest = digest
}

func TestProviderEffectCommandCanonicalAndRetryIndependent(t *testing.T) {
	record := effectCodecFixture(t)
	before := record.Facts.ProviderCommandDigest
	cloned := record.Facts
	cloned.Original.Key.OriginalCommandID = id.New(PrefixOperation)
	cloned.Original.Permission.CommandID = cloned.Original.Key.OriginalCommandID
	cloned.Original.Receipt.ID = cloned.Original.Key.OriginalCommandID

	after, err := ProviderEffectCommandDigest(cloned.Command)
	if err != nil || before != after {
		t.Fatal("local invocation changed provider command", err)
	}

	factHash, err := ProviderEffectBindingDigest(cloned)
	if err != nil || factHash == record.Digest {
		t.Fatal("invocation association was lost", err)
	}

	for _, raw := range [][]byte{append([]byte(" "), cloned.Command...), bytes.Replace(cloned.Command, []byte(`"method":"remove"`), []byte(`"method":"remove","extra":true`), 1), append(append([]byte(nil), cloned.Command...), []byte(`{}`)...)} {
		if _, err = DecodeProviderEffectCommand(raw); err == nil {
			t.Fatal("noncanonical command accepted")
		}
	}

	command, err := DecodeProviderEffectCommand(cloned.Command)
	if err != nil {
		t.Fatal(err)
	}

	command.Options[0], command.Options[1] = command.Options[1], command.Options[0]
	if _, err = EncodeProviderEffectCommand(command); err == nil {
		t.Fatal("unordered options accepted")
	}
}

func TestProviderEffectEncodedCommandAndAttestationCaps(t *testing.T) {
	record := effectCodecFixture(t)

	command, err := DecodeProviderEffectCommand(record.Facts.Command)
	if err != nil {
		t.Fatal(err)
	}

	command.Options = []ProviderEffectOption{{Name: "option", Value: ""}}

	base, err := EncodeProviderEffectCommand(command)
	if err != nil {
		t.Fatal(err)
	}

	command.Options[0].Value = strings.Repeat("x", MaxProviderEffectCommand-len(base))

	raw, err := EncodeProviderEffectCommand(command)
	if err != nil || len(raw) != MaxProviderEffectCommand {
		t.Fatal("command cap fixture", len(raw), err)
	}

	command.Options[0].Value += "x"
	if _, err = EncodeProviderEffectCommand(command); !errors.Is(err, ErrCapacity) {
		t.Fatal("command cap+1 accepted", err)
	}

	record.Facts.Attestation = bytes.Repeat([]byte{255}, MaxProviderEffectAttestation)
	if err = ValidateProviderEffectBindingFacts(record.Facts); err != nil {
		t.Fatal(err)
	}

	record.Facts.Attestation = append(record.Facts.Attestation, 255)
	if err = ValidateProviderEffectBindingFacts(record.Facts); !errors.Is(err, ErrCapacity) {
		t.Fatal("attestation cap+1 accepted", err)
	}
}

func TestProviderEffectCompleteRowCapIncludesBase64AndWrappers(t *testing.T) {
	record := effectCodecFixture(t)
	long := strings.Repeat("x", 256)
	original := &record.Facts.Original
	original.Key.TenantID = long
	original.Key.Step.TenantID = long
	original.Key.Step.Service = long
	original.Permission.Step = original.Key.Step
	original.Permission.Physical = Physical{Provider: long, Placement: long, Project: long, Generation: long}
	original.Permission.ResourceID = long
	record.Facts.Producer.VerifierID = long
	record.Facts.Producer.Profile = long

	command, err := DecodeProviderEffectCommand(record.Facts.Command)
	if err != nil {
		t.Fatal(err)
	}

	command.ResourceID = long
	command.Profile = long
	command.Options = []ProviderEffectOption{{Name: "fixture", Value: ""}}

	base, err := EncodeProviderEffectCommand(command)
	if err != nil {
		t.Fatal(err)
	}

	command.Options[0].Value = strings.Repeat("x", MaxProviderEffectCommand-len(base))

	record.Facts.Command, err = EncodeProviderEffectCommand(command)
	if err != nil {
		t.Fatal(err)
	}

	record.Facts.ProviderCommandDigest, err = ProviderEffectCommandDigest(record.Facts.Command)
	if err != nil {
		t.Fatal(err)
	}
	// Base64 grows in four-byte steps. A bounded producer token fills the remainder.
	found := false

	for size := 1; size <= MaxProviderEffectAttestation; size++ {
		record.Facts.Attestation = bytes.Repeat([]byte{255}, size)
		record.Facts.Producer.ProducerID = "p"

		record.Digest, err = ProviderEffectBindingDigest(record.Facts)
		if err != nil {
			t.Fatal(err)
		}

		effectCodecBindAcceptance(t, &record)

		candidate, marshalErr := json.Marshal(record)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}

		padding := MaxProviderEffectBinding - len(candidate)
		if padding < 0 || padding > 254 {
			continue
		}

		record.Facts.Producer.ProducerID += strings.Repeat("p", padding)

		record.Digest, err = ProviderEffectBindingDigest(record.Facts)
		if err != nil {
			t.Fatal(err)
		}

		effectCodecBindAcceptance(t, &record)

		found = true

		break
	}

	if !found {
		t.Fatal("cannot construct exact complete-row cap within field limits")
	}

	raw, err := EncodeProviderEffectBinding(record)
	if err != nil || len(raw) != MaxProviderEffectBinding {
		t.Fatal("complete row cap", len(raw), err)
	}

	if !bytes.Contains(raw, []byte(`"command":"`)) || !bytes.Contains(raw, []byte(`"attestation":"`)) || !bytes.Contains(raw, []byte(`"acceptance":`)) {
		t.Fatal("fixture omitted encoded wrappers")
	}

	if _, err = DecodeProviderEffectBinding(raw); err != nil {
		t.Fatal(err)
	}

	record.Facts.Producer.ProducerID += "p"

	record.Digest, err = ProviderEffectBindingDigest(record.Facts)
	if err != nil {
		t.Fatal(err)
	}

	effectCodecBindAcceptance(t, &record)

	raw, err = json.Marshal(record)
	if err != nil || len(raw) != MaxProviderEffectBinding+1 {
		t.Fatal("cap+1 fixture", len(raw), err)
	}

	if _, err = EncodeProviderEffectBinding(record); !errors.Is(err, ErrCapacity) {
		t.Fatal("encoder accepted cap+1", err)
	}

	if _, err = DecodeProviderEffectBinding(raw); !errors.Is(err, ErrCapacity) {
		t.Fatal("decoder accepted cap+1", err)
	}
}

func TestProviderEffectOriginalAssociationsRefuseSubstitution(t *testing.T) {
	for name, change := range map[string]func(*ProviderEffectOriginal){
		"request":   func(o *ProviderEffectOriginal) { o.Receipt.Digest = strings.Repeat("f", 64) },
		"revision":  func(o *ProviderEffectOriginal) { o.Receipt.Revision = "2" },
		"operation": func(o *ProviderEffectOriginal) { o.Key.OriginalCommandID = id.New(PrefixOperation) },
		"attempt":   func(o *ProviderEffectOriginal) { o.AttemptRef.AttemptID = id.New(PrefixOperation) },
	} {
		t.Run(name, func(t *testing.T) {
			record := effectCodecFixture(t)
			change(&record.Facts.Original)

			if err := ValidateProviderEffectBindingFacts(record.Facts); err == nil {
				t.Fatal("substitution accepted")
			}
		})
	}
}

func TestProviderEffectReceiptBindsVerifiedFacts(t *testing.T) {
	record := effectCodecFixture(t)
	originalAcceptance := record.Acceptance
	record.Facts.Attestation = []byte("replacement custody")

	var err error

	record.Digest, err = ProviderEffectBindingDigest(record.Facts)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = EncodeProviderEffectBinding(record); !errors.Is(err, ErrInvalid) {
		t.Fatal("self-digested replacement retained old receipt", err)
	}

	effectCodecBindAcceptance(t, &record)

	if record.Acceptance.RequestDigest == originalAcceptance.RequestDigest {
		t.Fatal("acceptance does not bind facts")
	}

	if _, err = EncodeProviderEffectBinding(record); err != nil {
		t.Fatal(err)
	}
	// The store must now find this different digest in its independent parent receipt.
}
