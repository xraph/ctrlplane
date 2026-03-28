package extension

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/xraph/forge"
	dashboard "github.com/xraph/forge/extensions/dashboard"
	"github.com/xraph/forge/extensions/dashboard/contributor"
	"github.com/xraph/grove"
	"github.com/xraph/vessel"

	"github.com/xraph/ctrlplane/api"
	"github.com/xraph/ctrlplane/app"
	"github.com/xraph/ctrlplane/auth"
	cpdash "github.com/xraph/ctrlplane/dashboard"
	"github.com/xraph/ctrlplane/secrets"
	memoryvault "github.com/xraph/ctrlplane/secrets/memoryvault"
	"github.com/xraph/ctrlplane/store"
	memorystore "github.com/xraph/ctrlplane/store/memory"
	mongostore "github.com/xraph/ctrlplane/store/mongo"
	pgstore "github.com/xraph/ctrlplane/store/postgres"
	sqlitestore "github.com/xraph/ctrlplane/store/sqlite"
)

// ExtensionName is the name registered with Forge.
const ExtensionName = "ctrlplane"

// ExtensionDescription is the human-readable description of the extension.
const ExtensionDescription = "CtrlPlane control plane for managing cloud infrastructure and deployments"

// ExtensionVersion is the semantic version of the extension.
const ExtensionVersion = "0.1.0"

// Ensure Extension implements forge.Extension at compile time.
var _ forge.Extension = (*Extension)(nil)

// Ensure Extension implements dashboard.DashboardAware at compile time.
var _ dashboard.DashboardAware = (*Extension)(nil)

// Extension adapts CtrlPlane as a Forge extension.
// It implements the forge.Extension interface when used with Forge.
type Extension struct {
	*forge.BaseExtension

	config        Config
	cp            *app.CtrlPlane
	api           *api.API
	opts          []app.Option
	useGrove      bool // True when explicitly configured for grove via WithGroveDatabase.
	storeProvided bool // True when WithStore was called explicitly.
	useVault      bool // True when explicitly configured for vault via WithVaultName.
	vaultProvided bool // True when WithVault was called explicitly.
}

// New creates a CtrlPlane Forge extension with the given options.
func New(opts ...ExtOption) *Extension {
	ext := &Extension{
		BaseExtension: forge.NewBaseExtension(ExtensionName, ExtensionVersion, ExtensionDescription),
	}

	for _, opt := range opts {
		opt(ext)
	}

	return ext
}

// CtrlPlane returns the underlying CtrlPlane instance.
// This is nil until Init is called.
func (e *Extension) CtrlPlane() *app.CtrlPlane {
	return e.cp
}

// API returns the API handler.
func (e *Extension) API() *api.API {
	return e.api
}

// Register implements [forge.Extension].
func (e *Extension) Register(fapp forge.App) error {
	if err := e.BaseExtension.Register(fapp); err != nil {
		return err
	}

	if err := e.loadConfiguration(); err != nil {
		return err
	}

	if err := e.Init(fapp); err != nil {
		return err
	}

	return vessel.Provide(fapp.Container(), func() (*app.CtrlPlane, error) {
		return e.cp, nil
	})
}

// Init initializes the extension. In a Forge environment, this is called
// during app setup. For standalone use, call it manually.
func (e *Extension) Init(fapp forge.App) error {
	authProvider := e.config.AuthProvider

	if authProvider == nil {
		authProvider = &auth.NoopProvider{}
	}

	cpOpts := make([]app.Option, 0, len(e.opts)+3)
	cpOpts = append(cpOpts, e.opts...)

	// Resolve the store if one was not explicitly provided via WithStore.
	if !e.storeProvided {
		s, err := e.resolveStore(fapp)
		if err != nil {
			return err
		}

		cpOpts = append(cpOpts, app.WithStore(s))
	}

	// Resolve the vault if one was not explicitly provided via WithVault.
	if !e.vaultProvided {
		v, err := e.resolveVault(fapp)
		if err != nil {
			return err
		}

		cpOpts = append(cpOpts, app.WithVault(v))
	}

	cpOpts = append(cpOpts,
		app.WithConfig(e.config.ToCtrlPlaneConfig()),
		app.WithAuth(authProvider),
	)

	var err error

	e.cp, err = app.New(cpOpts...)
	if err != nil {
		return err
	}

	// Run migrations if not disabled.
	if !e.config.DisableMigrate {
		if err := e.cp.Store().Migrate(context.Background()); err != nil {
			return err
		}
	}

	e.api = api.New(e.cp, fapp.Router())

	if !e.config.DisableRoutes {
		basePath := e.config.BasePath
		if basePath == "" {
			basePath = "/ctrlplane"
		}
		e.api.RegisterRoutes(fapp.Router().Group(basePath))
	}

	return nil
}

