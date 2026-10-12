package store_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/store/internal/continuationtest"
	"github.com/xraph/ctrlplane/store/postgres"
)

func TestManagedRemovalContinuationRecoveryIdentity(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newRemovalFixture(t, backend)
			if _, err := f.s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
				t.Fatal(err)
			}

			continueRemovalFixture(t, f)
			grant := f.command(f.reservations[0], managed.AttemptBegin)

			raw, err := json.Marshal(grant.Command)
			if err != nil {
				t.Fatal(err)
			}

			q := managed.AttemptRecoveryRequest{AttemptRequest: f.request(), ReservationID: f.reservations[0], Kind: managed.AttemptBegin, CommandDigest: managed.Digest(append([]byte("ctrlplane.managed.attempt-command.v1\n"), raw...))}

			before, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
			if err != nil {
				t.Fatal(err)
			}

			for name, reserved := range map[string]id.ID{"attempt": f.key.AttemptID, "provider": f.provider} {
				t.Run(name, func(t *testing.T) {
					collision := q
					collision.ID = reserved

					got, e := f.s.AuthorizeManagedAttemptRecovery(t.Context(), collision)
					if e == nil || !reflect.DeepEqual(got, managed.AttemptRecoveryGrant{}) {
						t.Fatalf("reserved role became recovery: %+v %v", got, e)
					}

					after, e := f.s.ReadManagedTarget(t.Context(), f.key.Key)
					if e != nil || !reflect.DeepEqual(before, after) {
						t.Fatal("collision changed target", e)
					}
				})
			}

			got, err := f.s.AuthorizeManagedAttemptRecovery(t.Context(), q)
			if err != nil || !got.NewlyAuthorized {
				t.Fatal("fresh recovery", err)
			}

			replay, err := f.s.AuthorizeManagedAttemptRecovery(t.Context(), q)

			got.NewlyAuthorized = false
			if err != nil || !reflect.DeepEqual(got, replay) {
				t.Fatal("saved recovery not nonissuing", err)
			}
		})
	}
}

func TestManagedRemovalContinuationPostgresMissingHistoricalLink(t *testing.T) {
	f := newRemovalFixture(t, "postgres")

	var (
		selected managed.AttemptKey
		replay   managed.ContinuationRequest
	)

	for n := 2; n <= 5; n++ {
		if _, err := f.s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
			t.Fatal(err)
		}

		q := continuationRequest(t, f)

		got, err := f.s.BeginManagedRemovalContinuation(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}

		f.key = managed.AttemptKey{Key: f.key.Key, AttemptID: got.Successor.AttemptID, Generation: got.Successor.Generation}
		f.provider = got.ProviderOperationID

		f.s.action = q.Action
		if n == 3 {
			selected = f.key
			replay = q
		}
	}

	s := f.s.Authority.(*postgres.Store)

	pg := pgdriver.Unwrap(s.DB())
	if _, err := s.ReadManagedRemovalAttempt(t.Context(), selected); err != nil {
		t.Fatal("valid history", err)
	}

	var raw []byte
	if err := pg.NewRaw("SELECT data FROM cp_managed_controller_records WHERE kind='removal_attempt_state' AND key=$1", selected.AttemptID.String()).Scan(t.Context(), &raw); err != nil {
		t.Fatal(err)
	}

	var state managed.AttemptState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}

	state.Successor = nil
	state.ClosureDigest = ""

	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = pg.Exec(t.Context(), "UPDATE cp_managed_controller_records SET data=$1 WHERE kind='removal_attempt_state' AND key=$2", raw, selected.AttemptID.String()); err != nil {
		t.Fatal(err)
	}

	before := attemptPostgresSnapshot(t, s)
	if _, err := s.ReadManagedRemovalAttempt(t.Context(), selected); !errors.Is(err, managed.ErrUnavailable) {
		t.Error("missing selected closure accepted", err)
	}

	if got, err := s.BeginManagedRemovalContinuation(t.Context(), replay); !errors.Is(err, managed.ErrUnavailable) || !reflect.DeepEqual(got, managed.ContinuationReceipt{}) {
		t.Error("damaged historical replay accepted", err)
	}

	if !reflect.DeepEqual(before, attemptPostgresSnapshot(t, s)) {
		t.Fatal("damaged graph repaired")
	}
}

