//go:build linux || darwin

package docker

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

// These fixtures model already enrolled authority. They are deliberately not a
// production enrollment implementation or proof of launcher/admission configuration.
type custodyAuthorityFixture struct {
	mu              sync.Mutex
	requests        map[string]ManagedDockerRequest
	invalid         bool
	sealUnavailable bool
}

func (a *custodyAuthorityFixture) trust(r ManagedDockerRequest) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.requests[custodyKey(r.Permission)] = custodyClone(r)
}
func (a *custodyAuthorityFixture) VerifyRequest(_ context.Context, r ManagedDockerRequest) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.invalid || !reflect.DeepEqual(a.requests[custodyKey(r.Permission)], r) {
		return managed.ErrAuthority
	}

	return nil
}
func (a *custodyAuthorityFixture) Admit(ctx context.Context, r ManagedDockerRequest) error {
	return a.VerifyRequest(ctx, r)
}
func custodyMAC(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}

	mac := hmac.New(sha256.New, []byte("test-only-independent-custody-key"))
	_, _ = mac.Write(raw)

	return mac.Sum(nil)
}
func (a *custodyAuthorityFixture) SealResponse(ctx context.Context, r ManagedDockerRequest, response ManagedDockerResponse) (ManagedDockerCustody, error) {
	if err := a.VerifyRequest(ctx, r); err != nil {
		return ManagedDockerCustody{}, err
	}

	a.mu.Lock()
	unavailable := a.sealUnavailable
	a.mu.Unlock()

	if unavailable {
		return ManagedDockerCustody{}, managed.ErrUnavailable
	}

	c := ManagedDockerCustody{ResourceID: response.ResourceID, Incarnation: "enrolled-creation-A"}
	if r.Command.Kind == managed.StepStartContainer {
		c.OriginalProcess = &managed.OriginalProcessFacts{RuntimeID: "enrolled-process-A"}
	}

	c.Attestation = custodyMAC(struct {
		Request  ManagedDockerRequest
		Response ManagedDockerResponse
		Custody  ManagedDockerCustody
	}{r, response, c})

	return c, nil
}
func (a *custodyAuthorityFixture) VerifyCustody(ctx context.Context, r ManagedDockerRequest, response ManagedDockerResponse, c ManagedDockerCustody) error {
	if err := a.VerifyRequest(ctx, r); err != nil {
		return err
	}

	attestation := c.Attestation

	c.Attestation = nil
	if c.Incarnation != "enrolled-creation-A" || c.OriginalProcess != nil && c.OriginalProcess.RuntimeID != "enrolled-process-A" || !hmac.Equal(attestation, custodyMAC(struct {
		Request  ManagedDockerRequest
		Response ManagedDockerResponse
		Custody  ManagedDockerCustody
	}{r, response, c})) {
		return managed.ErrAuthority
	}

	return nil
}
func (a *custodyAuthorityFixture) NoDispatch(ctx context.Context, r ManagedDockerRequest, digest string) (managed.ProviderInvocationFailure, error) {
	if err := a.VerifyRequest(ctx, r); err != nil {
		return managed.ProviderInvocationFailure{}, err
	}

	p, _ := r.Permission.Value()

	permissionRaw, err := json.Marshal(p)
	if err != nil {
		return managed.ProviderInvocationFailure{}, err
	}

	result := managed.ProviderInvocationFailure{Key: r.Invocation.Key, RootDigest: r.Invocation.RootDigest, PermissionDigest: managed.Digest(permissionRaw), Failure: managed.ProviderStepFailure{SchemaVersion: "1", Permission: p, AttemptRef: r.Binding.Original.AttemptRef, InvocationID: p.CommandID, ProducerProfile: managed.LocalNoDispatchProfile, VerifierID: "custody-fixture", Class: "local_no_dispatch", Phase: "before_transport_delegate", Reason: "journal_closed", ObservedAt: time.Now().UTC()}}
	result.Failure.Witness = custodyMAC(struct {
		Request ManagedDockerRequest
		Closed  string
		Failure managed.ProviderInvocationFailure
	}{r, digest, result})
	result.Failure.WitnessDigest = managed.Digest(result.Failure.Witness)

	return result, nil
}
func (a *custodyAuthorityFixture) VerifyNoDispatch(ctx context.Context, r ManagedDockerRequest, digest string, witness managed.ProviderInvocationFailure) error {
	if err := a.VerifyRequest(ctx, r); err != nil {
		return err
	}

	attestation := witness.Failure.Witness
	witness.Failure.Witness = nil

	witness.Failure.WitnessDigest = ""
	if !hmac.Equal(attestation, custodyMAC(struct {
		Request ManagedDockerRequest
		Closed  string
		Failure managed.ProviderInvocationFailure
	}{r, digest, witness})) {
		return managed.ErrAuthority
	}

	return nil
}
func (a *custodyAuthorityFixture) VerifyProviderEffectBinding(_ context.Context, o managed.ProviderEffectOriginal, f managed.ProviderEffectBindingFacts) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, r := range a.requests {
		if r.Binding != nil && reflect.DeepEqual(r.Binding.Original, o) && reflect.DeepEqual(*r.Binding, f) {
			return nil
		}
	}

	return managed.ErrAuthority
}

