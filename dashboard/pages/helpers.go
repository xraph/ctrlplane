package pages

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/template"
)

// formatWorkerInterval formats a duration as a human-readable interval string.
func formatWorkerInterval(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

// formatCPU formats CPU millis as a human-readable string.
// Returns "default" when millis is 0, otherwise formats as fractional CPUs.
func formatCPU(millis int) string {
	if millis == 0 {
		return "default"
	}

	if millis%1000 == 0 {
		return strconv.Itoa(millis/1000) + " CPU"
	}

	return fmt.Sprintf("%.1f CPU", float64(millis)/1000.0)
}

// formatMemory formats memory in megabytes as a human-readable string.
// Returns "default" when mb is 0, formats as GB when >= 1024 MB.
func formatMemory(mb int) string {
	if mb == 0 {
		return "default"
	}

	if mb >= 1024 && mb%1024 == 0 {
		return strconv.Itoa(mb/1024) + " GB"
	}

	if mb >= 1024 {
		return fmt.Sprintf("%.1f GB", float64(mb)/1024.0)
	}

	return strconv.Itoa(mb) + " MB"
}

// formatDuration formats a time.Duration as a compact human-readable string.
// Returns "default" when d is 0.
func formatDuration(d time.Duration) string {
	if d == 0 {
		return "default"
	}

	if d < time.Minute {
		return strconv.Itoa(int(d.Seconds())) + "s"
	}

	return strconv.Itoa(int(d.Minutes())) + "m"
}

// durationSeconds converts a time.Duration to a string of whole seconds
// suitable for form input pre-fill. Returns "" when d is 0.
func durationSeconds(d time.Duration) string {
	if d == 0 {
		return ""
	}

	return strconv.Itoa(int(d.Seconds()))
}

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

// --- JSON Serializer Helpers ---

// envToJSON converts an env map to a JSON string for the hidden form field.
func envToJSON(env map[string]string) string {
	if len(env) == 0 {
		return "{}"
	}

	b, err := json.Marshal(env)
	if err != nil {
		return "{}"
	}

	return string(b)
}

// labelsToJSON converts a labels map to a JSON string for the hidden form field.
func labelsToJSON(labels map[string]string) string {
	return envToJSON(labels)
}

// portsToJSON converts a slice of PortSpec to a JSON string for the hidden form field.
func portsToJSON(ports []provider.PortSpec) string {
	if len(ports) == 0 {
		return "[]"
	}

	b, err := json.Marshal(ports)
	if err != nil {
		return "[]"
	}

	return string(b)
}

// volumesToJSON converts a slice of VolumeSpec to a JSON string for the hidden form field.
func volumesToJSON(volumes []provider.VolumeSpec) string {
	if len(volumes) == 0 {
		return "[]"
	}

	b, err := json.Marshal(volumes)
	if err != nil {
		return "[]"
	}

	return string(b)
}

// secretsToJSON converts a slice of SecretRef to a JSON string for the hidden form field.
func secretsToJSON(secs []template.SecretRef) string {
	if len(secs) == 0 {
		return "[]"
	}

	b, err := json.Marshal(secs)
	if err != nil {
		return "[]"
	}

	return string(b)
}

// configFilesToJSON converts a slice of ConfigFile to a JSON string for the hidden form field.
func configFilesToJSON(files []template.ConfigFile) string {
	if len(files) == 0 {
		return "[]"
	}

	b, err := json.Marshal(files)
	if err != nil {
		return "[]"
	}

	return string(b)
}

// healthCheckToJSON converts a HealthCheckSpec pointer to a JSON string for the hidden form field.
func healthCheckToJSON(hc *provider.HealthCheckSpec) string {
	if hc == nil {
		return "{}"
	}

	b, err := json.Marshal(hc)
	if err != nil {
		return "{}"
	}

	return string(b)
}

// truncStr trims s to maxLen and appends "…" when truncated.
// Mirrors components.truncStr but local to the pages package so
// templ files in this package can call it without importing
// components for one-off uses.
func truncStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	return s[:maxLen-3] + "..."
}

// mainServiceImage returns the Image of a multi-service slice's Main
// service, or empty when none is found. Used by dashboard tables that
// want a single representative image to display per row.
func mainServiceImage(services []provider.ServiceSpec) string {
	for i := range services {
		if services[i].Role == provider.RoleMain || services[i].Role == "" {
			return services[i].Image
		}
	}

	return ""
}

// mainDeployImage returns the first ServiceDeploySpec's Image, used by
// the deployment detail/listing pages to show "what was deployed".
func mainDeployImage(services []provider.ServiceDeploySpec) string {
	if len(services) == 0 {
		return ""
	}

	return services[0].Image
}

// mainSnapshotImage returns the first ServiceSnapshot's Image for
// release displays.
func mainSnapshotImage(services []provider.ServiceSnapshot) string {
	if len(services) == 0 {
		return ""
	}

	return services[0].Image
}

// collectDeployEnvKeys flattens env-var keys across every service in a
// deploy. Two services with the same key produce one badge — the
// dashboard displays keys only, never values.
func collectDeployEnvKeys(services []provider.ServiceDeploySpec) []string {
	seen := make(map[string]struct{})

	for i := range services {
		for k := range services[i].Env {
			seen[k] = struct{}{}
		}
	}

	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}

	return out
}

// templateMainImage returns the Main service's image from a template.
func templateMainImage(t *templateRef) string {
	if t == nil {
		return ""
	}

	if main := t.MainService(); main != nil {
		return main.Image
	}

	return ""
}

// templateMainResources returns the Main service's resources spec.
func templateMainResources(t *templateRef) provider.ResourceSpec {
	if t == nil {
		return provider.ResourceSpec{}
	}

	if main := t.MainService(); main != nil {
		return main.Resources
	}

	return provider.ResourceSpec{}
}

func totalPortsAcrossTemplate(t *templateRef) int {
	if t == nil {
		return 0
	}

	n := 0
	for i := range t.Services {
		n += len(t.Services[i].Ports)
	}

	return n
}

func totalVolumesAcrossTemplate(t *templateRef) int {
	if t == nil {
		return 0
	}

	n := 0
	for i := range t.Services {
		n += len(t.Services[i].Volumes)
	}

	return n
}

func totalSecretsAcrossTemplate(t *templateRef) int {
	if t == nil {
		return 0
	}

	n := 0
	for i := range t.Services {
		n += len(t.Services[i].Secrets)
	}

	return n
}

// totalEnvAcrossTemplate sums the env-var key counts across every
// service in the template. Surfaces an "N envs" badge in the
// templates list alongside ports / vols / secrets so operators can
// see at a glance whether a template carries env without clicking
// into the detail view.
func totalEnvAcrossTemplate(t *templateRef) int {
	if t == nil {
		return 0
	}

	n := 0
	for i := range t.Services {
		n += len(t.Services[i].Env)
	}

	return n
}

// templateRef is a type alias used by helpers above to avoid spelling
// the long template.Template path. Defined locally so helpers compile.
type templateRef = template.Template
