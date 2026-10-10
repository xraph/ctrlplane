package postgres

import (
	"context"

	"github.com/xraph/grove/migrate"

	"github.com/xraph/ctrlplane/managed"
)

func init() { //nolint:gochecknoinits // migrations use the existing registration mechanism
	Migrations.MustRegister(&migrate.Migration{
		Name: "create_cp_managed_targets", Version: "20261010000001",
		Up: func(ctx context.Context, exec migrate.Executor) error {
			_, err := exec.Exec(ctx, `CREATE TABLE IF NOT EXISTS cp_managed_targets (
 id TEXT PRIMARY KEY,
 tenant_id TEXT NOT NULL,
 provider TEXT NOT NULL,
 placement TEXT NOT NULL,
 project TEXT NOT NULL,
 generation TEXT NOT NULL,
 phase TEXT NOT NULL,
 desired TEXT NOT NULL,
 revision BIGINT NOT NULL CHECK (revision > 0),
 data JSONB NOT NULL CHECK (octet_length(data::text) <= 8388608),
 UNIQUE(provider,placement,project)
);
CREATE INDEX IF NOT EXISTS cp_managed_targets_tenant_id ON cp_managed_targets(tenant_id,id);
CREATE TABLE IF NOT EXISTS cp_managed_commands (
 target_id TEXT NOT NULL REFERENCES cp_managed_targets(id) ON DELETE RESTRICT,
 operation_id TEXT NOT NULL,
 receipt JSONB NOT NULL CHECK (octet_length(receipt::text) <= 2048),
 PRIMARY KEY(target_id,operation_id)
);`)

			return err
		},
		Down: func(context.Context, migrate.Executor) error { return managed.ErrBlocked },
	})
}