// continuationAckProxy injects one ambiguous transaction acknowledgement. In the
// absent case it substitutes ROLLBACK for COMMIT before hiding the server reply.
// This is a controlled protocol injection, not an uncontrolled network failure.
func continuationAckProxy(t *testing.T, dsn string, absent bool) (*postgres.Store, *atomic.Bool) {
	t.Helper()

	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Query().Get("sslmode") != "disable" {
		t.Fatal("private plaintext fixture required")
	}

	upstream := parsed.Host

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	var (
		armed   atomic.Bool
		workers sync.WaitGroup
		mu      sync.Mutex
	)

	connections := map[net.Conn]bool{}

	done := make(chan struct{})
	go func() {
		defer close(done)

		for {
			client, e := listener.Accept()
			if e != nil {
				return
			}

			mu.Lock()
			connections[client] = true
			mu.Unlock()
			workers.Go(func() {
				defer func() { _ = client.Close(); mu.Lock(); delete(connections, client); mu.Unlock() }()

				server, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(t.Context(), "tcp", upstream)
				if e != nil {
					return
				}

				defer func() { _ = server.Close() }()

				var hide atomic.Bool

				replies := make(chan struct{})
				go func() {
					defer close(replies)
					defer func() { _ = client.Close() }()

					for {
						frame, e := continuationFrame(server, false)
						if e != nil {
							return
						}

						if frame[0] == 'C' && hide.Load() && (bytes.Equal(frame[5:], []byte("COMMIT\x00")) || bytes.Equal(frame[5:], []byte("ROLLBACK\x00"))) {
							return
						}

						if _, e = client.Write(frame); e != nil {
							return
						}
					}
				}()

				defer func() { _ = server.Close(); <-replies }()

				startup := true
				for {
					frame, e := continuationFrame(client, startup)
					if e != nil {
						return
					}

					if !startup && (frame[0] == 'Q' || frame[0] == 'P') {
						offset := 5
						if frame[0] == 'P' {
							end := bytes.IndexByte(frame[offset:], 0)
							if end < 0 {
								return
							}

							offset += end + 1
						}

						end := bytes.IndexByte(frame[offset:], 0)
						if end >= 0 && strings.EqualFold(strings.TrimSpace(string(frame[offset:offset+end])), "commit") && armed.CompareAndSwap(true, false) {
							hide.Store(true)

							if absent {
								body := append([]byte{}, frame[:offset]...)
								body = append(body, []byte("rollback")...)
								body = append(body, frame[offset+end:]...)
								binary.BigEndian.PutUint32(body[1:5], uint32(len(body)-1))
								frame = body
							}
						}
					}

					if _, e = server.Write(frame); e != nil {
						return
					}

					startup = false
				}
			})
		}
	}()

	t.Cleanup(func() {
		_ = listener.Close()

		<-done
		mu.Lock()
		for c := range connections {
			_ = c.Close()
		}
		mu.Unlock()
		workers.Wait()
	})

	parsed.Host = listener.Addr().String()

	return openManagedPG(t, parsed.String()), &armed
}
func continuationFrame(r io.Reader, startup bool) ([]byte, error) {
	size := 5
	if startup {
		size = 4
	}

	head := make([]byte, size)
	if _, err := io.ReadFull(r, head); err != nil {
		return nil, err
	}

	n := int(binary.BigEndian.Uint32(head[size-4:]))
	if n < 4 || n > 16<<20 {
		return nil, managed.ErrInvalid
	}

	frame := make([]byte, size+n-4)
	copy(frame, head)
	_, err := io.ReadFull(r, frame[size:])

	return frame, err
}