type custodyTransportFixture struct {
	calls atomic.Int32
	fail  bool
}

func (t *custodyTransportFixture) Execute(_ context.Context, c ManagedDockerCommand) (string, error) {
	t.calls.Add(1)

	if t.fail {
		return "", errors.New("response lost after possible effect")
	}

	if c.ResourceID != "" {
		return c.ResourceID, nil
	}

	return strings.Repeat("a", 64), nil
}

func custodyRequestFixture(kind managed.ProviderStepKind) ManagedDockerRequest {
	now := time.Now().UTC().Add(-time.Second)

	step := managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: managed.Key{TenantID: "tenant", InstanceID: id.New(id.PrefixInstance)}, ActionID: id.New(managed.PrefixOperation)}, ProviderOperationID: id.New(managed.PrefixOperation), Kind: kind}
	if kind == managed.StepCreateContainer || kind == managed.StepStartContainer || kind == managed.StepRemoveContainer {
		step.Service = "worker"
	}

	p := managed.ProviderStepPermission{Step: step, CommandID: id.New(managed.PrefixOperation), Digest: strings.Repeat("b", 64), SpecDigest: strings.Repeat("c", 64), CreateProfile: "ctrlplane.managed.create.v1", Physical: managed.Physical{Provider: "docker", Placement: "host", Project: "project", Generation: "generation"}, IssuedAt: now, Revision: 1}

	c := ManagedDockerCommand{Kind: kind}
	switch kind {
	case managed.StepCreateNetwork:
		c.Name = "display-name-only"
	case managed.StepCreateContainer:
		c.Name = "display-name-only"
		c.NetworkID = strings.Repeat("e", 64)
		c.Service = &managed.ProvisionService{Name: "worker", Image: "example/worker@sha256:" + strings.Repeat("f", 64), Role: "main", CPUMillis: "100", MemoryMiB: "64", PIDLimit: "32", Startup: managed.StartupBindingRef{BindingID: "startup", BindingGeneration: "1", BindingDigest: strings.Repeat("d", 64)}}
	default:
		p.ResourceID = strings.Repeat("a", 64)
		c.ResourceID = p.ResourceID
	}

	if kind == managed.StepRemoveNetwork || kind == managed.StepRemoveContainer {
		p.Seal = 1
		p.FenceDigest = strings.Repeat("d", 64)
	}

	r := ManagedDockerRequest{BackendAuthority: "engine-A", Permission: managed.ProviderPermissionWire(p), Receipt: managed.ReceiptWire(managed.Receipt{ID: p.CommandID, Digest: p.Digest, Revision: p.Revision, LeaseEpoch: 1, AcceptedAt: now, LeaseUntil: now.Add(time.Minute)}), Command: c}

	if kind == managed.StepRemoveNetwork || kind == managed.StepRemoveContainer {
		key := managed.AttemptKey{Key: step.Key, AttemptID: id.New(managed.PrefixOperation), Generation: "1"}
		original := managed.ProviderEffectOriginal{Key: managed.ProviderEffectBindingKey{AttemptKey: key, Step: step, OriginalCommandID: p.CommandID}, AttemptRef: managed.AttemptRef{AttemptID: key.AttemptID, Generation: "1", DescriptorDigest: strings.Repeat("e", 64)}, Permission: r.Permission, Receipt: r.Receipt}
		resourceKind, method := "container", "container_remove"
		options := []managed.ProviderEffectOption{{Name: "force", Value: "true"}, {Name: "remove_volumes", Value: "false"}}

		if kind == managed.StepRemoveNetwork {
			resourceKind, method = "network", "network_remove"
			options = []managed.ProviderEffectOption{}
		}

		raw, _ := managed.EncodeProviderEffectCommand(managed.ProviderEffectCommand{SchemaVersion: "1", Profile: ManagedDockerProfile, BackendAuthority: "engine-A", ResourceKind: resourceKind, ResourceID: p.ResourceID, Incarnation: "enrolled-creation-A", Method: method, Options: options})
		digest, _ := managed.ProviderEffectCommandDigest(raw)
		r.Binding = &managed.ProviderEffectBindingFacts{Original: original, Producer: managed.RecoveryProducerIdentity{ProducerID: "custody-fixture", Profile: ManagedDockerProfile, Version: "1", VerifierID: "custody-fixture"}, Command: raw, ProviderCommandDigest: digest, Attestation: []byte("fixture-original-attestation")}
		r.Invocation = &managed.ProviderInvocationPermission{Key: managed.ProviderInvocationKey{AttemptKey: key, Step: step, CommandID: p.CommandID}, RootDigest: strings.Repeat("f", 64), Ordinal: "0", Permission: r.Permission, ProviderCommandDigest: digest, PermissionProofSetDigest: strings.Repeat("e", 64), PermissionProofCount: "1"}
	}

	return r
}

