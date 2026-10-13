package managed

import "github.com/xraph/ctrlplane/id"

// RecoveryOriginalPreEffect names the original Begin proof retained before ordinal
// zero permission. FactDigest uses the original removal_fence fact domain; the
// enclosing PreEffectProofDigest uses AttemptNativeAcceptance.ProofDigest.
// RetryPreEffect instead names the exact permission-time ProviderPermissionProof.
type RecoveryOriginalPreEffect struct {
	Key         AttemptBindingKey `json:"key"`
	OperationID id.ID             `json:"operation_id"`
	FactDigest  string            `json:"fact_digest"`
}
