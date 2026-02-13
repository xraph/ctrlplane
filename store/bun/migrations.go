package bun

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// runMigrations executes all database migrations.
func runMigrations(ctx context.Context, db *bun.DB) error {
	migrations := []migration{
		{name: "001_create_tenants", up: createTenantsTable},
		{name: "002_create_instances", up: createInstancesTable},
		{name: "003_create_deployments", up: createDeploymentsTable},
		{name: "004_create_releases", up: createReleasesTable},
		{name: "005_create_health_checks", up: createHealthChecksTable},
		{name: "006_create_health_results", up: createHealthResultsTable},
		{name: "007_create_metrics", up: createMetricsTable},
		{name: "008_create_logs", up: createLogsTable},
		{name: "009_create_traces", up: createTracesTable},
		{name: "010_create_resource_snapshots", up: createResourceSnapshotsTable},
		{name: "011_create_domains", up: createDomainsTable},
		{name: "012_create_routes", up: createRoutesTable},
		{name: "013_create_certificates", up: createCertificatesTable},
		{name: "014_create_secrets", up: createSecretsTable},
		{name: "015_create_audit_entries", up: createAuditEntriesTable},
	}

	for _, m := range migrations {
		if err := m.up(ctx, db); err != nil {
			return fmt.Errorf("migration %s failed: %w", m.name, err)
		}
	}

	return nil
}

type migration struct {
	name string
	up   func(ctx context.Context, db *bun.DB) error
}

func createTenantsTable(ctx context.Context, db *bun.DB) error {
	_, err := db.NewCreateTable().
		Model((*tenantModel)(nil)).
		IfNotExists().
		Exec(ctx)

	return err
}

func createInstancesTable(ctx context.Context, db *bun.DB) error {
	_, err := db.NewCreateTable().
		Model((*instanceModel)(nil)).
		IfNotExists().
		Exec(ctx)

	return err
}

func createDeploymentsTable(ctx context.Context, db *bun.DB) error {
	_, err := db.NewCreateTable().
		Model((*deploymentModel)(nil)).
		IfNotExists().
		Exec(ctx)

	return err
}

func createReleasesTable(ctx context.Context, db *bun.DB) error {
	_, err := db.NewCreateTable().
		Model((*releaseModel)(nil)).
		IfNotExists().
		Exec(ctx)

	return err
}

func createHealthChecksTable(ctx context.Context, db *bun.DB) error {
	_, err := db.NewCreateTable().
		Model((*healthCheckModel)(nil)).
		IfNotExists().
		Exec(ctx)

	return err
}

func createHealthResultsTable(ctx context.Context, db *bun.DB) error {
	_, err := db.NewCreateTable().
		Model((*healthResultModel)(nil)).
		IfNotExists().
		Exec(ctx)

	return err
}

func createMetricsTable(ctx context.Context, db *bun.DB) error {
	_, err := db.NewCreateTable().
		Model((*metricModel)(nil)).
		IfNotExists().
		Exec(ctx)

	return err
}

func createLogsTable(ctx context.Context, db *bun.DB) error {
	_, err := db.NewCreateTable().
		Model((*logEntryModel)(nil)).
		IfNotExists().
		Exec(ctx)

	return err
}

func createTracesTable(ctx context.Context, db *bun.DB) error {
	_, err := db.NewCreateTable().
		Model((*traceModel)(nil)).
		IfNotExists().
		Exec(ctx)

	return err
}

func createResourceSnapshotsTable(ctx context.Context, db *bun.DB) error {
	_, err := db.NewCreateTable().
		Model((*resourceSnapshotModel)(nil)).
		IfNotExists().
		Exec(ctx)

	return err
}

func createDomainsTable(ctx context.Context, db *bun.DB) error {
	_, err := db.NewCreateTable().
		Model((*domainModel)(nil)).
		IfNotExists().
		Exec(ctx)

	return err
}

func createRoutesTable(ctx context.Context, db *bun.DB) error {
	_, err := db.NewCreateTable().
		Model((*routeModel)(nil)).
		IfNotExists().
		Exec(ctx)

	return err
}

func createCertificatesTable(ctx context.Context, db *bun.DB) error {
	_, err := db.NewCreateTable().
		Model((*certificateModel)(nil)).
		IfNotExists().
		Exec(ctx)

	return err
}

func createSecretsTable(ctx context.Context, db *bun.DB) error {
	_, err := db.NewCreateTable().
		Model((*secretModel)(nil)).
		IfNotExists().
		Exec(ctx)

	return err
}

func createAuditEntriesTable(ctx context.Context, db *bun.DB) error {
	_, err := db.NewCreateTable().
		Model((*auditEntryModel)(nil)).
		IfNotExists().
		Exec(ctx)

	return err
}