// Start begins background workers.
// It also retries vault resolution from the DI container, since all extensions
// have registered their services by the time Start is called.
func (e *Extension) Start(ctx context.Context) error {
	// Late-resolve vault from DI if not explicitly provided.
	// During Register, the vault extension may not have registered yet.
	if !e.vaultProvided && !e.useVault {
		if v, err := vessel.Inject[secrets.Vault](e.App().Container()); err == nil {
			e.Logger().Info("ctrlplane: resolved vault from container")
			e.cp.SetVault(v)
		}
	}

	return e.cp.Start(ctx)
}

// Stop gracefully shuts down.
func (e *Extension) Stop(ctx context.Context) error {
	return e.cp.Stop(ctx)
}

// Handler returns the HTTP handler for all API routes.
// This is a convenience method for standalone use.
func (e *Extension) Handler() http.Handler {
	return e.api.Handler()
}

// Health implements [forge.Extension].
func (e *Extension) Health(ctx context.Context) error {
	if e.cp == nil || e.api == nil {
		return errors.New("ctrlplane extension not initialized")
	}

	return nil
}

// RegisterRoutes registers all ctrlplane API routes into a Forge router
// with full OpenAPI metadata. Use this for Forge extension integration
// where the parent app owns the router.
func (e *Extension) RegisterRoutes(router forge.Router) {
	e.api.RegisterRoutes(router)
}

// DashboardContributor implements dashboard.DashboardAware. It returns a
// LocalContributor that renders ctrlplane pages, widgets, and settings
// in the Forge dashboard using templ + ForgeUI.
func (e *Extension) DashboardContributor() contributor.LocalContributor {
	return cpdash.New(cpdash.NewManifest(), e.cp)
}

// --- Store Resolution ---

// resolveStore determines the store to use, following this priority:
//  1. Explicit grove database (WithGroveDatabase option or YAML grove_database).
//  2. Auto-discover default grove.DB from the Forge DI container.
//  3. Fallback to an in-memory store.
func (e *Extension) resolveStore(fapp forge.App) (store.Store, error) {
	if e.useGrove {
		// Explicitly configured to use a grove database.
		groveDB, err := e.resolveGroveDB(fapp)
		if err != nil {
			return nil, fmt.Errorf("ctrlplane: %w", err)
		}

		s, err := e.buildStoreFromGroveDB(groveDB)
		if err != nil {
			return nil, err
		}

		e.Logger().Info("ctrlplane: resolved grove.DB from container",
			forge.F("driver", groveDB.Driver().Name()),
		)

		return s, nil
	}

	// Auto-discover the default grove.DB from the DI container.
	if db, err := vessel.Inject[*grove.DB](fapp.Container()); err == nil {
		s, err := e.buildStoreFromGroveDB(db)
		if err != nil {
			return nil, err
		}

		e.Logger().Info("ctrlplane: auto-discovered grove.DB from container",
			forge.F("driver", db.Driver().Name()),
		)

		return s, nil
	}

	// No grove DB available — fall back to an in-memory store.
	e.Logger().Warn("ctrlplane: no grove.DB found, using in-memory store")

	return memorystore.New(), nil
}

// resolveGroveDB retrieves a *grove.DB from the Forge DI container.
// If GroveDatabase is set, it looks up the named DB; otherwise it uses the default.
func (e *Extension) resolveGroveDB(fapp forge.App) (*grove.DB, error) {
	if e.config.GroveDatabase != "" {
		db, err := vessel.InjectNamed[*grove.DB](fapp.Container(), e.config.GroveDatabase)
		if err != nil {
			return nil, fmt.Errorf("grove database %q not found in container: %w", e.config.GroveDatabase, err)
		}

		return db, nil
	}

	db, err := vessel.Inject[*grove.DB](fapp.Container())
	if err != nil {
		return nil, fmt.Errorf("default grove database not found in container: %w", err)
	}

	return db, nil
}

