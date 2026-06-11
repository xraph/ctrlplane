package kubernetes

import (
	"helm.sh/helm/v3/pkg/release"

	"github.com/xraph/ctrlplane/provider"
)

// helmStateFor maps a Helm release status to a ctrlplane InstanceState.
func helmStateFor(status release.Status) provider.InstanceState {
	switch status {
	case release.StatusDeployed:
		return provider.StateRunning
	case release.StatusFailed:
		return provider.StateFailed
	case release.StatusPendingInstall, release.StatusPendingUpgrade, release.StatusPendingRollback:
		return provider.StateStarting
	case release.StatusUninstalling:
		return provider.StateStopping
	case release.StatusUninstalled:
		return provider.StateStopped
	default:
		return provider.StateProvisioning
	}
}
