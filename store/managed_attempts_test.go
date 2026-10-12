package store_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/managedservice"
	"github.com/xraph/ctrlplane/store/postgres"
)

type removalFixture struct {
	maximumEvidence bool
	t               *testing.T
	s               *quiescenceActionStore
	key             managed.AttemptKey
	provider        id.ID
	reservations    []id.ID
	observations    []managed.QuiescenceAcceptanceRequest
	batches         []managed.QuiescenceBatchAcceptanceRequest
}

func newRemovalFixture(t *testing.T, backend string) *removalFixture {
	t.Helper()
	s := &quiescenceActionStore{Authority: controllerStore(t, backend), t: t}
	a, b := acceptOriginalPair(t, s)

	drain := originalDrain(t, s, a)
	if _, err := s.RecordManagedOriginalDrain(t.Context(), drain); err != nil {
		t.Fatal(err)
	}

	var observations []managed.QuiescenceAcceptanceRequest

	for _, registration := range []managed.OriginalRegistrationAcceptanceRequest{a, b} {
		q := quiescenceRequest(t, s, registration, drain, false)

		observations = append(observations, q)
		if _, err := s.AcceptManagedQuiescence(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := s.SealManagedTarget(t.Context(), quiescenceCommand(t, s, a.Command.Key)); err != nil {
		t.Fatal(err)
	}

	start := managed.AttemptStartRequest{Command: quiescenceCommand(t, s, a.Command.Key), ActionID: s.action.ID, AttemptID: id.New(managed.PrefixOperation), ProviderOperationID: id.New(managed.PrefixOperation), VerifierID: "provider"}

	receipt, err := s.BeginManagedRemovalAttempt(t.Context(), start)
	if err != nil {
		t.Fatal(err)
	}

	replay, err := s.BeginManagedRemovalAttempt(t.Context(), start)
	if err != nil || replay != receipt {
		t.Fatalf("start replay: %+v %v", replay, err)
	}

	return &removalFixture{t: t, s: s, key: managed.AttemptKey{Key: a.Command.Key, AttemptID: receipt.AttemptID, Generation: "1"}, provider: receipt.ProviderOperationID, observations: observations, reservations: []id.ID{a.Command.ReservationID, b.Command.ReservationID}}
}
func (f *removalFixture) request() managed.AttemptRequest {
	return managed.AttemptRequest{Command: quiescenceCommand(f.t, f.s, f.key.Key), AttemptID: f.key.AttemptID, Generation: f.key.Generation}
}
func (f *removalFixture) command(rid id.ID, kind string) managed.AttemptCommandGrant {
	f.t.Helper()

	raw := []byte("synthetic-original-" + kind)
	native := []byte("synthetic-native-" + kind)

	if f.maximumEvidence {
		raw = bytes.Repeat([]byte{0xff}, managed.MaxEvidence)
		native = bytes.Repeat([]byte{0xfe}, managed.MaxEvidence)
	}

	q := managed.AttemptCommandRequest{AttemptRequest: f.request(), ReservationID: rid, Kind: kind, ExpectedNativeVersion: "1", NativeCommand: managed.PortCommand{SchemaVersion: "1", OperationID: id.New(managed.PrefixOperation), RequestID: "original-" + rid.String() + kind, Original: raw, OriginalDigest: managed.Digest(raw), Native: native, NativeDigest: managed.Digest(native)}}

	if f.key.Generation != "1" {
		if kind == managed.AttemptBegin {
			q.CurrentBinding = successorCommandProof(f.t, f, rid)
			q.ExpectedNativeVersion = q.CurrentBinding.BindingVersion
		} else {
			q.ExpectedNativeVersion = "11"
		}
	}

	grant, err := f.s.AuthorizeManagedAttemptCommand(f.t.Context(), q)
	if err != nil {
		f.t.Fatal(err)
	}

	if !grant.NewlyIssued {
		f.t.Fatal("first native grant was not new")
	}

	replay, err := f.s.AuthorizeManagedAttemptCommand(f.t.Context(), q)
	if err != nil || replay.NewlyIssued || !reflect.DeepEqual(grant.Command, replay.Command) {
		f.t.Fatalf("command replay: %+v %v", replay, err)
	}

	return grant
}
func (f *removalFixture) acceptance(c managed.AttemptCommandRecord) managed.AttemptNativeAcceptance {
	f.t.Helper()

	receipt := []byte("synthetic-native-receipt-" + c.Key.Kind)
	proof := []byte("synthetic-authenticated-binding-proof")

	if f.maximumEvidence {
		receipt = bytes.Repeat([]byte{0xff}, managed.MaxEvidence)
		proof = bytes.Repeat([]byte{0xfe}, managed.MaxEvidence)
	}

	result := managed.AttemptNativeAcceptance{NativeAcceptance: managed.AttemptNativeFacts{RequestID: c.NativeCommand.RequestID, RequestDigest: c.NativeCommand.NativeDigest, CommandDigest: managed.Digest([]byte("operator-command")), ResponseVersion: "1", BindingVersion: "1", RemovalEpoch: "1", AcceptedAt: quiescenceClock(f.t, f.s)}, NativeFenceDigest: managed.Digest([]byte("native-fence-" + c.Key.ReservationID.String())), ReceiptID: "accepted-" + c.Key.Kind, Receipt: receipt, ReceiptDigest: managed.Digest(receipt), OriginalBindingProof: proof, ProofDigest: managed.Digest(proof)}
	if f.key.Generation != "1" {
		result.NativeAcceptance.BindingVersion = "11"
		result.NativeAcceptance.RemovalEpoch = "5"
	}

	return result
}
func (f *removalFixture) fence(rid id.ID) managed.AttemptCommandGrant {
	f.t.Helper()
	grant := f.command(rid, managed.AttemptBegin)

	proof := []byte("opaque-fence-" + rid.String())
	if f.maximumEvidence {
		proof = bytes.Repeat([]byte{0xfd}, managed.MaxEvidence)
	}

	q := managed.AttemptFenceRequest{AttemptRequest: f.request(), ReservationID: rid, Acceptance: f.acceptance(grant.Command), Fence: managed.FenceWire(managed.Fence{CompatibilityEvidence: []byte("retired"), Epoch: 1, Evidence: proof, Digest: managed.Digest(proof), ValidUntil: quiescenceClock(f.t, f.s).Add(time.Hour), Survivors: []string{"off-instance"}})}

	receipt, err := f.s.RecordManagedAttemptFence(f.t.Context(), q)
	if err != nil {
		f.t.Fatal(err)
	}

	again, err := f.s.RecordManagedAttemptFence(f.t.Context(), q)
	if err != nil || again != receipt {
		f.t.Fatalf("fence replay: %v", err)
	}

	return grant
}
func (f *removalFixture) admit() {
	f.t.Helper()

	for _, rid := range f.reservations {
		f.fence(rid)
	}

	if _, err := f.s.AdmitManagedRemovalAttempt(f.t.Context(), managed.AttemptAdmissionRequest{AttemptRequest: f.request(), VerifierID: "provider"}); err != nil {
		f.t.Fatal(err)
	}
}
func (f *removalFixture) step(service string, kind managed.ProviderStepKind) managed.ProviderStepGrant {
	f.t.Helper()
	q := managed.ProviderStepAuthorization{Command: quiescenceCommand(f.t, f.s, f.key.Key), Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: f.key.Key, ActionID: f.s.action.ID}, ProviderOperationID: f.provider, Kind: kind, Service: service}}

	grant, err := f.s.AuthorizeManagedProviderStep(f.t.Context(), q)
	if err != nil || !grant.NewlyIssued {
		f.t.Fatalf("provider permission: %+v %v", grant, err)
	}

	replay, err := f.s.AuthorizeManagedProviderStep(f.t.Context(), q)
	if err != nil || replay.NewlyIssued {
		f.t.Fatalf("provider replay: %+v %v", replay, err)
	}

	return grant
}
func (f *removalFixture) failure(grant managed.ProviderStepGrant) managed.ProviderStepFailure {
	f.t.Helper()

	record, err := f.s.ReadManagedRemovalAttempt(f.t.Context(), f.key)
	if err != nil {
		f.t.Fatal(err)
	}

	witness := []byte("synthetic qualified hook refusal storage fixture")

	return managed.ProviderStepFailure{SchemaVersion: "1", Permission: grant.Permission, AttemptRef: record.State.AttemptRef, InvocationID: grant.Permission.CommandID, ProducerProfile: managed.LocalNoDispatchProfile, VerifierID: "trusted-hook", Class: "local_no_dispatch", Phase: "before_transport_delegate", Reason: "authority_refused", ObservedAt: quiescenceClock(f.t, f.s), Witness: witness, WitnessDigest: managed.Digest(witness)}
}

func TestManagedAttemptRemoved(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newRemovalFixture(t, backend)
			f.admit()

			for _, step := range []struct {
				service string
				kind    managed.ProviderStepKind
			}{{"api", managed.StepRemoveContainer}, {"sidecar", managed.StepRemoveContainer}, {"", managed.StepRemoveNetwork}} {
				grant := f.step(step.service, step.kind)
				result := managed.ProviderStepResult{Permission: grant.Permission, Outcome: managed.PortTerminal, ResourceID: grant.Permission.ResourceID, Evidence: []byte("synthetic qualified complete204 response"), ObservedAt: quiescenceClock(t, f.s)}

				before, _ := json.Marshal(result)
				if err := f.s.RecordManagedProviderStepResult(t.Context(), result); err != nil {
					t.Fatal(err)
				}

				if err := f.s.RecordManagedProviderStepFailure(t.Context(), f.failure(grant)); !errors.Is(err, managed.ErrConflict) {
					t.Fatalf("positive/failure conflict: %v", err)
				}

				saved, err := f.s.ReadManagedProviderStep(t.Context(), grant.Permission.Step)
				if err != nil {
					t.Fatal(err)
				}

				after, _ := json.Marshal(saved.Result)
				if !bytes.Equal(before, after) {
					t.Fatal("positive bytes changed")
				}
			}

			if _, err := f.s.SettleManagedRemovalAttempt(t.Context(), managed.AttemptSettlementRequest{AttemptRequest: f.request(), Disposition: managed.Removed, ProducerProfile: "ctrlplane.managed.remove.v1", VerifierID: "provider", Evidence: []byte("full original204 coverage"), SettledAt: quiescenceClock(t, f.s)}); err != nil {
				t.Fatal(err)
			}

			for _, rid := range f.reservations {
				command := f.command(rid, managed.AttemptFinish)

				q := managed.AttemptFinishRequest{AttemptRequest: f.request(), ReservationID: rid, Acceptance: f.acceptance(command.Command)}
				assertAttemptFactLocalID(t, f, func(fresh bool) (managed.Receipt, error) {
					selected := q
					if fresh {
						selected.AttemptRequest = f.request()
					}

					return f.s.RecordManagedAttemptFinish(t.Context(), selected)
				})
			}

			q := f.request()

			receipt, err := f.s.TombstoneManagedRemovalAttempt(t.Context(), q)
			if err != nil {
				t.Fatal(err)
			}

			if replay, err := f.s.TombstoneManagedRemovalAttempt(t.Context(), q); err != nil || replay != receipt {
				t.Fatalf("terminal replay: %v", err)
			}

			target, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
			if err != nil || target.Phase != managed.Deleted {
				t.Fatalf("target: %+v %v", target, err)
			}
		})
	}
}

func TestManagedAttemptFailureSettlement(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newRemovalFixture(t, backend)
			f.admit()
			grant := f.step("api", managed.StepRemoveContainer)

			failure := f.failure(grant)
			if err := f.s.RecordManagedProviderStepFailure(t.Context(), failure); err != nil {
				t.Fatal(err)
			}

			if err := f.s.RecordManagedProviderStepFailure(t.Context(), failure); err != nil {
				t.Fatal(err)
			}

			positive := managed.ProviderStepResult{Permission: grant.Permission, Outcome: managed.PortTerminal, ResourceID: grant.Permission.ResourceID, Evidence: []byte("late positive"), ObservedAt: quiescenceClock(t, f.s)}
			if err := f.s.RecordManagedProviderStepResult(t.Context(), positive); !errors.Is(err, managed.ErrConflict) {
				t.Fatalf("failure/positive conflict: %v", err)
			}

			next := managed.ProviderStepAuthorization{Command: quiescenceCommand(t, f.s, f.key.Key), Step: grant.Permission.Step}

			next.Step.Service = "sidecar"
			if _, err := f.s.AuthorizeManagedProviderStep(t.Context(), next); err == nil {
				t.Fatal("failure advanced successor step")
			}

			if _, err := f.s.SettleManagedRemovalAttempt(t.Context(), managed.AttemptSettlementRequest{AttemptRequest: f.request(), Disposition: managed.SettledWithoutDeletion, ProducerProfile: managed.LocalNoDispatchProfile, VerifierID: "provider", Evidence: []byte("all issued calls positively refused"), SettledAt: quiescenceClock(t, f.s)}); err != nil {
				t.Fatal(err)
			}

			target, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
			if err != nil || !target.Deletion.Issued || target.Deletion.Settlement.Disposition != managed.SettledWithoutDeletion {
				t.Fatal("failure relabeled issued history", err)
			}
		})
	}
}

func TestManagedAttemptLegacyAndUnsupported(t *testing.T) {
	f := newRemovalFixture(t, "memory")

	before, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = f.s.IssueManagedDeletion(t.Context(), quiescenceCommand(t, f.s, f.key.Key)); err == nil {
		t.Fatal("legacy issue bypass")
	}

	newer := f.key

	newer.Generation = "2"
	if _, err = f.s.ReadManagedRemovalAttempt(t.Context(), newer); !errors.Is(err, managed.ErrAuthority) {
		t.Fatalf("generation2: %v", err)
	}

	after, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("refusal mutated parent", err)
	}
}

func TestManagedAttemptRecoveryConcurrent(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newRemovalFixture(t, backend)
			grant := f.command(f.reservations[0], managed.AttemptBegin)

			raw, err := json.Marshal(grant.Command)
			if err != nil {
				t.Fatal(err)
			}

			digest := managed.Digest(append([]byte("ctrlplane.managed.attempt-command.v1\n"), raw...))

			before, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
			if err != nil {
				t.Fatal(err)
			}

			base := managed.AttemptRecoveryRequest{AttemptRequest: f.request(), ReservationID: f.reservations[0], Kind: managed.AttemptBegin, CommandDigest: digest}
			results := make([]managed.AttemptRecoveryGrant, 2)
			failures := make([]error, 2)
			requests := []managed.AttemptRecoveryRequest{base, base}
			requests[1].ID = id.New(managed.PrefixOperation)

			var wait sync.WaitGroup
			for n := range requests {
				wait.Go(func() {
					results[n], failures[n] = f.s.AuthorizeManagedAttemptRecovery(context.Background(), requests[n])
				})
			}

			wait.Wait()

			for n, result := range results {
				if failures[n] != nil || !result.NewlyAuthorized || !reflect.DeepEqual(result.Command, grant.Command) {
					t.Fatalf("recovery%d: %+v %v", n, result, failures[n])
				}
			}

			after, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("recovery advanced parent", err)
			}

			again, err := f.s.AuthorizeManagedAttemptRecovery(t.Context(), requests[0])
			if err != nil || again.NewlyAuthorized {
				t.Fatal("replay authorized invocation", err)
			}
		})
	}
}

func TestManagedAttemptDecimalEncoding(t *testing.T) {
	f := newRemovalFixture(t, "memory")
	q := managed.AttemptCommandRequest{AttemptRequest: f.request(), ReservationID: f.reservations[0], Kind: managed.AttemptBegin, ExpectedNativeVersion: "1"}
	q.Revision = uint64(1<<63 - 1)
	q.LeaseEpoch = q.Revision

	raw, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Contains(raw, []byte(`"revision":"`+strconv.FormatUint(q.Revision, 10)+`"`)) {
		t.Fatalf("numeric parent counter: %s", raw)
	}

	var decoded managed.AttemptCommandRequest
	if err = json.Unmarshal(raw, &decoded); err != nil || !reflect.DeepEqual(q, decoded) {
		t.Fatal("wire roundtrip", err)
	}

	numeric := bytes.Replace(raw, []byte(`"revision":"9223372036854775807"`), []byte(`"revision":9223372036854775807`), 1)
	if err = json.Unmarshal(numeric, &decoded); err == nil {
		t.Fatal("numeric counter accepted")
	}

	duplicate := append([]byte(`{"generation":"1",`), raw[1:]...)
	if err = json.Unmarshal(duplicate, &decoded); err == nil {
		t.Fatal("duplicate field accepted")
	}
}

func fixtureAttemptFact(t *testing.T, kind string, value any) string {
	t.Helper()

	raw, err := json.Marshal(struct {
		Kind  string `json:"kind"`
		Value any    `json:"value"`
	}{kind, value})
	if err != nil {
		t.Fatal(err)
	}

	return managed.Digest(append([]byte("ctrlplane.managed.attempt-fact.v1\n"), raw...))
}
func TestManagedAttemptLateBeginAbort(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newRemovalFixture(t, backend)
			rid := f.reservations[0]
			begin := f.command(rid, managed.AttemptBegin)

			revoked, err := f.s.RevokeManagedRemovalAttempt(t.Context(), f.request())
			if err != nil {
				t.Fatal(err)
			}

			acceptance := f.acceptance(begin.Command)
			proof := []byte("expired native fence")

			q := managed.AttemptFenceRequest{AttemptRequest: f.request(), ReservationID: rid, Acceptance: acceptance, Fence: managed.FenceWire(managed.Fence{CompatibilityEvidence: []byte("retired"), Epoch: 1, Evidence: proof, Digest: managed.Digest(proof), ValidUntil: quiescenceClock(t, f.s).Add(-time.Second), Survivors: []string{"off-instance"}})}
			if _, err = f.s.RecordManagedAttemptFence(t.Context(), q); err != nil {
				t.Fatal(err)
			}

			target, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
			if err != nil {
				t.Fatal(err)
			}

			if target.Deletion != nil || target.Reservations[0].Fence != nil {
				t.Fatal("historical fence fabricated live admission")
			}

			bindingKey := managed.AttemptBindingKey{AttemptKey: f.key, ReservationID: rid}

			binding, err := f.s.ReadManagedAttemptBinding(t.Context(), bindingKey)
			if err != nil || binding.Fence == nil {
				t.Fatal("historical binding read", err)
			}

			witnessRequest := managed.AttemptBindingSettlementRequest{AttemptRequest: f.request(), ReservationID: rid, FenceFactDigest: fixtureAttemptFact(t, "removal_fence", *binding.Fence)}
			if _, err = f.s.RecordManagedAttemptBindingSettlement(t.Context(), witnessRequest); err != nil {
				t.Fatal(err)
			}

			historical, err := f.s.ReadManagedAttemptHistoricalSettlement(t.Context(), managed.AttemptSettlementReadRequest{AttemptBindingKey: bindingKey, ProviderOperationID: f.provider, NativeFenceDigest: acceptance.NativeFenceDigest})
			if err != nil {
				t.Fatal(err)
			}

			if historical.Admission != nil || historical.Witness.WitnessedAt.Before(acceptance.NativeAcceptance.AcceptedAt) || !historical.Revocation.RevokedAt.Equal(revoked.AcceptedAt) {
				t.Fatal("late witness changed original revocation or preceded native acceptance")
			}

			abort := f.command(rid, managed.AttemptAbort)
			abortRequest := managed.AttemptAbortRequest{AttemptRequest: f.request(), ReservationID: rid, Acceptance: f.acceptance(abort.Command), ProofVerifiedAt: quiescenceClock(t, f.s), ProofValidUntil: quiescenceClock(t, f.s).Add(time.Minute)}

			saved, err := f.s.RecordManagedAttemptAbort(t.Context(), abortRequest)
			if err != nil {
				t.Fatal(err)
			}

			if replay, err := f.s.RecordManagedAttemptAbort(t.Context(), abortRequest); err != nil || replay != saved {
				t.Fatal("abort replay", err)
			}

			binding, err = f.s.ReadManagedAttemptBinding(t.Context(), bindingKey)
			if err != nil || binding.Abort == nil {
				t.Fatal("accepted abort unavailable", err)
			}
		})
	}
}

