package postgres

import (
	"context"

	"github.com/xraph/grove/migrate"

	"github.com/xraph/ctrlplane/managed"
)

func init() { //nolint:gochecknoinits // Existing retained-record migration registry.
	Migrations.MustRegister(&migrate.Migration{Name: "bound_managed_work_discovery", Version: "20261013000006", Up: func(ctx context.Context, exec migrate.Executor) error {
		_, err := exec.Exec(ctx, `ALTER TABLE cp_managed_controller_records ADD CONSTRAINT cp_managed_work_discovery_size CHECK (
(kind NOT IN ('work_index','work_lookup') OR octet_length(data)<=2048) AND
(kind <> 'work_head' OR octet_length(data)<=8192) AND
(kind <> 'phase_command' OR octet_length(data)<=262144) AND
(kind <> 'phase_selection' OR octet_length(data)<=2048))`)

		return err
	}, Down: func(context.Context, migrate.Executor) error { return managed.ErrBlocked }})
}
