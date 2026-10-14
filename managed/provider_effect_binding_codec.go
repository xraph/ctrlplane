package managed

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
)

// EncodeProviderEffectCommand enforces the canonical command shape and actual byte cap.
func EncodeProviderEffectCommand(command ProviderEffectCommand) ([]byte, error) {
	if command.SchemaVersion != "1" || command.Options == nil || len(command.Options) > 32 {
		return nil, ErrInvalid
	}

	for _, value := range []string{command.Profile, command.BackendAuthority, command.ResourceID, command.Incarnation, command.Method} {
		if !wireToken(value) {
			return nil, ErrInvalid
		}
	}

	if command.ResourceKind != "container" && command.ResourceKind != "network" {
		return nil, ErrInvalid
	}

	previous := ""
	for _, option := range command.Options {
		if !wireToken(option.Name) || option.Name <= previous || len(option.Value) > MaxProviderEffectCommand {
			return nil, ErrInvalid
		}

		previous = option.Name
	}

	raw, err := json.Marshal(command)
	if err != nil {
		return nil, err
	}

	if len(raw) > MaxProviderEffectCommand {
		return nil, ErrCapacity
	}

	return raw, nil
}

// DecodeProviderEffectCommand refuses alternate encodings, unknown fields and trailing data.
func DecodeProviderEffectCommand(raw []byte) (ProviderEffectCommand, error) {
	var command ProviderEffectCommand
	if len(raw) == 0 || len(raw) > MaxProviderEffectCommand {
		return command, ErrCapacity
	}

	if err := json.Unmarshal(raw, &command); err != nil {
		return command, ErrInvalid
	}

	canonical, err := EncodeProviderEffectCommand(command)
	if err != nil {
		return command, err
	}

	if !bytes.Equal(raw, canonical) {
		return ProviderEffectCommand{}, ErrInvalid
	}

	return command, nil
}

// ProviderEffectCommandDigest excludes every local invocation and receipt association.
func ProviderEffectCommandDigest(raw []byte) (string, error) {
	if _, err := DecodeProviderEffectCommand(raw); err != nil {
		return "", err
	}

	return Digest(append([]byte(ProviderEffectBindingProfile+".command\n"), raw...)), nil
}

// ValidateProviderEffectOriginal checks shape and local identity associations.
// Ordinary permissions and command receipts use different digest domains.
// The store must also reload both records from the same persistence authority.
func ValidateProviderEffectOriginal(original ProviderEffectOriginal) error {
	key := original.Key

	generation, err := key.Generation.Uint64()
	if err != nil || generation == 0 || generation > math.MaxInt64 || key.AttemptID.Prefix() != PrefixOperation || original.AttemptRef.AttemptID != key.AttemptID || original.AttemptRef.Generation != key.Generation || !hexDigest(original.AttemptRef.DescriptorDigest) || key.Step.Key != key.Key || key.Step != original.Permission.Step || key.OriginalCommandID != original.Permission.CommandID {
		return ErrInvalid
	}

	if key.Step.Kind != StepRemoveContainer && key.Step.Kind != StepRemoveNetwork {
		return ErrInvalid
	}

	permission, err := original.Permission.Value()
	if err != nil {
		return err
	}

	receipt, err := original.Receipt.Value()
	if err != nil || !hexDigest(receipt.Digest) || receipt.ID != permission.CommandID || receipt.Revision != permission.Revision || !receipt.AcceptedAt.Equal(permission.IssuedAt) || !receipt.LeaseUntil.After(receipt.AcceptedAt) {
		return ErrInvalid
	}

	return nil
}