func TestManagedAttemptMissingLedgerRefuses(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := &quiescenceActionStore{Authority: controllerStore(t, backend), t: t}

			a, _ := acceptOriginalPair(t, s)
			if _, err := s.SealManagedTarget(t.Context(), quiescenceCommand(t, s, a.Command.Key)); err != nil {
				t.Fatal(err)
			}

			before, err := s.ReadManagedTarget(t.Context(), a.Command.Key)
			if err != nil {
				t.Fatal(err)
			}

			q := managed.AdmissionRequest{Command: quiescenceCommand(t, s, a.Command.Key), OperationID: id.New(managed.PrefixOperation), Verifier: "provider"}
			if _, err = s.AdmitManagedRemoval(t.Context(), q); !errors.Is(err, managed.ErrBlocked) {
				t.Fatalf("missing ledger admitted: %v", err)
			}

			after, err := s.ReadManagedTarget(t.Context(), a.Command.Key)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("missing ledger refusal mutated target", err)
			}
		})
	}
}

func mixedRemovalFixture(t *testing.T, backend string) (*removalFixture, *managedFixture) {
	t.Helper()

	return removalActionFixture(t, controllerStore(t, backend), controllerAction(t), 1)
}
func removalActionFixture(t *testing.T, authority managedservice.Authority, action managed.ActionSubmission, copies int) (*removalFixture, *managedFixture) {
	t.Helper()

	return removalActionSetup(t, authority, action, copies, true)
}
func removalActionSetup(t *testing.T, authority managedservice.Authority, action managed.ActionSubmission, copies int, startAttempt bool, nativeValidity ...time.Duration) (*removalFixture, *managedFixture) {
	t.Helper()

	nativeWindow := time.Minute
	if len(nativeValidity) > 0 {
		nativeWindow = nativeValidity[0]
	}

	if _, err := authority.SubmitManagedAction(t.Context(), action); err != nil {
		t.Fatal(err)
	}

	parent := &managedFixture{t: t, s: authority, k: action.Key}
	parent.must(authority.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: parent.command(), Duration: managed.MaxLease}))

	projection := managed.ProjectionRequest{Command: parent.command(), ActionID: action.ID, Stage: managed.ProjectionPending}
	if _, err := authority.ProjectManagedRows(t.Context(), projection); err != nil {
		t.Fatal(err)
	}

	parent.must(authority.IssueManagedProvision(t.Context(), parent.command()))

	spec, err := managed.ParseProvisionSpec(action.NewTarget.Manifest)
	if err != nil {
		t.Fatal(err)
	}

	resources := map[string]string{"network": "network-id"}
	for index, service := range spec.Services {
		resources[service.Name] = "resource-" + strconv.Itoa(index)
	}

	parent.must(authority.BindManagedResources(t.Context(), managed.ResourcesRequest{Command: parent.command(), OperationID: action.NewTarget.ProvisionOperationID, Receipt: []byte("full provider fixture result"), Resources: resources}))
	f := &removalFixture{t: t, s: &quiescenceActionStore{Authority: authority, t: t, action: action}, maximumEvidence: len(nativeValidity) > 0}

	var roles managed.ManagedSpec
	if err = json.Unmarshal(action.NewTarget.Manifest.Spec, &roles); err != nil {
		t.Fatal(err)
	}

	for n, member := range action.NewTarget.Manifest.Members {
		role := roles.RuntimeRoles[n]
		for copyIndex := range copies {
			reg := parent.reserve(member.Name, "runtime-"+strconv.Itoa(n)+"-"+strconv.Itoa(copyIndex))
			parent.must(authority.IssueManagedRegistration(t.Context(), managed.ReservationRequest{Command: parent.command(), ReservationID: reg.ID}))
			stamp := quiescenceClock(t, authority).Add(-time.Millisecond)

			raw := []byte("original registration storage fixture")
			if f.maximumEvidence {
				raw = bytes.Repeat([]byte{0xff}, managed.MaxEvidence)
			}

			c := originalCommand(t, authority, action.Key)
			c.ReservationID = reg.ID
			process := managed.OriginalProcessFacts{Installation: member.Installation, Namespace: member.Namespace, Build: member.Build, Queue: member.Queue, RuntimeID: reg.RuntimeID, InstanceID: reg.InstanceID, HostID: reg.HostID, IdentityVersion: "1", ArtifactDigest: member.ArtifactDigest, ConfigurationDigest: member.ConfigurationDigest, ConfigurationVersion: "config-v1", EnrollmentEvidenceDigest: managed.Digest([]byte("enrollment")), ProbePolicyID: "policy", ProbePolicyVersion: "1", MaximumProofValidityNS: managed.Decimal(strconv.FormatInt(int64(nativeWindow), 10)), VerifierID: "native-verifier", Descriptor: spec.Services[n].Startup, ProducerVerifier: "trusted-descriptor-verifier"}

			q := managed.OriginalRegistrationAcceptanceRequest{Command: c, Facts: managed.OriginalRegistrationFacts{Process: process, Native: managed.OriginalNativeAcceptance{Operation: "query_runtime.register", RequestID: reg.RequestID, RequestDigest: managed.Digest([]byte("native request")), CommandDigest: managed.Digest([]byte("operator command")), ResponseVersion: "1", DeliveryID: "delivery", AcceptedAt: stamp}, Member: member.Name, Role: role, StoredCommandDigest: reg.CommandDigest, BindingVersion: "1", BindingState: "active", RemovalEpoch: "0", CreatedAt: stamp, ChangedAt: stamp}, NativeReceipt: raw, NativeReceiptDigest: managed.Digest(raw)}
			if _, err = authority.AcceptManagedOriginalRegistration(t.Context(), q); err != nil {
				t.Fatal(err)
			}
			{
				d := originalDrain(t, authority, q)
				if f.maximumEvidence {
					d.NativeReceipt = bytes.Repeat([]byte{0xfe}, managed.MaxEvidence)
					d.NativeReceiptDigest = managed.Digest(d.NativeReceipt)
				}

				if _, err = authority.RecordManagedOriginalDrain(t.Context(), d); err != nil {
					t.Fatal(err)
				}

				observed := quiescenceRequest(t, authority, q, d, false)
				if _, err = authority.AcceptManagedQuiescence(t.Context(), observed); err != nil {
					t.Fatal(err)
				}

				f.observations = append(f.observations, observed)
			}

			f.reservations = append(f.reservations, reg.ID)
		}
	}

	parent.must(authority.SealManagedTarget(t.Context(), parent.command()))

	f.key = managed.AttemptKey{Key: action.Key, Generation: "1"}
	if !startAttempt {
		return f, parent
	}

	start := managed.AttemptStartRequest{Command: parent.command(), ActionID: action.ID, AttemptID: id.New(managed.PrefixOperation), ProviderOperationID: id.New(managed.PrefixOperation), VerifierID: "provider"}

	receipt, err := authority.BeginManagedRemovalAttempt(t.Context(), start)
	if err != nil {
		t.Fatal(err)
	}

	f.key = managed.AttemptKey{Key: action.Key, AttemptID: receipt.AttemptID, Generation: "1"}
	f.provider = receipt.ProviderOperationID

	return f, parent
}

func TestManagedAttemptManifestCapacityCorrection(t *testing.T) {
	action := controllerAction(t)
	manifest := action.NewTarget.Manifest
	spec := managed.ProvisionSpec{Version: "1"}

	for n := range 32 {
		role := "sidecar"
		if n == 0 {
			role = "main"
		}

		spec.Services = append(spec.Services, managed.ProvisionService{Name: "s" + strconv.Itoa(n), Image: "x@sha256:" + strings.Repeat("a", 64), Role: role, CPUMillis: "1", MemoryMiB: "1", PIDLimit: "1", Startup: managed.StartupBindingRef{BindingID: "b", BindingGeneration: "1", BindingDigest: strings.Repeat("b", 64)}})
	}

	provision, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}

	manifest.Spec, err = json.Marshal(managed.ManagedSpec{Profile: "ctrlplane.managed.v1", RuntimeRoles: []managed.RuntimeRole{managed.Polling, managed.RetainedQuery}, Provision: provision})
	if err != nil {
		t.Fatal(err)
	}

	manifest.Members[0].Service = "s0"
	manifest.Members[1].Service = "s1"
	manifest.SpecDigest = managed.Digest(manifest.Spec)
	_, parseErr := managed.ParseProvisionSpec(manifest)
	t.Logf("Existing 32-service minimum practical encoded manifest: %d bytes; MaxEvidence=%d; parse=%v", len(manifest.Spec), managed.MaxEvidence, parseErr)

	if parseErr != nil || len(manifest.Spec) <= managed.MaxEvidence || len(manifest.Spec) > managed.MaxManifestSpec {
		t.Fatal("fixed-profile capacity correction failed")
	}
}

