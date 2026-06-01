// Package testutil provides a ready-to-use ctrlplane wired against
// in-memory backends so consumer projects can write tests without
// standing up a full deployment. Mirrors the shape of authsome's
// testutil package — same constructor pattern, same option style —
// so an engineer who's used one already knows the other.
//
// Typical use:
//
//	cp := ctrlplanetest.NewServer(t)
//	tenant := cp.SeedTenant(t, "Acme", "org-acme", "free")
//	got, err := cp.CP.Admin.GetTenantByExternalID(cp.UserContext("u1"), "org-acme")
//
// Tests that need an HTTP surface (e.g. cross-service e2e) can opt
// into one via WithHTTPAPI() — currently a no-op placeholder reserved
// for the iteration that mounts ctrlplane's API onto a forge router.
package testutil

import (
	"net/http/httptest"
	"testing"

	"github.com/xraph/ctrlplane/app"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/store/memory"
)

// TestServer wraps a fully-wired ctrlplane backed by in-memory stores.
// All fields are exposed so tests can poke directly at the underlying
// services, store, and auth provider when the helpers don't cover a
// specific assertion.
type TestServer struct {
	CP    *app.CtrlPlane
	Store *memory.Store
	Auth  auth.Provider

	// Server is non-nil only when the WithHTTPAPI option was passed.
	// Reserved for cross-service e2e where studio (or another consumer)
	// needs to talk to ctrlplane over HTTP rather than via direct
	// method calls.
	Server *httptest.Server
}

// ServerOption configures the test server.
type ServerOption func(*serverConfig)

type serverConfig struct {
	authProvider auth.Provider
	providers    map[string]provider.Provider
	httpAPI      bool
}

// WithAuthProvider replaces the default NoopProvider. Use when a test
// needs realistic auth claims to flow through ctrlplane's per-method
// auth.RequireClaims gate.
//
// For the common case where a test wants to call admin-gated methods
// directly, prefer the AdminContext helper which injects claims into
// the request context — that path doesn't require the auth provider
// to do anything.
func WithAuthProvider(p auth.Provider) ServerOption {
	return func(c *serverConfig) { c.authProvider = p }
}

// WithProvider registers an infrastructure provider (Docker, K8s, or
// a fake). Tests that don't actually exercise instance lifecycle can
// skip this; ctrlplane will accept the lack of providers and simply
// reject Instances.Create calls.
func WithProvider(name string, p provider.Provider) ServerOption {
	return func(c *serverConfig) {
		if c.providers == nil {
			c.providers = map[string]provider.Provider{}
		}

		c.providers[name] = p
	}
}

// WithHTTPAPI mounts the ctrlplane HTTP API on the test server's
// httptest.Server. Reserved for cross-service e2e tests; current
// implementation panics with a clear message until the wiring lands.
//
// Marked as accepted-but-not-yet-implemented so consumer test code
// can write the call now and have it light up automatically when
// support arrives, rather than discovering the gap later.
func WithHTTPAPI() ServerOption {
	return func(c *serverConfig) { c.httpAPI = true }
}

// NewServer constructs a CtrlPlane backed by an in-memory store and
// the in-memory event bus, with sensible defaults so most tests need
// zero options. A t.Cleanup hook closes the HTTP server when present.
func NewServer(t *testing.T, opts ...ServerOption) *TestServer {
	t.Helper()

	cfg := &serverConfig{
		authProvider: &auth.NoopProvider{},
		providers:    map[string]provider.Provider{},
	}
	for _, opt := range opts {
		opt(cfg)
	}

	store := memory.New()

	cpOpts := []app.Option{
		app.WithStore(store),
		app.WithAuth(cfg.authProvider),
		app.WithEventBus(event.NewInMemoryBus()),
	}
	for name, p := range cfg.providers {
		cpOpts = append(cpOpts, app.WithProvider(name, p))
	}

	cp, err := app.New(cpOpts...)
	if err != nil {
		t.Fatalf("ctrlplanetest: app.New: %v", err)
	}

	ts := &TestServer{
		CP:    cp,
		Store: store,
		Auth:  cfg.authProvider,
	}

	if cfg.httpAPI {
		// Placeholder — when ctrlplane gains a forge-router mount
		// helper for tests, wire it up here and assign ts.Server.
		// Until then, fail loudly so the test author knows this
		// option doesn't yet do anything.
		t.Fatalf("ctrlplanetest: WithHTTPAPI not yet implemented; use direct method calls via ts.CP")
	}

	t.Cleanup(ts.Close)

	return ts
}

// Close releases resources. Safe to call multiple times.
func (ts *TestServer) Close() {
	if ts == nil {
		return
	}

	if ts.Server != nil {
		ts.Server.Close()
		ts.Server = nil
	}
}