func TestManagedRemovalContinuationPostgresUncertainReopen(t *testing.T) {
	stage := os.Getenv("CTRLPLANE_B23B_FIX_PROCESS_STAGE")
	if stage == "" {
		for _, mode := range []string{"persisted", "absent"} {
			t.Run(mode, func(t *testing.T) {
				_, dsn := managedPG(t)
				path := filepath.Join(t.TempDir(), "uncertain.json")

				binaryPath, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}

				for _, phase := range []string{"seed", "reconcile"} {
					command := exec.CommandContext(t.Context(), binaryPath, "-test.run=^TestManagedRemovalContinuationPostgresUncertainReopen$", "-test.v")
					command.Env = []string{"CTRLPLANE_TEST_POSTGRES_DSN=" + dsn, "CTRLPLANE_B23B_FIX_PROCESS_STAGE=" + phase, "CTRLPLANE_B23B_FIX_PROCESS_MODE=" + mode, "CTRLPLANE_B23B_FIX_PROCESS_STATE=" + path}

					output, e := command.CombinedOutput()
					if e != nil {
						t.Fatalf("independent %s/%s: %v %s", mode, phase, e, strings.ReplaceAll(string(output), dsn, "[private DSN]"))
					}

					t.Logf("%s fresh process %s passed", mode, phase)
				}
			})
		}

		return
	}

	dsn := os.Getenv("CTRLPLANE_TEST_POSTGRES_DSN")
	path := os.Getenv("CTRLPLANE_B23B_FIX_PROCESS_STATE")
	absent := os.Getenv("CTRLPLANE_B23B_FIX_PROCESS_MODE") == "absent"
	s := openManagedPG(t, dsn)

	var q managed.ContinuationRequest

	if stage == "seed" {
		f, _ := removalActionFixture(t, s, controllerAction(t), 1)
		if _, err := s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
			t.Fatal(err)
		}

		q = continuationRequest(t, f)
		before := attemptPostgresSnapshot(t, s)
		injected, armed := continuationAckProxy(t, dsn, absent)
		armed.Store(true)

		out, err := injected.BeginManagedRemovalContinuation(t.Context(), q)
		if err == nil || !reflect.DeepEqual(out, managed.ContinuationReceipt{}) || armed.Load() {
			t.Fatal("injection did not produce zero uncertain result", err)
		}

		if absent && !reflect.DeepEqual(before, attemptPostgresSnapshot(t, s)) {
			t.Fatal("rollback outcome retained partial graph")
		}

		raw, e := json.Marshal(q)
		if e != nil {
			t.Fatal(e)
		}

		if e = os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal(e)
		}

		t.Log("controlled withheld transaction acknowledgement returned error and zero receipt")

		return
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if err = json.Unmarshal(raw, &q); err != nil {
		t.Fatal(err)
	}

	k := managed.ContinuationKey{Key: q.Action.Key, ActionID: q.Action.ID}
	if absent {
		if _, err = s.ReadManagedRemovalContinuation(t.Context(), k); !errors.Is(err, managed.ErrNotFound) {
			t.Fatal("absence not authoritative", err)
		}

		if _, err = s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: quiescenceCommand(t, s, q.Action.Key), Duration: managed.MaxLease}); err != nil {
			t.Fatal(err)
		}

		before := attemptPostgresSnapshot(t, s)
		if out, e := s.BeginManagedRemovalContinuation(t.Context(), q); !errors.Is(e, managed.ErrConflict) || !reflect.DeepEqual(out, managed.ContinuationReceipt{}) {
			t.Fatal("stale absent retry authorized", e)
		}

		if !reflect.DeepEqual(before, attemptPostgresSnapshot(t, s)) {
			t.Fatal("stale retry mutated graph")
		}

		operation := q.Action.ID
		q.Action.Command = quiescenceCommand(t, s, q.Action.Key)
		q.Action.ID = operation
		q.Action.CanonicalDigest = ""

		q.Action.CanonicalDigest, err = managed.RemovalContinuationDigest(q)
		if err != nil {
			t.Fatal(err)
		}

		if _, err = s.BeginManagedRemovalContinuation(t.Context(), q); err != nil {
			t.Fatal("fresh same-ID absent retry", err)
		}
	}

	saved, err := s.ReadManagedRemovalContinuation(t.Context(), k)
	if err != nil {
		t.Fatal(err)
	}

	before := attemptPostgresSnapshot(t, s)

	out, err := s.BeginManagedRemovalContinuation(t.Context(), q)
	if err != nil || !reflect.DeepEqual(out, saved.Receipt) || !reflect.DeepEqual(before, attemptPostgresSnapshot(t, s)) {
		t.Fatal("persisted exact reconciliation", err)
	}

	f := &removalFixture{t: t, s: &quiescenceActionStore{Authority: s, t: t, action: q.Action}, key: managed.AttemptKey{Key: q.Action.Key, AttemptID: out.Successor.AttemptID, Generation: out.Successor.Generation}, provider: out.ProviderOperationID}
	if _, err = s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
		t.Fatal(err)
	}

	next := continueRemovalFixture(t, f)

	before = attemptPostgresSnapshot(t, s)
	if _, err = s.BeginManagedRemovalContinuation(t.Context(), q); err != nil {
		t.Fatal("historical replay", err)
	}

	current, err := s.ReadManagedRemovalAttempt(t.Context(), f.key)
	if err != nil || current.State.AttemptRef != next.Successor || !reflect.DeepEqual(before, attemptPostgresSnapshot(t, s)) {
		t.Fatal("historical replay moved pointer", err)
	}

	pg := pgdriver.Unwrap(s.DB())
	if _, err = pg.Exec(t.Context(), "DELETE FROM cp_managed_controller_records WHERE kind='removal_attempt_closure' AND key=$1", q.Predecessor.AttemptID.String()); err != nil {
		t.Fatal(err)
	}

	before = attemptPostgresSnapshot(t, s)
	if out, err = s.BeginManagedRemovalContinuation(t.Context(), q); !errors.Is(err, managed.ErrUnavailable) || !reflect.DeepEqual(out, managed.ContinuationReceipt{}) {
		t.Fatal("partial graph accepted", err)
	}

	if !reflect.DeepEqual(before, attemptPostgresSnapshot(t, s)) {
		t.Fatal("partial graph repaired")
	}
}