func (f *removalFixture) refreshObservation(index int) {
	f.t.Helper()
	q := f.observations[index]
	rid := q.Command.ReservationID
	q.Command = originalCommand(f.t, f.s, f.key.Key)
	q.Command.ReservationID = rid
	q.Observation.ObservedAt = quiescenceClock(f.t, f.s)
	q.Observation.ValidUntil = q.Observation.ObservedAt.Add(time.Minute)
	signQuiescence(f.t, &q.Observation)

	if _, err := f.s.AcceptManagedQuiescence(f.t.Context(), q); err != nil {
		f.t.Fatal(err)
	}

	f.observations[index] = q
}
func (f *removalFixture) refreshObservations() {
	f.t.Helper()

	for index := range f.observations {
		f.refreshObservation(index)
	}
}
func (f *removalFixture) refreshExpiringObservations(minimum time.Duration) (int, time.Duration) {
	f.t.Helper()

	for count := 0; count <= len(f.observations); count++ {
		now := quiescenceClock(f.t, f.s)

		oldest := 0
		for index := range f.observations {
			if f.observations[index].Observation.ValidUntil.Before(f.observations[oldest].Observation.ValidUntil) {
				oldest = index
			}
		}

		margin := f.observations[oldest].Observation.ValidUntil.Sub(now)
		if margin >= minimum {
			return count, margin
		}

		if count == len(f.observations) {
			f.t.Fatalf("one complete renewal pass cannot establish required proof margin: %s", margin)
		}

		f.refreshObservation(oldest)
	}

	return 0, 0
}
func largeRemovalAction(t *testing.T) managed.ActionSubmission {
	t.Helper()
	action := controllerAction(t)
	identity := action.NewTarget
	member := identity.Manifest.Members[0]
	spec := managed.ProvisionSpec{Version: "1"}
	roles := []managed.RuntimeRole{}
	identity.Manifest.Members = nil

	for n := range 32 {
		name := "service" + strconv.Itoa(n)
		m := member
		m.Name = name
		m.Service = name
		m.Namespace = "namespace" + strconv.Itoa(n)
		identity.Manifest.Members = append(identity.Manifest.Members, m)

		role := "sidecar"
		if n == 0 {
			role = "main"
		}

		spec.Services = append(spec.Services, managed.ProvisionService{Name: name, Image: "x@sha256:" + strings.Repeat("a", 64), Role: role, CPUMillis: "1", MemoryMiB: "1", PIDLimit: "1", Startup: managed.StartupBindingRef{BindingID: "binding" + strconv.Itoa(n), BindingGeneration: "1", BindingDigest: strings.Repeat("b", 64)}})
		roles = append(roles, managed.Polling)
	}

	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}

	identity.Manifest.Spec, err = json.Marshal(managed.ManagedSpec{Profile: "ctrlplane.managed.v1", RuntimeRoles: roles, Provision: raw})
	if err != nil {
		t.Fatal(err)
	}

	identity.Manifest.SpecDigest = managed.Digest(identity.Manifest.Spec)

	signControllerAction(t, &action)

	return action
}
func TestManagedAttemptFullFiniteSet(t *testing.T) {
	requireManagedMaximum(t, "individual")
	testManagedAttemptFullFiniteSet(t, false, "")
}
func TestManagedAttemptPostgresFullFiniteSetNearLimit(t *testing.T) {
	requireManagedMaximum(t, "individual")
	testManagedAttemptFullFiniteSet(t, true, "")
}
func testManagedAttemptFullFiniteSet(t *testing.T, nearLimit bool, diagnostic string) {
	testManagedFiniteSetWithBatch(t, nearLimit, diagnostic, false)
}
func testManagedFiniteSetWithBatch(t *testing.T, nearLimit bool, diagnostic string, batch bool) {
	backends := []string{"memory", "postgres"}
	if nearLimit || diagnostic != "" {
		backends = []string{"postgres"}
	}

	for _, backend := range backends {
		t.Run(backend, func(t *testing.T) {
			started := time.Now()
			authority := controllerStore(t, backend)
			action := largeRemovalAction(t)

			var (
				f               *removalFixture
				initialRevision uint64
			)

			if !nearLimit {
				f, _ = removalActionFixture(t, authority, action, 2)
			} else {
				var parent *managedFixture

				f, parent = removalActionSetup(t, authority, action, 2, false)
				pg := pgdriver.Unwrap(authority.(*postgres.Store).DB())
				setRevision := func(value uint64) {
					t.Helper()

					if _, err := pg.Exec(t.Context(), "UPDATE cp_managed_targets SET revision=$1,data=jsonb_set(data,'{revision}',to_jsonb($1::bigint)) WHERE id=$2", int64(value), action.InstanceID.String()); err != nil {
						t.Fatal(err)
					}
				}

				const reserve = 7*64 + 33 + 8
				setRevision((1<<63 - 1) - reserve)

				rejected := managed.AttemptStartRequest{Command: parent.command(), ActionID: action.ID, AttemptID: id.New(managed.PrefixOperation), ProviderOperationID: id.New(managed.PrefixOperation), VerifierID: "provider"}

				before := attemptPostgresSnapshot(t, authority.(*postgres.Store))
				if _, err := authority.BeginManagedRemovalAttempt(t.Context(), rejected); !errors.Is(err, managed.ErrCapacity) {
					t.Fatalf("64-binding insufficient initial reserve: %v", err)
				}

				if !reflect.DeepEqual(before, attemptPostgresSnapshot(t, authority.(*postgres.Store))) {
					t.Fatal("reserve refusal changed rows")
				}

				initialRevision = (1<<63 - 1) - (reserve + 65 + 1)
				if batch {
					initialRevision = (1<<63 - 1) - batchCompletionHeadroom
				}

				setRevision(initialRevision)

				q := rejected
				q.Command = parent.command()
				q.ID = id.New(managed.PrefixOperation)

				accepted, err := authority.BeginManagedRemovalAttempt(t.Context(), q)
				if err != nil {
					t.Fatal(err)
				}

				f.key.AttemptID = accepted.AttemptID
				f.provider = accepted.ProviderOperationID
			}

			if !nearLimit {
				initialTarget, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
				if err != nil {
					t.Fatal(err)
				}

				initialRevision = initialTarget.Revision - 1
			}

			if len(f.reservations) != 64 {
				t.Fatal("incomplete actual reservation set")
			}

			t.Logf("setup elapsed=%s", time.Since(started))

			leaseUpdates := 0
			renewals := newBatchRenewalBudget(t, nearLimit, diagnostic)
			t.Cleanup(func() { renewals.log(t) })

			claimLease := func() {
				t.Helper()

				if batch {
					renewals.leaseAttempt(t)
				}

				if _, err := f.s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: f.request().Command, Duration: managed.MaxLease}); err != nil {
					t.Fatal(err)
				}

				leaseUpdates++

				if batch {
					renewals.leases++
				}
			}
			refreshBatch := func() (int, time.Duration) {
				t.Helper()
				renewals.batchAttempt(t)

				count, margin := f.refreshObservationBatch()
				renewals.batches++

				return count, margin
			}

			renewSetupLease := func(force bool) {
				t.Helper()

				target, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
				if err != nil {
					t.Fatal(err)
				}

				margin := target.LeaseUntil.Sub(quiescenceClock(t, f.s))
				if !force && margin >= time.Minute {
					return
				}

				if !batch && leaseUpdates != 0 {
					t.Fatalf("setup needs more than the single budgeted lease renewal: margin=%s elapsed=%s", margin, time.Since(started))
				}

				t.Logf("pre-positive lease renewal margin=%s elapsed=%s", margin, time.Since(started))

				claimLease()
			}
			for _, rid := range f.reservations {
				renewSetupLease(false)
				f.fence(rid)
			}

			t.Logf("fences elapsed=%s", time.Since(started))
			renewSetupLease(nearLimit && leaseUpdates == 0)

			if diagnostic == "after-fence-proxy" {
				profileStore, counter := countedAttemptPostgres(t, authority.(*postgres.Store))
				f.s.Authority = profileStore
				profilePath := filepath.Join(os.Getenv("GOTMPDIR"), "attempt-full-ledger-refresh.cpu")

				file, err := os.Create(profilePath)
				if err != nil {
					t.Fatal("cannot create owned CPU profile")
				}

				if err = pprof.StartCPUProfile(file); err != nil {
					_ = file.Close()

					t.Fatal(err)
				}

				beforeQueries := counter.queries.Load()
				beforeBytes := counter.bytes.Load()
				startedRefresh := time.Now()

				f.refreshObservations()

				elapsed := time.Since(startedRefresh)
				queryCount := counter.queries.Load() - beforeQueries
				responseBytes := counter.bytes.Load() - beforeBytes

				pprof.StopCPUProfile()

				if err = file.Close(); err != nil {
					t.Fatal(err)
				}

				now := quiescenceClock(t, f.s)
				margin := f.observations[0].Observation.ValidUntil.Sub(now)
				t.Logf("bounded full64 renewal after all64 fences and before provider issue: queries=%d response_bytes=%d duration=%s oldest_age=%s margin=%s", queryCount, responseBytes, elapsed, time.Minute-margin, margin)

				if margin <= 0 {
					t.Fatal("all-fence proof renewal exceeds60s")
				}

				return
			}

			var raceProfile *os.File

			if os.Getenv("CTRLPLANE_ATTEMPT_CPU_PROFILE") == "1" {
				var err error

				raceProfile, err = os.Create(filepath.Join(os.Getenv("GOTMPDIR"), "attempt-race-refresh.cpu"))
				if err != nil {
					t.Fatal(err)
				}

				if err = pprof.StartCPUProfile(raceProfile); err != nil {
					_ = raceProfile.Close()

					t.Fatal(err)
				}

				t.Cleanup(func() { pprof.StopCPUProfile(); _ = raceProfile.Close() })
			}

			refreshStarted := time.Now()
			batchUpdates := 0

			if batch {
				refreshBatch()

				batchUpdates++
			} else {
				f.refreshObservations()
			}

			if raceProfile != nil {
				pprof.StopCPUProfile()

				if err := raceProfile.Close(); err != nil {
					t.Fatal(err)
				}
			}

			currentProofTime := quiescenceClock(t, f.s)
			t.Logf("observations refreshed elapsed=%s cost=%s oldest_proof_age=%s remaining_margin=%s", time.Since(started), time.Since(refreshStarted), currentProofTime.Sub(f.observations[0].Observation.ObservedAt), f.observations[0].Observation.ValidUntil.Sub(currentProofTime))

			if diagnostic == "after-fence-direct" {
				if !currentProofTime.Before(f.observations[0].Observation.ValidUntil) {
					t.Fatal("direct all-fence renewal exceeds60s")
				}

				return
			}

			if _, err := f.s.AdmitManagedRemovalAttempt(t.Context(), managed.AttemptAdmissionRequest{AttemptRequest: f.request(), VerifierID: "provider"}); err != nil {
				t.Fatal(err)
			}

			steps := 0
			minimumAuthorizationMargin := time.Minute
			proofUpdates := 64

			for n := range 33 {
				now := quiescenceClock(t, f.s)

				margin := time.Minute
				for _, proof := range f.observations {
					if left := proof.Observation.ValidUntil.Sub(now); left < margin {
						margin = left
					}
				}

				if margin < 5*time.Second {
					refreshStarted := time.Now()

					lease, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
					if err != nil {
						t.Fatal(err)
					}

					if lease.LeaseUntil.Sub(now) < time.Minute {
						claimLease()
					}

					var (
						count         int
						renewedMargin time.Duration
					)
					if batch {
						count, renewedMargin = refreshBatch()
						batchUpdates++
					} else {
						count, renewedMargin = f.refreshExpiringObservations(5 * time.Second)
					}

					proofUpdates += count
					now = quiescenceClock(t, f.s)
					margin = renewedMargin
					t.Logf("step=%d authenticated selective refresh count=%d cost=%s oldest proof age=%s", n, count, time.Since(refreshStarted), time.Minute-margin)
				}

				current, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
				if err != nil {
					t.Fatal(err)
				}

				t.Logf("step=%d elapsed=%s minimum proof margin=%s lease margin=%s fence margin=%s", n, time.Since(started), margin, current.LeaseUntil.Sub(now), current.Reservations[0].Fence.ValidUntil.Sub(now))
				service := "service" + strconv.Itoa(n)
				kind := managed.StepRemoveContainer

				if n == 32 {
					service = ""
					kind = managed.StepRemoveNetwork
				}

				issuedAt := time.Now()

				grant := f.step(service, kind)
				for _, proof := range f.observations {
					if margin := proof.Observation.ValidUntil.Sub(grant.Permission.IssuedAt); margin < minimumAuthorizationMargin {
						minimumAuthorizationMargin = margin
					}
				}

				if minimumAuthorizationMargin <= 0 {
					t.Fatal("provider permission crossed actual proof deadline")
				}

				t.Logf("step=%d permission cost=%s minimum actual authorization margin=%s", n, time.Since(issuedAt), minimumAuthorizationMargin)

				if err := f.s.RecordManagedProviderStepResult(t.Context(), managed.ProviderStepResult{Permission: grant.Permission, Outcome: managed.PortTerminal, ResourceID: grant.Permission.ResourceID, Evidence: bytes.Repeat([]byte{0xff}, managed.MaxEvidence), ObservedAt: quiescenceClock(t, f.s)}); err != nil {
					t.Fatal(err)
				}

				steps++
				if diagnostic == "positive-reserve" && steps == 24 {
					assertPostgresPositiveBatchReserve(t, f)

					return
				}

				if batch && !nearLimit && diagnostic == "" && (n == 0 || n == 32) {
					if n == 32 {
						refreshBatch()

						batchUpdates++
						proofUpdates += 64
					}

					f.exerciseIndividualObservation()

					proofUpdates++
				}
			}

			if diagnostic == "full-ledger-direct" {
				target, e := f.s.ReadManagedTarget(t.Context(), f.key.Key)
				if e != nil {
					t.Fatal(e)
				}

				if target.LeaseUntil.Sub(quiescenceClock(t, f.s)) < time.Minute {
					claimLease()
				}

				refreshStarted := time.Now()

				if batch {
					refreshBatch()

					batchUpdates++
					proofUpdates += 64
				} else {
					f.refreshObservations()
				}

				now := quiescenceClock(t, f.s)
				margin := f.observations[0].Observation.ValidUntil.Sub(now)
				t.Logf("direct fullest33-positive/max4096 full64 renewal cost=%s oldest_age=%s margin=%s actual minimum authorization margin=%s proof_updates=%d lease_updates=%d batches=%d", time.Since(refreshStarted), time.Minute-margin, margin, minimumAuthorizationMargin, proofUpdates, leaseUpdates, batchUpdates)

				if margin <= 0 {
					t.Fatal("direct fullest ledger renewal exceeds60s")
				}

				return
			}

			if _, err := f.s.SettleManagedRemovalAttempt(t.Context(), managed.AttemptSettlementRequest{AttemptRequest: f.request(), Disposition: managed.Removed, ProducerProfile: "ctrlplane.managed.remove.v1", VerifierID: "provider", Evidence: []byte("all 33 positive original results"), SettledAt: quiescenceClock(t, f.s)}); err != nil {
				t.Fatal(err)
			}

			settledTarget, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
			if err != nil {
				t.Fatal(err)
			}

			history, err := authority.(managed.HistoricalSettlementReader).ReadManagedHistoricalSettlement(t.Context(), managed.HistoricalSettlementRequest{Key: f.key.Key, SchemaVersion: "1", Installation: action.Installation, Authority: action.Authority, ManifestDigest: settledTarget.Manifest.SpecDigest, Seal: managed.Decimal(strconv.FormatUint(settledTarget.Seal, 10)), AttemptID: f.key.AttemptID, AttemptGeneration: "1", ProviderOperationID: f.provider, FenceDigest: settledTarget.Deletion.FenceDigest})
			if err != nil {
				t.Fatal(err)
			}

			if len(history.Steps) != 33 {
				t.Fatal("historical reader lost complete provider set")
			}

			historicalBytes, err := json.Marshal(history)
			if err != nil || len(historicalBytes) > managed.MaxAttemptSettlementResponse {
				t.Fatal("full historical reader capacity", len(historicalBytes), err)
			}

			if err = history.Validate(); err != nil {
				t.Fatal(err)
			}

			t.Logf("public full33 historical reader bytes=%d each positive evidence=%d", len(historicalBytes), managed.MaxEvidence)

			for _, rid := range f.reservations {
				target, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
				if err != nil {
					t.Fatal(err)
				}

				if target.LeaseUntil.Sub(quiescenceClock(t, f.s)) < time.Minute {
					claimLease()
				}

				grant := f.command(rid, managed.AttemptFinish)
				if _, err := f.s.RecordManagedAttemptFinish(t.Context(), managed.AttemptFinishRequest{AttemptRequest: f.request(), ReservationID: rid, Acceptance: f.acceptance(grant.Command)}); err != nil {
					t.Fatal(err)
				}
			}

			if _, err := f.s.TombstoneManagedRemovalAttempt(t.Context(), f.request()); err != nil {
				t.Fatal(err)
			}

			record, err := f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
			if err != nil {
				t.Fatal(err)
			}

			for n, mask := range record.State.BindingSlots {
				if mask != 1|4|8|32 {
					t.Fatalf("binding %d incomplete: %d", n, mask)
				}
			}

			for n, mask := range record.State.ProviderSlots {
				if mask != 1|2 {
					t.Fatalf("provider %d incomplete: %d", n, mask)
				}
			}

			target, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
			if err != nil || target.Phase != managed.Deleted {
				t.Fatal("not terminal", err)
			}

			raw, err := json.Marshal(target)
			if err != nil {
				t.Fatal(err)
			}

			state, err := json.Marshal(record.State)
			if err != nil {
				t.Fatal(err)
			}

			t.Logf("actual 64 binding / %d provider completion, target_bytes=%d state_bytes=%d parent_revision=%d attempt_revision=%s proof_updates=%d lease_updates=%d elapsed=%s", steps, len(raw), len(state), target.Revision, record.State.Revision, proofUpdates, leaseUpdates, time.Since(started))

			attemptRevision, err := record.State.Revision.Uint64()
			if err != nil || attemptRevision == 0 {
				t.Fatal("invalid final attempt counter", err)
			}

			t.Logf("parent delta=%d required lifecycle=293 optional proofs=%d leases=%d; attempt delta=%d initial=1 final=%d; unused parent headroom=%d minimum actual authorization margin=%s", target.Revision-initialRevision, proofUpdates, leaseUpdates, attemptRevision-1, attemptRevision, uint64(1<<63-1)-target.Revision, minimumAuthorizationMargin)

			if target.Revision-initialRevision != uint64(293+proofUpdates+leaseUpdates+batchUpdates) {
				t.Fatal("unaccounted parent transition")
			}

			if batch {
				f.verifyBatchHistory()

				individualUpdates := proofUpdates - 64*batchUpdates

				renewals.complete(t)

				if batchUpdates != renewals.batches || leaseUpdates != renewals.leases || target.Revision-initialRevision != uint64(293+individualUpdates+65*batchUpdates+leaseUpdates) {
					t.Fatal("batch renewal accounting mismatch")
				}

				t.Logf("finite batch envelope headroom=%d Bmax=%d Lmax=%d U=%d B=%d L=%d", batchCompletionHeadroom, renewals.maxBatches, renewals.maxLeases, individualUpdates, batchUpdates, leaseUpdates)
				t.Logf("batch aggregates=%d actual parent delta=%d attempt delta=%d", batchUpdates, target.Revision-initialRevision, attemptRevision-1)

				if attemptRevision-1 != 292 {
					t.Fatal("unaccounted attempt transition")
				}
			}

			if nearLimit {
				t.Logf("near-limit initial terminal reserve=489 batch=%t initial_headroom=%d parent_delta=%d signed_headroom=%d", batch, uint64(1<<63-1)-initialRevision, target.Revision-initialRevision, uint64(1<<63-1)-target.Revision)
			}
		})
	}
}

func TestManagedAttemptFiniteValidationProfile(t *testing.T) {
	if os.Getenv("CTRLPLANE_ATTEMPT_PROFILE") != "1" {
		t.Skip("opt-in development profiling; full finite-set qualification runs separately")
	}

	started := time.Now()

	f, _ := removalActionFixture(t, controllerStore(t, "memory"), largeRemovalAction(t), 2)
	for _, rid := range f.reservations {
		f.fence(rid)
	}

	setup := time.Since(started)

	for range 3 {
		record, err := f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
		if err != nil {
			t.Fatal(err)
		}

		if len(record.Descriptor.ReservationIDs) != 64 {
			t.Fatal("lost full set")
		}
	}

	t.Logf("64-binding setup=%s, three fully validated reads=%s", setup, time.Since(started)-setup)
}

func TestManagedAttemptOriginalFenceExpiry(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			for _, lateComplete := range []bool{false, true} {
				t.Run(strconv.FormatBool(lateComplete), func(t *testing.T) {
					f, _ := mixedRemovalFixture(t, backend)

					deadline := quiescenceClock(t, f.s).Add(3 * time.Second)
					for _, rid := range f.reservations {
						grant := f.command(rid, managed.AttemptBegin)

						proof := []byte("short actual original fence")
						if _, err := f.s.RecordManagedAttemptFence(t.Context(), managed.AttemptFenceRequest{AttemptRequest: f.request(), ReservationID: rid, Acceptance: f.acceptance(grant.Command), Fence: managed.FenceWire(managed.Fence{Epoch: 1, Evidence: proof, Digest: managed.Digest(proof), CompatibilityEvidence: []byte("retired"), ValidUntil: deadline, Survivors: []string{"off-instance"}})}); err != nil {
							t.Fatal(err)
						}
					}

					if _, err := f.s.AdmitManagedRemovalAttempt(t.Context(), managed.AttemptAdmissionRequest{AttemptRequest: f.request(), VerifierID: "provider"}); err != nil {
						t.Fatal(err)
					}

					first := f.step("api", managed.StepRemoveContainer)
					positive := func(grant managed.ProviderStepGrant) {
						t.Helper()

						if err := f.s.RecordManagedProviderStepResult(t.Context(), managed.ProviderStepResult{Permission: grant.Permission, Outcome: managed.PortTerminal, ResourceID: grant.Permission.ResourceID, Evidence: []byte("exact original positive"), ObservedAt: quiescenceClock(t, f.s)}); err != nil {
							t.Fatal(err)
						}
					}
					positive(first)

					var last managed.ProviderStepGrant

					if lateComplete {
						positive(f.step("sidecar", managed.StepRemoveContainer))
						last = f.step("", managed.StepRemoveNetwork)
					}

					if wait := deadline.Sub(quiescenceClock(t, f.s)) + 10*time.Millisecond; wait > 0 {
						time.Sleep(wait)
					}

					f.refreshObservations()
					now := quiescenceClock(t, f.s)
					t.Logf("actual original fence expired by=%s, renewed oldest quiescence age=%s", now.Sub(deadline), now.Sub(f.observations[0].Observation.ObservedAt))

					if !now.After(deadline) {
						t.Fatal("fixture did not cross real fence expiry")
					}

					if !lateComplete {
						before, err := f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
						if err != nil {
							t.Fatal(err)
						}

						q := managed.ProviderStepAuthorization{Command: f.request().Command, Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: f.key.Key, ActionID: f.s.action.ID}, ProviderOperationID: f.provider, Kind: managed.StepRemoveContainer, Service: "sidecar"}}
						if _, err = f.s.AuthorizeManagedProviderStep(t.Context(), q); err == nil {
							t.Fatal("fresh quiescence renewed original fence authority")
						}

						if _, err = f.s.SettleManagedRemovalAttempt(t.Context(), managed.AttemptSettlementRequest{AttemptRequest: f.request(), Disposition: managed.Removed, ProducerProfile: "ctrlplane.managed.remove.v1", VerifierID: "provider", Evidence: []byte("incomplete cannot settle"), SettledAt: now}); err == nil {
							t.Fatal("partial deletion invented completion")
						}

						if _, err = f.s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err == nil {
							t.Fatal("partial positive deletion became NotIssued")
						}

						after, err := f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
						if err != nil || !reflect.DeepEqual(before, after) {
							t.Fatal("expiry refusal mutated attempt", err)
						}

						return
					}

					positive(last)

					if _, err := f.s.SettleManagedRemovalAttempt(t.Context(), managed.AttemptSettlementRequest{AttemptRequest: f.request(), Disposition: managed.Removed, ProducerProfile: "ctrlplane.managed.remove.v1", VerifierID: "provider", Evidence: []byte("late all-positive completion"), SettledAt: now}); err != nil {
						t.Fatal(err)
					}

					for _, rid := range f.reservations {
						grant := f.command(rid, managed.AttemptFinish)
						if _, err := f.s.RecordManagedAttemptFinish(t.Context(), managed.AttemptFinishRequest{AttemptRequest: f.request(), ReservationID: rid, Acceptance: f.acceptance(grant.Command)}); err != nil {
							t.Fatal(err)
						}
					}

					if _, err := f.s.TombstoneManagedRemovalAttempt(t.Context(), f.request()); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}

func TestManagedAttemptUncertaintyReplayPreservesLatest(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newRemovalFixture(t, backend)
			f.admit()
			grant := f.step("api", managed.StepRemoveContainer)
			first := managed.ProviderStepResult{Permission: grant.Permission, Outcome: managed.PortUnavailable, Evidence: []byte("first uncertainty"), ObservedAt: quiescenceClock(t, f.s)}
			second := first
			second.Evidence = []byte("later uncertainty")

			second.ObservedAt = quiescenceClock(t, f.s)
			for _, q := range []managed.ProviderStepResult{first, second, first} {
				if err := f.s.RecordManagedProviderStepResult(t.Context(), q); err != nil {
					t.Fatal(err)
				}
			}

			current, err := f.s.ReadManagedProviderStep(t.Context(), grant.Permission.Step)
			if err != nil || current.Result == nil || !reflect.DeepEqual(*current.Result, second) {
				t.Fatal("replay regressed latest uncertainty", err)
			}
		})
	}
}

