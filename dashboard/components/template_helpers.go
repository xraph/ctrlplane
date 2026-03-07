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
