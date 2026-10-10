package memory

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

type managedBackend struct {
	store *Store
	held  bool
	clock time.Time
}

func (b managedBackend) lock() func() {
	if b.held {
		return func() {}
	}

	b.store.mu.Lock()

	return b.store.mu.Unlock
}
func (b managedBackend) readLock() func() {
	if b.held {
		return func() {}
	}

	b.store.mu.RLock()

	return b.store.mu.RUnlock
}
func (b managedBackend) timestamp() time.Time {
	if b.held {
		return b.clock
	}

	return now()
}

func (b managedBackend) Create(ctx context.Context, t *managed.Target) (*managed.Target, error) {
	s := b.store
	defer b.lock()()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	k := t.InstanceID.String()
	if old := s.managedTargets[k]; old != nil {
		if old.TenantID != t.TenantID {
			return nil, managed.ErrAuthority
		}

		if !managedstate.EqualIdentity(old.Identity, t.Identity) {
			return nil, managed.ErrConflict
		}

		return managedstate.Clone(old)
	}

	physical := managedstate.PhysicalKey(t.Physical)
	if s.managedPhysical[physical] != "" {
		return nil, managed.ErrConflict
	}

	cloned, err := managedstate.Clone(t)
	if err != nil {
		return nil, err
	}

	cloned.CreatedAt = b.timestamp()
	s.managedTargets[k] = cloned
	s.managedPhysical[physical] = k

	return managedstate.Clone(cloned)
}
func (b managedBackend) Read(ctx context.Context, k managed.Key) (*managed.Target, error) {
	s := b.store

	defer b.readLock()()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	t := s.managedTargets[k.InstanceID.String()]
	if t == nil {
		return nil, managed.ErrNotFound
	}

	if t.TenantID != k.TenantID {
		return nil, managed.ErrAuthority
	}

	return managedstate.Clone(t)
}
func (b managedBackend) List(ctx context.Context, q managed.PageRequest, after string) ([]managed.Summary, error) {
	s := b.store

	defer b.readLock()()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	rows := []managed.Summary{}

	for _, t := range s.managedTargets {
		if (q.TenantID == "" || t.TenantID == q.TenantID) && t.InstanceID.String() > after {
			rows = append(rows, managed.Summary{Key: t.Key, Phase: t.Phase, Desired: t.Desired, Revision: t.Revision, ReservationCount: len(t.Reservations)})
		}
	}

	slices.SortFunc(rows, func(a, b managed.Summary) int { return strings.Compare(a.InstanceID.String(), b.InstanceID.String()) })

	if len(rows) > q.Limit+1 {
		rows = rows[:q.Limit+1]
	}

	return rows, nil
}
func (b managedBackend) Obligations(ctx context.Context, tenant string) (bool, error) {
	s := b.store

	defer b.readLock()()

	if err := ctx.Err(); err != nil {
		return false, err
	}

	for _, t := range s.managedTargets {
		if t.TenantID == tenant && t.Phase != managed.Deleted {
			return true, nil
		}
	}

	return false, nil
}
func (b managedBackend) Atomic(ctx context.Context, k managed.Key, operation id.ID, digest string, fn func(*managed.Target, time.Time) (managed.Receipt, error)) (managed.Receipt, error) {
	s := b.store
	defer b.lock()()

	if err := ctx.Err(); err != nil {
		return managed.Receipt{}, err
	}

	old := s.managedTargets[k.InstanceID.String()]
	if old == nil {
		return managed.Receipt{}, managed.ErrNotFound
	}

	if old.TenantID != k.TenantID {
		return managed.Receipt{}, managed.ErrAuthority
	}

	receiptKey := k.InstanceID.String() + ":" + operation.String()
	if saved, ok := s.managedReceipts[receiptKey]; ok {
		if saved.Digest != digest {
			return managed.Receipt{}, managed.ErrConflict
		}

		return saved, nil
	}

	t, err := managedstate.Clone(old)
	if err != nil {
		return managed.Receipt{}, err
	}

	result, err := fn(t, b.timestamp())
	if err != nil {
		return managed.Receipt{}, err
	}

	cloned, err := managedstate.Clone(t)
	if err != nil {
		return managed.Receipt{}, err
	}

	if err = ctx.Err(); err != nil {
		return managed.Receipt{}, err
	}

	s.managedTargets[k.InstanceID.String()] = cloned
	s.managedReceipts[receiptKey] = result

	return result, nil
}
