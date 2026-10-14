package postgres

import (
	"context"

	"github.com/xraph/grove/migrate"

	"github.com/xraph/ctrlplane/managed"
)

func init() { //nolint:gochecknoinits // Migrations use the existing registration mechanism.
	Migrations.MustRegister(&migrate.Migration{Name: "bound_cp_managed_guarded_original", Version: "20261013000012", Up: func(ctx context.Context, exec migrate.Executor) error {
		_, err := exec.Exec(ctx, `ALTER TABLE cp_managed_controller_records ADD CONSTRAINT cp_managed_guarded_original_size CHECK (kind <> 'removal_guarded_original' OR octet_length(data)<=49152), ADD CONSTRAINT cp_managed_guarded_original_selection_size CHECK (kind <> 'guarded_original_selection' OR octet_length(data)<=2048), ADD CONSTRAINT cp_managed_guarded_original_materialization_size CHECK (kind <> 'guarded_original_materialization' OR octet_length(data)<=8192)`)

		return err
	}, Down: func(context.Context, migrate.Executor) error { return managed.ErrBlocked }})
}
