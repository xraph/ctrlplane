package postgres

import (
	"context"

	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

func (t managedControllerTransaction) ReserveRows(ctx context.Context, target managed.Identity, previous *managed.Identity) error {
	var occupied bool
	if err := t.tx.NewRaw("SELECT EXISTS(SELECT 1 FROM cp_instances WHERE id=$1)", target.InstanceID.String()).Scan(ctx, &occupied); err != nil {
		return err
	}

	if occupied {
		return managed.ErrConflict
	}

	owner, err := managed.ProjectionOwner(target)
	if err != nil {
		return err
	}

	occupied = false
	if !owner.IsNil() {
		if err = t.tx.NewRaw("SELECT EXISTS(SELECT 1 FROM cp_workloads WHERE id=$1)", owner.String()).Scan(ctx, &occupied); err != nil {
			return err
		}
	}

	return managedstate.ReserveProjectionOwner(ctx, t, target, previous, occupied)
}

func (t managedControllerTransaction) ProjectRows(ctx context.Context, target managed.Identity, receipt managed.ActionReceipt, replay bool) error {
	origin, err := managedstate.ProjectionOwnerOrigin(ctx, t, target)
	if err != nil {
		return err
	}

	inst, owner, release, err := managedstate.ProjectionEntities(target, receipt, origin)
	if err != nil {
		return err
	}

	if replay {
		var (
			im instanceModel
			rm releaseModel
		)

		if err = t.tx.NewSelect(&im).Where("id=$1", inst.ID.String()).Scan(ctx); err != nil {
			return err
		}

		if err = t.tx.NewSelect(&rm).Where("id=$1", release.ID.String()).Scan(ctx); err != nil {
			return err
		}

		if !managedstate.EqualProjectedInstance(fromInstanceModel(&im), inst) || !managedstate.EqualProjectedRelease(fromReleaseModel(&rm), release) {
			return managed.ErrConflict
		}
	} else {
		if _, err = t.tx.NewInsert(toInstanceModel(inst)).Exec(ctx); err != nil {
			return managedConflict(err)
		}

		if _, err = t.tx.NewInsert(toReleaseModel(release)).Exec(ctx); err != nil {
			return managedConflict(err)
		}
	}

	if owner != nil {
		var wm workloadModel

		err = t.tx.NewSelect(&wm).Where("id=$1", owner.ID.String()).Scan(ctx)
		if err == nil {
			if !replay && origin.Key == target.Key || !managedstate.EqualProjectedOwner(fromWorkloadModel(&wm), owner) {
				return managed.ErrConflict
			}
		} else {
			if !isNoRows(err) {
				return err
			}

			if replay || origin.InstanceID != target.InstanceID {
				return managed.ErrConflict
			}

			if _, err = t.tx.NewInsert(toWorkloadModel(owner)).Exec(ctx); err != nil {
				return managedConflict(err)
			}
		}
	}

	return nil
}
