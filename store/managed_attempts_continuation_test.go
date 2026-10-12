package store_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/store/postgres"

	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/store/memory"
)

func TestManagedRemovalContinuationCollisionAndConcurrency(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newRemovalFixture(t, backend)
			if _, err := f.s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
				t.Fatal(err)
			}

			base := continuationRequest(t, f)

			before, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
			if err != nil {
				t.Fatal(err)
			}

			for name, value := range map[string]id.ID{"attempt": f.key.AttemptID, "provider": f.provider, "action": f.s.action.ID, "registration": f.observations[0].Observation.Reference.RegistrationOperationID} {
				t.Run(name, func(t *testing.T) {
					q := base
					q.SuccessorAttemptID = value

					q.Action.CanonicalDigest, err = managed.RemovalContinuationDigest(q)
					if err != nil {
						if !errors.Is(err, managed.ErrInvalid) {
							t.Fatal(err)
						}

						return
					}

					got, e := f.s.BeginManagedRemovalContinuation(t.Context(), q)
					if e == nil || !reflect.DeepEqual(got, managed.ContinuationReceipt{}) {
						t.Fatal("retained ID collision granted", e)
					}

					after, e := f.s.ReadManagedTarget(t.Context(), f.key.Key)
					if e != nil || !reflect.DeepEqual(before, after) {
						t.Fatal("collision mutated target", e)
					}
				})
			}

			requests := []managed.ContinuationRequest{base, continuationRequest(t, f)}
			results := make([]managed.ContinuationReceipt, 2)
			errs := make([]error, 2)

			var group sync.WaitGroup
			for i := range requests {
				group.Go(func() {
					results[i], errs[i] = f.s.BeginManagedRemovalContinuation(t.Context(), requests[i])
				})
			}

			group.Wait()

			winners := 0

			for i, e := range errs {
				if e == nil {
					winners++

					replay, replayErr := f.s.BeginManagedRemovalContinuation(t.Context(), requests[i])
					if replayErr != nil || !reflect.DeepEqual(replay, results[i]) {
						t.Fatal("winner replay", replayErr)
					}
				} else if !errors.Is(e, managed.ErrConflict) || !reflect.DeepEqual(results[i], managed.ContinuationReceipt{}) {
					t.Fatal("losing allocation", e)
				}
			}

			after, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
			if err != nil || winners != 1 || after.Revision != before.Revision+1 {
				t.Fatal("concurrent allocation accounting", winners, err)
			}
		})
	}
}

func TestManagedRemovalContinuationNativeSourceBinding(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newRemovalFixture(t, backend)
			if _, err := f.s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
				t.Fatal(err)
			}

			observedBeforeContinue := quiescenceClock(t, f.s)
			continueRemovalFixture(t, f)
			rid := f.reservations[0]
			source := successorCommandProof(t, f, rid)
			validity := source.ValidUntil.Sub(source.ObservedAt)
			source.ObservedAt = observedBeforeContinue
			source.ValidUntil = observedBeforeContinue.Add(validity)
			raw := []byte("bound current native input")
			base := managed.AttemptCommandRequest{AttemptRequest: f.request(), ReservationID: rid, Kind: managed.AttemptBegin, ExpectedNativeVersion: source.BindingVersion, CurrentBinding: source, NativeCommand: managed.PortCommand{SchemaVersion: "1", OperationID: id.New(managed.PrefixOperation), RequestID: rid.String(), Original: raw, OriginalDigest: managed.Digest(raw), Native: raw, NativeDigest: managed.Digest(raw)}}

			before, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
			if err != nil {
				t.Fatal(err)
			}

			for name, change := range map[string]func(*managed.AttemptCommandRequest){
				"version": func(q *managed.AttemptCommandRequest) { q.ExpectedNativeVersion = "9" },
				"scope": func(q *managed.AttemptCommandRequest) {
					q.CurrentBinding.Key.AttemptID = id.New(managed.PrefixOperation)
				},
				"future": func(q *managed.AttemptCommandRequest) { q.CurrentBinding.ObservedAt = q.CurrentBinding.ValidUntil },
				"oversize": func(q *managed.AttemptCommandRequest) {
					q.CurrentBinding.Evidence = bytes.Repeat([]byte("x"), managed.MaxEvidence+1)
					q.CurrentBinding.EvidenceDigest = managed.Digest(q.CurrentBinding.Evidence)
				},
			} {
				t.Run(name, func(t *testing.T) {
					q := base
					proof := *source
					q.CurrentBinding = &proof
					change(&q)

					got, e := f.s.AuthorizeManagedAttemptCommand(t.Context(), q)
					if e == nil || !reflect.DeepEqual(got, managed.AttemptCommandGrant{}) {
						t.Fatal("invalid native source granted", e)
					}

					after, e := f.s.ReadManagedTarget(t.Context(), f.key.Key)
					if e != nil || !reflect.DeepEqual(before, after) {
						t.Fatal("invalid source mutated target", e)
					}
				})
			}

			got, err := f.s.AuthorizeManagedAttemptCommand(t.Context(), base)
			if err != nil || !got.NewlyIssued {
				t.Fatal("still-live pre-Continue source refused", err)
			}

			replay, err := f.s.AuthorizeManagedAttemptCommand(t.Context(), base)
			if err != nil || replay.NewlyIssued || !reflect.DeepEqual(got.Command, replay.Command) {
				t.Fatal("bound source replay", err)
			}
		})
	}
}

func TestManagedRemovalContinuationGenericRefusal(t *testing.T) {
	s := memory.New()
	q := controllerAction(t)

	q.Kind = managed.ActionContinue
	for _, advertised := range []string{"", "not-a-digest"} {
		q.CanonicalDigest = advertised

		receipt, err := s.SubmitManagedAction(t.Context(), q)
		if !errors.Is(err, managed.ErrUnsupported) || receipt != (managed.ActionReceipt{}) {
			t.Fatalf("generic Continue must refuse before digest or replay: receipt=%+v error=%v", receipt, err)
		}
	}
}

