package app

import (
	"context"
	"net/http"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/deploy/strategies"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/health"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/network"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/secrets"
	"github.com/xraph/ctrlplane/store"
	"github.com/xraph/ctrlplane/telemetry"
	"github.com/xraph/ctrlplane/worker"
)

// CtrlPlane is the root orchestrator that wires all subsystems together.
type CtrlPlane struct {
	config    ctrlplane.Config
	store     store.Store
	auth      auth.Provider
	providers *provider.Registry
	events    event.Bus
	scheduler *worker.Scheduler

	// Services are the public subsystem interfaces.
	Instances instance.Service
	Deploys   deploy.Service
	Health    health.Service
	Telemetry telemetry.Service
	Network   network.Service
	Secrets   secrets.Service
	Admin     admin.Service
}

// New creates a CtrlPlane with the given options.
func New(opts ...Option) (*CtrlPlane, error) {
	cp := &CtrlPlane{
		providers: provider.NewRegistry(),
		events:    event.NewInMemoryBus(),
		auth:      &auth.NoopProvider{},
	}

	for _, opt := range opts {
		if err := opt(cp); err != nil {
			return nil, err
		}
	}

	cp.wireServices()

	return cp, nil
}

// Store returns the underlying store.
func (cp *CtrlPlane) Store() store.Store {
	return cp.store
}

// Auth returns the auth provider.
func (cp *CtrlPlane) Auth() auth.Provider {
	return cp.auth
}

// Providers returns the provider registry.
func (cp *CtrlPlane) Providers() *provider.Registry {
	return cp.providers
}

// Events returns the event bus.
func (cp *CtrlPlane) Events() event.Bus {
	return cp.events
}

// Config returns the current configuration.
func (cp *CtrlPlane) Config() ctrlplane.Config {
	return cp.config
}

// Routes returns an http.Handler with all ctrlplane API routes mounted.
// This is a stub that will be filled in when the api/ package is implemented.
func (cp *CtrlPlane) Routes() http.Handler {
	return http.NotFoundHandler()
}

// Start begins background workers (health checks, reconciliation, GC).
func (cp *CtrlPlane) Start(ctx context.Context) error {
	return cp.scheduler.Start(ctx)
}

// Stop gracefully shuts down workers and flushes state.
func (cp *CtrlPlane) Stop(ctx context.Context) error {
	if err := cp.scheduler.Stop(ctx); err != nil {
		return err
	}

	return cp.events.Close()
}

// wireServices instantiates all service implementations and background workers.
func (cp *CtrlPlane) wireServices() {
	// Instance service.
	cp.Instances = instance.NewService(cp.store, cp.providers, cp.events, cp.auth)

	// Deploy service with strategies.
	deploySvc := deploy.NewService(cp.store, cp.store, cp.providers, cp.events, cp.auth)
	deploySvc.RegisterStrategy(strategies.NewRolling())
	deploySvc.RegisterStrategy(strategies.NewBlueGreen())
	deploySvc.RegisterStrategy(strategies.NewCanary())
	deploySvc.RegisterStrategy(strategies.NewRecreate())
	cp.Deploys = deploySvc

	// Health service with built-in checkers.
	healthSvc := health.NewService(cp.store, cp.events, cp.auth)
	cp.Health = healthSvc

	// Telemetry service.
	cp.Telemetry = telemetry.NewService(cp.store, cp.auth)

	// Network service (no external router by default).
	cp.Network = network.NewService(cp.store, nil, cp.events, cp.auth)

	// Secrets service (no vault by default; users should provide one via options).
	cp.Secrets = secrets.NewService(cp.store, nil, cp.auth)

	// Admin service.
	cp.Admin = admin.NewService(cp.store, cp.store, cp.store, cp.store, cp.providers, cp.events, cp.auth)

	// Background workers.
	healthInterval := cp.config.HealthInterval
	if healthInterval == 0 {
		healthInterval = 30 * time.Second
	}

	telemetryInterval := cp.config.TelemetryFlushInterval
	if telemetryInterval == 0 {
		telemetryInterval = 10 * time.Second
	}

	cp.scheduler = worker.NewScheduler()
	cp.scheduler.Register(worker.NewReconciler(cp.store, cp.providers, cp.events, 60*time.Second))
	cp.scheduler.Register(worker.NewHealthRunner(cp.Health, cp.events, healthInterval))
	cp.scheduler.Register(worker.NewTelemetryCollector(cp.Telemetry, cp.providers, telemetryInterval))
	cp.scheduler.Register(worker.NewGarbageCollector(cp.store, 5*time.Minute))
	cp.scheduler.Register(worker.NewCertRenewer(cp.Network, cp.events, 12*time.Hour))
}