// ValidateProviderEffectBindingFacts validates shape and local equality, not authenticity.
func ValidateProviderEffectBindingFacts(facts ProviderEffectBindingFacts) error {
	if err := ValidateProviderEffectOriginal(facts.Original); err != nil {
		return err
	}

	version, err := facts.Producer.Version.Uint64()
	if err != nil || version == 0 || version > math.MaxInt64 || !wireToken(facts.Producer.ProducerID) || !wireToken(facts.Producer.Profile) || !wireToken(facts.Producer.VerifierID) {
		return ErrInvalid
	}

	if len(facts.Attestation) == 0 || len(facts.Attestation) > MaxProviderEffectAttestation {
		return ErrCapacity
	}

	command, err := DecodeProviderEffectCommand(facts.Command)
	if err != nil {
		return err
	}

	hash, err := ProviderEffectCommandDigest(facts.Command)
	if err != nil {
		return err
	}

	expectedKind := "container"
	if facts.Original.Key.Step.Kind == StepRemoveNetwork {
		expectedKind = "network"
	}

	if command.Profile != facts.Producer.Profile || command.ResourceID != facts.Original.Permission.ResourceID || command.ResourceKind != expectedKind || hash != facts.ProviderCommandDigest {
		return ErrAuthority
	}

	return nil
}

// ProviderEffectBindingDigest binds evidence to the original independently of acceptance.
func ProviderEffectBindingDigest(facts ProviderEffectBindingFacts) (string, error) {
	if err := ValidateProviderEffectBindingFacts(facts); err != nil {
		return "", err
	}

	return recoveryHash(ProviderEffectBindingProfile, facts)
}

// ProviderEffectBindingAcceptanceDigest binds the request and verified facts to the
// independent parent receipt. Rewriting a row cannot authenticate replacement evidence.
func ProviderEffectBindingAcceptanceDigest(requestDigest, factDigest string) (string, error) {
	if !hexDigest(requestDigest) || !hexDigest(factDigest) {
		return "", ErrInvalid
	}

	return recoveryHash(ProviderEffectBindingProfile+".accept", struct {
		RequestDigest string `json:"request_digest"`
		FactDigest    string `json:"fact_digest"`
	}{requestDigest, factDigest})
}

// EncodeProviderEffectBinding checks the entire stored JSON including base64 and receipts.
func EncodeProviderEffectBinding(record ProviderEffectBindingRecord) ([]byte, error) {
	if record.SchemaVersion != "1" || record.Kind != ProviderEffectBindingKind {
		return nil, ErrUnsupported
	}

	hash, err := ProviderEffectBindingDigest(record.Facts)
	if err != nil {
		return nil, err
	}

	bound, err := ProviderEffectBindingAcceptanceDigest(record.RequestDigest, hash)
	if err != nil {
		return nil, err
	}

	acceptance, err := record.Acceptance.Receipt.Value()
	if err != nil || record.Acceptance.RequestDigest != bound || hash != record.Digest || !hexDigest(record.Acceptance.RequestDigest) || record.Acceptance.OperationID.Prefix() != PrefixOperation || acceptance.ID != record.Acceptance.OperationID || acceptance.Digest != record.Acceptance.RequestDigest || acceptance.AcceptedAt.IsZero() || !acceptance.LeaseUntil.After(acceptance.AcceptedAt) || acceptance.AcceptedAt.Before(record.Facts.Original.Permission.IssuedAt) || acceptance.ID == record.Facts.Original.Permission.CommandID {
		return nil, ErrInvalid
	}

	raw, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}

	if len(raw) > MaxProviderEffectBinding {
		return nil, ErrCapacity
	}

	return raw, nil
}

// DecodeProviderEffectBinding returns only canonical bounded records.
func DecodeProviderEffectBinding(raw []byte) (ProviderEffectBindingRecord, error) {
	var record ProviderEffectBindingRecord
	if len(raw) == 0 || len(raw) > MaxProviderEffectBinding {
		return record, ErrCapacity
	}

	if err := json.Unmarshal(raw, &record); err != nil {
		return record, ErrInvalid
	}

	canonical, err := EncodeProviderEffectBinding(record)
	if err != nil {
		return ProviderEffectBindingRecord{}, err
	}

	if !bytes.Equal(raw, canonical) {
		return ProviderEffectBindingRecord{}, ErrInvalid
	}

	return record, nil
}

// ProviderEffectOriginalEqual preserves the complete accepted permission and receipt.
func ProviderEffectOriginalEqual(left, right ProviderEffectOriginal) bool {
	return reflect.DeepEqual(left, right)
}
