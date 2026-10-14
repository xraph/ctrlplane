package postgres

import (
	"context"

	"github.com/xraph/grove/migrate"

	"github.com/xraph/ctrlplane/managed"
)

func init() { //nolint:gochecknoinits // Migrations use the existing registration mechanism.
	Migrations.MustRegister(&migrate.Migration{Name: "bound_cp_managed_recovery_terminal", Version: "20261013000011", Up: func(ctx context.Context, exec migrate.Executor) error {
		_, err := exec.Exec(ctx, `ALTER TABLE cp_managed_controller_records ADD CONSTRAINT cp_managed_recovery_terminal_request_size CHECK (kind <> 'removal_recovery_terminal_request' OR octet_length(data)<=49152), ADD CONSTRAINT cp_managed_recovery_terminal_selection_size CHECK (kind <> 'removal_recovery_terminal_selection' OR octet_length(data)<=2048)`)

		return err
	}, Down: func(context.Context, migrate.Executor) error { return managed.ErrBlocked }})
}
