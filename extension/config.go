package extension

import (
	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
)

// Config holds configuration for the CtrlPlane Forge extension.
// Fields can be set programmatically via ExtOption functions or loaded from
// YAML configuration files (under "extensions.ctrlplane" or "ctrlplane" keys).
type Config struct {
	// Config embeds the core ctrlplane configuration.
	ctrlplane.Config `json:",inline" mapstructure:",squash" yaml:",inline"`

	// BasePath is the URL prefix for all ctrlplane routes.
	BasePath string `json:"base_path" mapstructure:"base_path" yaml:"base_path"`

	// AuthProvider is an explicitly configured auth provider.
	// If nil, the extension auto-discovers from Forge's DI container.
	AuthProvider auth.Provider `json:"-" yaml:"-"`

	// DisableRoutes disables the registration of routes.
	DisableRoutes bool `json:"disable_routes" mapstructure:"disable_routes" yaml:"disable_routes"`

	// DisableMigrate disables automatic database migration on Register.
	DisableMigrate bool `json:"disable_migrate" mapstructure:"disable_migrate" yaml:"disable_migrate"`

	// RequireConfig requires config to be present in YAML files.
	// If true and no config is found, Register returns an error.
	RequireConfig bool `json:"-" yaml:"-"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		Config:   ctrlplane.DefaultCtrlPlaneConfig(),
		BasePath: "/api/cp",
	}
}

// ToCtrlPlaneConfig returns the embedded ctrlplane config.
func (c Config) ToCtrlPlaneConfig() ctrlplane.Config {
	return c.Config
}
