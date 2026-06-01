package app

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/admin"
	audithook "github.com/xraph/ctrlplane/audit_hook"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/bootstrap"
	"github.com/xraph/ctrlplane/datacenter"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/deploy/strategies"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/health"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/metrics"
	"github.com/xraph/ctrlplane/network"
	"github.com/xraph/ctrlplane/plugin"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/providerhealth"
	"github.com/xraph/ctrlplane/secrets"
	"github.com/xraph/ctrlplane/secrets/memoryvault"
	"github.com/xraph/ctrlplane/store"
	"github.com/xraph/ctrlplane/telemetry"
	"github.com/xraph/ctrlplane/template"
	"github.com/xraph/ctrlplane/worker"
	"github.com/xraph/ctrlplane/workload"
)

// CtrlPlane is the root orchestrator that wires all subsystems together.
type CtrlPlane struct {
	config      ctrlplane.Config
	store       store.Store
	vault       secrets.Vault
	auth        auth.Provider
	providers   *provider.Registry
	events      event.Bus
	scheduler   *worker.Scheduler
	extensions  *plugin.Registry
	pendingExts []plugin.Extension
	auditHook   *audithook.Extension

	// bootstrapHooks is the registry of programmatic bootstrap-service
	// contributors. Wired before wireServices() so options like
	// WithBootstrapHook can populate it during construction.
	bootstrapHooks *bootstrap.Registry

	// Services are the public subsystem interfaces.
	Datacenters    datacenter.Service
	Instances      instance.Service
	Workloads      workload.Service
	Deploys        deploy.Service
	Templates      template.Service
	Health         health.Service
	Metrics        metrics.Service
	ProviderHealth *providerhealth.Cache
	Telemetry      telemetry.Service
	Network        network.Service
	Secrets        secrets.Service
	Admin          admin.Service
	Bootstraps     bootstrap.Service
}

