package extension

import (
	"context"
	"net/http"

	"github.com/xraph/ctrlplane/api"
	"github.com/xraph/ctrlplane/app"
	"github.com/xraph/ctrlplane/auth"
)

// ExtensionName is the name registered with Forge.
const ExtensionName = "ctrlplane"

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

// CtrlPlane returns the underlying CtrlPlane instance.
// This is nil until Init is called.
func (e *Extension) CtrlPlane() *app.CtrlPlane {
	return e.cp
}

// API returns the API handler.
func (e *Extension) API() *api.API {
	return e.api
}

// Init initializes the extension. In a Forge environment, this is called
// during app setup. For standalone use, call it manually.
func (e *Extension) Init(_ context.Context) error {
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

	e.api = api.New(e.cp)

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
func (e *Extension) Handler() http.Handler {
	return e.api.Handler()
}