func custodyJournalFixture(t *testing.T) (*FileManagedDockerJournal, string) {
	t.Helper()

	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if err = os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}

	journal, err := OpenFileManagedDockerJournal(directory)
	if err != nil {
		t.Fatal(err)
	}

	return journal, directory
}
func custodyExecutorFixture(t *testing.T, j ManagedDockerJournal, transport ManagedDockerTransport, a *custodyAuthorityFixture) *ManagedDockerExecutor {
	t.Helper()

	e, err := NewManagedDockerExecutor(j, transport, a, a, "engine-A")
	if err != nil {
		t.Fatal(err)
	}

	return e
}

func TestManagedDockerCreationAndRemovalReopen(t *testing.T) {
	for _, kind := range []managed.ProviderStepKind{managed.StepCreateNetwork, managed.StepCreateContainer, managed.StepStartContainer, managed.StepRemoveContainer, managed.StepRemoveNetwork} {
		t.Run(string(kind), func(t *testing.T) {
			journal, directory := custodyJournalFixture(t)
			transport := &custodyTransportFixture{}
			authority := &custodyAuthorityFixture{requests: map[string]ManagedDockerRequest{}}
			request := custodyRequestFixture(kind)
			authority.trust(request)
			executor := custodyExecutorFixture(t, journal, transport, authority)

			first, err := executor.Execute(t.Context(), request)
			if err != nil || first.State != "complete" || first.Response.ResourceID != strings.Repeat("a", 64) {
				t.Fatalf("result=%+v err=%v", first, err)
			}

			reopened, err := OpenFileManagedDockerJournal(directory)
			if err != nil {
				t.Fatal(err)
			}

			second, err := custodyExecutorFixture(t, reopened, transport, authority).Execute(t.Context(), request)
			if err != nil || !reflect.DeepEqual(first, second) || transport.calls.Load() != 1 {
				t.Fatalf("replay err=%v calls=%d", err, transport.calls.Load())
			}

			if request.Binding != nil {
				facts, readErr := executor.RecoverProviderEffectBinding(t.Context(), request.Binding.Original)
				if readErr != nil || !reflect.DeepEqual(facts, *request.Binding) {
					t.Fatal("original binding recovery", readErr)
				}
			}
		})
	}
}

