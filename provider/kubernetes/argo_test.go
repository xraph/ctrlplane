package kubernetes

import (
	"testing"

	"github.com/xraph/ctrlplane/provider"
)

func TestArgoStateFor(t *testing.T) {
	tests := []struct {
		sync   string
		health string
		want   provider.InstanceState
	}{
		{"Synced", "Healthy", provider.StateRunning},
		{"OutOfSync", "Healthy", provider.StateStarting},
		{"Synced", "Progressing", provider.StateStarting},
		{"Synced", "Degraded", provider.StateFailed},
		{"Synced", "Suspended", provider.StateStopped},
		{"OutOfSync", "Missing", provider.StateProvisioning},
		{"", "", provider.StateProvisioning},
	}

	for _, tt := range tests {
		t.Run(tt.sync+"/"+tt.health, func(t *testing.T) {
			if got := argoStateFor(tt.sync, tt.health); got != tt.want {
				t.Errorf("argoStateFor(%q, %q) = %s, want %s", tt.sync, tt.health, got, tt.want)
			}
		})
	}
}
