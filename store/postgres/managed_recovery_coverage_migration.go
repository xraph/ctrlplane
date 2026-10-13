package postgres

import (
	"context"

	"github.com/xraph/grove/migrate"

	"github.com/xraph/ctrlplane/managed"
)

func init() { //nolint:gochecknoinits // Migrations use the existing registration mechanism.
	Migrations.MustRegister(&migrate.Migration{Name: "bound_cp_managed_recovery_coverage", Version: "20261013000008", Up: func(ctx context.Context, exec migrate.Executor) error {
		_, err := exec.Exec(ctx, `ALTER TABLE cp_managed_controller_records ADD CONSTRAINT cp_managed_effective_binding_size CHECK (kind <> 'removal_effective_binding' OR octet_length(data)<=8192), ADD CONSTRAINT cp_managed_coverage_selection_size CHECK (kind <> 'removal_coverage_selection' OR octet_length(data)<=2048)`)

		return err
	}, Down: func(context.Context, migrate.Executor) error { return managed.ErrBlocked }})
}
