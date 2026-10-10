package postgres

import (
	"context"

	"github.com/xraph/grove/migrate"

	"github.com/xraph/ctrlplane/managed"
)

func init() { //nolint:gochecknoinits // Migrations use the existing registration mechanism.
	Migrations.MustRegister(&migrate.Migration{Name: "create_cp_managed_controller_records", Version: "20261010000002", Up: func(ctx context.Context, exec migrate.Executor) error {
		_, err := exec.Exec(ctx, `CREATE TABLE IF NOT EXISTS cp_managed_controller_records (
 kind TEXT NOT NULL,
 scope TEXT NOT NULL,
 key TEXT NOT NULL,
 data BYTEA NOT NULL CHECK (octet_length(data)<=262144),
 PRIMARY KEY(kind,scope,key)
 );`)

		return err
	}, Down: func(context.Context, migrate.Executor) error { return managed.ErrBlocked }})
}
