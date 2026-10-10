package memory

import (
	"context"

	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

func (t managedControllerTransaction) ReserveRows(ctx context.Context, target managed.Identity, previous *managed.Identity) error {
	if t.store.instances[target.InstanceID.String()] != nil {
		return managed.ErrConflict
	}

	owner, err := managed.ProjectionOwner(target)
	if err != nil {
		return err
	}

	return managedstate.ReserveProjectionOwner(ctx, t, target, previous, !owner.IsNil() && t.store.workloads[owner.String()] != nil)
}

func (t managedControllerTransaction) ProjectRows(ctx context.Context, target managed.Identity, receipt managed.ActionReceipt, replay bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	origin, err := managedstate.ProjectionOwnerOrigin(ctx, t, target)
	if err != nil {
		return err
	}

	inst, owner, release, err := managedstate.ProjectionEntities(target, receipt, origin)
	if err != nil {
		return err
	}

	oldInstance := t.store.instances[inst.ID.String()]

	oldRelease := t.store.releases[release.ID.String()]
	if replay {
		if !managedstate.EqualProjectedInstance(oldInstance, inst) || !managedstate.EqualProjectedRelease(oldRelease, release) {
			return managed.ErrConflict
		}
	} else if oldInstance != nil || oldRelease != nil {
		return managed.ErrConflict
	}

	if owner != nil {
		oldOwner := t.store.workloads[owner.ID.String()]
		if oldOwner != nil {
			if !replay && origin.Key == target.Key || !managedstate.EqualProjectedOwner(oldOwner, owner) {
				return managed.ErrConflict
			}
		} else {
			if replay || origin.InstanceID != target.InstanceID {
				return managed.ErrConflict
			}

			t.store.workloads[owner.ID.String()] = owner
		}
	}

	if !replay {
		t.store.instances[inst.ID.String()] = inst
		t.store.releases[release.ID.String()] = release
	}

	return nil
}
