// Package providerhealth caches per-provider HealthChecker results
// so dashboards can render workspace state without hammering the
// underlying control planes (docker socket, k8s API server, nomad
// agent) on every request.
//
// The cache is in-memory, populated by a periodic poller that
// runs HealthChecker.HealthCheck on every registered provider.
// Consumers (studio handlers, the workload-health aggregator,
// admin views) read from the cache via Get / Snapshot — both are
// O(1) and lock-free in the read path.
package providerhealth

import (
	"context"
	"sync"
	"time"

	"github.com/xraph/ctrlplane/provider"
)

// Status is the cached health snapshot for one provider.
type Status struct {
	Name      string        `json:"name"`
	Healthy   bool          `json:"healthy"`
	Message   string        `json:"message"`
	Latency   time.Duration `json:"latency"`
	CheckedAt time.Time     `json:"checked_at"`
}

// Cache holds the most recent health status per provider name.
// Polled by Run; consumers read with Get / Snapshot.
type Cache struct {
	registry     *provider.Registry
	pollInterval time.Duration
	checkTimeout time.Duration

	mu      sync.RWMutex
	results map[string]Status
}

// Config tunes the cache.
type Config struct {
	// PollInterval is how often the cache re-checks every provider.
	// Default 30s — provider-control-plane health changes slowly,
	// so this trades a small staleness window for tiny load.
	PollInterval time.Duration

	// CheckTimeout caps each individual HealthCheck call. Set
	// shorter than PollInterval so a hung provider doesn't block
	// the whole sweep. Default 5s.
	CheckTimeout time.Duration
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		PollInterval: 30 * time.Second,
		CheckTimeout: 5 * time.Second,
	}
}

// NewCache wires a cache against the given registry. The cache
// is empty until Run() ticks at least once. Consumers calling Get
// before the first tick get ok=false — the studio handlers treat
// that as "unknown" and degrade the badge gracefully.
func NewCache(registry *provider.Registry, cfg Config) *Cache {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultConfig().PollInterval
	}
	if cfg.CheckTimeout <= 0 {
		cfg.CheckTimeout = DefaultConfig().CheckTimeout
	}
	return &Cache{
		registry:     registry,
		pollInterval: cfg.PollInterval,
		checkTimeout: cfg.CheckTimeout,
		results:      make(map[string]Status),
	}
}

// Run blocks until ctx is cancelled. Does an upfront sweep so the
// cache populates promptly, then ticks at the poll interval.
// Spawn this in a background goroutine — never call it inline:
// the sweep can block on a slow provider HealthCheck (k8s when
// the cluster is unreachable, for instance), and we don't want
// startup to depend on that.
func (c *Cache) Run(ctx context.Context) {
	c.sweep(ctx)
	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.sweep(ctx)
		}
	}
}

// CheckNow runs a one-shot sweep ignoring the schedule. Useful
// after a provider re-registers or a manual "refresh" action.
func (c *Cache) CheckNow(ctx context.Context) {
	c.sweep(ctx)
}

// Get returns the last-known status for a provider. ok=false when
// the cache hasn't seen this provider yet (cold cache, or provider
// just registered).
func (c *Cache) Get(name string) (Status, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.results[name]
	return s, ok
}

// Snapshot returns a copy of every cached status. Stable iteration
// order is not guaranteed; sort by Name in the caller if needed.
func (c *Cache) Snapshot() []Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Status, 0, len(c.results))
	for _, s := range c.results {
		out = append(out, s)
	}
	return out
}

// sweep runs HealthCheck against every registered provider in
// parallel and writes the results into the cache. Providers that
// don't implement HealthChecker get a synthetic "unknown" status
// so the cache surface stays uniform.
//
// Bounds the total wait to checkTimeout * 2 so a single provider
// whose HealthCheck doesn't honor ctx (e.g. k8s
// Discovery().ServerVersion(), which is not ctx-aware) can't
// stall the sweep forever. Workers that haven't reported by the
// deadline are abandoned — their goroutines leak temporarily
// (until their underlying call returns), but the sweep returns
// promptly with an "unhealthy: timed out" entry for the missing
// providers so the cache stays useful.
func (c *Cache) sweep(ctx context.Context) {
	providers := c.registry.All()
	if len(providers) == 0 {
		return
	}

	type result struct {
		name   string
		status Status
	}
	results := make(chan result, len(providers))

	for name, p := range providers {
		go func(name string, p provider.Provider) {
			// Non-blocking send — if the deadline hits and we've
			// stopped reading, the worker still completes its
			// own send into the buffer (channel has capacity =
			// len(providers)) so no goroutine leaks past the
			// HealthCheck completion.
			results <- result{name: name, status: c.checkOne(ctx, name, p)}
		}(name, p)
	}

	// Bound the iteration: collect what completes within the
	// deadline, mark the rest as "timed out".
	deadline := time.NewTimer(2 * c.checkTimeout)
	defer deadline.Stop()

	collected := make(map[string]Status, len(providers))
	for i := 0; i < len(providers); i++ {
		select {
		case r := <-results:
			collected[r.name] = r.status
		case <-ctx.Done():
			i = len(providers) // break outer loop
		case <-deadline.C:
			i = len(providers) // break outer loop
		}
	}

	now := time.Now().UTC()
	c.mu.Lock()
	defer c.mu.Unlock()
	for name := range providers {
		if s, ok := collected[name]; ok {
			c.results[name] = s
			continue
		}
		// Worker didn't finish by the deadline. Don't clobber a
		// previously-good cache entry — just leave it stale and
		// note the tardiness on first sweep.
		if _, exists := c.results[name]; exists {
			continue
		}
		c.results[name] = Status{
			Name:      name,
			Healthy:   false,
			Message:   "health check did not complete within sweep deadline",
			CheckedAt: now,
		}
	}
}

func (c *Cache) checkOne(parent context.Context, name string, p provider.Provider) Status {
	now := time.Now().UTC()

	hc, ok := p.(provider.HealthChecker)
	if !ok {
		return Status{
			Name:      name,
			Healthy:   true, // assume healthy — provider just doesn't expose a probe
			Message:   "provider does not implement HealthChecker",
			CheckedAt: now,
		}
	}

	ctx, cancel := context.WithTimeout(parent, c.checkTimeout)
	defer cancel()

	hs, err := hc.HealthCheck(ctx)
	checked := time.Now().UTC()
	if err != nil {
		return Status{
			Name:      name,
			Healthy:   false,
			Message:   err.Error(),
			Latency:   time.Since(now),
			CheckedAt: checked,
		}
	}
	if hs == nil {
		return Status{
			Name:      name,
			Healthy:   false,
			Message:   "provider HealthCheck returned nil",
			Latency:   time.Since(now),
			CheckedAt: checked,
		}
	}
	return Status{
		Name:      name,
		Healthy:   hs.Healthy,
		Message:   hs.Message,
		Latency:   hs.Latency,
		CheckedAt: hs.CheckedAt,
	}
}
