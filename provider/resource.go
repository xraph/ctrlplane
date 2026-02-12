package provider

import (
	"io"
	"time"
)

// ResourceSpec declares desired resources for an instance.
type ResourceSpec struct {
	CPUMillis int    `json:"cpu_millis"`
	MemoryMB  int    `json:"memory_mb"`
	DiskMB    int    `json:"disk_mb,omitempty"`
	Replicas  int    `json:"replicas"`
	GPU       string `json:"gpu,omitempty"`
}

// ResourceUsage reports actual resource utilization.
type ResourceUsage struct {
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryUsedMB  int     `json:"memory_used_mb"`
	MemoryLimitMB int     `json:"memory_limit_mb"`
	DiskUsedMB    int     `json:"disk_used_mb,omitempty"`
	NetworkInMB   float64 `json:"network_in_mb"`
	NetworkOutMB  float64 `json:"network_out_mb"`
}

// PortSpec declares a port mapping for an instance.
type PortSpec struct {
	Container int    `json:"container"`
	Host      int    `json:"host,omitempty"`
	Protocol  string `json:"protocol"`
}

// VolumeSpec declares a volume mount for an instance.
type VolumeSpec struct {
	Name      string `json:"name"`
	MountPath string `json:"mount_path"`
	SizeMB    int    `json:"size_mb"`
	Type      string `json:"type"`
}

// Endpoint describes an accessible endpoint for an instance.
type Endpoint struct {
	URL      string `json:"url"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Public   bool   `json:"public"`
}

// HealthCheckSpec configures health checking during deployment.
type HealthCheckSpec struct {
	Path     string        `json:"path,omitempty"`
	Port     int           `json:"port"`
	Interval time.Duration `json:"interval"`
	Timeout  time.Duration `json:"timeout"`
	Retries  int           `json:"retries"`
}

// LogOptions configures log streaming.
type LogOptions struct {
	Follow bool      `json:"follow"`
	Since  time.Time `json:"since,omitzero"`
	Tail   int       `json:"tail,omitempty"`
}

// ExecRequest describes a command to run inside an instance.
type ExecRequest struct {
	Command []string  `json:"command"`
	Stdin   io.Reader `json:"-"`
	TTY     bool      `json:"tty"`
}

// ExecResult holds the result of an exec operation.
type ExecResult struct {
	ExitCode int    `json:"exit_code"`
	Stdout   []byte `json:"stdout"`
	Stderr   []byte `json:"stderr"`
}
