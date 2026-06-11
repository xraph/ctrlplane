package kubernetes

import (
	"github.com/xraph/ctrlplane/provider"
)

// argoStateFor maps an Argo CD Application's sync and health status to a
// ctrlplane InstanceState. Health is primary; sync refines the healthy case
// (healthy-but-out-of-sync is still converging).
func argoStateFor(sync, health string) provider.InstanceState {
	switch health {
	case "Healthy":
		if sync == "Synced" {
			return provider.StateRunning
		}

		return provider.StateStarting
	case "Progressing":
		return provider.StateStarting
	case "Degraded":
		return provider.StateFailed
	case "Suspended":
		return provider.StateStopped
	default:
		return provider.StateProvisioning
	}
}
