package managedstate

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

// floorFixture is a transactional storage witness, not a production Memory store.
type floorFixture struct {
	current []byte
	history map[id.ID][]byte
	now     time.Time
	failAt  int
	writes  int
	readErr error
}

func newFloorFixture() *floorFixture {
	return &floorFixture{history: map[id.ID][]byte{}, now: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)}
}

func (f *floorFixture) ReadCurrentWriterFloor(context.Context) ([]byte, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}

	if f.current == nil {
		return nil, managed.ErrNotFound
	}

	return bytes.Clone(f.current), nil
}

func (f *floorFixture) ReadWriterFloorActivation(_ context.Context, activation id.ID) ([]byte, error) {
	raw, ok := f.history[activation]
	if !ok {
		return nil, managed.ErrNotFound
	}

	return bytes.Clone(raw), nil
}

func (f *floorFixture) WriterFloorSnapshot(ctx context.Context, fn func(WriterFloorSnapshot) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	return fn(f)
}

func (f *floorFixture) WriterFloorTime(context.Context) (time.Time, error) { return f.now, nil }

func (f *floorFixture) WriterFloorAdminAtomic(ctx context.Context, fn func(WriterFloorAdminTransaction) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	clone := *f
	clone.current = bytes.Clone(f.current)

	clone.history = make(map[id.ID][]byte, len(f.history))
	for key, raw := range f.history {
		clone.history[key] = bytes.Clone(raw)
	}

	if err := fn(&clone); err != nil {
		return err
	}

	*f = clone

	return nil
}

func (f *floorFixture) insert(raw []byte) error {
	value, err := managed.DecodeWriterFloor(raw)
	if err != nil {
		return err
	}

	if _, exists := f.history[value.ActivationID]; exists {
		return managed.ErrConflict
	}
	// A real backend uses a unique (authority, epoch) index, not this fixture loop.
	for _, existing := range f.history {
		old, decodeErr := managed.DecodeWriterFloor(existing)
		if decodeErr != nil {
			return managed.ErrWriterFloorCorrupt
		}

		if old.Epoch == value.Epoch {
			return managed.ErrConflict
		}
	}

	f.writes++
	if f.failAt == f.writes {
		return managed.ErrUnavailable
	}

	f.history[value.ActivationID] = bytes.Clone(raw)

	f.writes++
	if f.failAt == f.writes {
		return managed.ErrUnavailable
	}

	f.current = bytes.Clone(raw)

	return nil
}

func (f *floorFixture) InstallInitialWriterFloor(_ context.Context, raw []byte) error {
	if f.current != nil || len(f.history) != 0 {
		return managed.ErrWriterFloorCorrupt
	}

	return f.insert(raw)
}

func (f *floorFixture) AdvanceWriterFloor(_ context.Context, expected string, raw []byte) error {
	current, err := managed.DecodeWriterFloor(f.current)
	if err != nil {
		return err
	}

	if current.Digest != expected {
		return managed.ErrConflict
	}

	return f.insert(raw)
}

func floorRequest() managed.WriterFloorActivation {
	return managed.WriterFloorActivation{Schema: managed.WriterFloorSchema, AuthorityID: "authority-one", MinimumProtocol: "1", Epoch: "1", ActivationID: id.New(managed.PrefixOperation)}
}

func activateFloor(t *testing.T, f *floorFixture, q managed.WriterFloorActivation) managed.WriterFloor {
	t.Helper()

	value, err := ActivateWriterFloor(t.Context(), f, "authority-one", q)
	if err != nil {
		t.Fatal(err)
	}

	return value
}

func nextFloorRequest(floor managed.WriterFloor) managed.WriterFloorActivation {
	epoch, _ := floor.Epoch.Uint64()
	q := floor.Activation()
	q.ActivationID = id.New(managed.PrefixOperation)
	q.Epoch = managed.Decimal(strconv.FormatUint(epoch+1, 10))
	q.ExpectedDigest = floor.Digest

	return q
}

