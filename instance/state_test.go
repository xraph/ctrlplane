package instance_test

import (
	"errors"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/provider"
)

func TestValidateTransition_Valid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		current provider.InstanceState
		target  provider.InstanceState
	}{
		{"provisioning to starting", provider.StateProvisioning, provider.StateStarting},
		{"provisioning to failed", provider.StateProvisioning, provider.StateFailed},
		{"provisioning to destroying", provider.StateProvisioning, provider.StateDestroying},
		{"starting to running", provider.StateStarting, provider.StateRunning},
		{"starting to failed", provider.StateStarting, provider.StateFailed},
		{"running to stopping", provider.StateRunning, provider.StateStopping},
		{"running to failed", provider.StateRunning, provider.StateFailed},
		{"running to destroying", provider.StateRunning, provider.StateDestroying},
		{"stopping to stopped", provider.StateStopping, provider.StateStopped},
		{"stopping to failed", provider.StateStopping, provider.StateFailed},
		{"stopped to starting", provider.StateStopped, provider.StateStarting},
		{"stopped to destroying", provider.StateStopped, provider.StateDestroying},
		{"failed to starting", provider.StateFailed, provider.StateStarting},
		{"failed to destroying", provider.StateFailed, provider.StateDestroying},
		{"destroying to destroyed", provider.StateDestroying, provider.StateDestroyed},
		{"destroying to failed", provider.StateDestroying, provider.StateFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := instance.ValidateTransition(tt.current, tt.target); err != nil {
				t.Errorf("expected valid transition from %s to %s, got: %v", tt.current, tt.target, err)
			}
		})
	}
}

func TestValidateTransition_Invalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		current provider.InstanceState
		target  provider.InstanceState
	}{
		{"provisioning to running", provider.StateProvisioning, provider.StateRunning},
		{"starting to stopped", provider.StateStarting, provider.StateStopped},
		{"running to starting", provider.StateRunning, provider.StateStarting},
		{"stopped to running", provider.StateStopped, provider.StateRunning},
		{"destroyed to starting", provider.StateDestroyed, provider.StateStarting},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := instance.ValidateTransition(tt.current, tt.target)
			if err == nil {
				t.Errorf("expected error for transition from %s to %s", tt.current, tt.target)
			}

			if !errors.Is(err, ctrlplane.ErrInvalidState) {
				t.Errorf("expected ErrInvalidState, got: %v", err)
			}
		})
	}
}