func TestManagedAttemptRecursiveWireCounters(t *testing.T) {
	maximum := managed.Decimal("9223372036854775807")
	key := managed.Key{TenantID: "tenant", InstanceID: id.New(id.PrefixInstance)}
	operation := id.New(managed.PrefixOperation)
	rid := id.New(managed.PrefixReservation)
	digest := strings.Repeat("a", 64)
	stamp := time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC)
	request := managed.AttemptRequest{Command: managed.Command{Key: key, ID: operation, Revision: 1<<63 - 1, LeaseEpoch: 1<<63 - 1, Owner: "controller"}, AttemptID: operation, Generation: "1"}
	binding := managed.AttemptBindingKey{AttemptKey: managed.AttemptKey{Key: key, AttemptID: operation, Generation: "1"}, ReservationID: rid}
	ref := managed.AttemptRef{AttemptID: operation, Generation: "1", DescriptorDigest: digest}
	native := managed.PortCommand{SchemaVersion: "1", OperationID: operation, RequestID: "request", Original: []byte("original"), OriginalDigest: digest, Native: []byte("native"), NativeDigest: digest}
	receipt := managed.AttemptReceipt{Receipt: managed.Receipt{ID: operation, Digest: digest, Revision: 1<<63 - 1, LeaseEpoch: 1<<63 - 1, AcceptedAt: stamp, LeaseUntil: stamp}, AttemptID: operation, Generation: "1", ProviderOperationID: operation, DescriptorDigest: digest}
	command := managed.AttemptCommandRecord{SchemaVersion: "1", Key: managed.AttemptCommandKey{AttemptBindingKey: binding, Kind: managed.AttemptBegin}, DescriptorDigest: digest, BindingReference: managed.AttemptBindingReference{ReservationID: rid, ReservationVersion: maximum, OriginalRegistrationOperationID: operation, OriginalRegistrationDigest: digest, ProcessDigest: digest}, ExpectedNativeVersion: maximum, NativeCommand: native, AuthorizationID: operation, AuthorizationDigest: digest, IssuedAt: stamp}
	acceptance := managed.AttemptNativeAcceptance{NativeAcceptance: managed.AttemptNativeFacts{RequestID: "request", RequestDigest: digest, CommandDigest: digest, ResponseVersion: maximum, BindingVersion: maximum, RemovalEpoch: maximum, AcceptedAt: stamp}, NativeFenceDigest: digest, ReceiptID: "receipt", Receipt: []byte("receipt"), ReceiptDigest: digest, OriginalBindingProof: []byte("proof"), ProofDigest: digest}
	fence := managed.AttemptFenceRecord{LocalOperationID: operation, SchemaVersion: "1", Key: binding, DescriptorDigest: digest, CommandDigest: digest, AttemptNativeAcceptance: acceptance, Fence: managed.WireFence{Epoch: maximum, Evidence: []byte("evidence"), Digest: digest, ValidUntil: stamp, CompatibilityEvidence: []byte("compatibility"), Survivors: []string{"survivor"}}}
	terminal := managed.AttemptTerminalRecord{LocalOperationID: operation, SchemaVersion: "1", Key: binding, DescriptorDigest: digest, CommandDigest: digest, FenceFactDigest: digest, AttemptNativeAcceptance: acceptance, ProofVerifiedAt: stamp, ProofValidUntil: stamp, SettlementWitnessDigest: digest}
	witness := managed.AttemptBindingSettlement{LocalOperationID: operation, SchemaVersion: "1", Key: binding, DescriptorDigest: digest, FenceFactDigest: digest, NativeFenceDigest: digest, RevocationDigest: digest, SettlementDigest: digest, CoverageDigest: digest, Disposition: managed.NotIssued, WitnessedAt: stamp, ParentRevision: maximum}
	admission := managed.AttemptAdmission{LocalOperationID: operation, SchemaVersion: "1", AttemptRef: ref, ProviderOperationID: operation, FenceSetDigest: digest, RegistrationSetDigest: digest, ResourcesDigest: digest, VerifierID: "verifier", AcceptedAt: stamp, ParentRevision: maximum}
	revocation := managed.AttemptRevocation{LocalOperationID: operation, SchemaVersion: "1", AttemptRef: ref, ProviderOperationID: operation, CoverageDigest: digest, Disposition: managed.NotIssued, SettlementDigest: digest, RevokedAt: stamp, ParentRevision: maximum}
	descriptor := managed.AttemptDescriptor{SchemaVersion: "1", Key: key, AttemptID: operation, Generation: "1", ActionID: operation, ProjectionActionID: operation, ProviderOperationID: operation, IdentityDigest: digest, ManifestDigest: digest, Seal: maximum, RegistrationSetDigest: digest, ReservationIDs: []id.ID{rid}, ResourcesDigest: digest, VerifierID: "verifier", AcceptedAt: stamp}
	permission := managed.ProviderStepPermission{Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: key, ActionID: operation}, ProviderOperationID: operation, Kind: managed.StepRemoveContainer, Service: "api"}, CommandID: operation, Digest: digest, SpecDigest: digest, CreateProfile: "ctrlplane.managed.create.v1", Physical: managed.Physical{Provider: "docker", Placement: "local", Project: "project", Generation: "generation"}, Seal: 1<<63 - 1, FenceDigest: digest, ResourceID: "resource", IssuedAt: stamp, Revision: 1<<63 - 1}
	// Measure the complete finite codec envelopes with maximum legal field widths.
	// Public-authority acceptance of the payload bounds is exercised separately.
	maximumToken := strings.Repeat("x", 256)
	maximumEvidence := bytes.Repeat([]byte{0xff}, managed.MaxEvidence)
	key.TenantID = maximumToken
	request.Key = key
	request.Owner = maximumToken
	binding.Key = key
	native.RequestID = maximumToken
	native.Native = maximumEvidence
	native.Original = maximumEvidence
	command.Key.AttemptBindingKey = binding
	command.NativeCommand = native
	acceptance.NativeAcceptance.RequestID = maximumToken
	acceptance.ReceiptID = maximumToken
	acceptance.Receipt = maximumEvidence
	acceptance.OriginalBindingProof = maximumEvidence
	fence.Key = binding
	fence.AttemptNativeAcceptance = acceptance
	fence.Fence.Evidence = maximumEvidence
	fence.Fence.CompatibilityEvidence = maximumEvidence
	fence.Fence.Survivors = nil
	descriptor.Key = key
	descriptor.VerifierID = maximumToken
	descriptor.ReservationIDs = nil

	for n := range 64 {
		fence.Fence.Survivors = append(fence.Fence.Survivors, strings.Repeat("s", 253)+fmt.Sprintf("%03d", n))
		descriptor.ReservationIDs = append(descriptor.ReservationIDs, id.New(managed.PrefixReservation))
	}

	terminal.Key = binding
	terminal.AttemptNativeAcceptance = acceptance
	witness.Key = binding
	witness.Disposition = managed.SettledWithoutDeletion
	admission.VerifierID = maximumToken
	permission.Step.Key = key
	permission.Step.Service = maximumToken
	permission.Physical = managed.Physical{Provider: maximumToken, Placement: maximumToken, Project: maximumToken, Generation: maximumToken}
	permission.ResourceID = maximumToken

	stepReferences := make([]string, 33)
	for n := range stepReferences {
		stepReferences[n] = digest
	}

	settlement := managed.AttemptSettlement{LocalOperationID: operation, SchemaVersion: "1", AttemptRef: ref, ProviderOperationID: operation, ResourcesDigest: digest, AdmissionDigest: digest, RevocationDigest: digest, Disposition: managed.SettledWithoutDeletion, StepReferences: stepReferences, CoverageDigest: digest, ProducerProfile: managed.LocalNoDispatchProfile, VerifierID: maximumToken, Evidence: maximumEvidence, EvidenceDigest: digest, SettledAt: stamp}
	abortCommand := command
	abortCommand.Key.Kind = managed.AttemptAbort
	cases := []any{request, managed.AttemptStartRequest{Command: request.Command, ActionID: operation, AttemptID: operation, ProviderOperationID: operation, VerifierID: "verifier"}, managed.AttemptCommandRequest{AttemptRequest: request, ReservationID: rid, Kind: managed.AttemptBegin, ExpectedNativeVersion: maximum, NativeCommand: native}, managed.AttemptRecoveryRequest{AttemptRequest: request, ReservationID: rid, Kind: managed.AttemptBegin, CommandDigest: digest}, managed.AttemptFenceRequest{AttemptRequest: request, ReservationID: rid, Acceptance: acceptance, Fence: fence.Fence}, managed.AttemptAdmissionRequest{AttemptRequest: request, VerifierID: "verifier"}, managed.AttemptSettlementRequest{AttemptRequest: request, Disposition: managed.Removed, ProducerProfile: "profile", VerifierID: "verifier", Evidence: []byte("evidence"), SettledAt: stamp}, managed.AttemptBindingSettlementRequest{AttemptRequest: request, ReservationID: rid, FenceFactDigest: digest}, managed.AttemptAbortRequest{AttemptRequest: request, ReservationID: rid, Acceptance: acceptance, ProofVerifiedAt: stamp, ProofValidUntil: stamp}, managed.AttemptFinishRequest{AttemptRequest: request, ReservationID: rid, Acceptance: acceptance}, receipt, command, managed.AttemptCommandGrant{Receipt: receipt, Command: command, NewlyIssued: true}, managed.AttemptRecoveryGrant{Receipt: receipt, Command: command, NewlyAuthorized: true}, managed.AttemptRecoveryInvocation{Grant: managed.AttemptRecoveryGrant{Receipt: receipt, Command: command, NewlyAuthorized: true}}, fence, terminal, witness, admission, revocation, managed.AttemptRecord{Descriptor: descriptor, State: managed.AttemptState{BindingSlots: make([]byte, 64), ProviderSlots: make([]byte, 33), SchemaVersion: "1", AttemptRef: ref, Revision: maximum, Phase: "settled", AdmissionDigest: digest, ProviderIssuedAt: &stamp, RevocationDigest: digest, SettlementDigest: digest}, Receipt: receipt}, managed.AttemptBindingRecord{Key: binding, DescriptorDigest: digest, Commands: []managed.AttemptCommandRecord{command, abortCommand}, Fence: &fence, Abort: &terminal, Witness: &witness}, settlement, managed.AttemptHistoricalSettlement{Request: managed.AttemptSettlementReadRequest{AttemptBindingKey: binding, ProviderOperationID: operation, NativeFenceDigest: digest}, AttemptRef: ref, Admission: &admission, Revocation: &revocation, Settlement: &settlement, Witness: witness, StepReferences: stepReferences, ResourcesDigest: digest}, managed.ProviderStepFailure{SchemaVersion: "1", Permission: permission, AttemptRef: ref, InvocationID: operation, ProducerProfile: managed.LocalNoDispatchProfile, VerifierID: maximumToken, Class: "local_no_dispatch", Phase: "before_transport_delegate", Reason: "request_binding_mismatch", ObservedAt: stamp, Witness: maximumEvidence, WitnessDigest: digest}}

	legacy := managed.HistoricalSettlement{Request: managed.HistoricalSettlementRequest{Key: key, SchemaVersion: "1", Installation: maximumToken, Authority: maximumToken, ManifestDigest: digest, Seal: maximum, AttemptID: operation, AttemptGeneration: "1", ProviderOperationID: operation, FenceDigest: digest}, Physical: permission.Physical, Resources: map[string]string{}, Disposition: managed.Removed, Settlement: managed.Settlement{Disposition: managed.Removed, Evidence: maximumEvidence, Verifier: maximumToken, SettledAt: stamp}, Receipt: managed.ReceiptWire(receipt.Receipt)}
	for n := range 33 {
		stepPermission := permission

		stepPermission.Step.Service = strings.Repeat("s", 253) + fmt.Sprintf("%03d", n)
		if n == 32 {
			stepPermission.Step.Service = ""
			stepPermission.Step.Kind = managed.StepRemoveNetwork
		}

		resourceKey := stepPermission.Step.Service
		if resourceKey == "" {
			resourceKey = "network"
		}

		legacy.Resources[resourceKey] = maximumToken
		positive := managed.ProviderStepResult{Permission: stepPermission, Outcome: managed.PortTerminal, ResourceID: maximumToken, Evidence: maximumEvidence, ObservedAt: stamp, Diagnostic: maximumToken}
		legacy.Steps = append(legacy.Steps, managed.ProviderStepWire(managed.ProviderStepRecord{Permission: stepPermission, Result: &positive}))
	}

	if err := legacy.Validate(); err != nil {
		t.Fatal("maximum full33 historical codec invalid", err)
	}

	cases = append(cases, legacy)
	limits := map[string]int{"HistoricalSettlement": managed.MaxAttemptSettlementResponse, "AttemptDescriptor": managed.MaxAttemptFact, "AttemptReceipt": managed.MaxAttemptFact, "AttemptCommandRecord": managed.MaxAttemptCommand, "AttemptCommandGrant": managed.MaxAttemptBindingResponse, "AttemptRecoveryGrant": managed.MaxAttemptBindingResponse, "AttemptRecoveryInvocation": managed.MaxAttemptBindingResponse, "AttemptFenceRecord": managed.MaxAttemptFence, "AttemptTerminalRecord": managed.MaxAttemptCommand, "AttemptBindingSettlement": managed.MaxAttemptFact, "AttemptAdmission": managed.MaxAttemptFact, "AttemptRevocation": managed.MaxAttemptFact, "AttemptSettlement": managed.MaxAttemptSettlement, "AttemptRecord": managed.MaxAttemptBindingResponse, "AttemptBindingRecord": managed.MaxAttemptBindingResponse, "AttemptHistoricalSettlement": managed.MaxAttemptBindingResponse, "ProviderStepFailure": managed.MaxAttemptSettlement}

	cases = append(cases, descriptor)

	var walk func(any)

	walk = func(value any) {
		switch v := value.(type) {
		case json.Number:
			t.Fatalf("new nested encoding leaked numeric counter %s", v)
		case map[string]any:
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	for _, value := range cases {
		t.Run(reflect.TypeOf(value).Name(), func(t *testing.T) {
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}

			var document any

			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()

			if err = decoder.Decode(&document); err != nil {
				t.Fatal(err)
			}

			if limit, bounded := limits[reflect.TypeOf(value).Name()]; bounded {
				if len(raw) > limit {
					t.Fatalf("actual codec bytes %d exceed cap %d", len(raw), limit)
				}

				t.Logf("actual maximum codec bytes=%d cap=%d", len(raw), limit)
			}

			walk(document)

			target := reflect.New(reflect.TypeOf(value))
			if err = json.Unmarshal(raw, target.Interface()); err != nil {
				t.Fatal("accepted-byte roundtrip", err)
			}

			again, err := json.Marshal(target.Elem().Interface())
			if err != nil || !bytes.Equal(raw, again) {
				t.Fatal("accepted bytes changed", err)
			}
		})
	}

	oldReceipt, err := json.Marshal(receipt.Receipt)
	if err != nil {
		t.Fatal(err)
	}

	oldPermission, err := json.Marshal(permission)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Contains(oldReceipt, []byte(`"revision":9223372036854775807`)) || !bytes.Contains(oldPermission, []byte(`"seal":9223372036854775807`)) {
		t.Fatal("existing A or positive-provider counter encoding changed")
	}
}

