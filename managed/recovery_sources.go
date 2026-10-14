package managed

import "context"

// ProviderInvocationSources retains the original root and the complete permission
// proof set in descriptor order. Legacy ordinal-zero originals have no proof set.
// These are historical observations, never a reconstructed delegation grant.
type ProviderInvocationSources struct {
	Root             RecoveryRecord[ProviderStepRetryRoot]     `json:"root"`
	PermissionProofs []RecoveryRecord[ProviderPermissionProof] `json:"permission_proofs"`
}

// ProviderInvocationSourcesStore reads exact sources under one checked snapshot.
// Missing members or receipts refuse the whole response; no newer proof substitutes.
type ProviderInvocationSourcesStore interface {
	ReadManagedProviderInvocationSources(ctx context.Context, key ProviderInvocationKey) (ProviderInvocationSources, error)
}

// RemovalCutoffReader inspects the exact permanent cutoff, including its original
// receipt. Reading cutoff alone proves neither effect completion nor native closure.
type RemovalCutoffReader interface {
	ReadManagedRemovalCutoffExact(ctx context.Context, request RecoveryExactRequest) (RecoveryRecord[RemovalCutoffFacts], error)
}