func TestWriterFloorActivationReplayAndMonotonicity(t *testing.T) {
	f := newFloorFixture()
	firstRequest := floorRequest()
	first := activateFloor(t, f, firstRequest)
	secondRequest := nextFloorRequest(first)
	secondRequest.MinimumProtocol = "2"
	f.now = f.now.Add(time.Second)
	second := activateFloor(t, f, secondRequest)
	writes := f.writes
	// Historical replay deliberately ignores the new callback clock and never moves current.
	f.now = time.Time{}

	got, err := ActivateWriterFloor(t.Context(), f, "authority-one", firstRequest)
	if err != nil || got != first || f.writes != writes {
		t.Fatal("historical activation replay", err)
	}

	reader, err := NewWriterFloorReader(f, "authority-one")
	if err != nil {
		t.Fatal(err)
	}

	current, err := reader.ReadManagedWriterFloor(t.Context())
	if err != nil || current != second {
		t.Fatal("replay rewound current", err)
	}

	historical, err := reader.ReadManagedWriterFloorActivation(t.Context(), first.Reference())
	if err != nil || historical != first {
		t.Fatal("exact history changed", err)
	}

	for name, q := range map[string]managed.WriterFloorActivation{
		"stale_cas": nextFloorRequest(first),
		"decrease_floor": func() managed.WriterFloorActivation {
			q := nextFloorRequest(second)
			q.MinimumProtocol = "1"

			return q
		}(),
		"skip_epoch": func() managed.WriterFloorActivation {
			q := nextFloorRequest(second)
			q.Epoch = "4"

			return q
		}(),
		"conflicting_replay": func() managed.WriterFloorActivation {
			q := firstRequest
			q.MinimumProtocol = "2"

			return q
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ActivateWriterFloor(t.Context(), f, "authority-one", q); !errors.Is(err, managed.ErrConflict) {
				t.Fatal(err)
			}

			if f.writes != writes {
				t.Fatal("refusal wrote state")
			}
		})
	}
}

func TestWriterFloorWriteBoundariesRollback(t *testing.T) {
	for _, initial := range []bool{true, false} {
		for boundary := 1; boundary <= 2; boundary++ {
			f := newFloorFixture()

			q := floorRequest()
			if !initial {
				q = nextFloorRequest(activateFloor(t, f, q))
			}

			before := bytes.Clone(f.current)
			count, writes := len(f.history), f.writes

			f.failAt = writes + boundary
			if _, err := ActivateWriterFloor(t.Context(), f, "authority-one", q); !errors.Is(err, managed.ErrUnavailable) {
				t.Fatal("injection missed", err)
			}

			if !bytes.Equal(before, f.current) || len(f.history) != count || f.writes != writes {
				t.Fatal("partial activation survived")
			}

			f.failAt = 0
			activateFloor(t, f, q)
		}
	}
}

func TestWriterFloorCorruptionAndMissingStateRefuse(t *testing.T) {
	for _, damage := range []string{"head_missing", "head_corrupt", "history_missing", "history_corrupt"} {
		t.Run(damage, func(t *testing.T) {
			f := newFloorFixture()
			floor := activateFloor(t, f, floorRequest())

			switch damage {
			case "head_missing":
				f.current = nil
			case "head_corrupt":
				f.current = []byte("corrupt")
			case "history_missing":
				delete(f.history, floor.ActivationID)
			case "history_corrupt":
				f.history[floor.ActivationID] = []byte("corrupt")
			}

			if err := CheckWriterFloorMutation(t.Context(), f, "authority-one"); err == nil {
				t.Fatal("damaged marker authorized mutation")
			}

			if _, err := ActivateWriterFloor(t.Context(), f, "authority-one", floor.Activation()); err == nil {
				t.Fatal("replay repaired corruption")
			}

			if _, err := ActivateWriterFloor(t.Context(), f, "authority-one", floorRequest()); err == nil {
				t.Fatal("new identity hid corruption")
			}
		})
	}
}

