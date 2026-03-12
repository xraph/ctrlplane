package datacenter

import (
	"fmt"
	"slices"

	ctrlplane "github.com/xraph/ctrlplane"
)

// validTransitions defines allowed status transitions for datacenters.
var validTransitions = map[Status][]Status{
	StatusActive:      {StatusMaintenance, StatusDraining, StatusOffline},
	StatusMaintenance: {StatusActive, StatusDraining, StatusOffline},
	StatusDraining:    {StatusOffline, StatusActive},
	StatusOffline:     {StatusActive, StatusMaintenance},
}

// ValidateTransition checks if a status transition is allowed.
func ValidateTransition(from, to Status) error {
	allowed, ok := validTransitions[from]
	if !ok {
		return fmt.Errorf("datacenter status %q: %w", from, ctrlplane.ErrInvalidState)
	}

	if slices.Contains(allowed, to) {
		return nil
	}

	return fmt.Errorf("datacenter %s -> %s: %w", from, to, ctrlplane.ErrInvalidState)
}