func TestManagedRemovalContinuationHistoricalSelection(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newRemovalFixture(t, backend)

			capability, ok := f.s.Authority.(managed.RemovalContinuationStore)
			if !ok {
				t.Fatal("missing typed removal continuation capability")
			}

			root, err := f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
			if err != nil {
				t.Fatal(err)
			}

			previous := root

			var (
				first managed.ContinuationRequest
				saved managed.ContinuationReceipt
			)

			for i := range 3 {
				request := managed.AttemptRequest{Command: quiescenceCommand(t, f.s, f.key.Key), AttemptID: previous.Descriptor.AttemptID, Generation: previous.Descriptor.Generation}
				if _, err = f.s.RevokeManagedRemovalAttempt(t.Context(), request); err != nil {
					t.Fatal(err)
				}

				action := f.s.action
				action.Command = quiescenceCommand(t, f.s, f.key.Key)
				action.Kind = managed.ActionContinue
				action.NewTarget = nil
				action.ReleaseID = id.Nil
				action.RollbackReleaseID = id.Nil
				action.ExpectedDesiredRevision = action.Revision // replaced with actual desired below

				target, e := f.s.ReadManagedTarget(t.Context(), f.key.Key)
				if e != nil {
					t.Fatal(e)
				}

				action.ExpectedDesiredRevision = target.DesiredRevision
				action.OriginalCommand = []byte("continue settled removal")
				action.OriginalDigest = managed.Digest(action.OriginalCommand)
				action.CanonicalDigest = ""
				q := managed.ContinuationRequest{Action: action, Predecessor: previous.State.AttemptRef, SuccessorAttemptID: id.New(managed.PrefixOperation), ProviderOperationID: id.New(managed.PrefixOperation)}

				q.Action.CanonicalDigest, err = managed.RemovalContinuationDigest(q)
				if err != nil {
					t.Fatal(err)
				}

				got, e := capability.BeginManagedRemovalContinuation(t.Context(), q)
				if e != nil {
					t.Fatal(e)
				}

				if got.Receipt.Revision != target.Revision+1 || got.ActionReceipt.DesiredRevision != target.DesiredRevision {
					t.Fatalf("unexpected acceptance accounting: %+v", got)
				}

				previous, err = f.s.ReadManagedRemovalAttempt(t.Context(), managed.AttemptKey{Key: f.key.Key, AttemptID: got.Successor.AttemptID, Generation: got.Successor.Generation})
				if err != nil {
					t.Fatal(err)
				}

				if previous.State.Revision != "1" {
					t.Fatalf("successor starts at %s", previous.State.Revision)
				}

				if i == 0 {
					first = q
					saved = got
				}
			}

			replay, err := capability.BeginManagedRemovalContinuation(t.Context(), first)
			if err != nil || !reflect.DeepEqual(replay, saved) {
				t.Fatalf("historical replay: %+v %v", replay, err)
			}

			record, err := capability.ReadManagedRemovalContinuation(t.Context(), managed.ContinuationKey{Key: f.key.Key, ActionID: first.Action.ID})
			if err != nil || !reflect.DeepEqual(record.Receipt, saved) {
				t.Fatalf("historical reader: %+v %v", record, err)
			}

			original, err := f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
			if err != nil || !reflect.DeepEqual(original.Descriptor, root.Descriptor) {
				t.Fatalf("original descriptor changed: %v", err)
			}
		})
	}
}

func continuationRequest(t *testing.T, f *removalFixture) managed.ContinuationRequest {
	t.Helper()

	previous, err := f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
	if err != nil {
		t.Fatal(err)
	}

	action := f.s.action
	action.Command = quiescenceCommand(t, f.s, f.key.Key)
	action.Kind = managed.ActionContinue
	action.NewTarget = nil
	action.ReleaseID = id.Nil
	action.RollbackReleaseID = id.Nil
	action.CanonicalDigest = ""

	target, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
	if err != nil {
		t.Fatal(err)
	}

	action.ExpectedDesiredRevision = target.DesiredRevision

	action.OriginalCommand = []byte("continue settled removal")
	if f.maximumEvidence {
		action.OriginalCommand = bytes.Repeat([]byte{0xff}, managed.MaxEvidence)
	}

	action.OriginalDigest = managed.Digest(action.OriginalCommand)
	q := managed.ContinuationRequest{Action: action, Predecessor: previous.State.AttemptRef, SuccessorAttemptID: id.New(managed.PrefixOperation), ProviderOperationID: id.New(managed.PrefixOperation)}

	q.Action.CanonicalDigest, err = managed.RemovalContinuationDigest(q)
	if err != nil {
		t.Fatal(err)
	}

	return q
}
func continueRemovalFixture(t *testing.T, f *removalFixture) managed.ContinuationReceipt {
	t.Helper()
	q := continuationRequest(t, f)

	receipt, err := f.s.BeginManagedRemovalContinuation(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}

	f.key = managed.AttemptKey{Key: f.key.Key, AttemptID: receipt.Successor.AttemptID, Generation: receipt.Successor.Generation}
	f.provider = receipt.ProviderOperationID
	f.s.action = q.Action

	return receipt
}

func successorCommandProof(t *testing.T, f *removalFixture, rid id.ID) *managed.AttemptCurrentBindingObservation {
	t.Helper()

	attempt, err := f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
	if err != nil {
		t.Fatal(err)
	}

	var observation managed.QuiescenceObservation

	for _, o := range f.observations {
		if o.Command.ReservationID == rid {
			observation = o.Observation
		}
	}

	reference := observation.Reference

	reg, err := f.s.ReadManagedOriginalLifecycle(t.Context(), managed.OriginalLifecycleKey{Key: f.key.Key, Kind: managed.OriginalRegistration, OperationID: reference.RegistrationOperationID})
	if err != nil {
		t.Fatal(err)
	}

	binding := managed.AttemptBindingReference{ReservationID: rid, ReservationVersion: reg.Material.Registration.BindingVersion, OriginalRegistrationOperationID: reference.RegistrationOperationID, OriginalRegistrationDigest: reference.RegistrationMaterialDigest, ProcessDigest: fixtureAttemptFact(t, "original_process", reg.Material.Registration.Process)}

	evidence := []byte("synthetic authenticated current binding")
	if f.maximumEvidence {
		evidence = bytes.Repeat([]byte{0xff}, managed.MaxEvidence)
	}

	now := quiescenceClock(t, f.s)

	return &managed.AttemptCurrentBindingObservation{SchemaVersion: "1", Key: managed.AttemptBindingKey{AttemptKey: f.key, ReservationID: rid}, DescriptorDigest: attempt.State.AttemptRef.DescriptorDigest, BindingReference: binding, BindingVersion: "10", RemovalEpoch: "4", State: "active", ObservedAt: now, ValidUntil: now.Add(time.Duration(mustContinuationDecimal(t, reg.Material.Registration.Process.MaximumProofValidityNS))), VerifierID: attempt.Descriptor.VerifierID, Evidence: evidence, EvidenceDigest: managed.Digest(evidence)}
}