// New creates a CtrlPlane with the given options.
func New(opts ...Option) (*CtrlPlane, error) {
	cp := &CtrlPlane{
		providers:      provider.NewRegistry(),
		events:         event.NewInMemoryBus(),
		auth:           &auth.NoopProvider{},
		bootstrapHooks: bootstrap.NewRegistry(),
	}

	for _, opt := range opts {
		if err := opt(cp); err != nil {
			return nil, err
		}
	}

	// Wire up the plugin extension registry.
	cp.extensions = plugin.NewRegistry(slog.Default())
	for _, ext := range cp.pendingExts {
		cp.extensions.Register(ext)
	}

	cp.pendingExts = nil

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

// Vault returns the vault backend used for secret storage.
func (cp *CtrlPlane) Vault() secrets.Vault {
	return cp.vault
}

// vaultSetter is satisfied by services that support late-binding a vault.
type vaultSetter interface {
	SetVault(v secrets.Vault)
}

// SetVault replaces the vault and propagates it to dependent services.
func (cp *CtrlPlane) SetVault(v secrets.Vault) {
	cp.vault = v

	if vs, ok := cp.Deploys.(vaultSetter); ok {
		vs.SetVault(v)
	}

	if vs, ok := cp.Secrets.(vaultSetter); ok {
		vs.SetVault(v)
	}
}

// SetAuditRecorder swaps the audit-trail backend used by the
// default audit_hook plugin. Pass a chronicle-backed adapter (or
// any audithook.Recorder) to redirect audit events from the
// in-store table to a richer system. No-op when audit_hook wasn't
// constructed (e.g. cp.extensions==nil during a stripped-down
// test build).
func (cp *CtrlPlane) SetAuditRecorder(r audithook.Recorder) {
	if cp.auditHook == nil {
		return
	}

	cp.auditHook.SetRecorder(r)
}

// Scheduler returns the background worker scheduler.
func (cp *CtrlPlane) Scheduler() *worker.Scheduler {
	return cp.scheduler
}

// Extensions returns the plugin registry.
func (cp *CtrlPlane) Extensions() *plugin.Registry {
	return cp.extensions
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
	if cp.ProviderHealth != nil {
		// Run the periodic poller in the background. Run() does
		// its own initial sweep before the first tick, so the
		// cache populates ASAP — but we DON'T block Start on it.
		//
		// History: an earlier version called CheckNow synchronously
		// here so the cache was warm by the time Start returned.
		// That deadlocked the server when any provider's
		// HealthCheck (e.g. kubernetes Discovery().ServerVersion())
		// didn't respect ctx and blocked indefinitely against an
		// unreachable cluster. The synchronous sweep then waited
		// on the stuck goroutine via wg.Wait(), Start hung, and
		// forge killed the process. Now the sweep happens in the
		// background goroutine where a hung provider only delays
		// the first cache entry, not server startup.
		go cp.ProviderHealth.Run(ctx)
	}

	return cp.scheduler.Start(ctx)
}

// Stop gracefully shuts down workers and flushes state.
func (cp *CtrlPlane) Stop(ctx context.Context) error {
	// Emit shutdown to all plugins first.
	if cp.extensions != nil {
		cp.extensions.EmitShutdown(ctx)
	}

	if err := cp.scheduler.Stop(ctx); err != nil {
		return err
	}

	return cp.events.Close()
}

// wireServices instantiates all service implementations and background workers.
func (cp *CtrlPlane) wireServices() {
	// Default to an in-memory vault when none was provided via WithVault.
	if cp.vault == nil {
		cp.vault = memoryvault.New()
	}

	// Datacenter service (wired before instances so it can serve as resolver).
	cp.Datacenters = datacenter.NewService(cp.store, cp.providers, cp.events, cp.auth)

	// Bootstrap service — drives shared platform services per
	// datacenter. The reconciler worker (registered below) calls
	// Reconcile on every tick; nothing in the user request path
	// invokes it directly. Hooks registered via WithBootstrapHook
	// already populated cp.bootstrapHooks during option apply.
	cp.Bootstraps = bootstrap.NewService(cp.store, cp.providers, cp.bootstrapHooks, cp.events)

	// Instance service.
	cp.Instances = instance.NewService(cp.store, cp.providers, cp.events, cp.auth, cp.Datacenters)

	// Deploy service with strategies.
	deploySvc := deploy.NewService(cp.store, cp.store, cp.providers, cp.events, cp.auth, cp.vault)
	deploySvc.RegisterStrategy(strategies.NewRolling())
	deploySvc.RegisterStrategy(strategies.NewBlueGreen())
	deploySvc.RegisterStrategy(strategies.NewCanary())
	deploySvc.RegisterStrategy(strategies.NewRecreate())
	cp.Deploys = deploySvc

	// Health service with built-in checkers (declared before Workloads
	// since Workloads.WatchHealth depends on Health.Watch).
	healthSvc := health.NewService(cp.store, cp.events, cp.auth)
	cp.Health = healthSvc

	// Metrics service — in-memory ring buffer + 10s poller per
	// tracked instance. AttachToEvents wires Track / Untrack to
	// instance lifecycle events so the poller follows the live
	// instance set without manual upkeep.
	cp.Metrics = metrics.NewService(metrics.NewInstanceSampler(cp.Instances), metrics.DefaultConfig())
	metrics.AttachToEvents(cp.Metrics, cp.events)

	// Provider-health cache — periodically pings every registered
	// provider's HealthCheck endpoint so dashboards can show "k8s
	// API unreachable" without hitting the cluster on every page
	// load. Poller is started by Start() so single-process tests
	// that don't call Start() get a cold cache (Get returns
	// ok=false → handlers degrade to "unknown").
	cp.ProviderHealth = providerhealth.NewCache(cp.providers, providerhealth.DefaultConfig())

	// Network service (no external router by default). Declared
	// before Workloads so the workload service can read aggregated
	// domains/routes per replica via cp.Network.
	cp.Network = network.NewService(cp.store, nil, cp.events, cp.auth)

	// Template service — workload blueprints. Constructed before
	// Workloads so it can be passed in for FromTemplateID flows; the
	// reverse-direction WorkloadSpecReader is registered after the
	// workload service is built. The shared event bus is wired so
	// template lifecycle events flow into the audit hook (and any
	// other registered subscribers).
	tplSvc := template.NewService(cp.store, cp.events)
	cp.Templates = tplSvc

	// Workload service — orchestrates per-replica Instance lifecycle
	// through the Instance + Deploy + Health services. Wired after
	// all of them so the dependency graph is bottom-up.
	wlSvc := workload.NewService(cp.store, cp.Instances, cp.Deploys, cp.Templates, cp.Health, cp.Metrics, cp.Network, cp.events, cp.auth)
	cp.Workloads = wlSvc

	// Now that the workload service exists, register the spec reader
	// so template.CreateFromWorkload can fork from a live workload.
	tplSvc.SetWorkloadReader(workload.NewSpecReader(wlSvc))

	// Telemetry service.
	cp.Telemetry = telemetry.NewService(cp.store, cp.auth)

	// Secrets service backed by the configured vault.
	cp.Secrets = secrets.NewService(cp.store, cp.vault, cp.auth)

	// Admin service. Wire the providerhealth cache as the live
	// source for ListProviders so the dashboard's Providers page
	// reflects actual reachability instead of always showing
	// healthy. The setter accepts an interface, so we adapt the
	// cache via a small typed adapter that translates Status →
	// ProviderHealthSnapshot without cross-importing.
	adminSvc := admin.NewService(cp.store, cp.store, cp.store, cp.store, cp.providers, cp.events, cp.auth)
	if setter, ok := adminSvc.(interface {
		SetProviderHealth(getter admin.ProviderHealthGetter)
	}); ok {
		setter.SetProviderHealth(providerHealthAdapter{cache: cp.ProviderHealth})
	}

	cp.Admin = adminSvc

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
	cp.scheduler.Register(worker.NewReconciler(cp.store, cp.store, cp.Bootstraps, cp.providers, cp.events, 60*time.Second))
	cp.scheduler.Register(worker.NewHealthRunner(cp.Health, cp.events, healthInterval))
	cp.scheduler.Register(worker.NewTelemetryCollector(cp.Telemetry, cp.providers, telemetryInterval))
	cp.scheduler.Register(worker.NewGarbageCollector(cp.store, cp.Instances, cp.store, cp.events, 5*time.Minute, worker.GCConfig{}))
	cp.scheduler.Register(worker.NewCertRenewer(cp.Network, cp.events, 12*time.Hour))

	// Default audit-trail plugin: bridges every lifecycle event to
	// admin.AuditEntry rows in the store. Without this nothing
	// writes to the audit log table — the dashboard's Audit Log
	// page would always be empty (which it was, until this turn).
	// Operators / parent apps can swap the recorder later via
	// SetAuditRecorder — the ctrlplane forge extension does this
	// at Start to bind a chronicle.Emitter resolved from DI.
	if cp.extensions != nil {
		cp.auditHook = audithook.New(newStoreAuditRecorder(cp.store))
		cp.extensions.Register(cp.auditHook)
	}

	// Subscribe the plugin registry to all events on the bus.
	// This bridges fire-and-forget events to plugin lifecycle hooks.
	if cp.extensions != nil {
		cp.events.Subscribe(cp.extensions.HandleEvent)
	}
}

// storeAuditRecorder is the default audit_hook.Recorder. Translates
// the audit_hook AuditEvent shape into an admin.AuditEntry and
// writes via store.InsertAuditEntry. Failures are returned to the
// caller so the audit_hook extension can log them — no swallow.
type storeAuditRecorder struct {
	store admin.Store
}

func newStoreAuditRecorder(s admin.Store) *storeAuditRecorder {
	return &storeAuditRecorder{store: s}
}

func (r *storeAuditRecorder) Record(ctx context.Context, evt *audithook.AuditEvent) error {
	if r == nil || r.store == nil || evt == nil {
		return nil
	}

	// Pull the event-shaped fields back out of Metadata. The
	// audit_hook extension stuffs them in there because it doesn't
	// know what storage shape we use; we put them back into the
	// admin.AuditEntry's first-class fields so queries / dashboards
	// can filter on them efficiently.
	tenantID, _ := evt.Metadata["tenant_id"].(string)
	actorID, _ := evt.Metadata["actor_id"].(string)

	resourceID := evt.ResourceID
	if resourceID == "" {
		if v, ok := evt.Metadata["instance_id"].(string); ok {
			resourceID = v
		}
	}

	// Strip the lifted fields from Details so we don't double-store
	// them; everything else (provider-specific event payload) stays
	// for forensics.
	details := make(map[string]any, len(evt.Metadata)+3)
	for k, v := range evt.Metadata {
		switch k {
		case "tenant_id", "actor_id", "instance_id":
			continue
		default:
			details[k] = v
		}
	}

	if evt.Outcome != "" {
		details["outcome"] = evt.Outcome
	}

	if evt.Severity != "" {
		details["severity"] = evt.Severity
	}

	if evt.Reason != "" {
		details["reason"] = evt.Reason
	}

	if evt.Category != "" {
		details["category"] = evt.Category
	}

	entry := &admin.AuditEntry{
		Entity:     ctrlplane.NewEntity(id.PrefixAuditEntry),
		TenantID:   tenantID,
		ActorID:    actorID,
		ActorType:  "user",
		Resource:   evt.Resource,
		ResourceID: resourceID,
		Action:     evt.Action,
		Details:    details,
	}

	return r.store.InsertAuditEntry(ctx, entry)
}

// providerHealthAdapter bridges providerhealth.Cache to admin's
// ProviderHealthGetter shape. The two packages have intentionally
// disjoint Status types (admin doesn't import providerhealth); the
// adapter translates one read-side shape into the other.
type providerHealthAdapter struct {
	cache *providerhealth.Cache
}

func (a providerHealthAdapter) Get(name string) (admin.ProviderHealthSnapshot, bool) {
	if a.cache == nil {
		return admin.ProviderHealthSnapshot{}, false
	}

	s, ok := a.cache.Get(name)
	if !ok {
		return admin.ProviderHealthSnapshot{}, false
	}

	return admin.ProviderHealthSnapshot{
		Healthy:   s.Healthy,
		Message:   s.Message,
		CheckedAt: s.CheckedAt,
	}, true
}
