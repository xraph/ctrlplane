package managed

import (
	"context"

	"github.com/xraph/ctrlplane/id"
)

// ProviderEffectBindingProfile identifies additive evidence beside an original permission.
const ProviderEffectBindingProfile = "ctrlplane.managed.provider-effect-binding.v1"

// ProviderEffectBindingKind is the exact immutable controller row kind.
const ProviderEffectBindingKind = "provider_effect_binding"

// Effect evidence has independent input caps and a cap on the complete encoded row.
const (
	MaxProviderEffectCommand     = 4096
	MaxProviderEffectAttestation = 4096
	MaxProviderEffectBinding     = 16384
)

// ProviderEffectOption is a non-secret transport option in strict name order.
type ProviderEffectOption struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ProviderEffectCommand is the normalized transport command, independent of local retry IDs.
// The configured producer defines Method and Options through its authenticated profile.
// BackendAuthority and Incarnation come from custody evidence, never a resource name,
// Physical.Generation, current inspection, or a core-synthesized provider identity.
type ProviderEffectCommand struct {
	SchemaVersion    Decimal                `json:"schema_version"`
	Profile          string                 `json:"profile"`
	BackendAuthority string                 `json:"backend_authority"`
	ResourceKind     string                 `json:"resource_kind"`
	ResourceID       string                 `json:"resource_id"`
	Incarnation      string                 `json:"incarnation"`
	Method           string                 `json:"method"`
	Options          []ProviderEffectOption `json:"options"`
}

// ProviderEffectBindingKey selects the original invocation within one immutable attempt.
type ProviderEffectBindingKey struct {
	AttemptKey

	Step              ProviderStepKey `json:"step"`
	OriginalCommandID id.ID           `json:"original_command_id"`
}

// ProviderEffectOriginal is the exact locally accepted input supplied to host code.
type ProviderEffectOriginal struct {
	Key        ProviderEffectBindingKey   `json:"key"`
	AttemptRef AttemptRef                 `json:"attempt_ref"`
	Permission WireProviderStepPermission `json:"permission"`
	Receipt    WireReceipt                `json:"receipt"`
}

// ProviderEffectBindingFacts carry authenticated command and resource custody evidence.
// Command contains canonical non-secret JSON; Attestation must also be safe to retain.
type ProviderEffectBindingFacts struct {
	Original              ProviderEffectOriginal   `json:"original"`
	Producer              RecoveryProducerIdentity `json:"producer"`
	Command               []byte                   `json:"command"`
	ProviderCommandDigest string                   `json:"provider_command_digest"`
	Attestation           []byte                   `json:"attestation"`
}

// ProviderEffectBindingRecord adds its own acceptance without rewriting permission bytes.
type ProviderEffectBindingRecord struct {
	SchemaVersion Decimal                    `json:"schema_version"`
	Kind          string                     `json:"kind"`
	Facts         ProviderEffectBindingFacts `json:"facts"`
	Digest        string                     `json:"digest"`
	RequestDigest string                     `json:"request_digest"`
	Acceptance    RecoveryAcceptance         `json:"acceptance"`
}

// ProviderEffectBindingRequest selects accepted inputs; callers cannot supply a resolver.
type ProviderEffectBindingRequest struct {
	Command  Command                  `json:"command"`
	Original ProviderEffectBindingKey `json:"original"`
}

// ProviderEffectBindingProducer recovers independently retained evidence for this exact
// original. Missing historical evidence returns ErrUnsupported or ErrUnavailable. It
// must never issue transport, inspect current state to infer custody, or treat a missing
// binding as an unissued invocation. Production Docker has no qualified producer yet.
type ProviderEffectBindingProducer interface {
	RecoverProviderEffectBinding(ctx context.Context, original ProviderEffectOriginal) (ProviderEffectBindingFacts, error)
}

// ProviderEffectBindingVerifier authenticates the producer profile, canonical transport
// semantics and exact backend/resource creation custody against the accepted original.
// Core shape validation alone cannot authenticate these claims or detect secret options.
type ProviderEffectBindingVerifier interface {
	VerifyProviderEffectBinding(ctx context.Context, original ProviderEffectOriginal, facts ProviderEffectBindingFacts) error
}

// ProviderEffectBindingHost is trusted composition, independent of complete recovery.
// Provider and BackendAuthority are fixed by host configuration, never request fields.
type ProviderEffectBindingHost struct {
	Producer         ProviderEffectBindingProducer
	Verifier         ProviderEffectBindingVerifier
	Identity         RecoveryProducerIdentity
	Provider         string
	BackendAuthority string
}