func TestManagedRemovalContinuationTerminalAuthorization(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newRemovalFixture(t, backend)
			if _, err := f.s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
				t.Fatal(err)
			}

			continueRemovalFixture(t, f)
			batchAbort(t, f, true)

			if _, err := f.s.ReadManagedAttemptBinding(t.Context(), managed.AttemptBindingKey{AttemptKey: f.key, ReservationID: f.reservations[0]}); err != nil {
				t.Fatal(err)
			}

			continueRemovalFixture(t, f)
		})
	}
}

func TestManagedRemovalContinuationPositiveCompletion(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newRemovalFixture(t, backend)

			oldBatch := batchRequest(t, f)
			if _, err := f.s.Authority.(managed.QuiescenceBatchStore).AcceptManagedQuiescenceBatch(t.Context(), oldBatch); err != nil {
				t.Fatal(err)
			}

			if _, err := f.s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
				t.Fatal(err)
			}

			before, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
			if err != nil {
				t.Fatal(err)
			}

			continueRemovalFixture(t, f)

			if _, err = f.s.Authority.(managed.QuiescenceBatchStore).AcceptManagedQuiescenceBatch(t.Context(), batchRequest(t, f)); err != nil {
				t.Fatal(err)
			}

			batchRemoved(t, f, true)

			if _, err = f.s.TombstoneManagedRemovalAttempt(t.Context(), f.request()); err != nil {
				t.Fatal(err)
			}

			after, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
			if err != nil {
				t.Fatal(err)
			}

			terminal, readErr := f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
			if readErr != nil {
				t.Fatal(readErr)
			}

			if terminal.State.Revision != "15" || after.Revision-before.Revision != 18 {
				t.Fatalf("two-binding successor accounting: parent=%d state=%s", after.Revision-before.Revision, terminal.State.Revision)
			}

			t.Logf("measured successor: N=2 S=3 Continue_parent=1 successor_parent=14 successor_state=14 batch_parent=3 total_parent=18")

			if after.Phase != managed.Deleted || !reflect.DeepEqual(before.Reservations, after.Reservations) || !reflect.DeepEqual(before.Deletion, after.Deletion) {
				t.Fatal("successor rewrote original projection or failed terminal completion")
			}

			if _, err = f.s.Authority.(managed.QuiescenceBatchStore).AcceptManagedQuiescenceBatch(t.Context(), oldBatch); err != nil {
				t.Fatalf("old batch after pointer advance: %v", err)
			}
		})
	}
}

func mustContinuationDecimal(t *testing.T, d managed.Decimal) uint64 {
	t.Helper()

	n, e := d.Uint64()
	if e != nil {
		t.Fatal(e)
	}

	return n
}

type oldB23aProcess struct {
	allowIssue bool
	command    *exec.Cmd
	input      io.WriteCloser
	output     *bufio.Reader
	sequence   uint64
	closed     bool
}
type oldB23aResult struct {
	SchemaVersion   string `json:"schema_version"`
	Sequence        string `json:"sequence"`
	Operation       string `json:"operation"`
	Status          string `json:"status"`
	ErrorClass      string `json:"error_class"`
	ResultNonzero   bool   `json:"result_nonzero"`
	NewlyIssued     bool   `json:"newly_issued"`
	NewlyAuthorized bool   `json:"newly_authorized"`
	ResultDigest    string `json:"result_digest"`
}

