package ctrlplane

import "time"

// Config holds global configuration for the CtrlPlane.
type Config struct {
	// DatabaseURL is the database connection string.
	DatabaseURL string `env:"CP_DATABASE_URL" json:"database_url"`

	// DefaultProvider is the provider name used when none is specified.
	DefaultProvider string `env:"CP_DEFAULT_PROVIDER" json:"default_provider"`

	// HealthInterval is the default health check interval for all instances.
	HealthInterval time.Duration `default:"30s" env:"CP_HEALTH_INTERVAL" json:"health_interval"`

	// TelemetryFlushInterval is how often telemetry data is flushed.
	TelemetryFlushInterval time.Duration `default:"10s" env:"CP_TELEMETRY_FLUSH" json:"telemetry_flush_interval"`

	// MaxInstancesPerTenant is the default quota for instances per tenant.
	// A value of 0 means unlimited.
	MaxInstancesPerTenant int `default:"0" env:"CP_MAX_INSTANCES" json:"max_instances_per_tenant"`

	// AuditEnabled controls whether audit logging is active.
	AuditEnabled bool `default:"true" env:"CP_AUDIT_ENABLED" json:"audit_enabled"`
}
