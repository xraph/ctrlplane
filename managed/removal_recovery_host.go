package managed

import "context"

// RecoveryProducerIdentity fixes the producer identity authenticated by host configuration.
type RecoveryProducerIdentity struct {
	ProducerID string  `json:"producer_id"`
	Profile    string  `json:"profile"`
	Version    Decimal `json:"version"`
	VerifierID string  `json:"verifier_id"`
}

// RemovalRecoveryVerifier authenticates normalized facts and their exact retained inputs.
// Implementations are trusted host configuration. A request cannot replace this verifier.
// Methods run before authority locks; stores still recheck time and local state at commit.
type RemovalRecoveryVerifier interface {
	VerifyRemovalRevalidation(ctx context.Context, intent RecoveryRecord[RecoveryNativeIntent], facts RemovalRevalidationFacts) error
	VerifyRemovalCommit(ctx context.Context, intent RecoveryRecord[RecoveryNativeIntent], facts ProviderNativeCommitFacts) error
	VerifyRemovalClose(ctx context.Context, intent RecoveryRecord[RecoveryNativeIntent], facts RecoveryNativeCloseFacts) error
	VerifyProviderNoDispatch(ctx context.Context, permission RecoveryRecord[ProviderInvocationPermission], marker *RecoveryRecord[ProviderInvocationMarker], failure ProviderInvocationFailure) error
}

// RemovalRecoveryHost is a complete configured host capability, never a request DTO.
// A configured repository view shares the existing backend and its authority locks.
type RemovalRecoveryHost struct {
	Revalidation         RemovalRevalidationProducer
	Commit               RemovalCommitProducer
	Close                RemovalGuardCloseProducer
	Closure              ProviderInvocationClosureProducer
	Verifier             RemovalRecoveryVerifier
	RevalidationIdentity RecoveryProducerIdentity
	CommitIdentity       RecoveryProducerIdentity
	CloseIdentity        RecoveryProducerIdentity
	ClosureVerifierID    string
}