func TestManagedRemovalContinuationPostgresParentBoundary(t *testing.T) {
	s, _ := managedPG(t)

	f, _ := removalActionSetup(t, s, largeRemovalAction(t), 2, true, 30*time.Minute)
	if _, err := s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
		t.Fatal(err)
	}

	pg := pgdriver.Unwrap(s.DB())

	for _, headroom := range []int64{489, 490} {
		revision := int64(math.MaxInt64) - headroom
		if _, err := pg.Exec(t.Context(), "UPDATE cp_managed_targets SET revision=$1,data=jsonb_set(data,'{revision}',to_jsonb($1::bigint)) WHERE id=$2", revision, f.key.InstanceID.String()); err != nil {
			t.Fatal(err)
		}

		q := continuationRequest(t, f)
		before := attemptPostgresSnapshot(t, s)

		got, err := s.BeginManagedRemovalContinuation(t.Context(), q)
		if headroom == 489 {
			if !errors.Is(err, managed.ErrCapacity) || !reflect.DeepEqual(got, managed.ContinuationReceipt{}) || !reflect.DeepEqual(before, attemptPostgresSnapshot(t, s)) {
				t.Fatal("489 boundary mutated or accepted", err)
			}
		} else if err != nil || got.Receipt.Revision != uint64(revision+1) || got.Successor.Generation != "2" {
			t.Fatal("490 boundary refused", err)
		}

		t.Logf("synthetic parent seed=%d headroom=%d N=64 S=33 accepted=%t", revision, headroom, err == nil)
	}
}