func startOldB23a(t *testing.T, dsn string) *oldB23aProcess {
	t.Helper()

	binary := os.Getenv("CTRLPLANE_OLD_B23A_BINARY")
	if binary == "" {
		if os.Getenv("CTRLPLANE_OLD_B23A_REQUIRED") == "1" {
			t.Fatal("required old binary missing")
		}

		t.Skip("old binary not configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	t.Cleanup(cancel)

	command := exec.CommandContext(ctx, binary)
	command.Env = []string{"CTRLPLANE_OLD_B23A_DSN=" + dsn}

	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}

	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}

	if err = command.Start(); err != nil {
		t.Fatal("old binary could not start")
	}

	p := &oldB23aProcess{command: command, input: input, output: bufio.NewReaderSize(output, 8193)}

	t.Cleanup(func() { p.close(t) })

	return p
}
func (p *oldB23aProcess) close(t *testing.T) {
	t.Helper()

	if p.closed {
		return
	}

	p.closed = true
	if e := p.input.Close(); e != nil {
		t.Error(e)
	}

	if e := p.command.Wait(); e != nil {
		t.Error("old process failed", e)
	}
}
func (p *oldB23aProcess) call(t *testing.T, operation string, payload any) oldB23aResult {
	t.Helper()

	p.sequence++

	raw, e := json.Marshal(struct {
		SchemaVersion string `json:"schema_version"`
		Sequence      string `json:"sequence"`
		Operation     string `json:"operation"`
		Payload       any    `json:"payload"`
	}{"1", strconv.FormatUint(p.sequence, 10), operation, payload})
	if e != nil || len(raw)+1 > 65536 {
		t.Fatal("old request size", e)
	}

	if _, e = p.input.Write(append(raw, '\n')); e != nil {
		t.Fatal(e)
	}

	line, e := p.output.ReadSlice('\n')
	if e != nil || len(line) > 8192 {
		t.Fatal("old response bound", e)
	}

	var out oldB23aResult

	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()

	if e = dec.Decode(&out); e != nil {
		t.Fatal(e)
	}

	if out.SchemaVersion != "1" || out.Sequence != strconv.FormatUint(p.sequence, 10) || out.Operation != operation || (out.NewlyIssued || out.NewlyAuthorized) && !p.allowIssue {
		t.Fatalf("unexpected old result: %+v", out)
	}

	t.Logf("old operation=%s status=%s error_class=%s result_nonzero=%t", operation, out.Status, out.ErrorClass, out.ResultNonzero)

	return out
}
func oldB23aImmutable(t *testing.T, s *postgres.Store, p *oldB23aProcess, operation string, q any, want string) {
	t.Helper()
	before := attemptPostgresSnapshot(t, s)

	out := p.call(t, operation, q)
	if out.ErrorClass != want {
		t.Fatalf("%s error class=%q want=%q", operation, out.ErrorClass, want)
	}

	if !reflect.DeepEqual(before, attemptPostgresSnapshot(t, s)) {
		t.Fatal("old immutable call changed retained graph")
	}
}
func TestManagedAttemptPostgresOldB23aBinary(t *testing.T) {
	if os.Getenv("CTRLPLANE_OLD_B23A_BINARY") == "" || os.Getenv("CTRLPLANE_TEST_POSTGRES_DSN") == "" {
		if os.Getenv("CTRLPLANE_OLD_B23A_REQUIRED") == "1" {
			t.Fatal("required old binary/PG missing")
		}

		t.Skip("old binary/PG not configured")
	}

	identityRaw, e := exec.CommandContext(t.Context(), os.Getenv("CTRLPLANE_OLD_B23A_BINARY"), "identity").Output()
	if e != nil {
		t.Fatal(e)
	}

	var identity struct {
		Version  string `json:"version"`
		Sum      string `json:"sum"`
		Replaced bool   `json:"replaced"`
	}
	if e = json.Unmarshal(identityRaw, &identity); e != nil || identity.Version != "v1.7.1-0.20261011231032-8827a4f36fad" || identity.Sum != "h1:KKPnswqMXoylKncOYcW/PBx5xzi8i39xa4ql0wZU198=" || identity.Replaced {
		t.Fatal("wrong old binary identity", e)
	}

	for _, name := range []string{"gen1_control", "successor_refusal", "continue_envelopes", "legacy_refusal", "collision_no_change", "historical_nonissuing", "reopen_refusal", "journal_advance_limitation"} {
		t.Run(name, func(t *testing.T) {
			s, dsn := managedPG(t)
			f, _ := removalActionFixture(t, s, controllerAction(t), 1)
			p := startOldB23a(t, dsn)
			originalKey := f.key
			originalAction := f.s.action
			oldB23aImmutable(t, s, p, "read_attempt", originalKey, "")

			var savedPause managed.ActionSubmission
			if name == "historical_nonissuing" {
				savedPause = originalAction
				savedPause.Command = quiescenceCommand(t, s, f.key.Key)
				savedPause.Kind = managed.ActionPause
				savedPause.NewTarget = nil
				savedPause.ReleaseID = id.Nil
				savedPause.RollbackReleaseID = id.Nil

				target, err := s.ReadManagedTarget(t.Context(), f.key.Key)
				if err != nil {
					t.Fatal(err)
				}

				savedPause.ExpectedDesiredRevision = target.DesiredRevision

				savedPause.CanonicalDigest, e = managed.ActionDigest(savedPause)
				if e != nil {
					t.Fatal(e)
				}

				if _, err = s.SubmitManagedAction(t.Context(), savedPause); err != nil {
					t.Fatal(err)
				}
			}

			if name == "gen1_control" {
				oldB23aImmutable(t, s, p, "read_operation", managed.OperationKey{Key: f.key.Key, ActionID: originalAction.ID}, "")

				raw := []byte("old-valid-native-command")
				native := managed.AttemptCommandRequest{AttemptRequest: f.request(), ReservationID: f.reservations[0], Kind: managed.AttemptBegin, ExpectedNativeVersion: "1", NativeCommand: managed.PortCommand{SchemaVersion: "1", OperationID: id.New(managed.PrefixOperation), RequestID: "old-control", Original: raw, Native: raw, OriginalDigest: managed.Digest(raw), NativeDigest: managed.Digest(raw)}}
				p.allowIssue = true
				out := p.call(t, "authorize_attempt_command", native)
				p.allowIssue = false

				if out.Status != "ok" || !out.NewlyIssued {
					t.Fatal("old valid native control did not issue")
				}

				oldB23aImmutable(t, s, p, "authorize_attempt_command", native, "")

				return
			}

			if _, err := s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
				t.Fatal(err)
			}

			continueRemovalFixture(t, f)

			switch name {
			case "successor_refusal":
				oldB23aImmutable(t, s, p, "read_attempt", f.key, "unsupported")
				oldB23aImmutable(t, s, p, "read_attempt", originalKey, "unsupported")
				oldB23aImmutable(t, s, p, "admit_attempt", managed.AttemptAdmissionRequest{AttemptRequest: f.request(), VerifierID: "provider"}, "unsupported")
			case "continue_envelopes":
				oldB23aImmutable(t, s, p, "submit_action", f.s.action, "invalid")
				ordinary := f.s.action

				ordinary.CanonicalDigest, e = managed.ActionDigest(ordinary)
				if e != nil {
					t.Fatal(e)
				}

				oldB23aImmutable(t, s, p, "submit_action", ordinary, "conflict")
				ordinary.Command = quiescenceCommand(t, s, f.key.Key)

				ordinary.CanonicalDigest, e = managed.ActionDigest(ordinary)
				if e != nil {
					t.Fatal(e)
				}

				oldB23aImmutable(t, s, p, "submit_action", ordinary, "unsupported")
			case "legacy_refusal":
				oldB23aImmutable(t, s, p, "legacy_admit_removal", managed.AdmissionRequest{Command: quiescenceCommand(t, s, f.key.Key), OperationID: id.New(managed.PrefixOperation), Verifier: "provider"}, "unsupported")
				oldB23aImmutable(t, s, p, "legacy_issue_deletion", quiescenceCommand(t, s, f.key.Key), "unsupported")
				oldB23aImmutable(t, s, p, "begin_attempt", managed.AttemptStartRequest{Command: quiescenceCommand(t, s, f.key.Key), ActionID: f.s.action.ID, AttemptID: id.New(managed.PrefixOperation), ProviderOperationID: id.New(managed.PrefixOperation), VerifierID: "provider"}, "unsupported")
			case "collision_no_change":
				changed := f.s.action
				changed.OriginalCommand = []byte("changed")
				changed.OriginalDigest = managed.Digest(changed.OriginalCommand)

				changed.CanonicalDigest, e = managed.ActionDigest(changed)
				if e != nil {
					t.Fatal(e)
				}

				oldB23aImmutable(t, s, p, "submit_action", changed, "conflict")
			case "historical_nonissuing":
				oldB23aImmutable(t, s, p, "submit_action", savedPause, "")
				oldB23aImmutable(t, s, p, "read_target", f.key.Key, "")
				oldB23aImmutable(t, s, p, "read_operation", managed.OperationKey{Key: f.key.Key, ActionID: originalAction.ID}, "")
				oldB23aImmutable(t, s, p, "read_operation", managed.OperationKey{Key: f.key.Key, ActionID: f.s.action.ID}, "")
			case "reopen_refusal":
				oldB23aImmutable(t, s, p, "read_attempt", f.key, "unsupported")
				p.close(t)

				if _, err := s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
					t.Fatal(err)
				}

				continueRemovalFixture(t, f)
				reopened := startOldB23a(t, dsn)
				oldB23aImmutable(t, s, reopened, "read_attempt", f.key, "unsupported")
				oldB23aImmutable(t, s, reopened, "read_target", f.key.Key, "")
			case "journal_advance_limitation":
				before := attemptPostgresSnapshot(t, s)
				key := managed.OperationKey{Key: f.key.Key, ActionID: f.s.action.ID}

				out := p.call(t, "advance_operation", managed.OperationAdvance{Command: quiescenceCommand(t, s, f.key.Key), ActionID: key.ActionID, ExpectedOperationRevision: 1, Observation: managed.OperationObservation{Kind: "retry", ObservedAt: quiescenceClock(t, s), RetryAt: quiescenceClock(t, s).Add(time.Minute), Diagnostic: "isolated-old-journal"}})
				if out.Status != "ok" || reflect.DeepEqual(before, attemptPostgresSnapshot(t, s)) {
					t.Fatal("expected actual old journal mutation was not reproduced")
				}

				if _, err := s.ReadManagedOperation(t.Context(), key); !errors.Is(err, managed.ErrUnavailable) {
					t.Fatalf("new reader failed to detect association loss: %v", err)
				}

				t.Log("LIMITATION: valid-authority old journal advance committed and lost the successor association; fixture remains damaged until isolated database teardown")
			}
		})
	}
}

