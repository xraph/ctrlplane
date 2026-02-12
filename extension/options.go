package extension

import (
	"github.com/xraph/ctrlplane/app"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/provider"
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

// WithStore sets the store via an app option.
func WithStore(opt app.Option) ExtOption {
	return func(e *Extension) {
		e.opts = append(e.opts, opt)
	}
}