func TestManagedRemovalContinuationPostgresCounterBounds(t *testing.T) {
	f := newRemovalFixture(t, "postgres")
	s := f.s.Authority.(*postgres.Store)

	pg := pgdriver.Unwrap(s.DB())
	if _, err := s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
		t.Fatal(err)
	}

	continueRemovalFixture(t, f)
	opKey := managed.OperationKey{Key: f.key.Key, ActionID: f.s.action.ID}

	op, err := s.ReadManagedOperation(t.Context(), opKey)
	if err != nil {
		t.Fatal(err)
	}

	op.Revision = math.MaxInt64 - 1

	raw, err := json.Marshal(op)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = pg.Exec(t.Context(), "UPDATE cp_managed_controller_records SET data=$1 WHERE kind='operation' AND key=$2", raw, opKey.ActionID.String()); err != nil {
		t.Fatal(err)
	}

	if _, err = s.ReadManagedOperation(t.Context(), opKey); err != nil {
		t.Fatal("seeded journal invalid", err)
	}

	q := managed.OperationAdvance{Command: quiescenceCommand(t, s, f.key.Key), ActionID: opKey.ActionID, ExpectedOperationRevision: op.Revision, Observation: managed.OperationObservation{Kind: "pending", ObservedAt: quiescenceClock(t, s)}}

	before := attemptPostgresSnapshot(t, s)
	if _, err = s.AdvanceManagedOperation(t.Context(), q); !errors.Is(err, managed.ErrCapacity) || !reflect.DeepEqual(before, attemptPostgresSnapshot(t, s)) {
		t.Fatal("journal consumed final terminal slot", err)
	}

	q.Observation.Kind = "completed"

	got, err := s.AdvanceManagedOperation(t.Context(), q)
	if err != nil || got.Revision != math.MaxInt64 {
		t.Fatal("journal final slot", err)
	}

	before = attemptPostgresSnapshot(t, s)

	replay, err := s.AdvanceManagedOperation(t.Context(), q)
	if err != nil || !reflect.DeepEqual(got, replay) || !reflect.DeepEqual(before, attemptPostgresSnapshot(t, s)) {
		t.Fatal("final journal replay", err)
	}

	q.Command = quiescenceCommand(t, s, f.key.Key)

	q.ExpectedOperationRevision = math.MaxInt64
	if _, err = s.AdvanceManagedOperation(t.Context(), q); !errors.Is(err, managed.ErrCapacity) || !reflect.DeepEqual(before, attemptPostgresSnapshot(t, s)) {
		t.Fatal("journal overflow", err)
	}

	if _, err = s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
		t.Fatal(err)
	}

	qContinue := continuationRequest(t, f)

	var state managed.AttemptState

	if err = pg.NewRaw("SELECT data FROM cp_managed_controller_records WHERE kind='removal_attempt_state' AND key=$1", f.key.AttemptID.String()).Scan(t.Context(), &raw); err != nil {
		t.Fatal(err)
	}

	if err = json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}

	state.Revision = managed.Decimal(strconv.FormatInt(math.MaxInt64, 10))

	raw, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = pg.Exec(t.Context(), "UPDATE cp_managed_controller_records SET data=$1 WHERE kind='removal_attempt_state' AND key=$2", raw, f.key.AttemptID.String()); err != nil {
		t.Fatal(err)
	}

	if _, err = s.ReadManagedRemovalAttempt(t.Context(), f.key); err != nil {
		t.Fatal("seeded attempt invalid", err)
	}

	before = attemptPostgresSnapshot(t, s)
	if _, err = s.BeginManagedRemovalContinuation(t.Context(), qContinue); !errors.Is(err, managed.ErrCapacity) || !reflect.DeepEqual(before, attemptPostgresSnapshot(t, s)) {
		t.Fatal("attempt revision overflow", err)
	}

	t.Log("synthetic journal M-1 reserves terminal; completed reaches M; attempt state M refuses Continue")
}

