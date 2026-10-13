package postgres

import (
	"context"

	"github.com/xraph/grove/migrate"

	"github.com/xraph/ctrlplane/managed"
)

func init() { //nolint:gochecknoinits // Migrations use the existing registration mechanism.
	Migrations.MustRegister(&migrate.Migration{Name: "bound_cp_managed_provider_effect_binding", Version: "20261013000002", Up: func(ctx context.Context, exec migrate.Executor) error {
		_, err := exec.Exec(ctx, `ALTER TABLE cp_managed_controller_records ADD CONSTRAINT cp_managed_provider_effect_binding_size CHECK (kind <> 'provider_effect_binding' OR octet_length(data)<=16384)`)

		return err
	}, Down: func(context.Context, migrate.Executor) error { return managed.ErrBlocked }})
}
