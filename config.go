package ctrlplane

import "time"

// Config holds global configuration for the CtrlPlane.
type Config struct {
	// DatabaseURL is the database connection string.
	DatabaseURL string `json:"database_url" mapstructure:"database_url" yaml:"database_url"`

	// DefaultProvider is the provider name used when none is specified.
	DefaultProvider string `json:"default_provider" mapstructure:"default_provider" yaml:"default_provider"`

	// HealthInterval is the default health check interval for all instances.
	HealthInterval time.Duration `json:"health_interval" mapstructure:"health_interval" yaml:"health_interval"`

	// TelemetryFlushInterval is how often telemetry data is flushed.
	TelemetryFlushInterval time.Duration `json:"telemetry_flush_interval" mapstructure:"telemetry_flush_interval" yaml:"telemetry_flush_interval"`

	// MaxInstancesPerTenant is the default quota for instances per tenant.
	// A value of 0 means unlimited.
	MaxInstancesPerTenant int `json:"max_instances_per_tenant" mapstructure:"max_instances_per_tenant" yaml:"max_instances_per_tenant"`

	// AuditEnabled controls whether audit logging is active.
	AuditEnabled bool `json:"audit_enabled" mapstructure:"audit_enabled" yaml:"audit_enabled"`
}

// DefaultCtrlPlaneConfig returns a Config with sensible defaults.
func DefaultCtrlPlaneConfig() Config {
	return Config{
		HealthInterval:         30 * time.Second,
		TelemetryFlushInterval: 10 * time.Second,
		AuditEnabled:           true,
	}
}