// buildStoreFromGroveDB constructs the appropriate store backend
// based on the grove driver type (pg, sqlite, mongo).
func (e *Extension) buildStoreFromGroveDB(db *grove.DB) (store.Store, error) {
	switch db.Driver().Name() {
	case "pg":
		return pgstore.New(db), nil
	case "sqlite":
		return sqlitestore.New(db), nil
	case "mongo":
		return mongostore.New(db), nil
	default:
		return nil, fmt.Errorf("ctrlplane: unsupported grove driver %q", db.Driver().Name())
	}
}

// --- Vault Resolution ---

// resolveVault determines the vault to use, following this priority:
//  1. Explicit vault name (WithVaultName option or YAML vault_name).
//  2. Auto-discover default secrets.Vault from the Forge DI container.
//  3. Fallback to an in-memory vault.
func (e *Extension) resolveVault(fapp forge.App) (secrets.Vault, error) {
	if e.useVault {
		// Explicitly configured to use a named vault.
		v, err := e.resolveVaultFromContainer(fapp)
		if err != nil {
			return nil, fmt.Errorf("ctrlplane: %w", err)
		}

		e.Logger().Info("ctrlplane: resolved vault from container",
			forge.F("vault_name", e.config.VaultName),
		)

		return v, nil
	}

	// Auto-discover default secrets.Vault from DI container.
	// This may fail during Register if the vault extension has not registered yet.
	// Start() retries resolution after all extensions have registered.
	if v, err := vessel.Inject[secrets.Vault](fapp.Container()); err == nil {
		e.Logger().Info("ctrlplane: auto-discovered vault from container")

		return v, nil
	}

	// No vault available yet — fall back to an in-memory vault.
	// Start() will retry DI resolution when the container is fully populated.
	e.Logger().Debug("ctrlplane: no vault found during register, using in-memory vault (will retry in start)")

	return memoryvault.New(), nil
}

// resolveVaultFromContainer retrieves a secrets.Vault from the Forge DI container.
// If VaultName is set, it looks up the named vault; otherwise it uses the default.
func (e *Extension) resolveVaultFromContainer(fapp forge.App) (secrets.Vault, error) {
	if e.config.VaultName != "" {
		v, err := vessel.InjectNamed[secrets.Vault](fapp.Container(), e.config.VaultName)
		if err != nil {
			return nil, fmt.Errorf("vault %q not found in container: %w", e.config.VaultName, err)
		}

		return v, nil
	}

	v, err := vessel.Inject[secrets.Vault](fapp.Container())
	if err != nil {
		return nil, fmt.Errorf("default vault not found in container: %w", err)
	}

	return v, nil
}

// --- Config Loading (mirrors relay extension pattern) ---

// loadConfiguration loads config from YAML files or programmatic sources.
func (e *Extension) loadConfiguration() error {
	programmaticConfig := e.config

	// Try loading from config file.
	fileConfig, configLoaded := e.tryLoadFromConfigFile()

	if !configLoaded {
		if programmaticConfig.RequireConfig {
			return errors.New("ctrlplane: configuration is required but not found in config files; " +
				"ensure 'extensions.ctrlplane' or 'ctrlplane' key exists in your config")
		}

		// Use programmatic config merged with defaults.
		e.config = e.mergeWithDefaults(programmaticConfig)
	} else {
		// Config loaded from YAML -- merge with programmatic options.
		e.config = e.mergeConfigurations(fileConfig, programmaticConfig)
	}

	// Enable grove resolution if YAML config specifies a grove database.
	if e.config.GroveDatabase != "" {
		e.useGrove = true
	}

	// Enable vault resolution if YAML config specifies a vault name.
	if e.config.VaultName != "" {
		e.useVault = true
	}

	e.Logger().Debug("ctrlplane: configuration loaded",
		forge.F("disable_routes", e.config.DisableRoutes),
		forge.F("disable_migrate", e.config.DisableMigrate),
		forge.F("base_path", e.config.BasePath),
		forge.F("grove_database", e.config.GroveDatabase),
		forge.F("vault_name", e.config.VaultName),
	)

	return nil
}