// Maximum qualification is opt-in so the mandatory hosted campaign is counted once.
func TestManagedRemovalContinuationFullFiniteSet(t *testing.T) {
	if os.Getenv("CTRLPLANE_MANAGED_MAXIMUM") != "successor" {
		t.Skip("named successor maximum campaign required")
	}

	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) { testContinuationMaximum(t, backend, backend == "postgres") })
	}
}
func testContinuationMaximum(t *testing.T, backend string, nearLimit bool) {
	t.Helper()

	started := time.Now()
	authority := controllerStore(t, backend)

	const nativeWindow = 30 * time.Minute

	f, _ := removalActionSetup(t, authority, largeRemovalAction(t), 2, true, nativeWindow)

	original, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
	if err != nil {
		t.Fatal(err)
	}
	// Close two generations without native or provider sends; the third does all work.
	for range 2 {
		if _, err = f.s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
			t.Fatal(err)
		}

		if f.key.Generation == "2" && nearLimit {
			pg := pgdriver.Unwrap(authority.(*postgres.Store).DB())
			// Admission reserves both terminal branches, plus finite optional renewals.
			beforeSeed, e := f.s.ReadManagedTarget(t.Context(), f.key.Key)
			if e != nil {
				t.Fatal(e)
			}

			beforeRaw, e := json.Marshal(beforeSeed)
			if e != nil {
				t.Fatal(e)
			}

			revision := int64(math.MaxInt64 - (490 + 65*34 + 162 + 2))
			if _, err = pg.Exec(t.Context(), "UPDATE cp_managed_targets SET revision=$1,data=jsonb_set(data,'{revision}',to_jsonb($1::bigint)) WHERE id=$2", revision, f.key.InstanceID.String()); err != nil {
				t.Fatal(err)
			}

			afterSeed, e := f.s.ReadManagedTarget(t.Context(), f.key.Key)
			if e != nil {
				t.Fatal(e)
			}

			afterRaw, e := json.Marshal(afterSeed)
			if e != nil {
				t.Fatal(e)
			}

			t.Logf("SYNTHETIC seed before_gen3_continue old_revision=%d new_revision=%d before_target_digest=%s after_target_digest=%s priorAPI=root_allocation+two_revocations+one_Continue", beforeSeed.Revision, afterSeed.Revision, managed.Digest(beforeRaw), managed.Digest(afterRaw))
		}

		continueRemovalFixture(t, f)
	}

	allocation, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
	if err != nil {
		t.Fatal(err)
	}

	var campaignCounter *attemptQueryCounter

	if backend == "postgres" {
		counted, counter := countedAttemptPostgres(t, authority.(*postgres.Store))
		f.s.Authority = counted
		campaignCounter = counter
	}

	renewals := newBatchRenewalBudget(t, true, "")
	renewLease := func() {
		target, e := f.s.ReadManagedTarget(t.Context(), f.key.Key)
		if e != nil {
			t.Fatal(e)
		}

		if target.LeaseUntil.Sub(quiescenceClock(t, f.s)) >= time.Minute {
			return
		}

		renewals.leaseAttempt(t)

		if _, e = f.s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: f.request().Command, Duration: managed.MaxLease}); e != nil {
			t.Fatal(e)
		}

		renewals.leases++
	}
	refresh := func() { renewals.batchAttempt(t); f.refreshObservationBatch(); renewals.batches++ }

	for _, rid := range f.reservations {
		renewLease()
		f.fence(rid)
	}

	renewLease()
	refresh()

	if _, err = f.s.AdmitManagedRemovalAttempt(t.Context(), managed.AttemptAdmissionRequest{AttemptRequest: f.request(), VerifierID: "provider"}); err != nil {
		t.Fatal(err)
	}

	minimum := time.Minute

	for n := range 33 {
		renewLease()

		now := quiescenceClock(t, f.s)

		margin := time.Minute
		for _, o := range f.observations {
			if remaining := o.Observation.ValidUntil.Sub(now); remaining < margin {
				margin = remaining
			}
		}

		if margin < 10*time.Second {
			refresh()
		}

		service := "service" + strconv.Itoa(n)
		kind := managed.StepRemoveContainer

		if n == 32 {
			service = ""
			kind = managed.StepRemoveNetwork
		}

		grant := f.step(service, kind)
		for _, o := range f.observations {
			if remaining := o.Observation.ValidUntil.Sub(grant.Permission.IssuedAt); remaining < minimum {
				minimum = remaining
			}
		}

		if minimum <= 0 {
			t.Fatal("issued after actual quiescence expiry")
		}

		if err = f.s.RecordManagedProviderStepResult(t.Context(), managed.ProviderStepResult{Permission: grant.Permission, Outcome: managed.PortTerminal, ResourceID: grant.Permission.ResourceID, Evidence: bytes.Repeat([]byte{0xff}, managed.MaxEvidence), ObservedAt: quiescenceClock(t, f.s)}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err = f.s.SettleManagedRemovalAttempt(t.Context(), managed.AttemptSettlementRequest{AttemptRequest: f.request(), Disposition: managed.Removed, ProducerProfile: "ctrlplane.managed.remove.v1", VerifierID: "provider", Evidence: []byte("all successor provider steps"), SettledAt: quiescenceClock(t, f.s)}); err != nil {
		t.Fatal(err)
	}

	for _, rid := range f.reservations {
		renewLease()

		grant := f.command(rid, managed.AttemptFinish)
		if _, err = f.s.RecordManagedAttemptFinish(t.Context(), managed.AttemptFinishRequest{AttemptRequest: f.request(), ReservationID: rid, Acceptance: f.acceptance(grant.Command)}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err = f.s.TombstoneManagedRemovalAttempt(t.Context(), f.request()); err != nil {
		t.Fatal(err)
	}

	terminal, err := f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
	if err != nil {
		t.Fatal(err)
	}

	target, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
	if err != nil {
		t.Fatal(err)
	}

	firstStep, err := f.s.ReadManagedProviderStep(t.Context(), managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: f.key.Key, ActionID: f.s.action.ID}, ProviderOperationID: f.provider, Kind: managed.StepRemoveContainer, Service: "service0"})
	if err != nil {
		t.Fatal(err)
	}

	history, err := f.s.Authority.(managed.HistoricalSettlementReader).ReadManagedHistoricalSettlement(t.Context(), managed.HistoricalSettlementRequest{Key: f.key.Key, SchemaVersion: "1", Installation: f.s.action.Installation, Authority: f.s.action.Authority, ManifestDigest: terminal.Descriptor.ManifestDigest, Seal: terminal.Descriptor.Seal, AttemptID: f.key.AttemptID, AttemptGeneration: f.key.Generation, ProviderOperationID: f.provider, FenceDigest: firstStep.Permission.FenceDigest})
	if err != nil {
		t.Fatal(err)
	}

	historyRaw, err := json.Marshal(history)
	if err != nil || history.Validate() != nil || len(history.Steps) != 33 || len(historyRaw) > managed.MaxAttemptSettlementResponse {
		t.Fatal("full successor historical settlement", err)
	}

	t.Logf("MEASURED historical settlement bytes=%d all33 provider evidence4096", len(historyRaw))
	renewals.complete(t)

	if terminal.State.Revision != "293" || target.Revision-allocation.Revision != uint64(292+65*renewals.batches+renewals.leases) || target.Phase != managed.Deleted {
		t.Fatal("successor transition accounting or terminal phase")
	}

	if !reflect.DeepEqual(target.Reservations, original.Reservations) || !reflect.DeepEqual(target.Deletion, original.Deletion) {
		t.Fatal("successor changed original A projection")
	}

	for _, slot := range terminal.State.BindingSlots {
		if slot != 1|4|8|32 {
			t.Fatal("incomplete binding slots")
		}
	}

	for _, slot := range terminal.State.ProviderSlots {
		if slot != 1|2 {
			t.Fatal("incomplete provider slots")
		}
	}

	raw, err := json.Marshal(terminal)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("MEASURED successor generation=3 N=64 S=33 attempt_delta=292 parent_after_allocation=%d B=%d L=%d U=0 reader_bytes=%d min_quiescence_margin=%s native_profile=%s elapsed=%s remaining_parent=%d", target.Revision-allocation.Revision, renewals.batches, renewals.leases, len(raw), minimum, nativeWindow, time.Since(started), uint64(math.MaxInt64)-target.Revision)

	if campaignCounter != nil {
		queriesBefore, bytesBefore := campaignCounter.queries.Load(), campaignCounter.bytes.Load()

		if _, e := f.s.ReadManagedRemovalAttempt(t.Context(), f.key); e != nil {
			t.Fatal(e)
		}

		t.Logf("MEASURED final held-view queries=%d response_bytes=%d campaign_queries=%d campaign_response_bytes=%d", campaignCounter.queries.Load()-queriesBefore, campaignCounter.bytes.Load()-bytesBefore, campaignCounter.queries.Load(), campaignCounter.bytes.Load())

		pg := pgdriver.Unwrap(authority.(*postgres.Store).DB())

		var sizes string
		if e := pg.NewRaw("SELECT jsonb_object_agg(kind,jsonb_build_object('rows',n,'total_bytes',total,'max_bytes',largest))::text FROM (SELECT kind,count(*) n,sum(octet_length(data)) total,max(octet_length(data)) largest FROM cp_managed_controller_records GROUP BY kind) s").Scan(t.Context(), &sizes); e != nil {
			t.Fatal(e)
		}

		t.Log("MEASURED retained rows " + sizes)
	}

	f.verifyBatchHistory()
}

func TestManagedRemovalContinuationNativeExpiry(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newRemovalFixture(t, backend)
			if _, err := f.s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
				t.Fatal(err)
			}

			continueRemovalFixture(t, f)

			var deadline time.Time

			for _, rid := range f.reservations {
				source := successorCommandProof(t, f, rid)

				source.ValidUntil = source.ObservedAt.Add(2 * time.Second)
				if deadline.IsZero() || source.ValidUntil.Before(deadline) {
					deadline = source.ValidUntil
				}

				raw := []byte("native expiry command")
				q := managed.AttemptCommandRequest{AttemptRequest: f.request(), ReservationID: rid, Kind: managed.AttemptBegin, ExpectedNativeVersion: source.BindingVersion, CurrentBinding: source, NativeCommand: managed.PortCommand{SchemaVersion: "1", OperationID: id.New(managed.PrefixOperation), RequestID: rid.String(), Original: raw, OriginalDigest: managed.Digest(raw), Native: raw, NativeDigest: managed.Digest(raw)}}

				grant, err := f.s.AuthorizeManagedAttemptCommand(t.Context(), q)
				if err != nil {
					t.Fatal(err)
				}

				fence := managed.Fence{Epoch: 1, Evidence: raw, Digest: managed.Digest(raw), ValidUntil: source.ObservedAt.Add(time.Minute), Survivors: []string{"off-instance"}}
				if _, err = f.s.RecordManagedAttemptFence(t.Context(), managed.AttemptFenceRequest{AttemptRequest: f.request(), ReservationID: rid, Acceptance: f.acceptance(grant.Command), Fence: managed.FenceWire(fence)}); err != nil {
					t.Fatal(err)
				}
			}

			timer := time.NewTimer(time.Until(deadline.Add(20 * time.Millisecond)))
			defer timer.Stop()

			select {
			case <-timer.C:
			case <-t.Context().Done():
				t.Fatal("expiry wait canceled")
			}
			// Fresh quiescence cannot retime the immutable native observation.
			f.refreshObservationBatch()

			before, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
			if err != nil {
				t.Fatal(err)
			}

			if _, err = f.s.AdmitManagedRemovalAttempt(t.Context(), managed.AttemptAdmissionRequest{AttemptRequest: f.request(), VerifierID: "provider"}); !errors.Is(err, managed.ErrBlocked) {
				t.Fatalf("expired native source admitted: %v", err)
			}

			after, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("expiry refusal changed parent", err)
			}
		})
	}
}

