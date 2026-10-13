package memory

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

// WriterFloorReader exposes protected inspection without administrative methods.
type WriterFloorReader struct {
	*managedstate.WriterFloorReader
}

// WriterFloorAdmin is a separate administrative capability for a memory store.
// Memory cannot isolate credentials. Keep this handle out of runtime dependencies.
// The caller must already hold the deployment barrier; this handle cannot prove it.
type WriterFloorAdmin struct{ backend *writerFloorBackend }

type writerFloorBackend struct {
	store     *Store
	authority string
}

type writerFloorState struct {
	current []byte
	history map[string][]byte
	epochs  map[managed.Decimal]string
}

type writerFloorSnapshot struct {
	state     *writerFloorState
	authority string
}

type writerFloorTransaction struct{ writerFloorSnapshot }

// NewWriterFloorReader binds inspection to the host's configured authority.
func NewWriterFloorReader(store *Store, authority string) (*WriterFloorReader, error) {
	if store == nil {
		return nil, managed.ErrInvalid
	}

	reader, err := managedstate.NewWriterFloorReader(&writerFloorBackend{store: store, authority: authority}, authority)
	if err != nil {
		return nil, err
	}

	return &WriterFloorReader{WriterFloorReader: reader}, nil
}

// NewWriterFloorAdmin constructs an explicit administrative handle, never a Store method.
func NewWriterFloorAdmin(store *Store, authority string) (*WriterFloorAdmin, error) {
	if _, err := NewWriterFloorReader(store, authority); err != nil {
		return nil, err
	}

	return &WriterFloorAdmin{backend: &writerFloorBackend{store: store, authority: authority}}, nil
}

// Activate retains one administrative activation atomically. It cannot enable a protocol.
func (a *WriterFloorAdmin) Activate(ctx context.Context, request managed.WriterFloorActivation) (managed.WriterFloor, error) {
	if a == nil || a.backend == nil {
		return managed.WriterFloor{}, managed.ErrAuthority
	}

	return managedstate.ActivateWriterFloor(ctx, a.backend, a.backend.authority, request)
}

func (b *writerFloorBackend) WriterFloorSnapshot(ctx context.Context, fn func(managedstate.WriterFloorSnapshot) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	b.store.mu.RLock()
	defer b.store.mu.RUnlock()

	if err := ctx.Err(); err != nil {
		return err
	}

	if err := fn(writerFloorSnapshot{state: b.store.writerFloors[b.authority], authority: b.authority}); err != nil {
		return err
	}

	return ctx.Err()
}

func (b *writerFloorBackend) WriterFloorAdminAtomic(ctx context.Context, fn func(managedstate.WriterFloorAdminTransaction) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	b.store.mu.Lock()
	defer b.store.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}

	staged := &writerFloorState{history: map[string][]byte{}, epochs: map[managed.Decimal]string{}}
	if old := b.store.writerFloors[b.authority]; old != nil {
		// Stored byte slices are immutable and all reads return owned copies.
		staged.current = old.current
		staged.history = maps.Clone(old.history)

		staged.epochs = maps.Clone(old.epochs)
		if staged.history == nil || staged.epochs == nil {
			return managed.ErrWriterFloorCorrupt
		}
	}

	if err := fn(&writerFloorTransaction{writerFloorSnapshot{state: staged, authority: b.authority}}); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	if b.store.writerFloors == nil {
		b.store.writerFloors = map[string]*writerFloorState{}
	}

	b.store.writerFloors[b.authority] = staged

	return nil
}

func (s writerFloorSnapshot) checked(ctx context.Context, raw []byte, activation string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	floor, err := managed.DecodeWriterFloor(raw)
	if err != nil || floor.AuthorityID != s.authority || floor.ActivationID.String() != activation {
		return nil, managed.ErrWriterFloorCorrupt
	}

	if s.state == nil || s.state.epochs[floor.Epoch] != activation {
		return nil, managed.ErrWriterFloorCorrupt
	}

	return bytes.Clone(raw), nil
}

