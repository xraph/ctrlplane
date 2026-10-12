package postgres

import (
	"context"

	"github.com/xraph/grove/migrate"

	"github.com/xraph/ctrlplane/managed"
)

func init() { //nolint:gochecknoinits // Migrations use the existing registration mechanism.
	Migrations.MustRegister(&migrate.Migration{Name: "bound_cp_managed_removal_continuations", Version: "20261012000001", Up: func(ctx context.Context, exec migrate.Executor) error {
		_, err := exec.Exec(ctx, `ALTER TABLE cp_managed_controller_records ADD CONSTRAINT cp_managed_removal_continuation_size CHECK (
  (kind <> 'removal_continuation' OR octet_length(data)<=16384)
  AND (kind NOT IN ('removal_attempt_closure','removal_current_binding') OR octet_length(data)<=8192))`)

		return err
	}, Down: func(context.Context, migrate.Executor) error { return managed.ErrBlocked }})
}