func TestManagedDockerNoDispatchWinsOrDelegationWins(t *testing.T) {
	for range 12 {
		journal, directory := custodyJournalFixture(t)

		other, err := OpenFileManagedDockerJournal(directory)
		if err != nil {
			t.Fatal(err)
		}

		a := &custodyAuthorityFixture{requests: map[string]ManagedDockerRequest{}}
		request := custodyRequestFixture(managed.StepRemoveContainer)
		a.trust(request)

		transport := &custodyTransportFixture{}
		executor := custodyExecutorFixture(t, journal, transport, a)

		closer := custodyExecutorFixture(t, other, transport, a)
		if _, err = executor.Prepare(t.Context(), request); err != nil {
			t.Fatal(err)
		}

		start := make(chan struct{})

		var wg sync.WaitGroup
		wg.Add(2)

		var (
			closed   managed.ProviderInvocationFailure
			closeErr error
		)

		go func() { defer wg.Done(); <-start; _, _ = executor.Execute(t.Context(), request) }()
		go func() {
			defer wg.Done()

			<-start

			closed, closeErr = closer.WitnessManagedProviderNoDispatch(t.Context(), request.Invocation.Key)
		}()

		close(start)
		wg.Wait()

		if closeErr == nil {
			if transport.calls.Load() != 0 {
				t.Fatal("closed invocation dispatched")
			}

			replay, replayErr := closer.WitnessManagedProviderNoDispatch(t.Context(), request.Invocation.Key)
			if replayErr != nil || !reflect.DeepEqual(replay, closed) {
				t.Fatal("closure replay changed", replayErr)
			}
		} else if transport.calls.Load() != 1 {
			t.Fatalf("neither contender succeeded: %v", closeErr)
		}

		_, _ = executor.Execute(t.Context(), request)
		if transport.calls.Load() > 1 {
			t.Fatal("invocation resent")
		}
	}
}

func TestManagedDockerResponseLossAndMissingEnrollmentRemainUnresolved(t *testing.T) {
	for _, loss := range []string{"transport", "enrollment"} {
		t.Run(loss, func(t *testing.T) {
			journal, directory := custodyJournalFixture(t)
			a := &custodyAuthorityFixture{requests: map[string]ManagedDockerRequest{}, sealUnavailable: loss == "enrollment"}
			request := custodyRequestFixture(managed.StepCreateContainer)
			a.trust(request)

			transport := &custodyTransportFixture{fail: loss == "transport"}
			e := custodyExecutorFixture(t, journal, transport, a)

			first, err := e.Execute(t.Context(), request)
			if err == nil {
				t.Fatal("expected uncertainty")
			}

			if loss == "enrollment" && (first.State != "observed" || first.Response.ResourceID != strings.Repeat("a", 64)) {
				t.Fatal("lost actual creation ID")
			}

			reopened, err := OpenFileManagedDockerJournal(directory)
			if err != nil {
				t.Fatal(err)
			}

			e = custodyExecutorFixture(t, reopened, transport, a)
			a.mu.Lock()
			a.sealUnavailable = false
			a.mu.Unlock()

			second, replayErr := e.Execute(t.Context(), request)
			if transport.calls.Load() != 1 {
				t.Fatal("resent possible effect")
			}

			if loss == "transport" && (!errors.Is(replayErr, managed.ErrUnavailable) || second.State != "delegated") {
				t.Fatal("lost response became certain", replayErr)
			}

			if loss == "enrollment" && (replayErr != nil || second.State != "complete") {
				t.Fatal("could not recover retained response", replayErr)
			}
		})
	}
}

