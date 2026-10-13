package postgres

import (
	"context"

	"github.com/xraph/grove/drivers/pgdriver/pgmigrate"
	"github.com/xraph/grove/migrate"

	"github.com/xraph/ctrlplane/managed"
)

// Separate from ordinary Store.Migrate: runtime credentials cannot install this schema.
var writerFloorMigrations = migrate.NewGroup("ctrlplane_writer_floor")

func init() { //nolint:gochecknoinits // Dedicated administrative migration registry.
	writerFloorMigrations.MustRegister(&migrate.Migration{
		Name: "create_protected_writer_floor", Version: "20261013000001",
		Up: func(ctx context.Context, exec migrate.Executor) error {
			_, err := exec.Exec(ctx, `
CREATE TABLE public.cp_writer_floor_history (
    authority_id TEXT NOT NULL CHECK (octet_length(authority_id) BETWEEN 1 AND 256),
    activation_id TEXT NOT NULL CHECK (activation_id ~ '^mop_[0-7][0123456789abcdefghjkmnpqrstvwxyz]{25}$'),
    epoch BIGINT NOT NULL CHECK (epoch > 0),
    minimum_protocol BIGINT NOT NULL CHECK (minimum_protocol > 0),
    digest TEXT NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
    data BYTEA NOT NULL CONSTRAINT cp_writer_floor_history_size CHECK (octet_length(data) BETWEEN 1 AND 4096),
    PRIMARY KEY (authority_id, activation_id),
    UNIQUE (authority_id, epoch)
);
CREATE TABLE public.cp_writer_floor_current (
    authority_id TEXT PRIMARY KEY CHECK (octet_length(authority_id) BETWEEN 1 AND 256),
    activation_id TEXT NOT NULL CHECK (activation_id ~ '^mop_[0-7][0123456789abcdefghjkmnpqrstvwxyz]{25}$'),
    epoch BIGINT NOT NULL CHECK (epoch > 0),
    minimum_protocol BIGINT NOT NULL CHECK (minimum_protocol > 0),
    digest TEXT NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
    data BYTEA NOT NULL CONSTRAINT cp_writer_floor_current_size CHECK (octet_length(data) BETWEEN 1 AND 4096),
    FOREIGN KEY (authority_id, activation_id) REFERENCES public.cp_writer_floor_history(authority_id, activation_id)
);
CREATE FUNCTION public.cp_writer_floor_history_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$ BEGIN
    RAISE EXCEPTION 'writer floor history is immutable' USING ERRCODE = '55000';
END $$;
CREATE TRIGGER cp_writer_floor_history_immutable
BEFORE UPDATE OR DELETE OR TRUNCATE ON public.cp_writer_floor_history
FOR EACH STATEMENT EXECUTE FUNCTION public.cp_writer_floor_history_immutable();
REVOKE ALL ON public.cp_writer_floor_current, public.cp_writer_floor_history FROM PUBLIC;
REVOKE ALL ON FUNCTION public.cp_writer_floor_history_immutable() FROM PUBLIC;
`)

			return err
		},
		Down: func(context.Context, migrate.Executor) error { return managed.ErrBlocked },
	})
}

// Migrate installs protected tables through the separately supplied administrative DB.
// You must provision and verify runtime SELECT-only grants separately, including inherited
// roles, owners and default ACLs. This migration cannot certify deployment custody.
func (a *WriterFloorAdmin) Migrate(ctx context.Context) error {
	if a == nil || a.backend == nil {
		return managed.ErrAuthority
	}

	executor := pgmigrate.New(a.backend.pg)
	_, err := migrate.NewOrchestrator(executor, writerFloorMigrations).Migrate(ctx)

	return err
}
