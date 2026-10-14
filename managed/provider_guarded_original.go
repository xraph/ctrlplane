package managed

import "context"

// GuardedOriginalRequest accepts a nonissuing original permission for exact
// configured provider evidence retention. It never grants transport delegation.
type GuardedOriginalRequest struct {
	Command RecoveryCommand `json:"command"`
	Step    ProviderStepKey `json:"step"`
}

// GuardedOriginalMaterializationRequest fixes the authenticated provider identity
// for the same pending permission and its original native guard proof set.
type GuardedOriginalMaterializationRequest struct {
	Command       RecoveryCommand `json:"command"`
	Step          ProviderStepKey `json:"step"`
	PendingDigest string          `json:"pending_digest"`
}

// GuardedOriginalPermission supplies evidence identity only. Callers must obtain
// the exact native binds and fresh one-shot marker before delegating transport.
type GuardedOriginalPermission struct {
	Original      ProviderEffectOriginal `json:"original"`
	PendingDigest string                 `json:"pending_digest"`
}

// ProviderGuardedOriginalStore completes first issuance after profile activation.
// Neither pending acceptance nor root materialization grants provider transport.
type ProviderGuardedOriginalStore interface {
	PrepareManagedGuardedOriginal(ctx context.Context, request GuardedOriginalRequest) (GuardedOriginalPermission, error)
	MaterializeManagedGuardedOriginal(ctx context.Context, request GuardedOriginalMaterializationRequest) (RecoveryAssociation, error)
}