type lostClaimJournal struct {
	ManagedDockerJournal

	lost bool
}

func (j *lostClaimJournal) CompareAndSwap(ctx context.Context, key string, expected, next []byte) (bool, error) {
	ok, err := j.ManagedDockerJournal.CompareAndSwap(ctx, key, expected, next)
	if err == nil && ok && bytesContainState(next, "delegated") && !j.lost {
		j.lost = true

		return false, managed.ErrUnavailable
	}

	return ok, err
}
func bytesContainState(raw []byte, state string) bool {
	var r ManagedDockerRecord

	return json.Unmarshal(raw, &r) == nil && r.State == state
}

func TestManagedDockerLostDurableClaimNeverSends(t *testing.T) {
	journal, _ := custodyJournalFixture(t)
	a := &custodyAuthorityFixture{requests: map[string]ManagedDockerRequest{}}
	request := custodyRequestFixture(managed.StepRemoveContainer)
	a.trust(request)

	transport := &custodyTransportFixture{}

	e := custodyExecutorFixture(t, &lostClaimJournal{ManagedDockerJournal: journal}, transport, a)
	if _, err := e.Execute(t.Context(), request); err == nil {
		t.Fatal("lost claim acknowledged")
	}

	if _, err := e.Execute(t.Context(), request); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal(err)
	}

	if _, err := e.WitnessManagedProviderNoDispatch(t.Context(), request.Invocation.Key); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("delegated became closed", err)
	}

	if transport.calls.Load() != 0 {
		t.Fatal("sent without acknowledged durable claim")
	}
}

func TestManagedDockerForgeryReplacementAndRestartRefused(t *testing.T) {
	journal, _ := custodyJournalFixture(t)
	a := &custodyAuthorityFixture{requests: map[string]ManagedDockerRequest{}}
	request := custodyRequestFixture(managed.StepRemoveContainer)
	a.trust(request)

	transport := &custodyTransportFixture{}

	e := custodyExecutorFixture(t, journal, transport, a)
	if _, err := e.Prepare(t.Context(), request); err != nil {
		t.Fatal(err)
	}

	mutations := []func(*ManagedDockerRequest){
		func(r *ManagedDockerRequest) { r.BackendAuthority = "engine-B" },
		func(r *ManagedDockerRequest) { r.Command.ResourceID = strings.Repeat("b", 64) },
		func(r *ManagedDockerRequest) { r.Receipt.Digest = strings.Repeat("d", 64) },
		func(r *ManagedDockerRequest) { r.Binding.Attestation = []byte("forged") },
		func(r *ManagedDockerRequest) { r.Invocation.Key.CommandID = id.New(managed.PrefixOperation) },
		func(r *ManagedDockerRequest) { r.Invocation.Ordinal = "1" },
	}
	for _, mutate := range mutations {
		altered := custodyClone(request)
		mutate(&altered)

		if _, err := e.Execute(t.Context(), altered); err == nil {
			t.Fatal("forged association accepted")
		}
	}

	a.mu.Lock()
	a.invalid = true
	a.mu.Unlock()

	if _, err := e.Execute(t.Context(), request); !errors.Is(err, managed.ErrAuthority) {
		t.Fatal("replacement or changed process enrollment accepted", err)
	}

	if transport.calls.Load() != 0 {
		t.Fatal("refused request reached transport")
	}
}

