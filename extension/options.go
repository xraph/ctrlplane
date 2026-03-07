package extension

import (
	"github.com/xraph/ctrlplane/app"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/plugin"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/secrets"
	"github.com/xraph/ctrlplane/store"
)

// ExtOption configures the CtrlPlane Forge extension.
type ExtOption func(*Extension)

// WithAuthProvider sets an explicit auth provider instead of auto-discovery.
func WithAuthProvider(p auth.Provider) ExtOption {
	return func(e *Extension) {
		e.config.AuthProvider = p
	}
}

// WithProvider registers a cloud/orchestrator provider.
func WithProvider(name string, p provider.Provider) ExtOption {
	return func(e *Extension) {
		e.opts = append(e.opts, app.WithProvider(name, p))
	}
}

// WithBasePath sets the URL prefix for all ctrlplane routes.
func WithBasePath(path string) ExtOption {
	return func(e *Extension) {
		e.config.BasePath = path
	}
}

// WithConfig sets the extension configuration directly.
func WithConfig(cfg Config) ExtOption {
	return func(e *Extension) {
		e.config = cfg
	}
}

// WithStore sets an explicit store for the extension.
// When provided, grove auto-discovery is skipped.
func WithStore(s store.Store) ExtOption {
	return func(e *Extension) {
		e.opts = append(e.opts, app.WithStore(s))
		e.storeProvided = true
	}
}

// WithGroveDatabase configures the extension to resolve a named grove.DB
// from the Forge DI container for its store backend.
func WithGroveDatabase(name string) ExtOption {
	return func(e *Extension) {
		e.config.GroveDatabase = name
		e.useGrove = true
	}
}

// WithVaultName configures the extension to resolve a named secrets.Vault
// from the Forge DI container for its vault backend.
func WithVaultName(name string) ExtOption {
	return func(e *Extension) {
		e.config.VaultName = name
		e.useVault = true
	}
}

// WithVault sets an explicit vault for the extension.
// When provided, vault auto-discovery is skipped.
func WithVault(v secrets.Vault) ExtOption {
	return func(e *Extension) {
		e.opts = append(e.opts, app.WithVault(v))
		e.vaultProvided = true
	}
}

// WithExtension registers a plugin extension (lifecycle hooks).
func WithExtension(x plugin.Extension) ExtOption {
	return func(e *Extension) {
		e.opts = append(e.opts, app.WithExtension(x))
	}
}

// WithDisableRoutes disables automatic route registration.
func WithDisableRoutes() ExtOption {
	return func(e *Extension) {
		e.config.DisableRoutes = true
	}
}

// WithDisableMigrate disables automatic database migration on Register.
func WithDisableMigrate() ExtOption {
	return func(e *Extension) {
		e.config.DisableMigrate = true
	}
}

// WithRequireConfig requires config to be present in YAML files.
func WithRequireConfig(require bool) ExtOption {
	return func(e *Extension) {
		e.config.RequireConfig = require
	}
}