func attemptPostgresSnapshot(t *testing.T, s *postgres.Store) []string {
	t.Helper()

	pg := pgdriver.Unwrap(s.DB())
	out := []string{}

	for _, table := range []string{"cp_managed_targets", "cp_managed_commands", "cp_managed_controller_records"} {
		var raw string
		if err := pg.NewRaw("SELECT COALESCE(jsonb_agg(rowdata ORDER BY rowdata::text),'[]'::jsonb)::text FROM (SELECT to_jsonb(t) AS rowdata FROM "+table+" t) x").Scan(t.Context(), &raw); err != nil {
			t.Fatal(err)
		}

		out = append(out, raw)
	}

	return out
}
func TestManagedAttemptPostgresFiniteCorruption(t *testing.T) {
	for _, scenario := range []string{"issued", "uncertainty", "failure", "removed"} {
		t.Run(scenario, func(t *testing.T) {
			s, _ := managedPG(t)
			f, _ := removalActionFixture(t, s, controllerAction(t), 1)
			f.admit()

			grant := f.step("api", managed.StepRemoveContainer)
			if scenario != "issued" {
				if err := s.RecordManagedProviderStepResult(t.Context(), managed.ProviderStepResult{Permission: grant.Permission, Outcome: managed.PortUnavailable, Evidence: []byte("uncertain original result"), ObservedAt: quiescenceClock(t, s)}); err != nil {
					t.Fatal(err)
				}
			}

			if scenario == "failure" {
				if err := s.RecordManagedProviderStepFailure(t.Context(), f.failure(grant)); err != nil {
					t.Fatal(err)
				}

				if _, err := s.SettleManagedRemovalAttempt(t.Context(), managed.AttemptSettlementRequest{AttemptRequest: f.request(), Disposition: managed.SettledWithoutDeletion, ProducerProfile: managed.LocalNoDispatchProfile, VerifierID: "provider", Evidence: []byte("qualified cutoff"), SettledAt: quiescenceClock(t, s)}); err != nil {
					t.Fatal(err)
				}
			}

			if scenario == "removed" {
				positive := func(g managed.ProviderStepGrant) {
					t.Helper()

					if err := s.RecordManagedProviderStepResult(t.Context(), managed.ProviderStepResult{Permission: g.Permission, Outcome: managed.PortTerminal, ResourceID: g.Permission.ResourceID, Evidence: []byte("positive"), ObservedAt: quiescenceClock(t, s)}); err != nil {
						t.Fatal(err)
					}
				}
				positive(grant)
				positive(f.step("sidecar", managed.StepRemoveContainer))
				positive(f.step("", managed.StepRemoveNetwork))

				if _, err := s.SettleManagedRemovalAttempt(t.Context(), managed.AttemptSettlementRequest{AttemptRequest: f.request(), Disposition: managed.Removed, ProducerProfile: "ctrlplane.managed.remove.v1", VerifierID: "provider", Evidence: []byte("terminal"), SettledAt: quiescenceClock(t, s)}); err != nil {
					t.Fatal(err)
				}

				for _, rid := range f.reservations {
					finish := f.command(rid, managed.AttemptFinish)
					if _, err := s.RecordManagedAttemptFinish(t.Context(), managed.AttemptFinishRequest{AttemptRequest: f.request(), ReservationID: rid, Acceptance: f.acceptance(finish.Command)}); err != nil {
						t.Fatal(err)
					}
				}
			}

			pg := pgdriver.Unwrap(s.DB())
			refuse := func() {
				t.Helper()

				before := attemptPostgresSnapshot(t, s)
				if _, err := s.ReadManagedRemovalAttempt(t.Context(), f.key); err == nil {
					t.Fatal("corrupt full-set read accepted")
				}

				if _, err := s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err == nil {
					t.Fatal("corrupt provider slot became unissued")
				}

				if after := attemptPostgresSnapshot(t, s); !reflect.DeepEqual(before, after) {
					t.Fatal("corruption repaired or mutated state")
				}
			}

			var encoded string
			if err := pg.NewRaw("SELECT jsonb_agg(jsonb_build_object('kind',kind,'scope',scope,'key',key,'data',encode(data,'base64')))::text FROM cp_managed_controller_records WHERE kind LIKE 'removal_%' OR kind IN ('step','step_history','step_failure')").Scan(t.Context(), &encoded); err != nil {
				t.Fatal(err)
			}

			var rows []struct {
				Kind  string `json:"kind"`
				Scope string `json:"scope"`
				Key   string `json:"key"`
				Data  []byte `json:"data"`
			}
			if err := json.Unmarshal([]byte(encoded), &rows); err != nil {
				t.Fatal(err)
			}

			for _, row := range rows {
				t.Run("missing/"+row.Kind+"/"+row.Key, func(t *testing.T) {
					if _, err := pg.Exec(t.Context(), "DELETE FROM cp_managed_controller_records WHERE kind=$1 AND scope=$2 AND key=$3", row.Kind, row.Scope, row.Key); err != nil {
						t.Fatal(err)
					}
					defer func() {
						if _, err := pg.Exec(t.Context(), "INSERT INTO cp_managed_controller_records(kind,scope,key,data) VALUES($1,$2,$3,$4)", row.Kind, row.Scope, row.Key, row.Data); err != nil {
							t.Fatal(err)
						}
					}()

					refuse()
				})
			}

			for _, row := range rows {
				if row.Kind != "removal_attempt_state" {
					continue
				}

				for _, field := range []string{"binding", "provider"} {
					limit := 7
					if field == "provider" {
						limit = 4
					}

					for bit := range limit {
						t.Run(field+"/toggle/"+strconv.Itoa(bit), func(t *testing.T) {
							var state managed.AttemptState
							if err := json.Unmarshal(row.Data, &state); err != nil {
								t.Fatal(err)
							}

							if field == "binding" {
								state.BindingSlots[0] ^= 1 << bit
							} else {
								state.ProviderSlots[0] ^= 1 << bit
							}

							raw, err := json.Marshal(state)
							if err != nil {
								t.Fatal(err)
							}

							if _, err = pg.Exec(t.Context(), "UPDATE cp_managed_controller_records SET data=$1 WHERE kind=$2 AND scope=$3 AND key=$4", raw, row.Kind, row.Scope, row.Key); err != nil {
								t.Fatal(err)
							}

							defer func() {
								if _, e := pg.Exec(t.Context(), "UPDATE cp_managed_controller_records SET data=$1 WHERE kind=$2 AND scope=$3 AND key=$4", row.Data, row.Kind, row.Scope, row.Key); e != nil {
									t.Fatal(e)
								}
							}()

							refuse()
						})
					}
				}
			}

			required := map[string]bool{}

			for _, row := range rows {
				if row.Kind == "removal_attempt_event" {
					required[row.Key] = true
				}

				if row.Kind == "removal_provider_slot" {
					var slot struct {
						Receipt managed.WireReceipt `json:"receipt"`
					}
					if err := json.Unmarshal(row.Data, &slot); err != nil {
						t.Fatal(err)
					}

					required[slot.Receipt.ID.String()] = true
				}
			}

			for operation := range required {
				t.Run("receipt/"+operation, func(t *testing.T) {
					var raw string
					if err := pg.NewRaw("SELECT receipt::text FROM cp_managed_commands WHERE target_id=$1 AND operation_id=$2", f.key.InstanceID.String(), operation).Scan(t.Context(), &raw); err != nil {
						t.Fatal(err)
					}

					if _, err := pg.Exec(t.Context(), "DELETE FROM cp_managed_commands WHERE target_id=$1 AND operation_id=$2", f.key.InstanceID.String(), operation); err != nil {
						t.Fatal(err)
					}
					defer func() {
						if _, err := pg.Exec(t.Context(), "INSERT INTO cp_managed_commands(target_id,operation_id,receipt) VALUES($1,$2,$3::jsonb)", f.key.InstanceID.String(), operation, raw); err != nil {
							t.Fatal(err)
						}
					}()

					refuse()
				})
			}
		})
	}
}

func attemptCommandInput(f *removalFixture, rid id.ID, kind string) managed.AttemptCommandRequest {
	raw := []byte("atomic original " + kind)

	return managed.AttemptCommandRequest{AttemptRequest: f.request(), ReservationID: rid, Kind: kind, ExpectedNativeVersion: "1", NativeCommand: managed.PortCommand{SchemaVersion: "1", OperationID: id.New(managed.PrefixOperation), RequestID: "request-" + kind, Original: raw, OriginalDigest: managed.Digest(raw), Native: raw, NativeDigest: managed.Digest(raw)}}
}
func attemptPostgresMutation(t *testing.T, kind string) (*postgres.Store, func() error, []string) {
	t.Helper()
	s, _ := managedPG(t)
	f, parent := removalActionSetup(t, s, controllerAction(t), 1, kind != "start")
	common := []string{"removal_attempt_state", "removal_attempt_event", "@target", "@receipt"}
	finish := func(call func() (managed.Receipt, error), row string) (*postgres.Store, func() error, []string) {
		return s, func() error {
			_, err := call()

			return err
		}, append([]string{row}, common...)
	}

	if kind == "start" {
		q := managed.AttemptStartRequest{Command: parent.command(), ActionID: f.s.action.ID, AttemptID: id.New(managed.PrefixOperation), ProviderOperationID: id.New(managed.PrefixOperation), VerifierID: "provider"}

		return s, func() error {
			_, err := s.BeginManagedRemovalAttempt(t.Context(), q)

			return err
		}, append([]string{"removal_attempt", "removal_attempt_root", "removal_attempt_current", "removal_identity", "operation"}, common...)
	}

	rid := f.reservations[0]
	if kind == "begin-command" {
		q := attemptCommandInput(f, rid, managed.AttemptBegin)

		return s, func() error {
			_, err := s.AuthorizeManagedAttemptCommand(t.Context(), q)

			return err
		}, append([]string{"removal_command"}, common...)
	}

	if kind == "fence" {
		grant := f.command(rid, managed.AttemptBegin)
		raw := []byte("fence")
		q := managed.AttemptFenceRequest{AttemptRequest: f.request(), ReservationID: rid, Acceptance: f.acceptance(grant.Command), Fence: managed.FenceWire(managed.Fence{Epoch: 1, Evidence: raw, Digest: managed.Digest(raw), CompatibilityEvidence: raw, ValidUntil: quiescenceClock(t, s).Add(time.Hour), Survivors: []string{"off-instance"}})}

		return finish(func() (managed.Receipt, error) { return s.RecordManagedAttemptFence(t.Context(), q) }, "removal_fence")
	}

	for _, binding := range f.reservations {
		f.fence(binding)
	}

	if kind == "admit" {
		q := managed.AttemptAdmissionRequest{AttemptRequest: f.request(), VerifierID: "provider"}

		return finish(func() (managed.Receipt, error) { return s.AdmitManagedRemovalAttempt(t.Context(), q) }, "removal_admission")
	}

	if kind == "recovery" {
		command, err := s.ReadManagedAttemptCommand(t.Context(), managed.AttemptCommandKey{AttemptBindingKey: managed.AttemptBindingKey{AttemptKey: f.key, ReservationID: rid}, Kind: managed.AttemptBegin})
		if err != nil {
			t.Fatal(err)
		}

		raw, err := json.Marshal(command)
		if err != nil {
			t.Fatal(err)
		}

		q := managed.AttemptRecoveryRequest{AttemptRequest: f.request(), ReservationID: rid, Kind: managed.AttemptBegin, CommandDigest: managed.Digest(append([]byte("ctrlplane.managed.attempt-command.v1\n"), raw...))}

		return s, func() error {
			_, err := s.AuthorizeManagedAttemptRecovery(t.Context(), q)

			return err
		}, []string{"removal_attempt_event", "@target", "@receipt"}
	}

	if kind == "witness" || kind == "abort-command" || kind == "abort" {
		if _, err := s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
			t.Fatal(err)
		}

		binding, err := s.ReadManagedAttemptBinding(t.Context(), managed.AttemptBindingKey{AttemptKey: f.key, ReservationID: rid})
		if err != nil {
			t.Fatal(err)
		}

		witness := managed.AttemptBindingSettlementRequest{AttemptRequest: f.request(), ReservationID: rid, FenceFactDigest: fixtureAttemptFact(t, "removal_fence", *binding.Fence)}

		if kind == "witness" {
			return finish(func() (managed.Receipt, error) { return s.RecordManagedAttemptBindingSettlement(t.Context(), witness) }, "removal_binding_settlement")
		}

		if _, err = s.RecordManagedAttemptBindingSettlement(t.Context(), witness); err != nil {
			t.Fatal(err)
		}

		if kind == "abort-command" {
			q := attemptCommandInput(f, rid, managed.AttemptAbort)

			return s, func() error {
				_, e := s.AuthorizeManagedAttemptCommand(t.Context(), q)

				return e
			}, append([]string{"removal_command"}, common...)
		}

		grant := f.command(rid, managed.AttemptAbort)
		now := quiescenceClock(t, s)
		q := managed.AttemptAbortRequest{AttemptRequest: f.request(), ReservationID: rid, Acceptance: f.acceptance(grant.Command), ProofVerifiedAt: now, ProofValidUntil: now.Add(time.Minute)}

		return finish(func() (managed.Receipt, error) { return s.RecordManagedAttemptAbort(t.Context(), q) }, "removal_abort")
	}

	if _, err := s.AdmitManagedRemovalAttempt(t.Context(), managed.AttemptAdmissionRequest{AttemptRequest: f.request(), VerifierID: "provider"}); err != nil {
		t.Fatal(err)
	}

	if kind == "permission" {
		q := managed.ProviderStepAuthorization{Command: f.request().Command, Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: f.key.Key, ActionID: f.s.action.ID}, ProviderOperationID: f.provider, Kind: managed.StepRemoveContainer, Service: "api"}}

		return s, func() error {
			_, err := s.AuthorizeManagedProviderStep(t.Context(), q)

			return err
		}, []string{"step", "removal_provider_slot", "removal_attempt_state", "@target", "@receipt"}
	}

	grant := f.step("api", managed.StepRemoveContainer)

	if kind == "positive" || kind == "uncertainty" {
		outcome := managed.PortTerminal
		if kind == "uncertainty" {
			outcome = managed.PortUnavailable
		}

		q := managed.ProviderStepResult{Permission: grant.Permission, Outcome: outcome, ResourceID: grant.Permission.ResourceID, Evidence: []byte("exact response"), ObservedAt: quiescenceClock(t, s)}

		return s, func() error { return s.RecordManagedProviderStepResult(t.Context(), q) }, []string{"step_history", "step", "removal_provider_slot", "removal_attempt_state"}
	}

	if kind == "failure" {
		q := f.failure(grant)

		return s, func() error { return s.RecordManagedProviderStepFailure(t.Context(), q) }, []string{"step_failure", "removal_provider_slot", "removal_attempt_state"}
	}

	if kind == "settlement-cutoff" {
		if err := s.RecordManagedProviderStepFailure(t.Context(), f.failure(grant)); err != nil {
			t.Fatal(err)
		}

		q := managed.AttemptSettlementRequest{AttemptRequest: f.request(), Disposition: managed.SettledWithoutDeletion, ProducerProfile: managed.LocalNoDispatchProfile, VerifierID: "provider", Evidence: []byte("qualified cutoff"), SettledAt: quiescenceClock(t, s)}

		return s, func() error {
			_, err := s.SettleManagedRemovalAttempt(t.Context(), q)

			return err
		}, append([]string{"removal_settlement", "removal_revocation"}, common...)
	}

	positive := func(g managed.ProviderStepGrant) {
		t.Helper()

		if err := s.RecordManagedProviderStepResult(t.Context(), managed.ProviderStepResult{Permission: g.Permission, Outcome: managed.PortTerminal, ResourceID: g.Permission.ResourceID, Evidence: []byte("removed"), ObservedAt: quiescenceClock(t, s)}); err != nil {
			t.Fatal(err)
		}
	}
	positive(grant)
	positive(f.step("sidecar", managed.StepRemoveContainer))
	positive(f.step("", managed.StepRemoveNetwork))

	if _, err := s.SettleManagedRemovalAttempt(t.Context(), managed.AttemptSettlementRequest{AttemptRequest: f.request(), Disposition: managed.Removed, ProducerProfile: "ctrlplane.managed.remove.v1", VerifierID: "provider", Evidence: []byte("terminal"), SettledAt: quiescenceClock(t, s)}); err != nil {
		t.Fatal(err)
	}

	if kind == "finish-command" {
		q := attemptCommandInput(f, rid, managed.AttemptFinish)

		return s, func() error {
			_, err := s.AuthorizeManagedAttemptCommand(t.Context(), q)

			return err
		}, append([]string{"removal_command"}, common...)
	}

	for _, binding := range f.reservations {
		native := f.command(binding, managed.AttemptFinish)
		q := managed.AttemptFinishRequest{AttemptRequest: f.request(), ReservationID: binding, Acceptance: f.acceptance(native.Command)}

		if kind == "finish" {
			return finish(func() (managed.Receipt, error) { return s.RecordManagedAttemptFinish(t.Context(), q) }, "removal_finish")
		}

		if _, err := s.RecordManagedAttemptFinish(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}

	q := f.request()

	return s, func() error {
		_, err := s.TombstoneManagedRemovalAttempt(t.Context(), q)

		return err
	}, common
}
func TestManagedAttemptPostgresAtomicWrites(t *testing.T) {
	for _, kind := range []string{"start", "begin-command", "fence", "admit", "recovery", "witness", "abort-command", "abort", "permission", "positive", "uncertainty", "failure", "settlement-cutoff", "finish-command", "finish", "tombstone"} {
		t.Run(kind, func(t *testing.T) {
			s, run, rows := attemptPostgresMutation(t, kind)
			pg := pgdriver.Unwrap(s.DB())

			before := attemptPostgresSnapshot(t, s)
			for _, row := range rows {
				t.Run(row, func(t *testing.T) {
					table := "cp_managed_controller_records"

					condition := "NEW.kind='" + row + "'"
					if row == "@target" {
						table = "cp_managed_targets"
						condition = "true"
					}

					if row == "@receipt" {
						table = "cp_managed_commands"
						condition = "true"
					}

					query := "CREATE FUNCTION attempt_write_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF " + condition + " THEN RAISE EXCEPTION 'injected attempt write failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER attempt_write_failure AFTER INSERT OR UPDATE ON " + table + " FOR EACH ROW EXECUTE FUNCTION attempt_write_failure();"
					if _, err := pg.Exec(t.Context(), query); err != nil {
						t.Fatal(err)
					}

					defer func() {
						if _, err := pg.Exec(t.Context(), "DROP TRIGGER attempt_write_failure ON "+table+"; DROP FUNCTION attempt_write_failure()"); err != nil {
							t.Fatal(err)
						}
					}()

					if err := run(); err == nil || !strings.Contains(err.Error(), "injected attempt write failure") {
						t.Fatalf("missing failure at %s: %v", row, err)
					}

					if after := attemptPostgresSnapshot(t, s); !reflect.DeepEqual(before, after) {
						t.Fatal("write failure leaked parent, receipt or associated rows")
					}
				})
			}

			if err := run(); err != nil {
				t.Fatal("retry after rolled-back boundaries", err)
			}
		})
	}
}

func TestManagedAttemptContainingManifestBounds(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			action := largeRemovalAction(t)
			maximum := strings.Repeat("x", 256)
			action.Installation = maximum
			action.Authority = maximum
			action.ApplicationID = maximum
			action.OrganizationID = maximum
			action.Actor.Subject = maximum
			action.TenantID = maximum
			action.NewTarget.TenantID = maximum
			action.NewTarget.Physical = managed.Physical{Provider: maximum, Placement: maximum, Project: maximum, Generation: maximum}
			action.NewTarget.Manifest.Version = 1<<63 - 1
			action.OriginalCommand = bytes.Repeat([]byte("x"), 4096)

			var envelope managed.ManagedSpec
			if err := json.Unmarshal(action.NewTarget.Manifest.Spec, &envelope); err != nil {
				t.Fatal(err)
			}

			var provision managed.ProvisionSpec
			if err := json.Unmarshal(envelope.Provision, &provision); err != nil {
				t.Fatal(err)
			}

			for n := range provision.Services {
				name := strings.Repeat("s", 253) + fmt.Sprintf("%03d", n)
				member := &action.NewTarget.Manifest.Members[n]
				member.Name = name
				member.Service = name
				member.Installation = maximum
				member.Namespace = maximum
				member.Build = maximum
				member.Queue = maximum
				provision.Services[n].Name = name
				provision.Services[n].Image = strings.Repeat("i", 184) + "@sha256:" + strings.Repeat("a", 64)
				provision.Services[n].CPUMillis = "9223372036854"
				provision.Services[n].MemoryMiB = "8796093022207"
				provision.Services[n].PIDLimit = "9223372036854775807"
				provision.Services[n].Startup.BindingID = maximum
				provision.Services[n].Startup.BindingGeneration = "9223372036854775807"
			}

			raw, err := json.Marshal(provision)
			if err != nil {
				t.Fatal(err)
			}

			envelope.Provision = raw

			raw, err = json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}

			t.Logf("actual maximal fixed fields spec_bytes=%d", len(raw))
			raw = append(raw, bytes.Repeat([]byte(" "), managed.MaxManifestSpec-len(raw))...)
			action.NewTarget.Manifest.Spec = raw
			action.NewTarget.Manifest.SpecDigest = managed.Digest(raw)
			signControllerAction(t, &action)
			s := controllerStore(t, backend)
			f, parent := removalActionFixture(t, s, action, 2)

			before, err := s.ReadManagedOperation(t.Context(), managed.OperationKey{Key: action.Key, ActionID: action.ID})
			if err != nil || before.RemovalAttemptRoot == nil {
				t.Fatal("missing accepted anchor", err)
			}

			command := managed.PortCommand{SchemaVersion: "1", OperationID: id.New(managed.PrefixOperation), RequestID: maximum, Original: action.OriginalCommand, OriginalDigest: managed.Digest(action.OriginalCommand), Native: action.OriginalCommand, NativeDigest: managed.Digest(action.OriginalCommand)}
			input := managed.OperationAdvance{Command: parent.command(), ActionID: action.ID, ExpectedOperationRevision: before.Revision, Observation: managed.OperationObservation{Kind: "completed", Command: &command, Evidence: action.OriginalCommand, Diagnostic: maximum, ObservedAt: quiescenceClock(t, s)}}

			accepted, err := s.AdvanceManagedOperation(t.Context(), input)
			if err != nil {
				t.Fatal("maximum anchored containing operation", err)
			}

			encoded, err := json.Marshal(accepted)
			if err != nil {
				t.Fatal(err)
			}

			history, err := json.Marshal(struct {
				Digest string                 `json:"digest"`
				State  managed.OperationState `json:"state"`
			}{strings.Repeat("a", 64), accepted})
			if err != nil {
				t.Fatal(err)
			}

			if len(history) > managed.MaxControllerRecord {
				t.Fatal("unchanged operation history cap exceeded")
			}

			replay, err := s.AdvanceManagedOperation(t.Context(), input)
			if err != nil || !reflect.DeepEqual(replay, accepted) {
				t.Fatal("maximum anchored operation replay", err)
			}

			record, err := s.ReadManagedRemovalAttempt(t.Context(), f.key)
			if err != nil {
				t.Fatal(err)
			}

			descriptor, err := json.Marshal(record.Descriptor)
			if err != nil {
				t.Fatal(err)
			}

			t.Logf("accepted exact64KiB spec,32 maximal members/services,64 bindings: operation_bytes=%d history_bytes=%d descriptor_bytes=%d", len(encoded), len(history), len(descriptor))
		})
	}
}

