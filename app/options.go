package app

import (
	"errors"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/bootstrap"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/plugin"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/secrets"
	"github.com/xraph/ctrlplane/store"
	"github.com/xraph/ctrlplane/worker"
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

// WithCredentialsResolver makes Ctrl Plane fetch a provider's credentials
// before each call to it, keyed by the provider's registry key. Providers
// read them with provider.CredentialsFromContext. Use it when credentials
// rotate or expire and handing them over once at construction is not
// enough.
func WithCredentialsResolver(r provider.CredentialsResolver) Option {
	return func(cp *CtrlPlane) error {
		cp.providers.SetCredentialsResolver(r)

		return nil
	}
}

// WithLeaderElector runs background workers only on the replica e elects
// as leader. Use it when you run several replicas against one store, or
// each replica runs every worker. With the postgres store, pass
// store.LeaderElector(name).
func WithLeaderElector(e worker.LeaderElector) Option {
	return func(cp *CtrlPlane) error {
		if e == nil {
			return errors.New("ctrlplane: WithLeaderElector: elector is nil")
		}

		cp.leaderElector = e

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

// WithVault sets the secret storage backend.
func WithVault(v secrets.Vault) Option {
	return func(cp *CtrlPlane) error {
		cp.vault = v

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

// WithBootstrapHook registers a bootstrap.Hook with the control plane.
//
// Hooks are the programmatic-contribution path for shared platform
// services that auto-deploy on every datacenter (declarative
// contributions live on Datacenter.BootstrapServices). The reconciler
// worker calls every registered hook on every datacenter on every
// tick — hooks self-filter by inspecting the supplied DatacenterInfo.
//
// Re-registering a hook with the same Name replaces the previous
// entry, supporting hot-reload of an extension's hook definition.
func WithBootstrapHook(h bootstrap.Hook) Option {
	return func(cp *CtrlPlane) error {
		if cp.bootstrapHooks == nil {
			cp.bootstrapHooks = bootstrap.NewRegistry()
		}

		cp.bootstrapHooks.Register(h)

		return nil
	}
}
