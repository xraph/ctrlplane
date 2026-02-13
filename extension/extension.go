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
	config Config
	cp     *app.CtrlPlane
	api    *api.API
	opts   []app.Option
}

// New creates a CtrlPlane Forge extension with the given options.
func New(opts ...ExtOption) *Extension {
	ext := &Extension{}

	for _, opt := range opts {
		opt(ext)
	}

	return ext
}

// Name returns the extension name.
func (e *Extension) Name() string {
	return ExtensionName
}

func (e *Extension) Description() string {
	return ExtensionDescription
}

// Version implements [forge.Extension].
func (e *Extension) Version() string {
	return ExtensionVersion
}

// Dependencies returns the list of extension names this extension depends on.
// CtrlPlane has no required dependencies, but can optionally use auth extensions.
func (e *Extension) Dependencies() []string {
	return []string{}
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
	if err := e.Init(fapp); err != nil {
		return err
	}

	if err := vessel.ProvideConstructor(fapp.Container(), func() (*app.CtrlPlane, error) {
		return e.cp, nil
	}); err != nil {
		return err
	}

	return nil
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

	e.api = api.New(e.cp, fapp.Router())
	e.api.RegisterRoutes(fapp.Router())

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
