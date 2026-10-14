package managed

import "context"

// ProviderInvocationCommitsStore reads the complete immutable native commit set
// selected by an accepted invocation marker, in descriptor order. Missing marker,
// member, original request, event or receipt refuses the entire response. These
// historical records confer no fresh delegation or native freshness authority.
type ProviderInvocationCommitsStore interface {
	ReadManagedProviderInvocationCommits(ctx context.Context, key ProviderInvocationKey) ([]RecoveryRecord[ProviderNativeCommitFacts], error)
}