func continuationPGRows(t *testing.T, s *postgres.Store, key managed.Key) continuationtest.Storage {
	t.Helper()

	pg := pgdriver.Unwrap(s.DB())

	return continuationtest.Storage{Rows: func() []continuationtest.Row {
		var rows []struct {
			Kind  string `grove:"kind"`
			Scope string `grove:"scope"`
			Key   string `grove:"key"`
			Data  []byte `grove:"data"`
		}
		if err := pg.NewRaw("SELECT kind,scope,key,data FROM cp_managed_controller_records ORDER BY kind,scope,key").Scan(t.Context(), &rows); err != nil {
			t.Fatal(err)
		}

		out := make([]continuationtest.Row, 0, len(rows))
		for _, r := range rows {
			out = append(out, continuationtest.Row{Kind: r.Kind, Scope: r.Scope, Key: r.Key, Data: r.Data})
		}

		return out
	}, Put: func(r continuationtest.Row) {
		if _, err := pg.Exec(t.Context(), "INSERT INTO cp_managed_controller_records(kind,scope,key,data) VALUES($1,$2,$3,$4) ON CONFLICT(kind,scope,key) DO UPDATE SET data=EXCLUDED.data", r.Kind, r.Scope, r.Key, r.Data); err != nil {
			t.Fatal(err)
		}
	}, Receipt: func(r managed.Receipt) {
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}

		if _, err = pg.Exec(t.Context(), "INSERT INTO cp_managed_commands(target_id,operation_id,receipt) VALUES($1,$2,$3::jsonb) ON CONFLICT(target_id,operation_id) DO UPDATE SET receipt=EXCLUDED.receipt", key.InstanceID.String(), r.ID.String(), string(raw)); err != nil {
			t.Fatal(err)
		}
	}}
}
func TestManagedRemovalContinuationPostgresGenerationBoundary(t *testing.T) {
	f := newRemovalFixture(t, "postgres")
	s := f.s.Authority.(*postgres.Store)

	root := f.key
	for range 4 {
		if _, err := s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
			t.Fatal(err)
		}

		continueRemovalFixture(t, f)
	}

	if _, err := s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
		t.Fatal(err)
	}

	target, readErr := s.ReadManagedTarget(t.Context(), f.key.Key)
	if readErr != nil {
		t.Fatal(readErr)
	}

	f.key = continuationtest.LateGeneration(t, continuationPGRows(t, s, f.key.Key), f.key, target)
	if _, err := s.ReadManagedRemovalAttempt(t.Context(), f.key); err != nil {
		t.Fatal("seeded current validation", err)
	}

	if _, err := s.ReadManagedRemovalAttempt(t.Context(), root); err != nil {
		t.Fatal("seeded historical root validation", err)
	}

	got := continueRemovalFixture(t, f)
	if got.Successor.Generation != managed.Decimal(strconv.FormatInt(math.MaxInt64, 10)) {
		t.Fatal("final generation")
	}

	if _, err := s.ReadManagedRemovalAttempt(t.Context(), f.key); err != nil {
		t.Fatal("final current validation", err)
	}

	q := managed.ContinuationRequest{Action: f.s.action, Predecessor: got.Successor, SuccessorAttemptID: id.New(managed.PrefixOperation), ProviderOperationID: id.New(managed.PrefixOperation)}
	q.Action.Command = quiescenceCommand(t, s, f.key.Key)
	q.Action.CanonicalDigest = ""

	before := attemptPostgresSnapshot(t, s)
	if _, err := s.BeginManagedRemovalContinuation(t.Context(), q); !errors.Is(err, managed.ErrInvalid) || !reflect.DeepEqual(before, attemptPostgresSnapshot(t, s)) {
		t.Fatal("generation/pointer M overflow", err)
	}

	t.Log("generation and pointer M-1 accepted final M; next request rejected by digest generation bound before writes")
}

func TestManagedRemovalContinuationPostgresExpiredFinish(t *testing.T) {
	f := newRemovalFixture(t, "postgres")

	s := f.s.Authority.(*postgres.Store)
	if _, err := s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
		t.Fatal(err)
	}

	continueRemovalFixture(t, f)

	observations := make([]managed.QuiescenceObservation, len(f.observations))
	for i := range f.observations {
		observations[i] = f.observations[i].Observation
	}

	current, err := s.ReadManagedRemovalAttempt(t.Context(), f.key)
	if err != nil {
		t.Fatal(err)
	}

	if observations[0].Reference.ReservationID == current.Descriptor.ReservationIDs[0] {
		observations[0], observations[1] = observations[1], observations[0]
	}

	t.Log("supplied observations in reverse descriptor order")
	continuationtest.ExpiredFinish(t, s, continuationPGRows(t, s, f.key.Key), f.key, observations, func() time.Time { return quiescenceClock(t, s) })
}

