package managedstate

import (
	"context"

	"github.com/xraph/ctrlplane/managed"
)

// readConfiguredAttemptInspection extends only configured read-only views. Legacy
// readers and every mutation keep their existing schema and admission boundaries.
func (r *Repository) readConfiguredAttemptInspection(ctx context.Context, tx ControllerTransaction, target *managed.Target, key managed.AttemptKey) (attemptView, error) {
	if r.recoveryHost == nil {
		return readAttempt(ctx, tx, target, key)
	}

	view, _, err := readRecoveryAttemptInspection(ctx, tx, target, key)

	return view, err
}
