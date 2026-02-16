package extension

import (
	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
)

// Config holds configuration for the CtrlPlane Forge extension.
type Config struct {
	// Config embeds the core ctrlplane configuration.
	ctrlplane.Config

	// BasePath is the URL prefix for all ctrlplane routes.
	BasePath string `default:"/api/cp" json:"base_path"`

	// AuthProvider is an explicitly configured auth provider.
	// If nil, the extension auto-discovers from Forge's DI container.
	AuthProvider auth.Provider `json:"-"`

	// DisableRoutes disables the registration of routes.
	DisableRoutes bool `default:"false" json:"disable_routes"`
}

// ToCtrlPlaneConfig returns the embedded ctrlplane config.
func (c Config) ToCtrlPlaneConfig() ctrlplane.Config {
	return c.Config
}
