package components

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/xraph/ctrlplane/provider"
)

// formatCPUCompact formats CPU millis as a compact string for resource summaries.
// Returns "" when millis is 0, otherwise formats like "250m" or "1CPU".
func formatCPUCompact(millis int) string {
	if millis == 0 {
		return ""
	}

	if millis%1000 == 0 {
		return strconv.Itoa(millis/1000) + "CPU"
	}

	return strconv.Itoa(millis) + "m"
}

// formatMemoryCompact formats memory in MB as a compact string for resource summaries.
// Returns "" when mb is 0, formats as GB when >= 1024 MB.
func formatMemoryCompact(mb int) string {
	if mb == 0 {
		return ""
	}

	if mb >= 1024 && mb%1024 == 0 {
		return strconv.Itoa(mb/1024) + "GB"
	}

	if mb >= 1024 {
		return fmt.Sprintf("%.1fGB", float64(mb)/1024.0)
	}

	return strconv.Itoa(mb) + "MB"
}

// compactResourceSummary builds a compact resource summary string
// like "250m / 512MB / 2r" from a ResourceSpec.
func compactResourceSummary(r provider.ResourceSpec) string {
	var parts []string

	if cpu := formatCPUCompact(r.CPUMillis); cpu != "" {
		parts = append(parts, cpu)
	}

	if mem := formatMemoryCompact(r.MemoryMB); mem != "" {
		parts = append(parts, mem)
	}

	if r.Replicas > 0 {
		parts = append(parts, strconv.Itoa(r.Replicas)+"r")
	}

	if len(parts) == 0 {
		return "default"
	}

	return strings.Join(parts, " / ")
}

// truncateDescription truncates a description to maxLen characters,
// appending "..." if truncated.
func truncateDescription(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	return s[:maxLen] + "..."
}

// mainServiceImage returns the Image of the Main service in a slice,
// or empty when none is configured. Used by tables that show a
// single representative image per row.
func mainServiceImage(services []provider.ServiceSpec) string {
	for i := range services {
		if services[i].Role == provider.RoleMain || services[i].Role == "" {
			return services[i].Image
		}
	}

	return ""
}

// mainServiceResources returns the Main service's ResourceSpec, or
// the zero value when none is found. Used by the template card's
// resource summary.
func mainServiceResources(services []provider.ServiceSpec) provider.ResourceSpec {
	for i := range services {
		if services[i].Role == provider.RoleMain || services[i].Role == "" {
			return services[i].Resources
		}
	}

	return provider.ResourceSpec{}
}

// totalPorts sums ports across every service in a slice.
func totalPorts(services []provider.ServiceSpec) int {
	n := 0
	for i := range services {
		n += len(services[i].Ports)
	}

	return n
}

// totalVolumes sums volumes across every service.
func totalVolumes(services []provider.ServiceSpec) int {
	n := 0
	for i := range services {
		n += len(services[i].Volumes)
	}

	return n
}

// totalSecrets sums secrets across every service.
func totalSecrets(services []provider.ServiceSpec) int {
	n := 0
	for i := range services {
		n += len(services[i].Secrets)
	}

	return n
}

// totalConfigFiles sums config files across every service.
func totalConfigFiles(services []provider.ServiceSpec) int {
	n := 0
	for i := range services {
		n += len(services[i].ConfigFiles)
	}

	return n
}

// mainDeployImage returns the first ServiceDeploySpec image (used by
// deployment row displays).
func mainDeployImage(services []provider.ServiceDeploySpec) string {
	if len(services) == 0 {
		return ""
	}

	return services[0].Image
}

// mainSnapshotImage returns the first ServiceSnapshot image (used by
// release row displays).
func mainSnapshotImage(services []provider.ServiceSnapshot) string {
	if len(services) == 0 {
		return ""
	}

	return services[0].Image
}
