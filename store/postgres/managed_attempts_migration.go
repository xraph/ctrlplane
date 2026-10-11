package postgres

import (
	"context"

	"github.com/xraph/grove/migrate"

	"github.com/xraph/ctrlplane/managed"
)

func init() { //nolint:gochecknoinits // Migrations use the existing registration mechanism.
	Migrations.MustRegister(&migrate.Migration{Name: "bound_cp_managed_removal_attempts", Version: "20261011000005", Up: func(ctx context.Context, exec migrate.Executor) error {
		_, err := exec.Exec(ctx, `ALTER TABLE cp_managed_controller_records ADD CONSTRAINT cp_managed_removal_attempt_size CHECK (
   (kind NOT IN ('removal_attempt','removal_command','removal_abort','removal_finish','removal_settlement','step_failure') OR octet_length(data)<=16384)
   AND (kind NOT IN ('removal_attempt_root','removal_attempt_current','removal_identity') OR octet_length(data)<=2048)
   AND (kind NOT IN ('removal_attempt_state','removal_admission','removal_revocation','removal_attempt_event','removal_binding_settlement') OR octet_length(data)<=8192)
   AND (kind<>'removal_provider_slot' OR octet_length(data)<=1024)
   AND (kind<>'removal_fence' OR octet_length(data)<=49152))`)

		return err
	}, Down: func(context.Context, migrate.Executor) error { return managed.ErrBlocked }})
}