func TestManagedRemovalContinuationPostgresAtomicity(t *testing.T) {
	s, _ := managedPG(t)

	f, _ := removalActionFixture(t, s, controllerAction(t), 1)
	if _, err := s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
		t.Fatal(err)
	}

	q := continuationRequest(t, f)
	before := attemptPostgresSnapshot(t, s)
	pg := pgdriver.Unwrap(s.DB())

	for _, kind := range []string{"removal_attempt_state", "removal_identity", "removal_attempt", "removal_attempt_current", "removal_continuation", "removal_attempt_closure", "operation", "action", "audit", "removal_attempt_event", "@target", "@receipt"} {
		t.Run(kind, func(t *testing.T) {
			table := "cp_managed_controller_records"

			condition := "NEW.kind='" + kind + "'"
			if kind == "@target" {
				table = "cp_managed_targets"
				condition = "true"
			}

			if kind == "@receipt" {
				table = "cp_managed_commands"
				condition = "true"
			}

			if _, err := pg.Exec(t.Context(), "CREATE FUNCTION continuation_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF "+condition+" THEN RAISE EXCEPTION 'injected continuation failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER continuation_failure AFTER INSERT OR UPDATE ON "+table+" FOR EACH ROW EXECUTE FUNCTION continuation_failure()"); err != nil {
				t.Fatal(err)
			}

			defer func() {
				if _, err := pg.Exec(t.Context(), "DROP TRIGGER continuation_failure ON "+table+"; DROP FUNCTION continuation_failure()"); err != nil {
					t.Fatal(err)
				}
			}()

			out, err := s.BeginManagedRemovalContinuation(t.Context(), q)
			if err == nil || !strings.Contains(err.Error(), "injected continuation failure") || !reflect.DeepEqual(out, managed.ContinuationReceipt{}) {
				t.Fatal("missing exact injected rollback", err)
			}

			if !reflect.DeepEqual(before, attemptPostgresSnapshot(t, s)) {
				t.Fatal("partial continuation survived rollback")
			}
		})
	}

	if _, err := s.BeginManagedRemovalContinuation(t.Context(), q); err != nil {
		t.Fatal("same-ID retry after known rollback", err)
	}
}

