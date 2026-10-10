// Package managedservice defines the authority boundary for managed consumers.
package managedservice

import (
	"context"

	"github.com/xraph/ctrlplane/managed"
)

// ProjectionStore materializes only identities already reserved by accepted actions.
type ProjectionStore interface {
	ProjectManagedRows(ctx context.Context, request managed.ProjectionRequest) (managed.ProjectionReceipt, error)
}

// Authority keeps lifecycle, controller and ordinary-row projection in one store.
type Authority interface {
	managed.ControllerStore
	ProjectionStore
}
