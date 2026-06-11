package kubernetes

import (
	"testing"

	"helm.sh/helm/v3/pkg/release"

	"github.com/xraph/ctrlplane/provider"
)

func TestHelmStateFor(t *testing.T) {
	tests := []struct {
		status release.Status
		want   provider.InstanceState
	}{
		{release.StatusDeployed, provider.StateRunning},
		{release.StatusFailed, provider.StateFailed},
		{release.StatusPendingInstall, provider.StateStarting},
		{release.StatusPendingUpgrade, provider.StateStarting},
		{release.StatusPendingRollback, provider.StateStarting},
		{release.StatusUninstalling, provider.StateStopping},
		{release.StatusUninstalled, provider.StateStopped},
		{release.StatusUnknown, provider.StateProvisioning},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := helmStateFor(tt.status); got != tt.want {
				t.Errorf("helmStateFor(%s) = %s, want %s", tt.status, got, tt.want)
			}
		})
	}
}
