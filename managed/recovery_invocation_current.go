package managed

import "context"

// ProviderInvocationCurrentStore reads the authenticated current selection for an
// exact invocation. A superseded invocation refuses. This read supplies a CAS
// digest, never a delegation grant; mutations recheck it under their own lock.
type ProviderInvocationCurrentStore interface {
	ReadManagedProviderInvocationCurrent(ctx context.Context, key ProviderInvocationKey) (ProviderStepRetryCurrent, error)
}
