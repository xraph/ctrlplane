package managed

import "context"

// RemovalCoverageSealer retains complete post-cutoff coverage before constructing
// a native close request. The seal is an immutable observation, not permission to
// send, close a native guard, or declare the attempt terminal. Exact replay returns
// the original acceptance without consuming another receipt or counter reserve.
type RemovalCoverageSealer interface {
	SealManagedEffectiveRemovalCoverage(ctx context.Context, command RecoveryCommand) (RecoveryRecord[EffectiveRemovalCoverage], error)
}