func TestManagedAttemptAcceptedNativeBounds(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			action := controllerAction(t)
			maximum := strings.Repeat("x", 256)
			action.TenantID = maximum
			action.NewTarget.TenantID = maximum
			signControllerAction(t, &action)
			s := controllerStore(t, backend)
			f, parent := removalActionSetup(t, s, action, 1, false)
			start := managed.AttemptStartRequest{Command: parent.command(), ActionID: action.ID, AttemptID: id.New(managed.PrefixOperation), ProviderOperationID: id.New(managed.PrefixOperation), VerifierID: maximum}

			accepted, err := s.BeginManagedRemovalAttempt(t.Context(), start)
			if err != nil {
				t.Fatal(err)
			}

			f.key.AttemptID = accepted.AttemptID
			f.provider = accepted.ProviderOperationID
			maximumBytes := bytes.Repeat([]byte{0xff}, managed.MaxEvidence)
			maximumDigest := managed.Digest(maximumBytes)
			maximumCommand := func(rid id.ID, kind string, version managed.Decimal) managed.AttemptCommandGrant {
				t.Helper()

				q := attemptCommandInput(f, rid, kind)
				q.ExpectedNativeVersion = version
				q.NativeCommand.RequestID = maximum
				q.NativeCommand.Original = maximumBytes
				q.NativeCommand.Native = maximumBytes
				q.NativeCommand.OriginalDigest = maximumDigest
				q.NativeCommand.NativeDigest = maximumDigest

				grant, e := s.AuthorizeManagedAttemptCommand(t.Context(), q)
				if e != nil {
					t.Fatal(e)
				}

				return grant
			}
			maximumAcceptance := func(command managed.AttemptCommandRecord) managed.AttemptNativeAcceptance {
				a := f.acceptance(command)
				a.ReceiptID = maximum
				a.Receipt = maximumBytes
				a.ReceiptDigest = maximumDigest
				a.OriginalBindingProof = maximumBytes
				a.ProofDigest = maximumDigest
				a.NativeAcceptance.ResponseVersion = "9223372036854775807"
				a.NativeAcceptance.BindingVersion = "9223372036854775807"
				a.NativeAcceptance.RemovalEpoch = "9223372036854775807"

				return a
			}

			survivors := []string{}
			for n := range 64 {
				survivors = append(survivors, strings.Repeat("s", 253)+fmt.Sprintf("%03d", n))
			}

			measure := func(name string, value any, limit int) {
				t.Helper()

				raw, e := json.Marshal(value)
				if e != nil {
					t.Fatal(e)
				}

				if len(raw) > limit {
					t.Fatalf("%s actual %d exceeds %d", name, len(raw), limit)
				}

				t.Logf("publicly accepted maximal %s bytes=%d cap=%d", name, len(raw), limit)
			}

			for _, rid := range f.reservations {
				grant := maximumCommand(rid, managed.AttemptBegin, "1")
				measure("command", grant.Command, managed.MaxAttemptCommand)
				measure("grant", grant, managed.MaxAttemptBindingResponse)

				q := managed.AttemptFenceRequest{AttemptRequest: f.request(), ReservationID: rid, Acceptance: maximumAcceptance(grant.Command), Fence: managed.WireFence{Epoch: "9223372036854775807", Evidence: maximumBytes, Digest: maximumDigest, CompatibilityEvidence: maximumBytes, ValidUntil: quiescenceClock(t, s).Add(time.Hour), Survivors: survivors}}
				if _, err = s.RecordManagedAttemptFence(t.Context(), q); err != nil {
					t.Fatal(err)
				}

				binding, e := s.ReadManagedAttemptBinding(t.Context(), managed.AttemptBindingKey{AttemptKey: f.key, ReservationID: rid})
				if e != nil {
					t.Fatal(e)
				}

				measure("fence", binding.Fence, managed.MaxAttemptFence)
			}

			if _, err = s.AdmitManagedRemovalAttempt(t.Context(), managed.AttemptAdmissionRequest{AttemptRequest: f.request(), VerifierID: maximum}); err != nil {
				t.Fatal(err)
			}

			grant := f.step("api", managed.StepRemoveContainer)

			permissionBefore, err := json.Marshal(grant.Permission)
			if err != nil {
				t.Fatal(err)
			}

			failure := f.failure(grant)
			failure.VerifierID = maximum
			failure.Witness = maximumBytes

			failure.WitnessDigest = maximumDigest
			if err = s.RecordManagedProviderStepFailure(t.Context(), failure); err != nil {
				t.Fatal(err)
			}

			measure("failure", failure, managed.MaxAttemptSettlement)

			if _, err = s.SettleManagedRemovalAttempt(t.Context(), managed.AttemptSettlementRequest{AttemptRequest: f.request(), Disposition: managed.SettledWithoutDeletion, ProducerProfile: managed.LocalNoDispatchProfile, VerifierID: maximum, Evidence: maximumBytes, SettledAt: quiescenceClock(t, s)}); err != nil {
				t.Fatal(err)
			}

			for _, rid := range f.reservations {
				key := managed.AttemptBindingKey{AttemptKey: f.key, ReservationID: rid}

				binding, e := s.ReadManagedAttemptBinding(t.Context(), key)
				if e != nil {
					t.Fatal(e)
				}

				if _, e = s.RecordManagedAttemptBindingSettlement(t.Context(), managed.AttemptBindingSettlementRequest{AttemptRequest: f.request(), ReservationID: rid, FenceFactDigest: fixtureAttemptFact(t, "removal_fence", *binding.Fence)}); e != nil {
					t.Fatal(e)
				}

				command := maximumCommand(rid, managed.AttemptAbort, "9223372036854775807")
				if _, e = s.RecordManagedAttemptAbort(t.Context(), managed.AttemptAbortRequest{AttemptRequest: f.request(), ReservationID: rid, Acceptance: maximumAcceptance(command.Command), ProofVerifiedAt: quiescenceClock(t, s), ProofValidUntil: quiescenceClock(t, s).Add(time.Minute)}); e != nil {
					t.Fatal(e)
				}

				binding, e = s.ReadManagedAttemptBinding(t.Context(), key)
				if e != nil {
					t.Fatal(e)
				}

				measure("terminal", binding.Abort, managed.MaxAttemptCommand)
				measure("binding reader", binding, managed.MaxAttemptBindingResponse)

				historical, e := s.ReadManagedAttemptHistoricalSettlement(t.Context(), managed.AttemptSettlementReadRequest{AttemptBindingKey: key, ProviderOperationID: f.provider, NativeFenceDigest: binding.Fence.NativeFenceDigest})
				if e != nil {
					t.Fatal(e)
				}

				measure("settlement", historical.Settlement, managed.MaxAttemptSettlement)
				measure("partial reader", historical, managed.MaxAttemptBindingResponse)
			}

			current, err := s.ReadManagedProviderStep(t.Context(), grant.Permission.Step)
			if err != nil {
				t.Fatal(err)
			}

			permissionAfter, err := json.Marshal(current.Permission)
			if err != nil || !bytes.Equal(permissionBefore, permissionAfter) {
				t.Fatal("positive-provider permission bytes changed", err)
			}
		})
	}
}

func TestManagedAttemptPostgresInitialReserve64(t *testing.T) {
	s, _ := managedPG(t)
	action := largeRemovalAction(t)
	_, parent := removalActionSetup(t, s, action, 2, false)

	const revision = int64(1<<63-1) - (7*64 + 33 + 8) - 1
	if _, err := pgdriver.Unwrap(s.DB()).Exec(t.Context(), "UPDATE cp_managed_targets SET revision=$1,data=jsonb_set(data,'{revision}',to_jsonb($1::bigint)) WHERE id=$2", revision+1, action.InstanceID.String()); err != nil {
		t.Fatal(err)
	}

	q := managed.AttemptStartRequest{Command: parent.command(), ActionID: action.ID, AttemptID: id.New(managed.PrefixOperation), ProviderOperationID: id.New(managed.PrefixOperation), VerifierID: "provider"}

	before := attemptPostgresSnapshot(t, s)
	if _, err := s.BeginManagedRemovalAttempt(t.Context(), q); !errors.Is(err, managed.ErrCapacity) {
		t.Fatal("exact489 start did not refuse", err)
	}

	if after := attemptPostgresSnapshot(t, s); !reflect.DeepEqual(after, before) {
		t.Fatal("exact489 refusal changed retained state")
	}

	var receipts int
	if err := pgdriver.Unwrap(s.DB()).NewRaw("SELECT COUNT(*) FROM cp_managed_commands WHERE target_id=$1 AND operation_id=$2", action.InstanceID.String(), q.ID.String()).Scan(t.Context(), &receipts); err != nil || receipts != 0 {
		t.Fatal("refused allocation retained receipt", err)
	}

	if _, err := pgdriver.Unwrap(s.DB()).Exec(t.Context(), "UPDATE cp_managed_targets SET revision=$1,data=jsonb_set(data,'{revision}',to_jsonb($1::bigint)) WHERE id=$2", revision, action.InstanceID.String()); err != nil {
		t.Fatal(err)
	}

	q.Command = parent.command()
	if _, err := s.BeginManagedRemovalAttempt(t.Context(), q); err != nil {
		t.Fatal("exact490 boundary rejected", err)
	}

	target, err := s.ReadManagedTarget(t.Context(), action.Key)
	if err != nil {
		t.Fatal(err)
	}

	if uint64(1<<63-1)-target.Revision != 489 {
		t.Fatal("exact reserve lost")
	}
}

func TestManagedAttemptPartialPositiveFailureRefusesCompletion(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newRemovalFixture(t, backend)
			f.admit()

			first := f.step("api", managed.StepRemoveContainer)
			if err := f.s.RecordManagedProviderStepResult(t.Context(), managed.ProviderStepResult{Permission: first.Permission, Outcome: managed.PortTerminal, ResourceID: first.Permission.ResourceID, Evidence: []byte("positive first deletion"), ObservedAt: quiescenceClock(t, f.s)}); err != nil {
				t.Fatal(err)
			}

			second := f.step("sidecar", managed.StepRemoveContainer)
			if err := f.s.RecordManagedProviderStepFailure(t.Context(), f.failure(second)); err != nil {
				t.Fatal(err)
			}

			before, err := f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
			if err != nil {
				t.Fatal(err)
			}

			for _, disposition := range []managed.Disposition{managed.Removed, managed.SettledWithoutDeletion} {
				q := managed.AttemptSettlementRequest{AttemptRequest: f.request(), Disposition: disposition, ProducerProfile: "ctrlplane.managed.remove.v1", VerifierID: "provider", Evidence: []byte("partial cannot settle"), SettledAt: quiescenceClock(t, f.s)}
				if disposition == managed.SettledWithoutDeletion {
					q.ProducerProfile = managed.LocalNoDispatchProfile
				}

				if _, err = f.s.SettleManagedRemovalAttempt(t.Context(), q); err == nil {
					t.Fatal("partial positive plus local failure invented settlement")
				}
			}

			if _, err = f.s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err == nil {
				t.Fatal("partial positive became NotIssued")
			}

			q := managed.ProviderStepAuthorization{Command: f.request().Command, Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: f.key.Key, ActionID: f.s.action.ID}, ProviderOperationID: f.provider, Kind: managed.StepRemoveNetwork}}
			if _, err = f.s.AuthorizeManagedProviderStep(t.Context(), q); err == nil {
				t.Fatal("local failure advanced another provider step")
			}

			after, err := f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("refusal mutated attempt", err)
			}
		})
	}
}
func TestManagedAttemptPositiveFailureRace(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newRemovalFixture(t, backend)
			f.admit()
			grant := f.step("api", managed.StepRemoveContainer)
			failure := f.failure(grant)
			positive := managed.ProviderStepResult{Permission: grant.Permission, Outcome: managed.PortTerminal, ResourceID: grant.Permission.ResourceID, Evidence: []byte("exact positive"), ObservedAt: quiescenceClock(t, f.s)}
			errorsOut := make([]error, 2)

			var ready sync.WaitGroup
			ready.Add(2)

			start := make(chan struct{})

			go func() {
				defer ready.Done()

				<-start

				errorsOut[0] = f.s.RecordManagedProviderStepResult(context.Background(), positive)
			}()
			go func() {
				defer ready.Done()

				<-start

				errorsOut[1] = f.s.RecordManagedProviderStepFailure(context.Background(), failure)
			}()

			close(start)
			ready.Wait()

			if (errorsOut[0] == nil) == (errorsOut[1] == nil) {
				t.Fatalf("terminal race not exclusive: %v", errorsOut)
			}

			record, err := f.s.ReadManagedProviderStep(t.Context(), grant.Permission.Step)
			if err != nil {
				t.Fatal(err)
			}

			retained, failed := f.s.ReadManagedProviderStepFailure(t.Context(), grant.Permission.Step)
			if errorsOut[0] == nil {
				if record.Result == nil || failed == nil {
					t.Fatal("positive winner retained conflicting failure")
				}
			} else if failed != nil || !reflect.DeepEqual(retained, failure) || record.Result != nil {
				t.Fatal("failure winner retained positive or lost original failure")
			}
		})
	}
}

func TestManagedAttemptPostgresProofRefreshProfile(t *testing.T) {
	if os.Getenv("CTRLPLANE_ATTEMPT_PROFILE") != "1" {
		t.Skip("opt-in development profiling; full finite-set qualification runs separately")
	}

	f, _ := removalActionFixture(t, controllerStore(t, "postgres"), largeRemovalAction(t), 2)
	started := time.Now()

	f.refreshObservations()
	now := quiescenceClock(t, f.s)
	margin := f.observations[0].Observation.ValidUntil.Sub(now)
	t.Logf("64-binding refresh before native fences: elapsed=%s oldest_proof_age=%s remaining_margin=%s", time.Since(started), now.Sub(f.observations[0].Observation.ObservedAt), margin)

	if margin <= 0 {
		t.Fatal("all64 current proofs cannot remain fresh through this measured refresh")
	}
}

type attemptProcessState struct {
	Action       managed.ActionSubmission                 `json:"action"`
	Key          managed.AttemptKey                       `json:"key"`
	Provider     id.ID                                    `json:"provider"`
	Reservations []id.ID                                  `json:"reservations"`
	Commands     []managed.AttemptCommandRecord           `json:"commands"`
	Recoveries   []managed.AttemptRecoveryRequest         `json:"recoveries"`
	Terminals    []managed.AttemptFinishRequest           `json:"terminals"`
	Final        json.RawMessage                          `json:"final"`
	Batch        managed.QuiescenceBatchAcceptanceRequest `json:"batch"`
	BatchReceipt managed.QuiescenceBatchReceipt           `json:"batch_receipt"`
}