func (s writerFloorSnapshot) ReadCurrentWriterFloor(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if s.state == nil || s.state.current == nil {
		return nil, managed.ErrNotFound
	}

	floor, err := managed.DecodeWriterFloor(s.state.current)
	if err != nil {
		return nil, managed.ErrWriterFloorCorrupt
	}

	return s.checked(ctx, s.state.current, floor.ActivationID.String())
}

func (s writerFloorSnapshot) ReadWriterFloorActivation(ctx context.Context, activation id.ID) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if s.state == nil {
		return nil, managed.ErrNotFound
	}

	raw, exists := s.state.history[activation.String()]
	if !exists {
		return nil, managed.ErrNotFound
	}

	return s.checked(ctx, raw, activation.String())
}

func (t *writerFloorTransaction) WriterFloorTime(ctx context.Context) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}

	return now(), nil
}

func (t *writerFloorTransaction) InstallInitialWriterFloor(ctx context.Context, raw []byte) error {
	if t.state.current != nil || len(t.state.history) != 0 || len(t.state.epochs) != 0 {
		return managed.ErrWriterFloorCorrupt
	}

	floor, err := managed.DecodeWriterFloor(raw)
	if err != nil {
		return err
	}

	if floor.Epoch != "1" || floor.PreviousDigest != "" {
		return managed.ErrConflict
	}

	return t.insert(ctx, floor, raw)
}

func (t *writerFloorTransaction) AdvanceWriterFloor(ctx context.Context, expected string, raw []byte) error {
	oldRaw, err := t.ReadCurrentWriterFloor(ctx)
	if err != nil {
		return err
	}

	old, err := managed.DecodeWriterFloor(oldRaw)
	if err != nil {
		return err
	}

	history, err := t.ReadWriterFloorActivation(ctx, old.ActivationID)
	if err != nil {
		if errors.Is(err, managed.ErrNotFound) {
			return managed.ErrWriterFloorCorrupt
		}

		return err
	}

	if !bytes.Equal(history, oldRaw) {
		return managed.ErrWriterFloorCorrupt
	}

	if old.Digest != expected {
		return managed.ErrConflict
	}

	floor, err := managed.DecodeWriterFloor(raw)
	if err != nil {
		return err
	}

	oldEpoch, _ := old.Epoch.Uint64()
	nextEpoch, _ := floor.Epoch.Uint64()
	oldProtocol, _ := old.MinimumProtocol.Uint64()

	nextProtocol, _ := floor.MinimumProtocol.Uint64()
	if floor.PreviousDigest != expected || nextEpoch != oldEpoch+1 || nextProtocol < oldProtocol || floor.ActivatedAt.Before(old.ActivatedAt) {
		return managed.ErrConflict
	}

	return t.insert(ctx, floor, raw)
}

func (t *writerFloorTransaction) insert(ctx context.Context, floor managed.WriterFloor, raw []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if floor.AuthorityID != t.authority {
		return managed.ErrAuthority
	}

	if _, exists := t.state.history[floor.ActivationID.String()]; exists {
		return managed.ErrConflict
	}

	if _, exists := t.state.epochs[floor.Epoch]; exists {
		return managed.ErrConflict
	}

	t.state.history[floor.ActivationID.String()] = bytes.Clone(raw)
	t.state.epochs[floor.Epoch] = floor.ActivationID.String()
	t.state.current = bytes.Clone(raw)

	return ctx.Err()
}

// checkWriterFloorMutationHeld is a future entry point under Store.mu, before other DML.
// The staged controller store must carry the protected state from its locked parent.
// This foundation never permits mutation and no existing runtime path calls this helper.
func (t managedControllerTransaction) checkWriterFloorMutationHeld(ctx context.Context, authority string) error {
	if !t.held || t.store == nil {
		return managed.ErrUnavailable
	}

	err := managedstate.CheckWriterFloorMutation(ctx, writerFloorSnapshot{state: t.store.writerFloors[authority], authority: authority}, authority)
	if errors.Is(err, managed.ErrNotFound) {
		return managed.ErrUnavailable
	}

	return err
}

var _ managedstate.WriterFloorReadBackend = (*writerFloorBackend)(nil)
var _ managedstate.WriterFloorAdminBackend = (*writerFloorBackend)(nil)
var _ managedstate.WriterFloorAdminTransaction = (*writerFloorTransaction)(nil)
