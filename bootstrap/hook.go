package bootstrap

import (
	"context"
	"sync"
)

// Hook is the programmatic contribution path for bootstrap services.
// Extensions implement Hook to inject shared services into datacenters
// matching the hook's criteria — the network extension's cert-manager
// install, the telemetry extension's fluent-bit DaemonSet, and so on.
//
// Hooks self-filter by inspecting DatacenterInfo (provider name,
// region, labels). A hook that only applies to k8s clusters returns
// nil for docker / nomad datacenters. The reconciler does not pre-
// filter — it calls every registered hook on every datacenter every
// tick.
//
// # Idempotency contract
//
// Hooks must return the same Spec.Name across reconcile calls when
// they want to keep a service installed. Same Name + different
// Spec body = update; missing Name = retire. The reconciler keys
// off Name to match desired against current rows.
//
// Hook implementations must be safe for concurrent calls — the
// reconciler may invoke the same hook against multiple datacenters
// in parallel in future phases.
type Hook interface {
	// Name returns a stable identifier for this hook. Used for
	// logging, audit events, and to dedupe re-registrations of
	// the same hook (Register replaces, never duplicates).
	Name() string

	// Services returns the bootstrap specs this hook contributes
	// for the given datacenter. Returning nil or empty means the
	// hook has nothing to contribute for this datacenter.
	//
	// An error from Services is logged and the hook is treated as
	// having contributed nothing for this tick — a broken hook
	// does not block other hooks or wipe the desired set.
	Services(ctx context.Context, dc DatacenterInfo) ([]BootstrapServiceSpec, error)
}

// Registry is the set of currently-registered hooks. Thread-safe;
// callers register at app construction (typically via the
// app.WithBootstrapHook option) and the reconciler reads on every
// tick.
type Registry struct {
	mu    sync.RWMutex
	hooks map[string]Hook
}

// NewRegistry constructs an empty Registry.
func NewRegistry() *Registry {
	return &Registry{hooks: make(map[string]Hook)}
}

// Register adds a hook to the registry, keyed by Name. Re-registering
// a hook with the same Name replaces the previous entry — used during
// hot-reload of an extension.
func (r *Registry) Register(h Hook) {
	if h == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.hooks[h.Name()] = h
}

// Hooks returns a snapshot of the currently-registered hooks. Safe
// for the caller to iterate without holding any lock.
func (r *Registry) Hooks() []Hook {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Hook, 0, len(r.hooks))
	for _, h := range r.hooks {
		out = append(out, h)
	}

	return out
}