func TestManagedAttemptPostgresProcessReplacement(t *testing.T) {
	stage := os.Getenv("CTRLPLANE_B23A_PROCESS_STAGE")
	if stage == "" {
		_, dsn := managedPG(t)
		path := filepath.Join(t.TempDir(), "attempt-recovery.json")

		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}

		for _, phase := range []string{"seed", "recover", "verify"} {
			command := exec.CommandContext(t.Context(), binary, "-test.run=^TestManagedAttemptPostgresProcessReplacement$", "-test.v")

			command.Env = append(os.Environ(), "CTRLPLANE_TEST_POSTGRES_DSN="+dsn, "CTRLPLANE_B23A_PROCESS_STAGE="+phase, "CTRLPLANE_B23A_PROCESS_STATE="+path)
			output, runErr := command.CombinedOutput()

			safe := strings.ReplaceAll(string(output), dsn, "[private fixture DSN]")
			if runErr != nil {
				t.Fatalf("replacement phase %s: %v: %s", phase, runErr, safe)
			}

			t.Logf("independent authority process %s exited successfully: %s", phase, safe)
		}

		return
	}

	s := openManagedPG(t, os.Getenv("CTRLPLANE_TEST_POSTGRES_DSN"))

	path := os.Getenv("CTRLPLANE_B23A_PROCESS_STATE")
	if path == "" {
		t.Fatal("private state path required")
	}

	var state attemptProcessState

	if stage == "seed" {
		action := controllerAction(t)
		f, _ := removalActionFixture(t, s, action, 1)
		f.admit()
		batch := batchRequest(t, f)

		batchReceipt, err := s.AcceptManagedQuiescenceBatch(t.Context(), batch)
		if err != nil {
			t.Fatal(err)
		}

		for _, service := range []string{"api", "sidecar", ""} {
			kind := managed.StepRemoveContainer
			if service == "" {
				kind = managed.StepRemoveNetwork
			}

			grant := f.step(service, kind)
			if err := s.RecordManagedProviderStepResult(t.Context(), managed.ProviderStepResult{Permission: grant.Permission, Outcome: managed.PortTerminal, ResourceID: grant.Permission.ResourceID, Evidence: []byte("synthetic positive response"), ObservedAt: quiescenceClock(t, s)}); err != nil {
				t.Fatal(err)
			}
		}

		if _, err := s.SettleManagedRemovalAttempt(t.Context(), managed.AttemptSettlementRequest{AttemptRequest: f.request(), Disposition: managed.Removed, ProducerProfile: "ctrlplane.managed.remove.v1", VerifierID: "provider", Evidence: []byte("complete original steps"), SettledAt: quiescenceClock(t, s)}); err != nil {
			t.Fatal(err)
		}

		state = attemptProcessState{Action: action, Key: f.key, Provider: f.provider, Reservations: f.reservations, Batch: batch, BatchReceipt: batchReceipt}
		for _, rid := range f.reservations {
			grant := f.command(rid, managed.AttemptFinish)
			state.Commands = append(state.Commands, grant.Command)
		}
	} else {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		if err = json.Unmarshal(raw, &state); err != nil {
			t.Fatal(err)
		}

		f := &removalFixture{t: t, s: &quiescenceActionStore{Authority: s, t: t, action: state.Action}, key: state.Key, provider: state.Provider, reservations: state.Reservations}

		operation, err := s.ReadManagedOperation(t.Context(), managed.OperationKey{Key: state.Key.Key, ActionID: state.Action.ID})
		if err != nil || operation.RemovalAttemptRoot == nil || operation.RemovalAttemptRoot.AttemptID != state.Key.AttemptID {
			t.Fatal("lost original operation anchor across process replacement", err)
		}

		beforeBatch := attemptPostgresSnapshot(t, s)
		if saved, e := s.ReadManagedQuiescenceBatch(t.Context(), managed.QuiescenceBatchKey{Key: state.Key.Key, BatchID: state.Batch.Command.ID}); e != nil || saved != state.BatchReceipt {
			t.Fatal("replacement aggregate history", e)
		}

		if saved, e := s.AcceptManagedQuiescenceBatch(t.Context(), state.Batch); e != nil || saved != state.BatchReceipt {
			t.Fatal("replacement aggregate replay", e)
		}

		for i := range state.Batch.Entries {
			child := batchChildRequest(state.Batch, i)

			saved, e := s.ReadManagedQuiescence(t.Context(), managed.QuiescenceKey{Key: state.Key.Key, ReservationID: child.Command.ReservationID, OperationID: child.Command.ID})
			if e != nil {
				t.Fatal(e)
			}

			replay, e := s.AcceptManagedQuiescence(t.Context(), child)
			if e != nil || managed.OriginalReceipt(replay) != saved.Receipt {
				t.Fatal("replacement individual replay", e)
			}
		}

		if !reflect.DeepEqual(beforeBatch, attemptPostgresSnapshot(t, s)) {
			t.Fatal("replacement replay wrote")
		}

		switch stage {
		case "recover":
			for _, saved := range state.Commands {
				retained, readErr := s.ReadManagedAttemptCommand(t.Context(), saved.Key)
				if readErr != nil || !reflect.DeepEqual(retained, saved) {
					t.Fatal("original command bytes changed across replacement", readErr)
				}

				raw, marshalErr := json.Marshal(saved)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}

				q := managed.AttemptRecoveryRequest{AttemptRequest: f.request(), ReservationID: saved.Key.ReservationID, Kind: managed.AttemptFinish, CommandDigest: managed.Digest(append([]byte("ctrlplane.managed.attempt-command.v1\n"), raw...))}

				recovered, recoverErr := s.AuthorizeManagedAttemptRecovery(t.Context(), q)
				if recoverErr != nil || !recovered.NewlyAuthorized || !reflect.DeepEqual(recovered.Command, saved) {
					t.Fatal("original Finish recovery failed", recoverErr)
				}

				state.Recoveries = append(state.Recoveries, q)

				terminal := managed.AttemptFinishRequest{AttemptRequest: f.request(), ReservationID: saved.Key.ReservationID, Acceptance: f.acceptance(saved)}
				if _, err = s.RecordManagedAttemptFinish(t.Context(), terminal); err != nil {
					t.Fatal(err)
				}

				state.Terminals = append(state.Terminals, terminal)
			}

			if _, err = s.TombstoneManagedRemovalAttempt(t.Context(), f.request()); err != nil {
				t.Fatal(err)
			}

			record, readErr := s.ReadManagedRemovalAttempt(t.Context(), state.Key)
			if readErr != nil {
				t.Fatal(readErr)
			}

			state.Final, err = json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
		case "verify":
			for n, q := range state.Recoveries {
				grant, readErr := s.AuthorizeManagedAttemptRecovery(t.Context(), q)
				if readErr != nil || grant.NewlyAuthorized || !reflect.DeepEqual(grant.Command, state.Commands[n]) {
					t.Fatal("replacement replay became new send", readErr)
				}
			}

			for _, q := range state.Terminals {
				if _, err = s.RecordManagedAttemptFinish(t.Context(), q); err != nil {
					t.Fatal("terminal replay lost", err)
				}
			}

			target, readErr := s.ReadManagedTarget(t.Context(), state.Key.Key)
			if readErr != nil || target.Phase != managed.Deleted {
				t.Fatal("terminal parent lost", readErr)
			}

			record, readErr := s.ReadManagedRemovalAttempt(t.Context(), state.Key)
			if readErr != nil {
				t.Fatal(readErr)
			}

			raw, marshalErr := json.Marshal(record)
			if marshalErr != nil || !bytes.Equal(raw, state.Final) {
				t.Fatal("retained final attempt changed", marshalErr)
			}
		default:
			t.Fatal("unknown replacement phase")
		}
	}

	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}

	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}

	t.Logf("phase=%s pid=%d original attempt=%s", stage, os.Getpid(), state.Key.AttemptID)
}

func TestManagedAttemptRevokeIssueRaces(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		for _, mode := range []string{"begin", "first-provider", "later-provider"} {
			t.Run(backend+"/"+mode, func(t *testing.T) {
				f := newRemovalFixture(t, backend)
				service := "api"

				if mode != "begin" {
					f.admit()
				}

				if mode == "later-provider" {
					first := f.step("api", managed.StepRemoveContainer)
					if err := f.s.RecordManagedProviderStepResult(t.Context(), managed.ProviderStepResult{Permission: first.Permission, Outcome: managed.PortTerminal, ResourceID: first.Permission.ResourceID, Evidence: []byte("positive first step"), ObservedAt: quiescenceClock(t, f.s)}); err != nil {
						t.Fatal(err)
					}

					service = "sidecar"
				}

				revoke := f.request()
				begin := managed.AttemptCommandRequest{AttemptRequest: revoke, ReservationID: f.reservations[0], Kind: managed.AttemptBegin, ExpectedNativeVersion: "1", NativeCommand: managed.PortCommand{SchemaVersion: "1", OperationID: id.New(managed.PrefixOperation), RequestID: "racing-original", Original: []byte("original"), OriginalDigest: managed.Digest([]byte("original")), Native: []byte("native"), NativeDigest: managed.Digest([]byte("native"))}}
				begin.ID = id.New(managed.PrefixOperation)
				provider := managed.ProviderStepAuthorization{Command: begin.Command, Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: f.key.Key, ActionID: f.s.action.ID}, ProviderOperationID: f.provider, Kind: managed.StepRemoveContainer, Service: service}}

				var (
					revokeErr, issueErr error
					newlyIssued         bool
				)

				ready := make(chan struct{})

				var wait sync.WaitGroup

				wait.Add(2)
				go func() {
					defer wait.Done()

					<-ready

					_, revokeErr = f.s.RevokeManagedRemovalAttempt(t.Context(), revoke)
				}()
				go func() {
					defer wait.Done()

					<-ready

					if mode == "begin" {
						grant, err := f.s.AuthorizeManagedAttemptCommand(t.Context(), begin)
						issueErr = err
						newlyIssued = grant.NewlyIssued
					} else {
						grant, err := f.s.AuthorizeManagedProviderStep(t.Context(), provider)
						issueErr = err
						newlyIssued = grant.NewlyIssued
					}
				}()

				close(ready)
				wait.Wait()

				if revokeErr == nil && issueErr == nil {
					t.Fatal("same advancing CAS authorized both revoke and issue")
				}

				if issueErr != nil && newlyIssued {
					t.Fatal("failed issuance leaked grant")
				}

				if mode == "later-provider" {
					if revokeErr == nil || issueErr != nil || !newlyIssued {
						t.Fatal("positive-first must prevent later revocation", revokeErr, issueErr)
					}

					return
				}

				if revokeErr == nil {
					if mode == "begin" {
						begin.AttemptRequest = f.request()
						if _, err := f.s.AuthorizeManagedAttemptCommand(t.Context(), begin); err == nil {
							t.Fatal("revoked native slot issued")
						}
					} else {
						provider.Command = quiescenceCommand(t, f.s, f.key.Key)
						if _, err := f.s.AuthorizeManagedProviderStep(t.Context(), provider); err == nil {
							t.Fatal("revoked provider slot issued")
						}
					}

					return
				}

				if issueErr != nil || !newlyIssued {
					t.Fatal("neither competing transition completed", revokeErr, issueErr)
				}

				_, err := f.s.RevokeManagedRemovalAttempt(t.Context(), f.request())
				if mode == "begin" && err != nil {
					t.Fatal("retained original native command could not be revoked", err)
				}

				if mode == "first-provider" && err == nil {
					t.Fatal("unresolved issued provider slot revoked")
				}
			})
		}
	}
}

func TestManagedAttemptPostgresAbsentBitPresentBrokenRow(t *testing.T) {
	for _, missing := range []string{"event", "receipt"} {
		t.Run(missing, func(t *testing.T) {
			f := newRemovalFixture(t, "postgres")
			grant := f.command(f.reservations[0], managed.AttemptBegin)
			s := f.s.Authority.(*postgres.Store)
			pg := pgdriver.Unwrap(s.DB())

			var raw []byte
			if err := pg.NewRaw("SELECT data FROM cp_managed_controller_records WHERE kind='removal_attempt_state' AND key=$1", f.key.AttemptID.String()).Scan(t.Context(), &raw); err != nil {
				t.Fatal(err)
			}

			var state managed.AttemptState
			if err := json.Unmarshal(raw, &state); err != nil {
				t.Fatal(err)
			}

			state.BindingSlots[0] = 0

			raw, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}

			if _, err = pg.Exec(t.Context(), "UPDATE cp_managed_controller_records SET data=$1 WHERE kind='removal_attempt_state' AND key=$2", raw, f.key.AttemptID.String()); err != nil {
				t.Fatal(err)
			}

			if missing == "event" {
				_, err = pg.Exec(t.Context(), "DELETE FROM cp_managed_controller_records WHERE kind='removal_attempt_event' AND key=$1", grant.Command.AuthorizationID.String())
			} else {
				_, err = pg.Exec(t.Context(), "DELETE FROM cp_managed_commands WHERE target_id=$1 AND operation_id=$2", f.key.InstanceID.String(), grant.Command.AuthorizationID.String())
			}

			if err != nil {
				t.Fatal(err)
			}

			before := attemptPostgresSnapshot(t, s)
			if _, err = s.ReadManagedRemovalAttempt(t.Context(), f.key); !errors.Is(err, managed.ErrUnavailable) {
				t.Fatal("broken present row became optional absence", err)
			}

			if !reflect.DeepEqual(before, attemptPostgresSnapshot(t, s)) {
				t.Fatal("broken row repaired")
			}
		})
	}
}

func assertAttemptFactLocalID(t *testing.T, f *removalFixture, run func(bool) (managed.Receipt, error)) {
	t.Helper()

	first, err := run(false)
	if err != nil {
		t.Fatal(err)
	}

	again, err := run(false)
	if err != nil || again != first {
		t.Fatal("same local ID changed accepted receipt", err)
	}

	before, err := f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = run(true); !errors.Is(err, managed.ErrConflict) {
		t.Fatal("fresh local ID reused immutable fact", err)
	}

	after, err := f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("different local ID changed retained fact", err)
	}
}
func TestManagedAttemptFactLocalOperationIDs(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newRemovalFixture(t, backend)
			for _, rid := range f.reservations {
				command := f.command(rid, managed.AttemptBegin)
				raw := []byte("original fence")
				q := managed.AttemptFenceRequest{AttemptRequest: f.request(), ReservationID: rid, Acceptance: f.acceptance(command.Command), Fence: managed.FenceWire(managed.Fence{Epoch: 1, Evidence: raw, Digest: managed.Digest(raw), CompatibilityEvidence: raw, ValidUntil: quiescenceClock(t, f.s).Add(time.Hour), Survivors: []string{"off-instance"}})}
				assertAttemptFactLocalID(t, f, func(fresh bool) (managed.Receipt, error) {
					selected := q
					if fresh {
						selected.AttemptRequest = f.request()
					}

					return f.s.RecordManagedAttemptFence(t.Context(), selected)
				})
			}

			admission := managed.AttemptAdmissionRequest{AttemptRequest: f.request(), VerifierID: "provider"}
			assertAttemptFactLocalID(t, f, func(fresh bool) (managed.Receipt, error) {
				q := admission
				if fresh {
					q.AttemptRequest = f.request()
				}

				return f.s.AdmitManagedRemovalAttempt(t.Context(), q)
			})

			permission := f.step("api", managed.StepRemoveContainer)
			if err := f.s.RecordManagedProviderStepFailure(t.Context(), f.failure(permission)); err != nil {
				t.Fatal(err)
			}

			settlement := managed.AttemptSettlementRequest{AttemptRequest: f.request(), Disposition: managed.SettledWithoutDeletion, ProducerProfile: managed.LocalNoDispatchProfile, VerifierID: "provider", Evidence: []byte("no transport delegate"), SettledAt: quiescenceClock(t, f.s)}
			assertAttemptFactLocalID(t, f, func(fresh bool) (managed.Receipt, error) {
				q := settlement
				if fresh {
					q.AttemptRequest = f.request()
				}

				return f.s.SettleManagedRemovalAttempt(t.Context(), q)
			})

			for _, rid := range f.reservations {
				key := managed.AttemptBindingKey{AttemptKey: f.key, ReservationID: rid}

				binding, err := f.s.ReadManagedAttemptBinding(t.Context(), key)
				if err != nil {
					t.Fatal(err)
				}

				witness := managed.AttemptBindingSettlementRequest{AttemptRequest: f.request(), ReservationID: rid, FenceFactDigest: fixtureAttemptFact(t, "removal_fence", *binding.Fence)}
				assertAttemptFactLocalID(t, f, func(fresh bool) (managed.Receipt, error) {
					q := witness
					if fresh {
						q.AttemptRequest = f.request()
					}

					return f.s.RecordManagedAttemptBindingSettlement(t.Context(), q)
				})
				command := f.command(rid, managed.AttemptAbort)
				q := managed.AttemptAbortRequest{AttemptRequest: f.request(), ReservationID: rid, Acceptance: f.acceptance(command.Command), ProofVerifiedAt: quiescenceClock(t, f.s), ProofValidUntil: quiescenceClock(t, f.s).Add(time.Minute)}
				assertAttemptFactLocalID(t, f, func(fresh bool) (managed.Receipt, error) {
					selected := q
					if fresh {
						selected.AttemptRequest = f.request()
					}

					return f.s.RecordManagedAttemptAbort(t.Context(), selected)
				})
			}

			separate := newRemovalFixture(t, backend)
			revocation := separate.request()
			assertAttemptFactLocalID(t, separate, func(fresh bool) (managed.Receipt, error) {
				q := revocation
				if fresh {
					q = separate.request()
				}

				return separate.s.RevokeManagedRemovalAttempt(t.Context(), q)
			})
		})
	}
}

func TestManagedAttemptPostgresWitnessAndAbortLockTime(t *testing.T) {
	f := newRemovalFixture(t, "postgres")
	s := f.s.Authority.(*postgres.Store)
	rid := f.reservations[0]
	f.fence(rid)

	revoked, err := f.s.RevokeManagedRemovalAttempt(t.Context(), f.request())
	if err != nil {
		t.Fatal(err)
	}

	bindingKey := managed.AttemptBindingKey{AttemptKey: f.key, ReservationID: rid}

	binding, err := f.s.ReadManagedAttemptBinding(t.Context(), bindingKey)
	if err != nil {
		t.Fatal(err)
	}

	q := managed.AttemptBindingSettlementRequest{AttemptRequest: f.request(), ReservationID: rid, FenceFactDigest: fixtureAttemptFact(t, "removal_fence", *binding.Fence)}

	threshold := quiescenceClock(t, f.s).Add(300 * time.Millisecond)
	if err = releaseAfterStoreTime(t, s, f.key.Key, threshold, func() error {
		_, e := s.RecordManagedAttemptBindingSettlement(t.Context(), q)

		return e
	}); err != nil {
		t.Fatal(err)
	}

	binding, err = f.s.ReadManagedAttemptBinding(t.Context(), bindingKey)
	if err != nil || binding.Witness == nil || binding.Witness.WitnessedAt.Before(threshold) {
		t.Fatal("witness did not use actual post-lock time", err)
	}

	history, err := f.s.ReadManagedAttemptHistoricalSettlement(t.Context(), managed.AttemptSettlementReadRequest{AttemptBindingKey: bindingKey, ProviderOperationID: f.provider, NativeFenceDigest: binding.Fence.NativeFenceDigest})
	if err != nil || !history.Revocation.RevokedAt.Equal(revoked.AcceptedAt) {
		t.Fatal("witness rewrote revocation", err)
	}

	grant := f.command(rid, managed.AttemptAbort)
	abort := managed.AttemptAbortRequest{AttemptRequest: f.request(), ReservationID: rid, Acceptance: f.acceptance(grant.Command), ProofVerifiedAt: quiescenceClock(t, f.s), ProofValidUntil: quiescenceClock(t, f.s).Add(300 * time.Millisecond)}

	before := attemptPostgresSnapshot(t, s)
	if err = releaseAfterStoreTime(t, s, f.key.Key, abort.ProofValidUntil, func() error {
		_, e := s.RecordManagedAttemptAbort(t.Context(), abort)

		return e
	}); !errors.Is(err, managed.ErrBlocked) {
		t.Fatal("expired binding proof accepted after real lock", err)
	}

	if !reflect.DeepEqual(before, attemptPostgresSnapshot(t, s)) {
		t.Fatal("expired binding proof mutated history")
	}

	abort.ProofVerifiedAt = quiescenceClock(t, f.s)

	abort.ProofValidUntil = abort.ProofVerifiedAt.Add(time.Minute)
	if _, err = s.RecordManagedAttemptAbort(t.Context(), abort); err != nil {
		t.Fatal(err)
	}
}

// This diagnostic counts protocol messages without retaining SQL or parameter bytes.
type attemptQueryCounter struct {
	queries atomic.Uint64
	bytes   atomic.Uint64
}
type attemptCountWriter struct {
	io.Writer

	counter *atomic.Uint64
}

