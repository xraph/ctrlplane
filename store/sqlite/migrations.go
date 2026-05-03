package sqlite

import (
	"context"

	"github.com/xraph/grove/migrate"
)

// Migrations is the grove migration group for the CtrlPlane SQLite store.
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
    metadata    TEXT,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
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
    config        TEXT,
    metadata      TEXT,
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at    TEXT NOT NULL DEFAULT (datetime('now'))
);
`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_cp_instances_tenant ON cp_instances (tenant_id);`)

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
    started_at   TEXT,
    finished_at  TEXT,
    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at   TEXT NOT NULL DEFAULT (datetime('now'))
);
`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_cp_deployments_tenant ON cp_deployments (tenant_id);`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_cp_deployments_instance ON cp_deployments (instance_id);`)

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
    version     INTEGER NOT NULL,
    image       TEXT NOT NULL,
    notes       TEXT NOT NULL DEFAULT '',
    commit_sha  TEXT NOT NULL DEFAULT '',
    active      INTEGER NOT NULL DEFAULT 0,
    config      TEXT,
    metadata    TEXT,
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_cp_releases_tenant ON cp_releases (tenant_id);`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_cp_releases_instance ON cp_releases (instance_id);`)

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
    enabled     INTEGER NOT NULL DEFAULT 1,
    interval    INTEGER NOT NULL DEFAULT 0,
    timeout     INTEGER NOT NULL DEFAULT 0,
    config      TEXT,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_cp_health_checks_instance ON cp_health_checks (instance_id);`)

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
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    check_id    TEXT NOT NULL,
    instance_id TEXT NOT NULL,
    tenant_id   TEXT NOT NULL,
    status      TEXT NOT NULL,
    latency     INTEGER NOT NULL DEFAULT 0,
    message     TEXT NOT NULL DEFAULT '',
    status_code INTEGER NOT NULL DEFAULT 0,
    checked_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_cp_health_results_check ON cp_health_results (check_id);`)

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
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id   TEXT NOT NULL,
    instance_id TEXT NOT NULL,
    name        TEXT NOT NULL,
    type        TEXT NOT NULL DEFAULT '',
    value       REAL NOT NULL DEFAULT 0,
    labels      TEXT,
    timestamp   TEXT NOT NULL DEFAULT (datetime('now'))
);
`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_cp_metrics_instance ON cp_metrics (instance_id);`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_cp_metrics_timestamp ON cp_metrics (timestamp);`)

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
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id   TEXT NOT NULL,
    instance_id TEXT NOT NULL,
    level       TEXT NOT NULL,
    message     TEXT NOT NULL,
    source      TEXT NOT NULL DEFAULT '',
    attributes  TEXT,
    timestamp   TEXT NOT NULL DEFAULT (datetime('now'))
);
`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_cp_logs_instance ON cp_logs (instance_id);`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_cp_logs_timestamp ON cp_logs (timestamp);`)

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
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id   TEXT NOT NULL,
    instance_id TEXT NOT NULL,
    trace_id    TEXT NOT NULL,
    span_id     TEXT NOT NULL,
    parent_id   TEXT NOT NULL DEFAULT '',
    operation   TEXT NOT NULL,
    duration    INTEGER NOT NULL DEFAULT 0,
    status      TEXT NOT NULL DEFAULT '',
    attributes  TEXT,
    timestamp   TEXT NOT NULL DEFAULT (datetime('now'))
);
`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_cp_traces_instance ON cp_traces (instance_id);`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_cp_traces_trace ON cp_traces (trace_id);`)

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
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id       TEXT NOT NULL,
    instance_id     TEXT NOT NULL,
    cpu_percent     REAL NOT NULL DEFAULT 0,
    memory_used_mb  INTEGER NOT NULL DEFAULT 0,
    memory_limit_mb INTEGER NOT NULL DEFAULT 0,
    disk_used_mb    INTEGER NOT NULL DEFAULT 0,
    network_in_mb   REAL NOT NULL DEFAULT 0,
    network_out_mb  REAL NOT NULL DEFAULT 0,
    timestamp       TEXT NOT NULL DEFAULT (datetime('now'))
);
`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_cp_resource_snapshots_instance ON cp_resource_snapshots (instance_id);`)

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
    verified     INTEGER NOT NULL DEFAULT 0,
    tls_enabled  INTEGER NOT NULL DEFAULT 0,
    cert_expiry  TEXT,
    dns_target   TEXT NOT NULL DEFAULT '',
    verify_token TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at   TEXT NOT NULL DEFAULT (datetime('now'))
);
`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_cp_domains_tenant ON cp_domains (tenant_id);`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_cp_domains_instance ON cp_domains (instance_id);`)

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
    port         INTEGER NOT NULL,
    protocol     TEXT NOT NULL DEFAULT '',
    weight       INTEGER NOT NULL DEFAULT 0,
    strip_prefix INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at   TEXT NOT NULL DEFAULT (datetime('now'))
);
`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_cp_routes_instance ON cp_routes (instance_id);`)

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
    expires_at TEXT NOT NULL,
    auto_renew INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_cp_certificates_domain ON cp_certificates (domain_id);`)

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
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id   TEXT NOT NULL,
    instance_id TEXT NOT NULL,
    key         TEXT NOT NULL,
    value       BLOB NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_cp_secrets_tenant_instance ON cp_secrets (tenant_id, instance_id);`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_cp_secrets_unique_key ON cp_secrets (tenant_id, instance_id, key);`)

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
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id   TEXT NOT NULL,
    actor_id    TEXT NOT NULL,
    action      TEXT NOT NULL,
    resource    TEXT NOT NULL DEFAULT '',
    resource_id TEXT NOT NULL DEFAULT '',
    details     TEXT,
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_cp_audit_entries_tenant ON cp_audit_entries (tenant_id);`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_cp_audit_entries_created ON cp_audit_entries (created_at);`)

				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS cp_audit_entries`)

				return err
			},
		},
		&migrate.Migration{
			Name:    "create_cp_templates",
			Version: "20240101000016",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS cp_templates (
    id               TEXT PRIMARY KEY,
    tenant_id        TEXT NOT NULL,
    name             TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    image            TEXT NOT NULL,
    default_strategy TEXT NOT NULL DEFAULT '',
    env              BLOB,
    resources        BLOB,
    ports            BLOB,
    volumes          BLOB,
    health_check     BLOB,
    secrets          BLOB,
    config_files     BLOB,
    labels           BLOB,
    annotations      BLOB,
    notes            TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at       TEXT NOT NULL DEFAULT (datetime('now'))
);
`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_cp_templates_tenant ON cp_templates (tenant_id);`)

				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS cp_templates`)

				return err
			},
		},
		// ─────────────────────────────────────────────────────────────
		// Phase 4 cleanup: drop the legacy single-image columns.
		// SQLite 3.35 (Mar 2021) introduced ALTER TABLE DROP COLUMN —
		// any modern SQLite has this. The Down migration adds the
		// columns back as nullable so a rollback can re-attach the
		// pre-multi-service code paths (data is gone, but the schema
		// shape is restored).
		// ─────────────────────────────────────────────────────────────
		&migrate.Migration{
			Name:    "drop_legacy_single_image_columns",
			Version: "20240101000017",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				stmts := []string{
					`ALTER TABLE cp_instances   DROP COLUMN image`,
					`ALTER TABLE cp_deployments DROP COLUMN image`,
					`ALTER TABLE cp_releases    DROP COLUMN image`,
					`ALTER TABLE cp_templates   DROP COLUMN image`,
					`ALTER TABLE cp_templates   DROP COLUMN env`,
					`ALTER TABLE cp_templates   DROP COLUMN resources`,
					`ALTER TABLE cp_templates   DROP COLUMN ports`,
					`ALTER TABLE cp_templates   DROP COLUMN volumes`,
					`ALTER TABLE cp_templates   DROP COLUMN health_check`,
					`ALTER TABLE cp_templates   DROP COLUMN secrets`,
					`ALTER TABLE cp_templates   DROP COLUMN config_files`,
					`ALTER TABLE cp_templates   DROP COLUMN annotations`,
				}

				for _, stmt := range stmts {
					if _, err := exec.Exec(ctx, stmt); err != nil {
						return err
					}
				}

				return nil
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				stmts := []string{
					`ALTER TABLE cp_instances   ADD COLUMN image TEXT NOT NULL DEFAULT ''`,
					`ALTER TABLE cp_deployments ADD COLUMN image TEXT NOT NULL DEFAULT ''`,
					`ALTER TABLE cp_releases    ADD COLUMN image TEXT NOT NULL DEFAULT ''`,
					`ALTER TABLE cp_templates   ADD COLUMN image TEXT NOT NULL DEFAULT ''`,
					`ALTER TABLE cp_templates   ADD COLUMN env BLOB`,
					`ALTER TABLE cp_templates   ADD COLUMN resources BLOB`,
					`ALTER TABLE cp_templates   ADD COLUMN ports BLOB`,
					`ALTER TABLE cp_templates   ADD COLUMN volumes BLOB`,
					`ALTER TABLE cp_templates   ADD COLUMN health_check BLOB`,
					`ALTER TABLE cp_templates   ADD COLUMN secrets BLOB`,
					`ALTER TABLE cp_templates   ADD COLUMN config_files BLOB`,
					`ALTER TABLE cp_templates   ADD COLUMN annotations BLOB`,
				}

				for _, stmt := range stmts {
					if _, err := exec.Exec(ctx, stmt); err != nil {
						return err
					}
				}

				return nil
			},
		},
		// ─────────────────────────────────────────────────────────────
		// Datacenter bootstrap services (Phase 2). See the matching
		// postgres migrations 20240101000020-21 for the rationale.
		// SQLite's JSONB equivalent is just BLOB / TEXT — bun stores
		// `[]byte` columns as BLOB.
		// ─────────────────────────────────────────────────────────────
		&migrate.Migration{
			Name:    "add_bootstrap_services_to_cp_datacenters",
			Version: "20240101000018",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `ALTER TABLE cp_datacenters ADD COLUMN bootstrap_services BLOB`)

				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `ALTER TABLE cp_datacenters DROP COLUMN bootstrap_services`)

				return err
			},
		},
		&migrate.Migration{
			Name:    "create_cp_bootstrap_workloads",
			Version: "20240101000019",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS cp_bootstrap_workloads (
    id            TEXT PRIMARY KEY,
    datacenter_id TEXT NOT NULL,
    name          TEXT NOT NULL,
    kind          TEXT NOT NULL,
    services      BLOB,
    state         TEXT NOT NULL,
    provider_ref  TEXT NOT NULL DEFAULT '',
    service_refs  BLOB,
    last_error    TEXT NOT NULL DEFAULT '',
    attempts      INTEGER NOT NULL DEFAULT 0,
    labels        BLOB,
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at    TEXT NOT NULL DEFAULT (datetime('now'))
);`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_cp_bootstrap_workloads_dc_name ON cp_bootstrap_workloads (datacenter_id, name);`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_cp_bootstrap_workloads_dc ON cp_bootstrap_workloads (datacenter_id);`)
				if err != nil {
					return err
				}

				_, err = exec.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_cp_bootstrap_workloads_state ON cp_bootstrap_workloads (state);`)

				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS cp_bootstrap_workloads`)

				return err
			},
		},
	)
}