func TestManagedRemovalContinuationPostgresProcessReplacement(t *testing.T) {
	stage := os.Getenv("CTRLPLANE_B23B_PROCESS_STAGE")
	if stage == "" {
		_, dsn := managedPG(t)
		state := filepath.Join(t.TempDir(), "continuation.json")

		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}

		for _, phase := range []string{"seed", "verify"} {
			command := exec.CommandContext(t.Context(), binary, "-test.run=^TestManagedRemovalContinuationPostgresProcessReplacement$", "-test.v")
			command.Env = []string{"CTRLPLANE_TEST_POSTGRES_DSN=" + dsn, "CTRLPLANE_B23B_PROCESS_STAGE=" + phase, "CTRLPLANE_B23B_PROCESS_STATE=" + state}

			output, e := command.CombinedOutput()
			if e != nil {
				t.Fatalf("independent %s failed: %v %s", phase, e, strings.ReplaceAll(string(output), dsn, "[private DSN]"))
			}

			t.Logf("independent %s process exited successfully", phase)
		}

		return
	}

	type retained struct {
		Request managed.ContinuationRequest `json:"request"`
		Receipt managed.ContinuationReceipt `json:"receipt"`
		Current managed.AttemptKey          `json:"current"`
	}

	s := openManagedPG(t, os.Getenv("CTRLPLANE_TEST_POSTGRES_DSN"))
	path := os.Getenv("CTRLPLANE_B23B_PROCESS_STATE")

	if stage == "seed" {
		f, _ := removalActionFixture(t, s, controllerAction(t), 1)
		if _, err := s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
			t.Fatal(err)
		}

		receipt := continueRemovalFixture(t, f)
		// Keep the exact accepted request, not the independently allocated helper IDs.
		record, err := s.ReadManagedRemovalContinuation(t.Context(), managed.ContinuationKey{Key: f.key.Key, ActionID: receipt.ActionReceipt.ID})
		if err != nil {
			t.Fatal(err)
		}

		q := record.Material.Request

		if _, err = s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
			t.Fatal(err)
		}

		continueRemovalFixture(t, f)

		raw, err := json.Marshal(retained{q, receipt, f.key})
		if err != nil {
			t.Fatal(err)
		}

		if err = os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}

		return
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var saved retained
	if err = json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}

	before := attemptPostgresSnapshot(t, s)

	out, err := s.BeginManagedRemovalContinuation(t.Context(), saved.Request)
	if err != nil || !reflect.DeepEqual(out, saved.Receipt) {
		t.Fatal("historical exact reconciliation after process replacement", err)
	}

	if _, err = s.ReadManagedRemovalAttempt(t.Context(), saved.Current); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(before, attemptPostgresSnapshot(t, s)) {
		t.Fatal("reopen replay changed graph")
	}
}

