package managed

import (
	"context"
	"reflect"
)

// RemovalRecoveryLifecycleStore supplies the terminal half of the optional recovery
// profile. It preserves original command and terminal fact codecs while resolving
// their authority through exact effective invocation coverage and retained guard closure.
// Implementing only revalidation, invocation and retry stores is incomplete.
type RemovalRecoveryLifecycleStore interface {
	ProviderGuardedOriginalStore
	ReadManagedEffectiveRemovalCoverageExact(ctx context.Context, request RecoveryExactRequest) (RecoveryRecord[EffectiveRemovalCoverage], error)
	SettleManagedRemovalRecovery(ctx context.Context, request AttemptSettlementRequest) (Receipt, error)
	RecordManagedRemovalRecoveryCommand(ctx context.Context, request AttemptCommandRequest) (AttemptCommandGrant, error)
	RecordManagedRemovalRecoveryBindingSettlement(ctx context.Context, request AttemptBindingSettlementRequest) (Receipt, error)
	RecordManagedRemovalRecoveryFinish(ctx context.Context, request AttemptFinishRequest) (Receipt, error)
	RecordManagedRemovalRecoveryAbort(ctx context.Context, request AttemptAbortRequest) (Receipt, error)
	TombstoneManagedRemovalRecovery(ctx context.Context, request AttemptRequest) (Receipt, error)
}

// RequireRemovalRecoveryLifecycle returns the separately complete terminal capability.
// Recovery cutoff may retain a Fenced revocation while effects remain unresolved.
// Fenced revokes authority only. It supplies neither NotIssued nor settlement or
// coverage proof; terminal operations require independently complete effective coverage.
func RequireRemovalRecoveryLifecycle(authority any) (RemovalRecoveryLifecycleStore, error) {
	lifecycle, ok := authority.(RemovalRecoveryLifecycleStore)
	if !ok {
		return nil, ErrUnsupported
	}

	if _, _, _, err := RequireRemovalRecovery(authority); err != nil {
		return nil, err
	}

	return lifecycle, nil
}

func recoveryCapabilityAbsent(authority any) bool {
	if authority == nil {
		return true
	}

	value := reflect.ValueOf(authority)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
