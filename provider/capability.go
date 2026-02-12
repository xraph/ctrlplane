package provider

import "slices"

// Capability declares what a provider supports.
// Consumers can query capabilities before calling unsupported methods.
type Capability string

const (
	// CapProvision indicates the provider can create infrastructure.
	CapProvision Capability = "provision"

	// CapDeploy indicates the provider can deploy releases.
	CapDeploy Capability = "deploy"

	// CapScale indicates the provider can adjust resources.
	CapScale Capability = "scale"

	// CapLogs indicates the provider can stream logs.
	CapLogs Capability = "logs"

	// CapExec indicates the provider can execute commands in instances.
	CapExec Capability = "exec"

	// CapVolumes indicates the provider supports persistent volumes.
	CapVolumes Capability = "volumes"

	// CapGPU indicates the provider supports GPU workloads.
	CapGPU Capability = "gpu"

	// CapBlueGreen indicates the provider supports blue-green deployments.
	CapBlueGreen Capability = "strategy:blue-green"

	// CapCanary indicates the provider supports canary deployments.
	CapCanary Capability = "strategy:canary"

	// CapRolling indicates the provider supports rolling deployments.
	CapRolling Capability = "strategy:rolling"

	// CapAutoScale indicates the provider supports autoscaling.
	CapAutoScale Capability = "autoscale"

	// CapCustomDomains indicates the provider supports custom domains.
	CapCustomDomains Capability = "custom-domains"

	// CapTLS indicates the provider supports TLS termination.
	CapTLS Capability = "tls"
)

// HasCapability checks whether the provider supports a given capability.
func HasCapability(p Provider, capability Capability) bool {
	return slices.Contains(p.Capabilities(), capability)
}