func TestManagedRemovalContinuationJointEvidence(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f, _ := removalActionSetup(t, controllerStore(t, backend), controllerAction(t), 1, true, 30*time.Minute)
			if _, err := f.s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
				t.Fatal(err)
			}

			continueRemovalFixture(t, f)
			batchRemoved(t, f, true)

			for _, rid := range f.reservations {
				binding, err := f.s.ReadManagedAttemptBinding(t.Context(), managed.AttemptBindingKey{AttemptKey: f.key, ReservationID: rid})
				if err != nil {
					t.Fatal(err)
				}

				if len(binding.CurrentBinding.Evidence) != 4096 || len(binding.Commands[0].NativeCommand.Original) != 4096 || len(binding.Fence.Receipt) != 4096 || len(binding.Finish.OriginalBindingProof) != 4096 {
					t.Fatal("joint evidence width not populated")
				}

				raw, err := json.Marshal(binding)
				if err != nil || len(raw) > managed.MaxAttemptBindingResponse {
					t.Fatal("full binding response", err)
				}

				t.Logf("MEASURED jointly populated binding bytes=%d source/original/native/receipt/proof raw_each=4096", len(raw))
			}
		})
	}
}

func TestManagedRemovalContinuationSingleBatchProjectedAbort(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newRemovalFixture(t, backend)
			batchAbort(t, f, false)
			rid := f.reservations[1]

			binding, err := f.s.ReadManagedAttemptBinding(t.Context(), managed.AttemptBindingKey{AttemptKey: f.key, ReservationID: rid})
			if err != nil {
				t.Fatal(err)
			}

			if _, err = f.s.RecordManagedAttemptBindingSettlement(t.Context(), managed.AttemptBindingSettlementRequest{AttemptRequest: f.request(), ReservationID: rid, FenceFactDigest: fixtureAttemptFact(t, "removal_fence", *binding.Fence)}); err != nil {
				t.Fatal(err)
			}

			grant := f.command(rid, managed.AttemptAbort)
			if _, err = f.s.RecordManagedAttemptAbort(t.Context(), managed.AttemptAbortRequest{AttemptRequest: f.request(), ReservationID: rid, Acceptance: f.acceptance(grant.Command), ProofVerifiedAt: quiescenceClock(t, f.s), ProofValidUntil: quiescenceClock(t, f.s).Add(time.Minute)}); err != nil {
				t.Fatal(err)
			}

			continueRemovalFixture(t, f)
			q := batchRequest(t, f)
			q.Entries = q.Entries[1:]

			before, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
			if err != nil {
				t.Fatal(err)
			}

			receipt, err := f.s.Authority.(managed.QuiescenceBatchStore).AcceptManagedQuiescenceBatch(t.Context(), q)
			if err != nil {
				t.Fatal("single batch classifies excluded sibling Abort", err)
			}

			revision, _ := receipt.Receipt.Revision.Uint64()
			if revision != before.Revision+2 {
				t.Fatal("size-one batch accounting")
			}

			individual := batchChildRequest(batchRequest(t, f), 0)
			if _, err = f.s.AcceptManagedQuiescence(t.Context(), individual); !errors.Is(err, managed.ErrBlocked) {
				t.Fatal("individual Abort behavior changed", err)
			}

			if backend == "postgres" {
				pg := f.s.Authority.(*postgres.Store)
				if _, err = pgdriver.Unwrap(pg.DB()).Exec(t.Context(), "UPDATE cp_managed_targets SET data=jsonb_set(data,'{reservations,0,abort,fence_digest}',to_jsonb($1::text)) WHERE id=$2", strings.Repeat("a", 64), f.key.InstanceID.String()); err != nil {
					t.Fatal(err)
				}

				q = batchRequest(t, f)
				q.Entries = q.Entries[1:]

				snapshot := attemptPostgresSnapshot(t, pg)
				if _, err = f.s.Authority.(managed.QuiescenceBatchStore).AcceptManagedQuiescenceBatch(t.Context(), q); err == nil {
					t.Fatal("unselected sibling's corrupt projected Abort ignored")
				}

				if !reflect.DeepEqual(snapshot, attemptPostgresSnapshot(t, pg)) {
					t.Fatal("corrupt sibling refusal changed graph")
				}
			}
		})
	}
}