func (w attemptCountWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	w.counter.Add(uint64(n))

	return n, err
}
func countedAttemptPostgres(t *testing.T, source *postgres.Store) (*postgres.Store, *attemptQueryCounter) {
	t.Helper()

	var database string
	if err := pgdriver.Unwrap(source.DB()).NewRaw("SELECT current_database()").Scan(t.Context(), &database); err != nil {
		t.Fatal("cannot identify owned fixture database")
	}

	parsed, err := url.Parse(os.Getenv("CTRLPLANE_TEST_POSTGRES_DSN"))
	if err != nil {
		t.Fatal("invalid private fixture DSN")
	}

	if parsed.Query().Get("sslmode") != "disable" {
		t.Fatal("counting fixture requires its existing private loopback plaintext profile")
	}

	parsed.Path = "/" + database
	upstream := parsed.Host

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	counter := &attemptQueryCounter{}

	var (
		workers sync.WaitGroup
		mu      sync.Mutex
	)

	connections := map[net.Conn]struct{}{}

	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)

		for {
			client, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}

			mu.Lock()
			connections[client] = struct{}{}
			mu.Unlock()

			workers.Go(func() {
				defer func() { _ = client.Close(); mu.Lock(); delete(connections, client); mu.Unlock() }()

				server, dialErr := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(t.Context(), "tcp", upstream)
				if dialErr != nil {
					return
				}

				defer func() { _ = server.Close() }()

				copied := make(chan struct{})

				go func() {
					_, _ = io.Copy(attemptCountWriter{Writer: client, counter: &counter.bytes}, server)
					_ = client.Close()

					close(copied)
				}()

				defer func() { _ = server.Close(); <-copied }()

				startup := true

				for {
					headerSize := 5
					if startup {
						headerSize = 4
					}

					header := make([]byte, headerSize)
					if _, readErr := io.ReadFull(client, header); readErr != nil {
						return
					}

					size := int(binary.BigEndian.Uint32(header[headerSize-4:]))
					if size < 4 || size > 16<<20 {
						return
					}

					frame := make([]byte, headerSize+size-4)
					copy(frame, header)

					if _, readErr := io.ReadFull(client, frame[headerSize:]); readErr != nil {
						return
					}

					if !startup && (header[0] == 'E' || header[0] == 'Q') {
						counter.queries.Add(1)
					}

					if _, writeErr := server.Write(frame); writeErr != nil {
						return
					}

					startup = false
				}
			})
		}
	}()

	t.Cleanup(func() {
		_ = listener.Close()

		<-acceptDone
		mu.Lock()
		for conn := range connections {
			_ = conn.Close()
		}
		mu.Unlock()
		workers.Wait()
	})

	parsed.Host = listener.Addr().String()

	return openManagedPG(t, parsed.String()), counter
}
func TestManagedAttemptPostgresAfterFenceRefreshProfile(t *testing.T) {
	if os.Getenv("CTRLPLANE_ATTEMPT_PROFILE") != "1" {
		t.Skip("opt-in development profiling; full finite-set qualification runs separately")
	}

	testManagedAttemptFullFiniteSet(t, false, "after-fence-proxy")
}

func TestManagedAttemptPostgresAfterFenceDirectRefresh(t *testing.T) {
	if os.Getenv("CTRLPLANE_ATTEMPT_PROFILE") != "1" {
		t.Skip("opt-in development profiling; full finite-set qualification runs separately")
	}

	testManagedAttemptFullFiniteSet(t, false, "after-fence-direct")
}
func TestManagedAttemptPostgresFullLedgerDirectRefresh(t *testing.T) {
	if os.Getenv("CTRLPLANE_ATTEMPT_PROFILE") != "1" {
		t.Skip("opt-in development profiling; full finite-set qualification runs separately")
	}

	testManagedAttemptFullFiniteSet(t, false, "full-ledger-direct")
}

func TestManagedAttemptPostgresUntrustedDiscovery(t *testing.T) {
	for _, mode := range []string{"unknown", "duplicate", "noncanonical"} {
		t.Run(mode, func(t *testing.T) {
			f := newRemovalFixture(t, "postgres")
			f.command(f.reservations[0], managed.AttemptBegin)
			s := f.s.Authority.(*postgres.Store)
			pg := pgdriver.Unwrap(s.DB())

			var row struct {
				Scope string `grove:"scope"`
				Key   string `grove:"key"`
				Data  []byte `grove:"data"`
			}
			if err := pg.NewRaw("SELECT scope,key,data FROM cp_managed_controller_records WHERE kind='removal_command'").Scan(t.Context(), &row); err != nil {
				t.Fatal(err)
			}

			var command managed.AttemptCommandRecord
			if err := json.Unmarshal(row.Data, &command); err != nil {
				t.Fatal(err)
			}

			raw := row.Data

			switch mode {
			case "unknown":
				raw = append([]byte(`{"unknown":true,`), raw[1:]...)
			case "duplicate":
				raw = append([]byte(`{"authorization_id":"`+command.AuthorizationID.String()+`",`), raw[1:]...)
			case "noncanonical":
				raw = append([]byte(" "), raw...)
			}

			if _, err := pg.Exec(t.Context(), "UPDATE cp_managed_controller_records SET data=$1 WHERE kind='removal_command' AND scope=$2 AND key=$3", raw, row.Scope, row.Key); err != nil {
				t.Fatal(err)
			}

			before := attemptPostgresSnapshot(t, s)
			if _, err := s.ReadManagedRemovalAttempt(t.Context(), f.key); !errors.Is(err, managed.ErrUnavailable) {
				t.Fatal("discovery projection bypassed canonical authority", err)
			}

			if _, err := s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: quiescenceCommand(t, f.s, f.key.Key), Duration: managed.MaxLease}); !errors.Is(err, managed.ErrUnavailable) {
				t.Fatal("fresh transition accepted corrupt discovery row", err)
			}

			if !reflect.DeepEqual(before, attemptPostgresSnapshot(t, s)) {
				t.Fatal("corrupt discovery mutated rows")
			}
		})
	}
}

func requireManagedMaximum(t *testing.T, mode string) {
	t.Helper()

	selected := os.Getenv("CTRLPLANE_MANAGED_MAXIMUM")
	if selected == "" {
		t.Skip("required explicit maximum campaign: set CTRLPLANE_MANAGED_MAXIMUM=" + mode + " with its documented test selection")
	}

	if selected != mode {
		t.Fatalf("incorrect maximum campaign selector: want %s, got %s", mode, selected)
	}
}
func TestManagedAttemptProviderPermissionCounters(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newRemovalFixture(t, backend)
			f.admit()

			for _, service := range []string{"api", "sidecar", ""} {
				before, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
				if err != nil {
					t.Fatal(err)
				}

				state, err := f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
				if err != nil {
					t.Fatal(err)
				}

				revision, err := state.State.Revision.Uint64()
				if err != nil {
					t.Fatal(err)
				}

				kind := managed.StepRemoveContainer
				if service == "" {
					kind = managed.StepRemoveNetwork
				}

				q := managed.ProviderStepAuthorization{Command: quiescenceCommand(t, f.s, f.key.Key), Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: f.key.Key, ActionID: f.s.action.ID}, ProviderOperationID: f.provider, Kind: kind, Service: service}}

				grant, err := f.s.AuthorizeManagedProviderStep(t.Context(), q)
				if err != nil || !grant.NewlyIssued {
					t.Fatal("fresh permission", err)
				}

				after, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
				if err != nil || after.Revision != before.Revision+1 {
					t.Fatal("permission parent increment", err)
				}

				issued, err := f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
				if err != nil || issued.State.Revision != managed.Decimal(strconv.FormatUint(revision+1, 10)) {
					t.Fatal("permission attempt increment", err)
				}

				replay, err := f.s.AuthorizeManagedProviderStep(t.Context(), q)
				if err != nil || replay.NewlyIssued || replay.Permission != grant.Permission {
					t.Fatal("permission replay", err)
				}

				retained, err := f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
				if err != nil || !reflect.DeepEqual(retained, issued) {
					t.Fatal("permission replay advanced attempt", err)
				}

				if err = f.s.RecordManagedProviderStepResult(t.Context(), managed.ProviderStepResult{Permission: grant.Permission, Outcome: managed.PortTerminal, ResourceID: grant.Permission.ResourceID, Evidence: []byte("positive"), ObservedAt: quiescenceClock(t, f.s)}); err != nil {
					t.Fatal(err)
				}

				retained, err = f.s.ReadManagedRemovalAttempt(t.Context(), f.key)
				if err != nil || retained.State.Revision != issued.State.Revision {
					t.Fatal("positive result advanced attempt", err)
				}

				current, err := f.s.ReadManagedTarget(t.Context(), f.key.Key)
				if err != nil || !reflect.DeepEqual(after, current) {
					t.Fatal("replay/result advanced parent", err)
				}
			}
		})
	}
}

func TestManagedAttemptPostgresInvalidCommand(t *testing.T) {
	cases := []struct {
		name   string
		change func(*managed.Command)
		want   error
	}{
		{"wrong-prefix", func(c *managed.Command) { c.ID = id.New(managed.PrefixReservation) }, managed.ErrInvalid},
		{"zero-id", func(c *managed.Command) { c.ID = id.ID{} }, managed.ErrInvalid},
		{"empty-tenant", func(c *managed.Command) { c.TenantID = "" }, managed.ErrInvalid},
		{"invalid-tenant", func(c *managed.Command) { c.TenantID = "bad tenant" }, managed.ErrInvalid},
		{"wrong-instance", func(c *managed.Command) { c.InstanceID = id.New(managed.PrefixReservation) }, managed.ErrInvalid},
		{"empty-owner", func(c *managed.Command) { c.Owner = "" }, managed.ErrInvalid},
		{"invalid-owner", func(c *managed.Command) { c.Owner = "bad owner" }, managed.ErrInvalid},
		{"zero-revision", func(c *managed.Command) { c.Revision = 0 }, managed.ErrInvalid},
		{"revision-overflow", func(c *managed.Command) { c.Revision = math.MaxInt64 + 1 }, managed.ErrInvalid},
		{"epoch-overflow", func(c *managed.Command) { c.LeaseEpoch = math.MaxInt64 + 1 }, managed.ErrInvalid},
		{"maximum-revision", func(c *managed.Command) { c.Revision = math.MaxInt64 }, managed.ErrConflict},
		{"maximum-epoch", func(c *managed.Command) { c.LeaseEpoch = math.MaxInt64 }, managed.ErrConflict},
		{"zero-epoch", func(c *managed.Command) { c.LeaseEpoch = 0 }, managed.ErrConflict},
	}

	for _, entry := range []string{"begin", "revoke", "command", "recovery"} {
		t.Run(entry, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					s := &quiescenceActionStore{Authority: controllerStore(t, "postgres"), t: t}
					a, b := acceptOriginalPair(t, s)

					drain := originalDrain(t, s, a)
					if _, err := s.RecordManagedOriginalDrain(t.Context(), drain); err != nil {
						t.Fatal(err)
					}

					for _, registration := range []managed.OriginalRegistrationAcceptanceRequest{a, b} {
						if _, err := s.AcceptManagedQuiescence(t.Context(), quiescenceRequest(t, s, registration, drain, false)); err != nil {
							t.Fatal(err)
						}
					}

					if _, err := s.SealManagedTarget(t.Context(), quiescenceCommand(t, s, a.Command.Key)); err != nil {
						t.Fatal(err)
					}

					rid := a.Command.ReservationID
					start := managed.AttemptStartRequest{Command: quiescenceCommand(t, s, a.Command.Key), ActionID: s.action.ID, AttemptID: id.New(managed.PrefixOperation), ProviderOperationID: id.New(managed.PrefixOperation), VerifierID: "provider"}

					attemptKey := managed.AttemptKey{Key: start.Key, AttemptID: start.AttemptID, Generation: "1"}
					if entry != "begin" {
						if _, err := s.BeginManagedRemovalAttempt(t.Context(), start); err != nil {
							t.Fatal(err)
						}
					}

					freshCommand := func() managed.Command { return quiescenceCommand(t, s, a.Command.Key) }
					raw := []byte("invalid-command-regression")
					native := managed.AttemptCommandRequest{AttemptRequest: managed.AttemptRequest{Command: freshCommand(), AttemptID: start.AttemptID, Generation: "1"}, ReservationID: rid, Kind: managed.AttemptBegin, ExpectedNativeVersion: "1", NativeCommand: managed.PortCommand{SchemaVersion: "1", OperationID: id.New(managed.PrefixOperation), RequestID: "invalid-command-regression", Original: raw, OriginalDigest: managed.Digest(raw), Native: raw, NativeDigest: managed.Digest(raw)}}
					recovery := managed.AttemptRecoveryRequest{ReservationID: rid, Kind: managed.AttemptBegin}

					if entry == "recovery" {
						grant, err := s.AuthorizeManagedAttemptCommand(t.Context(), native)
						if err != nil || !grant.NewlyIssued {
							t.Fatal("recovery setup", err)
						}

						encoded, err := json.Marshal(grant.Command)
						if err != nil {
							t.Fatal(err)
						}

						recovery.CommandDigest = managed.Digest(append([]byte("ctrlplane.managed.attempt-command.v1\n"), encoded...))
					}

					valid := freshCommand()
					if entry == "begin" {
						valid = start.Command
					}

					if valid.ID == start.AttemptID {
						t.Fatal("local and attempt identities must be distinct")
					}

					call := func(command managed.Command) (any, error) {
						request := managed.AttemptRequest{Command: command, AttemptID: start.AttemptID, Generation: "1"}

						switch entry {
						case "begin":
							q := start
							q.Command = command

							return s.BeginManagedRemovalAttempt(t.Context(), q)
						case "revoke":
							return s.RevokeManagedRemovalAttempt(t.Context(), request)
						case "command":
							q := native
							q.AttemptRequest = request

							return s.AuthorizeManagedAttemptCommand(t.Context(), q)
						default:
							q := recovery
							q.AttemptRequest = request

							return s.AuthorizeManagedAttemptRecovery(t.Context(), q)
						}
					}
					invalid := valid
					tc.change(&invalid)

					before := attemptPostgresSnapshot(t, s.Authority.(*postgres.Store))

					got, err := call(invalid)
					if !errors.Is(err, tc.want) {
						t.Errorf("invalid command: got %v, want %v", err, tc.want)
					}

					if !reflect.DeepEqual(got, reflect.Zero(reflect.TypeOf(got)).Interface()) {
						t.Error("refused command returned receipt or grant", got)
					}

					if !reflect.DeepEqual(before, attemptPostgresSnapshot(t, s.Authority.(*postgres.Store))) {
						t.Error("refused command changed retained state")
					}

					if t.Failed() {
						return
					}

					accepted, err := call(valid)
					if err != nil {
						t.Fatal("valid distinct local command", err)
					}

					switch grant := accepted.(type) {
					case managed.AttemptCommandGrant:
						if !grant.NewlyIssued {
							t.Fatal("fresh native command returned no authority")
						}

						grant.NewlyIssued = false
						accepted = grant
					case managed.AttemptRecoveryGrant:
						if !grant.NewlyAuthorized {
							t.Fatal("fresh recovery returned no authority")
						}

						grant.NewlyAuthorized = false
						accepted = grant
					}

					after := attemptPostgresSnapshot(t, s.Authority.(*postgres.Store))

					replay, err := call(valid)
					if err != nil || !reflect.DeepEqual(accepted, replay) {
						t.Fatal("exact replay changed", err)
					}

					if !reflect.DeepEqual(after, attemptPostgresSnapshot(t, s.Authority.(*postgres.Store))) {
						t.Fatal("exact replay changed retained state")
					}

					if _, err = s.ReadManagedRemovalAttempt(t.Context(), attemptKey); err != nil {
						t.Fatal("accepted attempt is unreadable", err)
					}
				})
			}
		})
	}
}

func TestManagedAttemptPostgresUnexpectedProviderErrorIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		size       int
	}{
		{"malformed-association", "removal_provider_slot", 2},
		{"oversized-association", "removal_provider_slot", managed.MaxAttemptAssociation + 1},
		{"oversized-failure", "step_failure", managed.MaxAttemptSettlement + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := managedPG(t)
			f, _ := removalActionFixture(t, s, controllerAction(t), 1)
			key := f.key

			record, err := s.ReadManagedRemovalAttempt(t.Context(), key)
			if err != nil {
				t.Fatal(err)
			}

			slotRaw, err := json.Marshal(struct {
				Key       managed.Key              `json:"key"`
				Operation id.ID                    `json:"operation"`
				Kind      managed.ProviderStepKind `json:"kind"`
				Service   string                   `json:"service"`
			}{key.Key, record.Descriptor.ProviderOperationID, managed.StepRemoveNetwork, ""})
			if err != nil {
				t.Fatal(err)
			}

			slot := managed.Digest(slotRaw)
			pg := pgdriver.Unwrap(s.DB())

			var scope string
			if err = pg.NewRaw("SELECT scope FROM cp_managed_controller_records WHERE kind = 'removal_attempt_state' AND key = $1", key.AttemptID.String()).Scan(t.Context(), &scope); err != nil {
				t.Fatal(err)
			}

			if tc.kind == "step_failure" {
				scope = key.TenantID
			}

			raw := []byte(`{}`)
			if tc.size > len(raw) {
				raw = bytes.Repeat([]byte("x"), tc.size)
			}
			// This disposable database injects corruption past the storage cap to test reader refusal.
			if tc.size > 2 {
				if _, err = pg.Exec(t.Context(), "ALTER TABLE cp_managed_controller_records DROP CONSTRAINT cp_managed_removal_attempt_size"); err != nil {
					t.Fatal(err)
				}
			}

			if _, err = pg.Exec(t.Context(), "INSERT INTO cp_managed_controller_records (kind, scope, key, data) VALUES ($1, $2, $3, $4)", tc.kind, scope, slot, raw); err != nil {
				t.Fatal(err)
			}

			before := attemptPostgresSnapshot(t, s)

			_, err = s.ReadManagedRemovalAttempt(t.Context(), key)
			if !reflect.DeepEqual(before, attemptPostgresSnapshot(t, s)) {
				t.Fatal("refused read mutated retained state")
			}

			if !errors.Is(err, managed.ErrUnavailable) || errors.Is(err, managed.ErrInvalid) || errors.Is(err, managed.ErrCapacity) {
				t.Fatal("unexpected provider presence must refuse as unavailable", err)
			}
		})
	}
}
