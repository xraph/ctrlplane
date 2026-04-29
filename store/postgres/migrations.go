package postgres

import (
	"context"

	"github.com/xraph/grove/migrate"
)

// Migrations is the grove migration group for the CtrlPlane store.
var Migrations = migrate.NewGroup("ctrlplane")

func init() { //nolint:gochecknoinits // migrations must self-register
	Migrations.MustRegister(
		&migrate.Migration{
			Name:    "create_cp_tenants",
			Version: "20240101000001",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS cp_tenants (
    id          TEXT PRIMARY KEY,
    external_id TEXT NOT NULL DEFAULT '',
    slug        TEXT NOT NULL UNIQUE,
    name        TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'active',
    metadata    JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`)

				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS cp_tenants`)

				return err
			},
		},
		&migrate.Migration{
			Name:    "create_cp_instances",
			Version: "20240101000002",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS cp_instances (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL,
    slug          TEXT NOT NULL,
    name          TEXT NOT NULL,
    state         TEXT NOT NULL,
    provider_name TEXT NOT NULL,
    provider_ref  TEXT NOT NULL DEFAULT '',
    region        TEXT NOT NULL DEFAULT '',
    image         TEXT NOT NULL DEFAULT '',
    config        JSONB,
    metadata      JSONB,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cp_instances_tenant ON cp_instances (tenant_id);
`)

				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS cp_instances`)

				return err
			},
		},
		&migrate.Migration{
			Name:    "create_cp_deployments",
			Version: "20240101000003",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS cp_deployments (
    id           TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL,
    instance_id  TEXT NOT NULL,
    release_id   TEXT NOT NULL,
    state        TEXT NOT NULL,
    strategy     TEXT NOT NULL DEFAULT '',
    image        TEXT NOT NULL DEFAULT '',
    provider_ref TEXT NOT NULL DEFAULT '',
    error        TEXT NOT NULL DEFAULT '',
    initiator    TEXT NOT NULL DEFAULT '',
    started_at   TIMESTAMPTZ,
    finished_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cp_deployments_tenant ON cp_deployments (tenant_id);
CREATE INDEX IF NOT EXISTS idx_cp_deployments_instance ON cp_deployments (instance_id);
`)

				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS cp_deployments`)

				return err
			},
		},
		&migrate.Migration{
			Name:    "create_cp_releases",
			Version: "20240101000004",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS cp_releases (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL,
    instance_id TEXT NOT NULL,
    version     INT NOT NULL,
    image       TEXT NOT NULL,
    notes       TEXT NOT NULL DEFAULT '',
    commit_sha  TEXT NOT NULL DEFAULT '',
    active      BOOLEAN NOT NULL DEFAULT FALSE,
    config      JSONB,
    metadata    JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cp_releases_tenant ON cp_releases (tenant_id);
CREATE INDEX IF NOT EXISTS idx_cp_releases_instance ON cp_releases (instance_id);
`)

				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS cp_releases`)

				return err
			},
		},
		&migrate.Migration{
			Name:    "create_cp_health_checks",
			Version: "20240101000005",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS cp_health_checks (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL,
    instance_id TEXT NOT NULL,
    name        TEXT NOT NULL,
    type        TEXT NOT NULL,
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    interval    BIGINT NOT NULL DEFAULT 0,
    timeout     BIGINT NOT NULL DEFAULT 0,
    config      JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cp_health_checks_instance ON cp_health_checks (instance_id);
`)

				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS cp_health_checks`)

				return err
			},
		},
		&migrate.Migration{
			Name:    "create_cp_health_results",
			Version: "20240101000006",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS cp_health_results (
    id          BIGSERIAL PRIMARY KEY,
    check_id    TEXT NOT NULL,
    instance_id TEXT NOT NULL,
    tenant_id   TEXT NOT NULL,
    status      TEXT NOT NULL,
    latency     BIGINT NOT NULL DEFAULT 0,
    message     TEXT NOT NULL DEFAULT '',
    status_code INT NOT NULL DEFAULT 0,
    checked_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cp_health_results_check ON cp_health_results (check_id);
`)

				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS cp_health_results`)

				return err
			},
		},
		&migrate.Migration{
			Name:    "create_cp_metrics",
			Version: "20240101000007",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS cp_metrics (
    id          BIGSERIAL PRIMARY KEY,
    tenant_id   TEXT NOT NULL,
    instance_id TEXT NOT NULL,
    name        TEXT NOT NULL,
    type        TEXT NOT NULL DEFAULT '',
    value       DOUBLE PRECISION NOT NULL DEFAULT 0,
    labels      JSONB,
    timestamp   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cp_metrics_instance ON cp_metrics (instance_id);
CREATE INDEX IF NOT EXISTS idx_cp_metrics_timestamp ON cp_metrics (timestamp);
`)

				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS cp_metrics`)

				return err
			},
		},
		&migrate.Migration{
			Name:    "create_cp_logs",
			Version: "20240101000008",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS cp_logs (
    id          BIGSERIAL PRIMARY KEY,
    tenant_id   TEXT NOT NULL,
    instance_id TEXT NOT NULL,
    level       TEXT NOT NULL,
    message     TEXT NOT NULL,
    source      TEXT NOT NULL DEFAULT '',
    attributes  JSONB,
    timestamp   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cp_logs_instance ON cp_logs (instance_id);
CREATE INDEX IF NOT EXISTS idx_cp_logs_timestamp ON cp_logs (timestamp);
`)

				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS cp_logs`)

				return err
			},
		},
		&migrate.Migration{
			Name:    "create_cp_traces",
			Version: "20240101000009",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS cp_traces (
    id          BIGSERIAL PRIMARY KEY,
    tenant_id   TEXT NOT NULL,
    instance_id TEXT NOT NULL,
    trace_id    TEXT NOT NULL,
    span_id     TEXT NOT NULL,
    parent_id   TEXT NOT NULL DEFAULT '',
    operation   TEXT NOT NULL,
    duration    BIGINT NOT NULL DEFAULT 0,
    status      TEXT NOT NULL DEFAULT '',
    attributes  JSONB,
    timestamp   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cp_traces_instance ON cp_traces (instance_id);
CREATE INDEX IF NOT EXISTS idx_cp_traces_trace ON cp_traces (trace_id);
`)

				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS cp_traces`)

				return err
			},
		},
		&migrate.Migration{
			Name:    "create_cp_resource_snapshots",
			Version: "20240101000010",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS cp_resource_snapshots (
    id              BIGSERIAL PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    instance_id     TEXT NOT NULL,
    cpu_percent     DOUBLE PRECISION NOT NULL DEFAULT 0,
    memory_used_mb  INT NOT NULL DEFAULT 0,
    memory_limit_mb INT NOT NULL DEFAULT 0,
    disk_used_mb    INT NOT NULL DEFAULT 0,
    network_in_mb   DOUBLE PRECISION NOT NULL DEFAULT 0,
    network_out_mb  DOUBLE PRECISION NOT NULL DEFAULT 0,
    timestamp       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cp_resource_snapshots_instance ON cp_resource_snapshots (instance_id);
`)

				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS cp_resource_snapshots`)

				return err
			},
		},
		&migrate.Migration{
			Name:    "create_cp_domains",
			Version: "20240101000011",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS cp_domains (
    id           TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL,
    instance_id  TEXT NOT NULL,
    hostname     TEXT NOT NULL UNIQUE,
    verified     BOOLEAN NOT NULL DEFAULT FALSE,
    tls_enabled  BOOLEAN NOT NULL DEFAULT FALSE,
    cert_expiry  TIMESTAMPTZ,
    dns_target   TEXT NOT NULL DEFAULT '',
    verify_token TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cp_domains_tenant ON cp_domains (tenant_id);
CREATE INDEX IF NOT EXISTS idx_cp_domains_instance ON cp_domains (instance_id);
`)

				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS cp_domains`)

				return err
			},
		},
		&migrate.Migration{
			Name:    "create_cp_routes",
			Version: "20240101000012",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS cp_routes (
    id           TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL,
    instance_id  TEXT NOT NULL,
    path         TEXT NOT NULL,
    port         INT NOT NULL,
    protocol     TEXT NOT NULL DEFAULT '',
    weight       INT NOT NULL DEFAULT 0,
    strip_prefix BOOLEAN NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cp_routes_instance ON cp_routes (instance_id);
`)

				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS cp_routes`)

				return err
			},
		},
		&migrate.Migration{
			Name:    "create_cp_certificates",
			Version: "20240101000013",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS cp_certificates (
    id         TEXT PRIMARY KEY,
    domain_id  TEXT NOT NULL,
    tenant_id  TEXT NOT NULL,
    issuer     TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    auto_renew BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cp_certificates_domain ON cp_certificates (domain_id);
`)

				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS cp_certificates`)

				return err
			},
		},
		&migrate.Migration{
			Name:    "create_cp_secrets",
			Version: "20240101000014",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS cp_secrets (
    id          BIGSERIAL PRIMARY KEY,
    tenant_id   TEXT NOT NULL,
    instance_id TEXT NOT NULL,
    key         TEXT NOT NULL,
    value       BYTEA NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cp_secrets_tenant_instance ON cp_secrets (tenant_id, instance_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_cp_secrets_unique_key ON cp_secrets (tenant_id, instance_id, key);
`)

				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS cp_secrets`)

				return err
			},
		},
		&migrate.Migration{
			Name:    "create_cp_audit_entries",
			Version: "20240101000015",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS cp_audit_entries (
    id          BIGSERIAL PRIMARY KEY,
    tenant_id   TEXT NOT NULL,
    actor_id    TEXT NOT NULL,
    action      TEXT NOT NULL,
    resource    TEXT NOT NULL DEFAULT '',
    resource_id TEXT NOT NULL DEFAULT '',
    details     JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cp_audit_entries_tenant ON cp_audit_entries (tenant_id);
CREATE INDEX IF NOT EXISTS idx_cp_audit_entries_created ON cp_audit_entries (created_at);
`)

				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS cp_audit_entries`)

				return err
			},
		},
		// Datacenters table — was missing from the initial migration
		// set, which meant the postgres backend silently couldn't host
		// datacenters. Mongo deployments worked because mongo creates
		// collections lazily on first insert and indexes are configured
		// separately (see store/mongo/store.go::migrationIndexes).
		//
		// (tenant_id, slug) is unique by design: every backend layer
		// pre-checks for duplicates in datacenter.service.Create, but
		// the constraint here is the race-free backstop for concurrent
		// creators (e.g. two studios booting simultaneously and both
		// running the platform-shared datacenter seeder).
		&migrate.Migration{
			Name:    "create_cp_datacenters",
			Version: "20240101000016",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS cp_datacenters (
    id              TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    name            TEXT NOT NULL,
    slug            TEXT NOT NULL,
    provider_name   TEXT NOT NULL,
    region          TEXT NOT NULL,
    zone            TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL,
    latitude        DOUBLE PRECISION NOT NULL DEFAULT 0,
    longitude       DOUBLE PRECISION NOT NULL DEFAULT 0,
    country         TEXT NOT NULL DEFAULT '',
    city            TEXT NOT NULL DEFAULT '',
    max_instances   INT NOT NULL DEFAULT 0,
    max_cpu_millis  INT NOT NULL DEFAULT 0,
    max_memory_mb   INT NOT NULL DEFAULT 0,
    labels          JSONB,
    metadata        JSONB,
    last_checked_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_cp_datacenters_tenant_slug ON cp_datacenters (tenant_id, slug);
CREATE INDEX IF NOT EXISTS idx_cp_datacenters_provider ON cp_datacenters (provider_name);
CREATE INDEX IF NOT EXISTS idx_cp_datacenters_region ON cp_datacenters (region);
`)

				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS cp_datacenters`)

				return err
			},
		},
		// cp_instances was created without an endpoints column, so any
		// pg-backed deployment dropped provider-supplied URLs/ports on
		// every reload. Adding it as a forward-compatible ALTER preserves
		// existing rows; the column defaults to NULL which fromInstanceModel
		// already treats as "no endpoints".
		&migrate.Migration{
			Name:    "add_endpoints_to_cp_instances",
			Version: "20240101000017",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `ALTER TABLE cp_instances ADD COLUMN IF NOT EXISTS endpoints JSONB`)

				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `ALTER TABLE cp_instances DROP COLUMN IF EXISTS endpoints`)

				return err
			},
		},
	)
}
