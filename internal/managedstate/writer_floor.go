package managedstate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

// WriterFloorSnapshot exposes exact protected reads within one authority snapshot.
// Current/history are separate from generic controller records and target deletion.
// Every returned byte slice is owned by the caller and bounded before allocation in SQL.
type WriterFloorSnapshot interface {
	ReadCurrentWriterFloor(ctx context.Context) ([]byte, error)
	ReadWriterFloorActivation(ctx context.Context, activationID id.ID) ([]byte, error)
}

// WriterFloorReadBackend binds a trusted configured authority to consistent protected reads.
// Callbacks run under a read snapshot or the existing held mutation transaction.
type WriterFloorReadBackend interface {
	WriterFloorSnapshot(ctx context.Context, fn func(WriterFloorSnapshot) error) error
}

// WriterFloorAdminTransaction is available only through a dedicated administrative connection.
// It must not be implemented by an ordinary ControllerTransaction or runtime repository.
type WriterFloorAdminTransaction interface {
	WriterFloorSnapshot
	WriterFloorTime(ctx context.Context) (time.Time, error)
	// InstallInitialWriterFloor atomically inserts history and current only into a pristine
	// protected namespace. Missing current with ANY retained history must refuse. The adapter
	// must enforce this even when the requested activation ID differs from existing history.
	InstallInitialWriterFloor(ctx context.Context, data []byte) error
	// AdvanceWriterFloor compares the exact current digest, inserts immutable history and
	// replaces current atomically. Existing history never changes; errors roll back all writes.
	// History has unique authority/activation and authority/epoch keys. Reusing an epoch with
	// a new ID must refuse, including after a current-only rollback. No history scan is needed.
	AdvanceWriterFloor(ctx context.Context, expectedDigest string, data []byte) error
}

// WriterFloorAdminBackend invokes its callback once and serializes authority activation.
// The outer deployment barrier has already closed traffic and holds the full DML/grant lock
// domain. This interface does not establish that barrier or authenticate the administrator.
type WriterFloorAdminBackend interface {
	WriterFloorAdminAtomic(ctx context.Context, fn func(WriterFloorAdminTransaction) error) error
}

// WriterFloorReader implements inspection over a protected backend with no activation method.
type WriterFloorReader struct {
	backend   WriterFloorReadBackend
	authority string
}

// NewWriterFloorReader binds trusted host configuration, not a request-selected authority.
func NewWriterFloorReader(backend WriterFloorReadBackend, authority string) (*WriterFloorReader, error) {
	if writerFloorNil(backend) || !token(authority) {
		return nil, managed.ErrInvalid
	}

	return &WriterFloorReader{backend: backend, authority: authority}, nil
}

var _ managed.ManagedWriterFloorReader = (*WriterFloorReader)(nil)
var _ managed.ManagedWriterFloorHistoryReader = (*WriterFloorReader)(nil)

// ReadManagedWriterFloor requires identical current and immutable history bytes.
func (r *WriterFloorReader) ReadManagedWriterFloor(ctx context.Context) (managed.WriterFloor, error) {
	var result managed.WriterFloor

	err := r.backend.WriterFloorSnapshot(ctx, func(snapshot WriterFloorSnapshot) error {
		var err error

		result, err = readWriterFloor(ctx, snapshot, r.authority)
		if errors.Is(err, managed.ErrNotFound) {
			return managed.ErrUnavailable
		}

		return err
	})
	if err != nil {
		return managed.WriterFloor{}, err
	}

	return result, nil
}

// ReadManagedWriterFloorActivation returns exact history even after current has advanced.
// It is inspection only and does not establish current readiness or repair missing metadata.
func (r *WriterFloorReader) ReadManagedWriterFloorActivation(ctx context.Context, ref managed.WriterFloorReference) (managed.WriterFloor, error) {
	var result managed.WriterFloor
	if ref.AuthorityID != r.authority || ref.ActivationID.Prefix() != managed.PrefixOperation || !digest(ref.Digest) {
		return result, managed.ErrAuthority
	}

	err := r.backend.WriterFloorSnapshot(ctx, func(snapshot WriterFloorSnapshot) error {
		raw, err := snapshot.ReadWriterFloorActivation(ctx, ref.ActivationID)
		if err != nil {
			return err
		}

		result, err = decodeProtectedWriterFloor(raw, r.authority)
		if err != nil {
			return err
		}

		if result.Reference() != ref {
			return managed.ErrWriterFloorCorrupt
		}

		return nil
	})
	if err != nil {
		return managed.WriterFloor{}, err
	}

	return result, nil
}

func decodeProtectedWriterFloor(raw []byte, authority string) (managed.WriterFloor, error) {
	floor, err := managed.DecodeWriterFloor(raw)
	if err != nil {
		return managed.WriterFloor{}, fmt.Errorf("%w: %w", managed.ErrWriterFloorCorrupt, err)
	}

	if floor.AuthorityID != authority {
		return managed.WriterFloor{}, managed.ErrWriterFloorCorrupt
	}

	return floor, nil
}