func TestWriterFloorOutageAndUncertifiedWriterRefuse(t *testing.T) {
	f := newFloorFixture()
	if err := CheckWriterFloorMutation(t.Context(), f, "authority-one"); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("absent floor allowed", err)
	}

	f.readErr = managed.ErrUnavailable
	if _, err := ActivateWriterFloor(t.Context(), f, "authority-one", floorRequest()); !errors.Is(err, managed.ErrUnavailable) || f.writes != 0 {
		t.Fatal("outage treated as initial installation", err)
	}

	f.readErr = nil
	request := floorRequest()
	request.MinimumProtocol = "2"
	activateFloor(t, f, request)

	if err := CheckWriterFloorMutation(t.Context(), f, "authority-one"); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal("above-ceiling floor allowed mutation", err)
	}

	if err := CheckWriterFloorMutation(t.Context(), f, "wrong-authority"); !errors.Is(err, managed.ErrWriterFloorCorrupt) {
		t.Fatal("authority mismatch", err)
	}
}

func TestWriterFloorExhaustionAndStoredRollback(t *testing.T) {
	f := newFloorFixture()
	first := activateFloor(t, f, floorRequest())
	second := activateFloor(t, f, nextFloorRequest(first))
	// An exact replay of known newer history detects a rolled-back current row.
	f.current = bytes.Clone(f.history[first.ActivationID])
	if _, err := ActivateWriterFloor(t.Context(), f, "authority-one", second.Activation()); !errors.Is(err, managed.ErrWriterFloorCorrupt) {
		t.Fatal("current rollback accepted", err)
	}

	if _, err := ActivateWriterFloor(t.Context(), f, "authority-one", nextFloorRequest(first)); !errors.Is(err, managed.ErrConflict) {
		t.Fatal("rollback reused an epoch with a different activation", err)
	}

	if err := managed.MatchWriterFloor(first, second.Reference()); !errors.Is(err, managed.ErrAuthority) {
		t.Fatal("restore disagreement ignored", err)
	}

	q := nextFloorRequest(second)
	q.Epoch = "9223372036854775807"

	limit, err := managed.NewWriterFloor(q, f.now)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := managed.EncodeWriterFloor(limit)
	if err != nil {
		t.Fatal(err)
	}
	// Seed the terminal epoch directly to inspect arithmetic without billions of activations.
	f.current, f.history[limit.ActivationID] = bytes.Clone(raw), bytes.Clone(raw)
	request := q
	request.ActivationID = id.New(managed.PrefixOperation)

	request.ExpectedDigest = limit.Digest
	if _, err = ActivateWriterFloor(t.Context(), f, "authority-one", request); !errors.Is(err, managed.ErrCapacity) {
		t.Fatal("counter exhaustion wrapped", err)
	}
}

func TestWriterFloorTypedNilRefused(t *testing.T) {
	var backend *floorFixture
	if _, err := NewWriterFloorReader(backend, "authority-one"); !errors.Is(err, managed.ErrInvalid) {
		t.Fatal(err)
	}

	if _, err := ActivateWriterFloor(t.Context(), backend, "authority-one", floorRequest()); !errors.Is(err, managed.ErrAuthority) {
		t.Fatal(err)
	}

	if err := CheckWriterFloorMutation(t.Context(), backend, "authority-one"); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal(err)
	}
}

func TestWriterFloorProtocolOneRevalidatesHistoryAndCeiling(t *testing.T) {
	f := newFloorFixture()

	first := activateFloor(t, f, floorRequest())
	if err := CheckWriterFloorMutation(t.Context(), f, "authority-one"); err != nil {
		t.Fatal("compiled protocol-one refused", err)
	}

	saved := bytes.Clone(f.history[first.ActivationID])

	f.history[first.ActivationID] = []byte("corrupt")
	if err := CheckWriterFloorMutation(t.Context(), f, "authority-one"); !errors.Is(err, managed.ErrWriterFloorCorrupt) {
		t.Fatal("positive decision cached past corrupt history", err)
	}

	f.history[first.ActivationID] = saved
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := CheckWriterFloorMutation(ctx, f, "authority-one"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled check admitted", err)
	}

	next := nextFloorRequest(first)
	next.MinimumProtocol = "2"
	activateFloor(t, f, next)

	if err := CheckWriterFloorMutation(t.Context(), f, "authority-one"); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal("above-ceiling floor admitted", err)
	}
}