func TestManagedDockerJournalBoundsCorruptionAndCancellation(t *testing.T) {
	journal, directory := custodyJournalFixture(t)

	key := strings.Repeat("a", 64)
	if _, err := journal.CompareAndSwap(t.Context(), key, nil, make([]byte, ManagedDockerJournalLimit+1)); !errors.Is(err, managed.ErrCapacity) {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := journal.CompareAndSwap(ctx, key, nil, []byte("{}")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	raw, err := journal.Load(t.Context(), key)
	if err != nil || len(raw) != 0 {
		t.Fatal("cancelled write leaked", err)
	}

	if err = os.Symlink(filepath.Join(directory, "untrusted"), filepath.Join(directory, key+".json")); err != nil {
		t.Fatal(err)
	}

	if _, err = journal.Load(t.Context(), key); err == nil {
		t.Fatal("symlink accepted")
	}

	if err = os.Remove(filepath.Join(directory, key+".json")); err != nil {
		t.Fatal(err)
	}

	a := &custodyAuthorityFixture{requests: map[string]ManagedDockerRequest{}}
	request := custodyRequestFixture(managed.StepCreateNetwork)
	a.trust(request)

	transport := &custodyTransportFixture{}

	e := custodyExecutorFixture(t, journal, transport, a)
	if _, err = e.Prepare(t.Context(), request); err != nil {
		t.Fatal(err)
	}

	if err = os.WriteFile(filepath.Join(directory, custodyKey(request.Permission)+".json"), []byte(`{"state":"complete"}`), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err = e.Execute(t.Context(), request); err == nil || transport.calls.Load() != 0 {
		t.Fatal("corrupt custody authorized send")
	}
}

func TestManagedDockerMissingConfiguredAuthorityRefused(t *testing.T) {
	journal, _ := custodyJournalFixture(t)

	var missing *custodyAuthorityFixture
	if _, err := NewManagedDockerExecutor(journal, &custodyTransportFixture{}, missing, missing, "engine-A"); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestManagedDockerRetainedCustodyAndClosureForgeryRefused(t *testing.T) {
	for _, kind := range []string{"creation_incarnation", "process_restart", "closure_witness"} {
		t.Run(kind, func(t *testing.T) {
			journal, directory := custodyJournalFixture(t)
			authority := &custodyAuthorityFixture{requests: map[string]ManagedDockerRequest{}}
			transport := &custodyTransportFixture{}

			step := managed.StepCreateContainer
			if kind == "process_restart" {
				step = managed.StepStartContainer
			}

			if kind == "closure_witness" {
				step = managed.StepRemoveContainer
			}

			request := custodyRequestFixture(step)
			authority.trust(request)
			e := custodyExecutorFixture(t, journal, transport, authority)

			var record ManagedDockerRecord

			if kind == "closure_witness" {
				if _, err := e.Prepare(t.Context(), request); err != nil {
					t.Fatal(err)
				}

				if _, err := e.WitnessManagedProviderNoDispatch(t.Context(), request.Invocation.Key); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := e.Execute(t.Context(), request); err != nil {
					t.Fatal(err)
				}
			}

			record, err := e.Read(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}

			switch kind {
			case "creation_incarnation":
				record.Custody.Incarnation = "replacement-B"
			case "process_restart":
				record.Custody.OriginalProcess.RuntimeID = "restarted-process-B"
			case "closure_witness":
				record.Closure.Failure.Witness = []byte("forged")
				record.Closure.Failure.WitnessDigest = managed.Digest(record.Closure.Failure.Witness)
			}

			raw, err := custodyBytes(record)
			if err != nil {
				t.Fatal(err)
			}

			if err = os.WriteFile(filepath.Join(directory, custodyKey(request.Permission)+".json"), raw, 0600); err != nil {
				t.Fatal(err)
			}

			if _, err = e.Read(t.Context(), request); !errors.Is(err, managed.ErrAuthority) {
				t.Fatal("canonical forged custody accepted", err)
			}
		})
	}
}
