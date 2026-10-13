package managed

import (
	"errors"
	"testing"
)

// Interface witnesses establish only the capability gate. They do not implement
// native verification, transport or a qualified lifecycle authority.
type recoveryWithoutLifecycleWitness struct {
	RemovalRevalidationStore
	ProviderInvocationStore
	ProviderStepRetryStore
}

type recoveryWithLifecycleWitness struct {
	recoveryWithoutLifecycleWitness
	RemovalRecoveryLifecycleStore
}

type recoveryLifecycleOnlyWitness struct{ RemovalRecoveryLifecycleStore }

func TestRecoveryGateRequiresCompleteLifecycleMembership(t *testing.T) {
	partial := &recoveryWithoutLifecycleWitness{}
	if _, _, _, err := RequireRemovalRecovery(partial); !errors.Is(err, ErrUnsupported) {
		t.Fatal("three nonterminal interfaces enabled recovery", err)
	}

	if _, err := RequireRemovalRecoveryLifecycle(partial); !errors.Is(err, ErrUnsupported) {
		t.Fatal("missing lifecycle accepted", err)
	}

	if _, err := RequireRemovalRecoveryLifecycle(&recoveryLifecycleOnlyWitness{}); !errors.Is(err, ErrUnsupported) {
		t.Fatal("lifecycle alone enabled recovery", err)
	}

	complete := &recoveryWithLifecycleWitness{}
	if _, _, _, err := RequireRemovalRecovery(complete); err != nil {
		t.Fatal("complete interface witness refused", err)
	}

	if _, err := RequireRemovalRecoveryLifecycle(complete); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryGateRefusesNilCompleteImplementation(t *testing.T) {
	var absent *recoveryWithLifecycleWitness
	for _, authority := range []any{nil, absent} {
		if _, _, _, err := RequireRemovalRecovery(authority); !errors.Is(err, ErrUnsupported) {
			t.Fatal("nil implementation enabled recovery", err)
		}

		if _, err := RequireRemovalRecoveryLifecycle(authority); !errors.Is(err, ErrUnsupported) {
			t.Fatal("nil implementation enabled lifecycle", err)
		}
	}
}