func readWriterFloor(ctx context.Context, snapshot WriterFloorSnapshot, authority string) (managed.WriterFloor, error) {
	raw, err := snapshot.ReadCurrentWriterFloor(ctx)
	if err != nil {
		return managed.WriterFloor{}, err
	}

	floor, err := decodeProtectedWriterFloor(raw, authority)
	if err != nil {
		return managed.WriterFloor{}, err
	}

	history, err := snapshot.ReadWriterFloorActivation(ctx, floor.ActivationID)
	if errors.Is(err, managed.ErrNotFound) {
		return managed.WriterFloor{}, managed.ErrWriterFloorCorrupt
	}

	if err != nil {
		return managed.WriterFloor{}, err
	}

	if !bytes.Equal(raw, history) {
		return managed.WriterFloor{}, managed.ErrWriterFloorCorrupt
	}

	return floor, nil
}

// CheckWriterFloorMutation is a held-transaction check with no caller-supplied protocol.
// No writer protocol is certified by this foundation. A valid positive floor therefore
// refuses all fresh mutation until the complete implementation and qualification land.
// Missing/corrupt metadata also refuses; there is no legacy-mode or cached allow argument.
func CheckWriterFloorMutation(ctx context.Context, snapshot WriterFloorSnapshot, authority string) error {
	if writerFloorNil(snapshot) || !token(authority) {
		return managed.ErrUnavailable
	}

	if _, err := readWriterFloor(ctx, snapshot, authority); err != nil {
		if errors.Is(err, managed.ErrNotFound) {
			return managed.ErrUnavailable
		}

		return err
	}

	return managed.ErrUnsupported
}

// ActivateWriterFloor stages one monotone administrative activation or returns exact history.
// It is not attached to Repository and is never a runtime mutation bypass. The backend must
// use separate administrative credentials and the external deployment barrier must be closed.
// The returned record is historical metadata, not permission to reopen or delegate.
func ActivateWriterFloor(ctx context.Context, backend WriterFloorAdminBackend, authority string, request managed.WriterFloorActivation) (managed.WriterFloor, error) {
	if writerFloorNil(backend) || !token(authority) || request.AuthorityID != authority {
		return managed.WriterFloor{}, managed.ErrAuthority
	}

	requestDigest, err := managed.WriterFloorActivationDigest(request)
	if err != nil {
		return managed.WriterFloor{}, err
	}

	var result managed.WriterFloor

	err = backend.WriterFloorAdminAtomic(ctx, func(tx WriterFloorAdminTransaction) error {
		current, currentErr := readWriterFloor(ctx, tx, authority)
		// ErrNotFound is not proof of pristine storage. The initial installer must
		// independently refuse any existing activation or incomplete protected state.
		if currentErr != nil && !errors.Is(currentErr, managed.ErrNotFound) {
			return currentErr
		}

		saved, savedErr := tx.ReadWriterFloorActivation(ctx, request.ActivationID)
		if savedErr == nil {
			if currentErr != nil {
				return managed.ErrWriterFloorCorrupt
			}

			old, decodeErr := decodeProtectedWriterFloor(saved, authority)
			if decodeErr != nil {
				return decodeErr
			}

			if old.ActivationID != request.ActivationID || old.ActivationDigest != requestDigest || old.Activation() != request {
				return managed.ErrConflict
			}

			oldEpoch, _ := old.Epoch.Uint64()
			currentEpoch, _ := current.Epoch.Uint64()
			oldProtocol, _ := old.MinimumProtocol.Uint64()

			currentProtocol, _ := current.MinimumProtocol.Uint64()
			if oldEpoch > currentEpoch || oldProtocol > currentProtocol || old.ActivatedAt.After(current.ActivatedAt) || oldEpoch == currentEpoch && old.Digest != current.Digest {
				return managed.ErrWriterFloorCorrupt
			}

			result = old

			return nil
		}

		if !errors.Is(savedErr, managed.ErrNotFound) {
			return savedErr
		}

		if currentErr != nil {
			if request.Epoch != "1" || request.ExpectedDigest != "" {
				return managed.ErrUnavailable
			}
		} else {
			epoch, _ := current.Epoch.Uint64()
			nextEpoch, _ := request.Epoch.Uint64()
			protocol, _ := current.MinimumProtocol.Uint64()
			nextProtocol, _ := request.MinimumProtocol.Uint64()

			if epoch == math.MaxInt64 {
				return managed.ErrCapacity
			}

			if request.ExpectedDigest != current.Digest || nextEpoch != epoch+1 || nextProtocol < protocol {
				return managed.ErrConflict
			}
		}

		now, clockErr := tx.WriterFloorTime(ctx)
		if clockErr != nil {
			return clockErr
		}

		if currentErr == nil && now.Before(current.ActivatedAt) {
			return managed.ErrUnavailable
		}

		next, buildErr := managed.NewWriterFloor(request, now)
		if buildErr != nil {
			return buildErr
		}

		raw, encodeErr := managed.EncodeWriterFloor(next)
		if encodeErr != nil {
			return encodeErr
		}

		if currentErr != nil {
			err = tx.InstallInitialWriterFloor(ctx, raw)
		} else {
			err = tx.AdvanceWriterFloor(ctx, current.Digest, raw)
		}

		if err != nil {
			return err
		}

		result = next

		return nil
	})
	if err != nil {
		return managed.WriterFloor{}, err
	}

	return result, nil
}

// writerFloorNil refuses nil pointers hidden inside backend interfaces.
func writerFloorNil(value any) bool {
	if value == nil {
		return true
	}

	switch reflect.ValueOf(value).Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflect.ValueOf(value).IsNil()
	default:
		return false
	}
}
