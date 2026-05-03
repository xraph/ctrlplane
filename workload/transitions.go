package workload

import (
	"fmt"
	"slices"

	ctrlplane "github.com/xraph/ctrlplane"
)

// validTransitions encodes the legal Workload state machine. Any
// transition not listed here is rejected. Mirrors the pattern used
// by datacenter.ValidateTransition.
//
// The state machine is intentionally simple: most flows funnel
// through StateActive (steady-state) and the operational verbs
// (Scale, Deploy, Pause) carve out their own transient states.
var validTransitions = map[State][]State{
	StateProvisioning: {StateActive, StateFailed, StateDestroying},
	StateActive: {
		StateScaling, StateDeploying, StatePaused,
		StateDestroying, StateFailed,
	},
	StateScaling:   {StateActive, StateFailed},
	StateDeploying: {StateActive, StateFailed},
	StatePaused:    {StateScaling, StateDestroying, StateActive},
	// StateFailed is recoverable: a customer who hits Start/Resume
	// on a workspace whose first provision errored mid-spawn (e.g.
	// the chosen DC was unreachable) needs Scale to reconcile by
	// spawning the missing replicas. Without StateScaling here, the
	// workload sticks in Failed forever and the only escape is
	// destroy + recreate. StateActive accommodates direct heal
	// paths that mark a workload healthy without going through
	// Scale (e.g. a manual reconcile). StateProvisioning stays for
	// re-provision; StateDestroying stays for cleanup.
	StateFailed:     {StateDestroying, StateProvisioning, StateScaling, StateActive},
	StateDestroying: {StateDestroyed},
	StateDestroyed:  {},
}

// ValidateTransition reports nil when (from → to) is allowed,
// or wraps ctrlplane.ErrInvalidState with detail.
func ValidateTransition(from, to State) error {
	if from == to {
		return nil
	}

	allowed, ok := validTransitions[from]
	if !ok {
		return fmt.Errorf("%w: unknown state %q", ctrlplane.ErrInvalidState, from)
	}

	if slices.Contains(allowed, to) {
		return nil
	}

	return fmt.Errorf("%w: %s → %s", ctrlplane.ErrInvalidState, from, to)
}
