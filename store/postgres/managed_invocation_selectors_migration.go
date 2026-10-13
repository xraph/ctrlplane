package postgres

import (
	"context"

	"github.com/xraph/grove/migrate"

	"github.com/xraph/ctrlplane/managed"
)

func init() { //nolint:gochecknoinits // Migrations use the existing registration mechanism.
	Migrations.MustRegister(&migrate.Migration{Name: "bound_cp_managed_invocation_selectors", Version: "20261013000005", Up: func(ctx context.Context, exec migrate.Executor) error {
		_, err := exec.Exec(ctx, `ALTER TABLE cp_managed_controller_records ADD CONSTRAINT cp_managed_invocation_selectors_size CHECK (kind NOT IN ('recovery_invocation_status','recovery_invocation_commit_current') OR octet_length(data)<=4096), ADD CONSTRAINT cp_managed_invocation_marker_members_size CHECK (kind <> 'recovery_invocation_marker_members' OR octet_length(data)<=4096)`)

		return err
	}, Down: func(context.Context, migrate.Executor) error { return managed.ErrBlocked }})
}
