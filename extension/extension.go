package extension

import (
	"context"
	"errors"
	"net/http"

	"github.com/xraph/forge"
	"github.com/xraph/vessel"

	"github.com/xraph/ctrlplane/api"
	"github.com/xraph/ctrlplane/app"
	"github.com/xraph/ctrlplane/auth"
)

// ExtensionName is the name registered with Forge.
const ExtensionName = "ctrlplane"
const ExtensionDescription = "CtrlPlane control plane for managing cloud infrastructure and deployments"
const ExtensionVersion = "0.1.0"

// Ensure Extension implements forge.Extension at compile time.
var _ forge.Extension = (*Extension)(nil)

// Extension adapts CtrlPlane as a Forge extension.
// It implements the forge.Extension interface when used with Forge.
type Extension struct {
	*forge.BaseExtension

	config Config
	cp     *app.CtrlPlane
	api    *api.API
	opts   []app.Option
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

	cpOpts := make([]app.Option, 0, len(e.opts)+2)
	cpOpts = append(cpOpts, e.opts...)
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
		e.api.RegisterRoutes(fapp.Router())
	}

	return nil
}

// Start begins background workers.
func (e *Extension) Start(ctx context.Context) error {
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

	e.Logger().Debug("ctrlplane: configuration loaded",
		forge.F("disable_routes", e.config.DisableRoutes),
		forge.F("disable_migrate", e.config.DisableMigrate),
		forge.F("base_path", e.config.BasePath),
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
