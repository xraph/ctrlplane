package app

import (
	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/plugin"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/store"
)

// Option configures a CtrlPlane instance.
type Option func(*CtrlPlane) error

// WithConfig sets the configuration.
func WithConfig(cfg ctrlplane.Config) Option {
	return func(cp *CtrlPlane) error {
		cp.config = cfg

		return nil
	}
}

// WithStore sets the persistence store.
func WithStore(s store.Store) Option {
	return func(cp *CtrlPlane) error {
		cp.store = s

		return nil
	}
}

// WithAuth sets the authentication/authorization provider.
func WithAuth(p auth.Provider) Option {
	return func(cp *CtrlPlane) error {
		cp.auth = p

		return nil
	}
}

// WithProvider registers a named infrastructure provider.
func WithProvider(name string, p provider.Provider) Option {
	return func(cp *CtrlPlane) error {
		cp.providers.Register(name, p)

		return nil
	}
}

// WithEventBus replaces the default in-memory event bus.
func WithEventBus(b event.Bus) Option {
	return func(cp *CtrlPlane) error {
		cp.events = b

		return nil
	}
}

// WithDefaultProvider sets the default provider name.
func WithDefaultProvider(name string) Option {
	return func(cp *CtrlPlane) error {
		cp.providers.SetDefault(name)

		return nil
	}
}

// WithExtension registers a plugin extension with the control plane.
func WithExtension(ext plugin.Extension) Option {
	return func(cp *CtrlPlane) error {
		cp.pendingExts = append(cp.pendingExts, ext)

		return nil
	}
}
