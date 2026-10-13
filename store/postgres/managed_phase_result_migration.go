package postgres

import (
	"context"

	"github.com/xraph/grove/migrate"

	"github.com/xraph/ctrlplane/managed"
)

func init() { //nolint:gochecknoinits // Existing retained-record migration registry.
	Migrations.MustRegister(&migrate.Migration{Name: "bound_managed_phase_original_result", Version: "20261013000007", Up: func(ctx context.Context, exec migrate.Executor) error {
		_, err := exec.Exec(ctx, `ALTER TABLE cp_managed_controller_records ADD CONSTRAINT cp_managed_phase_original_result_size CHECK (kind <> 'phase_original_result' OR octet_length(data)<=16384)`)

		return err
	}, Down: func(context.Context, migrate.Executor) error { return managed.ErrBlocked }})
}
