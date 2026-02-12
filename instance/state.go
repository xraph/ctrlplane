package instance

import (
	"fmt"
	"slices"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/provider"
)

// validTransitions maps each instance state to the set of states it can
// transition to. Transitions not present in this map are forbidden.
var validTransitions = map[provider.InstanceState][]provider.InstanceState{
	provider.StateProvisioning: {provider.StateStarting, provider.StateFailed, provider.StateDestroying},
	provider.StateStarting:     {provider.StateRunning, provider.StateFailed},
	provider.StateRunning:      {provider.StateStopping, provider.StateFailed, provider.StateDestroying},
	provider.StateStopping:     {provider.StateStopped, provider.StateFailed},
	provider.StateStopped:      {provider.StateStarting, provider.StateDestroying},
	provider.StateFailed:       {provider.StateStarting, provider.StateDestroying},
	provider.StateDestroying:   {provider.StateDestroyed, provider.StateFailed},
}

// ValidateTransition checks whether moving from the current state to the
// target state is allowed. It returns ctrlplane.ErrInvalidState if not.
func ValidateTransition(current, target provider.InstanceState) error {
	allowed, ok := validTransitions[current]
	if !ok {
		return fmt.Errorf("%w: no transitions from %s", ctrlplane.ErrInvalidState, current)
	}

	if !slices.Contains(allowed, target) {
		return fmt.Errorf("%w: cannot transition from %s to %s", ctrlplane.ErrInvalidState, current, target)
	}

	return nil
}