func TestManagedRemovalContinuationPostgresStorageCaps(t *testing.T) {
	f := newRemovalFixture(t, "postgres")
	s := f.s.Authority.(*postgres.Store)

	pg := pgdriver.Unwrap(s.DB())
	if _, err := s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
		t.Fatal(err)
	}

	continueRemovalFixture(t, f)
	f.command(f.reservations[0], managed.AttemptBegin)

	rows := continuationPGRows(t, s, f.key.Key).Rows()
	for _, entry := range []struct {
		kind  string
		limit int
	}{{"removal_continuation", managed.MaxRemovalContinuationMaterial}, {"removal_attempt_closure", managed.MaxRemovalContinuationClosure}, {"removal_current_binding", managed.MaxAttemptCurrentBinding}} {
		t.Run(entry.kind, func(t *testing.T) {
			var row continuationtest.Row

			for _, r := range rows {
				if r.Kind == entry.kind {
					row = r

					break
				}
			}

			if len(row.Data) == 0 {
				t.Fatal("missing row")
			}

			raw := append(append([]byte{}, row.Data...), bytes.Repeat([]byte(" "), entry.limit+1-len(row.Data))...)

			before := attemptPostgresSnapshot(t, s)
			if _, err := pg.Exec(t.Context(), "UPDATE cp_managed_controller_records SET data=$1 WHERE kind=$2 AND scope=$3 AND key=$4", raw, row.Kind, row.Scope, row.Key); err == nil {
				t.Fatal("database cap+1 accepted")
			}

			if !reflect.DeepEqual(before, attemptPostgresSnapshot(t, s)) {
				t.Fatal("constraint refusal changed rows")
			}

			t.Logf("encoded bytes=%d cap=%d; database octet constraint refused before storage", len(raw), entry.limit)
		})
	}
}
func TestManagedRemovalContinuationInputCaps(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newRemovalFixture(t, backend)
			if _, err := f.s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
				t.Fatal(err)
			}

			continueRemovalFixture(t, f)
			rid := f.reservations[0]
			raw := []byte("native")
			base := managed.AttemptCommandRequest{AttemptRequest: f.request(), ReservationID: rid, Kind: managed.AttemptBegin, ExpectedNativeVersion: "10", NativeCommand: managed.PortCommand{SchemaVersion: "1", OperationID: id.New(managed.PrefixOperation), RequestID: "input-cap", Original: raw, OriginalDigest: managed.Digest(raw), Native: raw, NativeDigest: managed.Digest(raw)}}

			for _, size := range []int{4097, managed.MaxAttemptCurrentBinding + 1, managed.MaxSuccessorCommandRequest + 1} {
				t.Run(strconv.Itoa(size), func(t *testing.T) {
					q := base
					q.CurrentBinding = successorCommandProof(t, f, rid)
					q.CurrentBinding.Evidence = bytes.Repeat([]byte("x"), size)
					q.CurrentBinding.EvidenceDigest = managed.Digest(q.CurrentBinding.Evidence)

					if size == managed.MaxSuccessorCommandRequest+1 {
						q.CurrentBinding.Evidence = []byte("proof")
						q.CurrentBinding.EvidenceDigest = managed.Digest(q.CurrentBinding.Evidence)

						initial, marshalErr := json.Marshal(q)
						if marshalErr != nil {
							t.Fatal(marshalErr)
						}

						q.CurrentBinding.VerifierID += strings.Repeat("x", managed.MaxSuccessorCommandRequest+1-len(initial))
					}

					encoded, e := json.Marshal(q)
					if e != nil {
						t.Fatal(e)
					}

					before, e := f.s.ReadManagedTarget(t.Context(), f.key.Key)
					if e != nil {
						t.Fatal(e)
					}

					out, e := f.s.AuthorizeManagedAttemptCommand(t.Context(), q)
					if e == nil || !reflect.DeepEqual(out, managed.AttemptCommandGrant{}) {
						t.Fatal("oversized source granted", e)
					}

					after, readErr := f.s.ReadManagedTarget(t.Context(), f.key.Key)
					if readErr != nil || !reflect.DeepEqual(before, after) {
						t.Fatal("refusal mutation", readErr)
					}

					t.Logf("evidence bytes=%d combined encoded=%d; refused=%v (evidence semantic bound precedes CB byte guard; combined request guard first above32768)", len(q.CurrentBinding.Evidence), len(encoded), e)
				})
			}
		})
	}
}
