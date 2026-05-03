package store

import (
	"context"

	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/bootstrap"
	"github.com/xraph/ctrlplane/datacenter"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/health"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/network"
	"github.com/xraph/ctrlplane/secrets"
	"github.com/xraph/ctrlplane/telemetry"
	"github.com/xraph/ctrlplane/template"
	"github.com/xraph/ctrlplane/workload"
)

// Store is the aggregate persistence interface.
// Each subsystem store is a composable interface.
type Store interface {
	instance.Store
	deploy.Store
	workload.Store
	template.Store
	health.Store
	telemetry.Store
	network.Store
	secrets.Store
	admin.Store
	datacenter.Store
	bootstrap.Store

	// Migrate runs all schema migrations.
	Migrate(ctx context.Context) error

	// Ping checks database connectivity.
	Ping(ctx context.Context) error

	// Close closes the store connection.
	Close() error
}