// tryLoadFromConfigFile attempts to load config from YAML files.
func (e *Extension) tryLoadFromConfigFile() (Config, bool) {
	cm := e.App().Config()

	var cfg Config

	// Try "extensions.ctrlplane" first (namespaced pattern).
	if cm.IsSet("extensions.ctrlplane") {
		if err := cm.Bind("extensions.ctrlplane", &cfg); err == nil {
			e.Logger().Debug("ctrlplane: loaded config from file",
				forge.F("key", "extensions.ctrlplane"),
			)

			return cfg, true
		}

		e.Logger().Warn("ctrlplane: failed to bind extensions.ctrlplane config",
			forge.F("error", "bind failed"),
		)
	}

	// Try legacy "ctrlplane" key.
	if cm.IsSet("ctrlplane") {
		if err := cm.Bind("ctrlplane", &cfg); err == nil {
			e.Logger().Debug("ctrlplane: loaded config from file",
				forge.F("key", "ctrlplane"),
			)

			return cfg, true
		}

		e.Logger().Warn("ctrlplane: failed to bind ctrlplane config",
			forge.F("error", "bind failed"),
		)
	}

	return Config{}, false
}

// mergeWithDefaults fills zero-valued fields with defaults.
func (e *Extension) mergeWithDefaults(cfg Config) Config {
	defaults := DefaultConfig()

	if cfg.BasePath == "" {
		cfg.BasePath = defaults.BasePath
	}

	if cfg.HealthInterval == 0 {
		cfg.HealthInterval = defaults.HealthInterval
	}

	if cfg.TelemetryFlushInterval == 0 {
		cfg.TelemetryFlushInterval = defaults.TelemetryFlushInterval
	}

	return cfg
}

// mergeConfigurations merges YAML config with programmatic options.
// YAML config takes precedence for most fields; programmatic bool flags fill gaps.
func (e *Extension) mergeConfigurations(yamlConfig, programmaticConfig Config) Config {
	// Programmatic bool flags override when true.
	if programmaticConfig.DisableRoutes {
		yamlConfig.DisableRoutes = true
	}

	if programmaticConfig.DisableMigrate {
		yamlConfig.DisableMigrate = true
	}

	// String fields: YAML takes precedence.
	if yamlConfig.BasePath == "" && programmaticConfig.BasePath != "" {
		yamlConfig.BasePath = programmaticConfig.BasePath
	}

	if yamlConfig.DatabaseURL == "" && programmaticConfig.DatabaseURL != "" {
		yamlConfig.DatabaseURL = programmaticConfig.DatabaseURL
	}

	if yamlConfig.DefaultProvider == "" && programmaticConfig.DefaultProvider != "" {
		yamlConfig.DefaultProvider = programmaticConfig.DefaultProvider
	}

	if yamlConfig.GroveDatabase == "" && programmaticConfig.GroveDatabase != "" {
		yamlConfig.GroveDatabase = programmaticConfig.GroveDatabase
	}

	if yamlConfig.VaultName == "" && programmaticConfig.VaultName != "" {
		yamlConfig.VaultName = programmaticConfig.VaultName
	}

	// Duration/int fields: YAML takes precedence, programmatic fills gaps.
	if yamlConfig.HealthInterval == 0 && programmaticConfig.HealthInterval != 0 {
		yamlConfig.HealthInterval = programmaticConfig.HealthInterval
	}

	if yamlConfig.TelemetryFlushInterval == 0 && programmaticConfig.TelemetryFlushInterval != 0 {
		yamlConfig.TelemetryFlushInterval = programmaticConfig.TelemetryFlushInterval
	}

	if yamlConfig.MaxInstancesPerTenant == 0 && programmaticConfig.MaxInstancesPerTenant != 0 {
		yamlConfig.MaxInstancesPerTenant = programmaticConfig.MaxInstancesPerTenant
	}

	// AuthProvider: programmatic always wins (can't come from YAML).
	if programmaticConfig.AuthProvider != nil {
		yamlConfig.AuthProvider = programmaticConfig.AuthProvider
	}

	// Fill remaining zeros with defaults.
	return e.mergeWithDefaults(yamlConfig)
}
