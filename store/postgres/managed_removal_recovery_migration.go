package postgres

import (
	"context"

	"github.com/xraph/grove/migrate"

	"github.com/xraph/ctrlplane/managed"
)

func init() { //nolint:gochecknoinits // Migrations use the existing registration mechanism.
	Migrations.MustRegister(&migrate.Migration{Name: "bound_cp_managed_removal_recovery", Version: "20261013000001", Up: func(ctx context.Context, exec migrate.Executor) error {
		_, err := exec.Exec(ctx, `ALTER TABLE cp_managed_controller_records ADD CONSTRAINT cp_managed_removal_recovery_size CHECK (
  (kind NOT IN ('removal_revalidation','removal_effective_coverage') OR octet_length(data)<=32768)
  AND (kind <> 'removal_revalidation_current' OR octet_length(data)<=1024)
  AND (kind NOT IN ('removal_recovery_acceptance','step_retry_current','step_retry_previous') OR octet_length(data)<=2048)
  AND (kind NOT IN ('removal_recovery_profile','provider_invocation_marker','provider_invocation_closure','removal_recovery_cutoff','step_retry_root','removal_permission_proof') OR octet_length(data)<=8192)
  AND (kind NOT IN ('removal_recovery_intent','removal_native_commit','removal_native_close','step_retry_authorization','step_retry_permission','step_retry_failure','step_retry_result') OR octet_length(data)<=16384))`)

		return err
	}, Down: func(context.Context, migrate.Executor) error { return managed.ErrBlocked }})
}
