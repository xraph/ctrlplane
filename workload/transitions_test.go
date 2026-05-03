package workload

import (
	"errors"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
)

// TestValidateTransition_FailedIsRecoverable pins the recovery
// edges out of StateFailed. A workload whose first provision
// errored mid-spawn (e.g. unreachable DC) gets stuck in Failed;
// without these edges, customer-initiated Start/Resume returns
// "invalid state transition" forever and the only escape is
// destroy + recreate.
func TestValidateTransition_FailedIsRecoverable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		to   State
	}{
		{"to scaling", StateScaling},
		{"to provisioning", StateProvisioning},
		{"to destroying", StateDestroying},
		{"to active", StateActive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := ValidateTransition(StateFailed, tc.to); err != nil {
				t.Fatalf("Failed → %s: want nil, got %v", tc.to, err)
			}
		})
	}
}

// TestValidateTransition_FailedRejectsTerminalCycles asserts the
// terminal sinks remain unreachable from Failed in a way that
// would corrupt the model (e.g. Failed → Destroyed without going
// through Destroying).
func TestValidateTransition_FailedRejectsTerminalCycles(t *testing.T) {
	t.Parallel()

	err := ValidateTransition(StateFailed, StateDestroyed)
	if err == nil {
		t.Fatal("Failed → Destroyed: want error, got nil")
	}

	if !errors.Is(err, ctrlplane.ErrInvalidState) {
		t.Fatalf("error: want wraps ErrInvalidState, got %v", err)
	}
}

// TestValidateTransition_NoOpAlwaysAllowed exercises the from==to
// short-circuit so a no-op state write doesn't get rejected.
func TestValidateTransition_NoOpAlwaysAllowed(t *testing.T) {
	t.Parallel()

	for _, s := range []State{
		StateProvisioning, StateActive, StateScaling, StateDeploying,
		StatePaused, StateFailed, StateDestroying, StateDestroyed,
	} {
		if err := ValidateTransition(s, s); err != nil {
			t.Errorf("%s → %s (no-op): want nil, got %v", s, s, err)
		}
	}
}
