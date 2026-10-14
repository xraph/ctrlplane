package memory

import (
	"context"

	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

// NewWithWriterFloorAdmission selects protected managed-mutation admission before
// publishing the store. There is no request switch or setter. New remains legacy.
// The compiled ceiling does not certify the host or fence ordinary Store methods.
func NewWithWriterFloorAdmission(authority string) (*Store, error) {
	s := New()
	if _, err := NewWriterFloorReader(s, authority); err != nil {
		return nil, err
	}

	s.writerFloorAuthority = authority

	return s, nil
}

// checkManagedMutationFloorLocked runs only while the original Store.mu is held.
// The staged store inherits both immutable configuration and protected state.
func (b managedBackend) checkManagedMutationFloorLocked(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if b.store == nil {
		return managed.ErrUnavailable
	}

	authority := b.store.writerFloorAuthority
	if authority == "" {
		return nil
	}

	if len(b.store.writerFloors) == 0 {
		return nil
	}
	// Multiple protected authorities cannot select a permissive fallback.
	if len(b.store.writerFloors) != 1 {
		return managed.ErrUnsupported
	}

	state, ok := b.store.writerFloors[authority]
	if !ok {
		return managed.ErrAuthority
	}

	if state == nil || state.current == nil {
		return managed.ErrWriterFloorCorrupt
	}

	return managedstate.CheckWriterFloorMutation(ctx, writerFloorSnapshot{state: state, authority: authority}, authority)
}

func (t managedControllerTransaction) checkManagedMutationFloor(ctx context.Context) error {
	if !t.held {
		return managed.ErrUnavailable
	}

	return t.checkManagedMutationFloorLocked(ctx)
}
