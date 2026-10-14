package memory

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

// This configured fixture authenticates deterministic test exports. It does not
// qualify a production provider, native guard adapter or external custody store.
type memoryRecoveryHost struct {
	managed.RemovalRevalidationProducer
	managed.RemovalCommitProducer
	managed.RemovalGuardCloseProducer
	managed.ProviderInvocationClosureProducer

	proofs, commits, closes int
}

func (h *memoryRecoveryHost) VerifyRemovalRevalidation(_ context.Context, _ managed.RecoveryRecord[managed.RecoveryNativeIntent], _ managed.RemovalRevalidationFacts) error {
	h.proofs++

	return nil
}
func (h *memoryRecoveryHost) VerifyRemovalCommit(_ context.Context, _ managed.RecoveryRecord[managed.RecoveryNativeIntent], _ managed.ProviderNativeCommitFacts) error {
	h.commits++

	return nil
}
func (h *memoryRecoveryHost) VerifyRemovalClose(_ context.Context, _ managed.RecoveryRecord[managed.RecoveryNativeIntent], _ managed.RecoveryNativeCloseFacts) error {
	h.closes++

	return nil
}
func (h *memoryRecoveryHost) VerifyProviderNoDispatch(context.Context, managed.RecoveryRecord[managed.ProviderInvocationPermission], *managed.RecoveryRecord[managed.ProviderInvocationMarker], managed.ProviderInvocationFailure) error {
	return managed.ErrUnsupported
}

func memoryRecoveryIdentity() managed.RecoveryProducerIdentity {
	return managed.RecoveryProducerIdentity{ProducerID: "fixture", Profile: "fixture.v1", Version: "1", VerifierID: "verifier"}
}
func memoryRecoveryExport() managed.RecoveryNativeExport {
	raw, attestation := []byte("authenticated fixture export"), []byte("fixture attestation")

	return managed.RecoveryNativeExport{Raw: raw, RawDigest: managed.Digest(raw), Attestation: attestation, AttestationDigest: managed.Digest(attestation), ProducerID: "fixture", ProducerProfile: "fixture.v1", ProducerVersion: "1", VerifierID: "verifier"}
}
func memoryRecoveryCommand(t *testing.T, s *Store, key managed.Key, ref managed.AttemptRef) managed.RecoveryCommand {
	t.Helper()
	local := projectionCommand(t, s, key)

	return managed.RecoveryCommand{Key: key, ID: local.ID, Revision: managed.Decimal(strconv.FormatUint(local.Revision, 10)), LeaseEpoch: managed.Decimal(strconv.FormatUint(local.LeaseEpoch, 10)), Owner: local.Owner, AttemptRef: ref, Profile: managed.RemovalRecoveryProfile}
}
func memoryRecoveryAttemptRequest(t *testing.T, s *Store, key managed.Key, ref managed.AttemptRef) managed.AttemptRequest {
	t.Helper()

	return managed.AttemptRequest{Command: projectionCommand(t, s, key), AttemptID: ref.AttemptID, Generation: ref.Generation}
}

// Association lookup inspects rows already accepted by public methods. It never
// writes authority or substitutes a synthetic checked attempt for the full graph.
func memoryRecoveryReference[T any](t *testing.T, s *Store, key managed.AttemptKey, record managed.RecoveryRecord[T]) managed.RecoveryAssociation {
	t.Helper()
	scope := memoryAttemptScope(t, key.Key)

	s.mu.RLock()
	defer s.mu.RUnlock()

	for rowKey, raw := range s.managedControllerRecords[string(record.Kind)+":"+scope] {
		saved, err := managed.DecodeRecoveryRecord[T](raw)
		if err != nil {
			t.Fatal(err)
		}

		if saved.Digest == record.Digest && saved.Acceptance == record.Acceptance {
			reference, err := managed.RecoveryAssociationFor(rowKey, saved)
			if err != nil {
				t.Fatal(err)
			}

			return reference
		}
	}

	t.Fatal("accepted recovery row not retained")

	return managed.RecoveryAssociation{}
}
func memoryRecoveryCurrent(t *testing.T, s *Store, key managed.AttemptKey, command id.ID) managed.ProviderStepRetryCurrent {
	t.Helper()
	scope := memoryAttemptScope(t, key.Key)

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, raw := range s.managedControllerRecords["step_retry_current:"+scope] {
		var current managed.ProviderStepRetryCurrent
		if err := json.Unmarshal(raw, &current); err != nil {
			t.Fatal(err)
		}

		if current.CommandID == command {
			return current
		}
	}

	t.Fatal("current invocation not retained")

	return managed.ProviderStepRetryCurrent{}
}
func memoryRecoveryPermissionProof(t *testing.T, s *Store, key managed.AttemptKey, invocation managed.ProviderInvocationKey) managed.RecoveryRecord[managed.ProviderPermissionProof] {
	t.Helper()
	scope := memoryAttemptScope(t, key.Key)

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, raw := range s.managedControllerRecords[string(managed.RecoveryPermissionProofRecord)+":"+scope] {
		record, err := managed.DecodeRecoveryRecord[managed.ProviderPermissionProof](raw)
		if err != nil {
			t.Fatal(err)
		}

		if record.Facts.Invocation == invocation {
			return record
		}
	}

	t.Fatal("permission proof not retained")

	return managed.RecoveryRecord[managed.ProviderPermissionProof]{}
}

type memoryRecoveryEffectHost struct{}

func (*memoryRecoveryEffectHost) RecoverProviderEffectBinding(_ context.Context, original managed.ProviderEffectOriginal) (managed.ProviderEffectBindingFacts, error) {
	kind := "container"
	if original.Key.Step.Kind == managed.StepRemoveNetwork {
		kind = "network"
	}
	// These identities stand for the fixture's independently retained create response.
	command := managed.ProviderEffectCommand{SchemaVersion: "1", Profile: "fixture.v1", BackendAuthority: "fixture-daemon", ResourceKind: kind, ResourceID: original.Permission.ResourceID, Incarnation: "fixture-create-response", Method: "remove", Options: []managed.ProviderEffectOption{}}

	raw, err := managed.EncodeProviderEffectCommand(command)
	if err != nil {
		return managed.ProviderEffectBindingFacts{}, err
	}

	digest, err := managed.ProviderEffectCommandDigest(raw)
	if err != nil {
		return managed.ProviderEffectBindingFacts{}, err
	}

	return managed.ProviderEffectBindingFacts{Original: original, Producer: memoryRecoveryIdentity(), Command: raw, ProviderCommandDigest: digest, Attestation: []byte("retained fixture create response")}, nil
}
func (*memoryRecoveryEffectHost) VerifyProviderEffectBinding(context.Context, managed.ProviderEffectOriginal, managed.ProviderEffectBindingFacts) error {
	return nil
}

func memoryRecoverySetup(t *testing.T) (*Store, *managedstate.Repository, *memoryRecoveryHost, managed.AttemptKey, managed.AttemptRef, managed.RemovalRecoveryBinding, managed.RecoveryRecord[managed.RemovalRevalidationFacts]) {
	t.Helper()
	s, action, key, rid := memoryAttemptFixture(t)
	record := memoryAttemptAdmit(t, s, action)

	original, err := s.ReadManagedAttemptBinding(t.Context(), managed.AttemptBindingKey{AttemptKey: key, ReservationID: rid})
	if err != nil {
		t.Fatal(err)
	}

	target, err := s.ReadManagedTarget(t.Context(), key.Key)
	if err != nil {
		t.Fatal(err)
	}

	var begin managed.AttemptCommandRecord

	for _, command := range original.Commands {
		if command.Key.Kind == managed.AttemptBegin {
			begin = command
		}
	}

	var drain managed.OriginalLifecycleReference

	for _, reservation := range target.Reservations {
		if reservation.ID == rid {
			if reservation.Drain == nil {
				t.Fatal("missing original drain")
			}

			drain, err = managed.ParseOriginalReference(reservation.Drain.Evidence)
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	binding := managed.RemovalRecoveryBinding{Key: original.Key, DescriptorDigest: record.State.AttemptRef.DescriptorDigest, ProviderOperationID: record.Descriptor.ProviderOperationID, OriginalRegistrationDigest: begin.BindingReference.OriginalRegistrationDigest, OriginalDrainDigest: drain.MaterialDigest, OriginalCommandDigest: original.Fence.CommandDigest, OriginalBeginRequestID: original.Fence.NativeAcceptance.RequestID, OriginalBeginRequestDigest: original.Fence.NativeAcceptance.RequestDigest, NativeFenceDigest: original.Fence.NativeFenceDigest, OriginalProcessDigest: begin.BindingReference.ProcessDigest, AdmissionDigest: record.State.AdmissionDigest}
	host := &memoryRecoveryHost{}
	identity := memoryRecoveryIdentity()

	repository, err := managedstate.New(managedBackend{store: s}).WithRemovalRecovery(managed.RemovalRecoveryHost{Revalidation: host, Commit: host, Close: host, Closure: host, Verifier: host, RevalidationIdentity: identity, CommitIdentity: identity, CloseIdentity: identity, ClosureVerifierID: "verifier"})
	if err != nil {
		t.Fatal(err)
	}

	ref := record.State.AttemptRef

	activation := managed.RemovalRecoveryActivationRequest{Command: memoryRecoveryCommand(t, s, key.Key, ref)}
	if _, err = repository.ActivateManagedRemovalRecovery(t.Context(), activation); err != nil {
		t.Fatal("activate full original graph", err)
	}

	native := []byte("native live guard")
	intentRequest := managed.RecoveryNativeIntentRequest{Command: memoryRecoveryCommand(t, s, key.Key, ref), Intent: managed.RecoveryNativeIntent{Binding: binding, Profile: managed.RemovalRecoveryProfile, Operation: "revalidate", RequestID: "native-live", RequestDigest: managed.Digest(native), Command: native, CommandDigest: managed.Digest(native)}}

	intent, err := repository.RecordManagedRecoveryNativeIntent(t.Context(), intentRequest)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	hash := strings.Repeat("a", 64)
	facts := managed.RemovalRevalidationFacts{Binding: binding, Intent: memoryRecoveryReference(t, s, key, intent), GuardID: "guard", GuardRootDigest: hash, NativeRequestID: intent.Facts.RequestID, NativeRequestDigest: intent.Facts.RequestDigest, NativeMaterialDigest: hash, NativeReceiptDigest: hash, NativeCurrentDigest: hash, NativeScopeDigest: hash, CandidateVersion: "1", RemovalEpoch: "1", CandidateDigest: hash, BuildDigest: hash, SurvivorDigest: hash, ExclusionDigest: hash, CoverageDigest: hash, ProcessDisposition: "live", SourceObservedAt: now, NativeAcceptedAt: now, ValidUntil: now.Add(time.Hour), Export: memoryRecoveryExport()}

	proof, err := repository.AcceptManagedRemovalRevalidation(t.Context(), managed.RemovalRevalidationRequest{Command: memoryRecoveryCommand(t, s, key.Key, ref), Facts: facts})
	if err != nil {
		t.Fatal(err)
	}

	return s, repository, host, key, ref, binding, proof
}

func memoryRecoveryCloseAndSettle(t *testing.T, s *Store, repository *managedstate.Repository, key managed.AttemptKey, ref managed.AttemptRef, binding managed.RemovalRecoveryBinding, proof managed.RecoveryRecord[managed.RemovalRevalidationFacts], disposition managed.Disposition) {
	t.Helper()

	cutoff, err := repository.CutoffManagedRemovalRecovery(t.Context(), memoryRecoveryCommand(t, s, key.Key, ref))
	if err != nil {
		t.Fatal(err)
	}

	cutoffRef, proofRef := memoryRecoveryReference(t, s, key, cutoff), memoryRecoveryReference(t, s, key, proof)
	native := []byte("native close guard")
	request := managed.RecoveryNativeIntentRequest{Command: memoryRecoveryCommand(t, s, key.Key, ref), Intent: managed.RecoveryNativeIntent{Binding: binding, Profile: managed.RemovalRecoveryProfile, Operation: "close", RequestID: "native-close", RequestDigest: managed.Digest(native), Command: native, CommandDigest: managed.Digest(native), Cutoff: &cutoffRef, Proof: &proofRef}}

	intent, err := repository.RecordManagedRecoveryNativeIntent(t.Context(), request)
	if err != nil {
		t.Fatal("first full close intent", err)
	}

	if intent.Facts.Coverage == nil {
		t.Fatal("missing derived coverage")
	}

	facts := managed.RecoveryNativeCloseFacts{Binding: binding, Intent: memoryRecoveryReference(t, s, key, intent), Cutoff: cutoffRef, Coverage: *intent.Facts.Coverage, GuardID: proof.Facts.GuardID, GuardRootDigest: proof.Facts.GuardRootDigest, NativeRequestID: intent.Facts.RequestID, NativeRequestDigest: intent.Facts.RequestDigest, NativeReceiptDigest: strings.Repeat("c", 64), NativeAcceptedAt: time.Now().UTC(), Export: memoryRecoveryExport()}

	closeRequest := managed.RecoveryNativeCloseRequest{Command: memoryRecoveryCommand(t, s, key.Key, ref), Facts: facts}
	if _, err = repository.RecordManagedRemovalGuardClose(t.Context(), closeRequest); err != nil {
		t.Fatal(err)
	}

	coverage, err := repository.ReadManagedEffectiveRemovalCoverage(t.Context(), key)
	if err != nil || coverage.Facts.Disposition != disposition {
		t.Fatal("coverage", err, coverage.Facts.Disposition)
	}

	profile := managed.LocalNoDispatchProfile
	if disposition == managed.Removed {
		profile = "ctrlplane.managed.remove.v1"
	}

	settlement := managed.AttemptSettlementRequest{AttemptRequest: memoryRecoveryAttemptRequest(t, s, key.Key, ref), Disposition: disposition, ProducerProfile: profile, VerifierID: "provider", Evidence: []byte("complete retained fixture custody"), SettledAt: time.Now().UTC()}

	first, err := repository.SettleManagedRemovalRecovery(t.Context(), settlement)
	if err != nil {
		t.Fatal("full settlement", err)
	}

	replay, err := repository.SettleManagedRemovalRecovery(t.Context(), settlement)
	if err != nil || replay != first {
		t.Fatal("settlement replay", err)
	}
}

func memoryRecoveryTerminalCommand(t *testing.T, s *Store, repository *managedstate.Repository, key managed.AttemptKey, ref managed.AttemptRef, binding managed.RemovalRecoveryBinding, kind string) managed.AttemptCommandGrant {
	t.Helper()

	native := []byte("original terminal " + kind)
	request := managed.AttemptCommandRequest{AttemptRequest: memoryRecoveryAttemptRequest(t, s, key.Key, ref), ReservationID: binding.Key.ReservationID, Kind: kind, ExpectedNativeVersion: "1", NativeCommand: managed.PortCommand{SchemaVersion: "1", OperationID: id.New(managed.PrefixOperation), RequestID: "terminal-" + kind, Original: native, OriginalDigest: managed.Digest(native), Native: native, NativeDigest: managed.Digest(native)}}

	grant, err := repository.RecordManagedRemovalRecoveryCommand(t.Context(), request)
	if err != nil || !grant.NewlyIssued {
		t.Fatal("terminal command", err)
	}

	replay, err := repository.RecordManagedRemovalRecoveryCommand(t.Context(), request)
	if err != nil || replay.NewlyIssued || !reflect.DeepEqual(replay.Command, grant.Command) {
		t.Fatal("terminal command replay", err)
	}

	return grant
}

func TestManagedRecoveryFullPublicUnissuedAbort(t *testing.T) {
	s, repository, host, key, ref, binding, proof := memoryRecoverySetup(t)
	memoryRecoveryCloseAndSettle(t, s, repository, key, ref, binding, proof, managed.SettledWithoutDeletion)
	// The original fence is read through the exact unchanged original record.
	scope := memoryAttemptScope(t, key.Key)

	var fence managed.AttemptFenceRecord

	s.mu.RLock()

	for _, raw := range s.managedControllerRecords["removal_fence:"+scope] {
		if err := json.Unmarshal(raw, &fence); err != nil {
			s.mu.RUnlock()
			t.Fatal(err)
		}
	}

	s.mu.RUnlock()

	var fenceEvent struct {
		FactDigest string `json:"fact_digest"`
	}

	s.mu.RLock()
	eventRaw := s.managedControllerRecords["removal_attempt_event:"+scope][fence.LocalOperationID.String()]
	s.mu.RUnlock()

	if err := json.Unmarshal(eventRaw, &fenceEvent); err != nil {
		t.Fatal(err)
	}

	fenceDigest := fenceEvent.FactDigest

	var err error

	witness := managed.AttemptBindingSettlementRequest{AttemptRequest: memoryRecoveryAttemptRequest(t, s, key.Key, ref), ReservationID: binding.Key.ReservationID, FenceFactDigest: fenceDigest}
	if _, err = repository.RecordManagedRemovalRecoveryBindingSettlement(t.Context(), witness); err != nil {
		t.Fatal("full witness", err)
	}

	grant := memoryRecoveryTerminalCommand(t, s, repository, key, ref, binding, managed.AttemptAbort)
	now := time.Now().UTC()
	request := managed.AttemptAbortRequest{AttemptRequest: memoryRecoveryAttemptRequest(t, s, key.Key, ref), ReservationID: binding.Key.ReservationID, Acceptance: memoryAttemptAcceptance(grant.Command), ProofVerifiedAt: now, ProofValidUntil: now.Add(time.Hour)}

	receipt, err := repository.RecordManagedRemovalRecoveryAbort(t.Context(), request)
	if err != nil {
		t.Fatal("full Abort", err)
	}

	replay, err := repository.RecordManagedRemovalRecoveryAbort(t.Context(), request)
	if err != nil || replay != receipt {
		t.Fatal("Abort replay", err)
	}

	coverage, err := repository.ReadManagedEffectiveRemovalCoverage(t.Context(), key)
	if err != nil {
		t.Fatal("historical coverage after Abort", err)
	}

	for _, step := range coverage.Facts.Steps {
		if step.Disposition != managed.EffectiveStepUnissued {
			t.Fatal("fabricated issuance", step)
		}
	}

	if host.proofs != 1 || host.closes != 1 || host.commits != 0 {
		t.Fatal("unexpected verifier calls", host)
	}
}

func TestManagedRecoveryFullPublicGuardedOriginalFinish(t *testing.T) {
	s, repository, host, key, ref, binding, proof := memoryRecoverySetup(t)

	target, err := s.ReadManagedTarget(t.Context(), key.Key)
	if err != nil {
		t.Fatal(err)
	}

	effectHost := &memoryRecoveryEffectHost{}

	effects, err := repository.WithProviderEffectBindings(managed.ProviderEffectBindingHost{Producer: effectHost, Verifier: effectHost, Identity: memoryRecoveryIdentity(), Provider: target.Physical.Provider, BackendAuthority: "fixture-daemon"})
	if err != nil {
		t.Fatal(err)
	}
	// The reused original fixture has api, sidecar and network in descriptor order.
	for _, service := range []string{"api", "sidecar", ""} {
		kind := managed.StepRemoveContainer
		if service == "" {
			kind = managed.StepRemoveNetwork
		}

		step := managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: key.Key}, ProviderOperationID: binding.ProviderOperationID, Kind: kind, Service: service}
		// The projection action is retained by the original descriptor.
		scope := memoryAttemptScope(t, key.Key)

		var descriptor managed.AttemptDescriptor

		s.mu.RLock()
		raw := s.managedControllerRecords["removal_attempt:"+scope][key.AttemptID.String()]
		s.mu.RUnlock()

		if err = json.Unmarshal(raw, &descriptor); err != nil {
			t.Fatal(err)
		}

		step.ActionID = descriptor.ActionID
		request := managed.GuardedOriginalRequest{Command: memoryRecoveryCommand(t, s, key.Key, ref), Step: step}

		pending, err := repository.PrepareManagedGuardedOriginal(t.Context(), request)
		if err != nil {
			t.Fatal("pending original", service, err)
		}

		replay, err := repository.PrepareManagedGuardedOriginal(t.Context(), request)
		if err != nil || !reflect.DeepEqual(replay, pending) {
			t.Fatal("pending replay", err)
		}

		materialize := managed.GuardedOriginalMaterializationRequest{Command: memoryRecoveryCommand(t, s, key.Key, ref), Step: step, PendingDigest: pending.PendingDigest}
		if _, err = repository.MaterializeManagedGuardedOriginal(t.Context(), materialize); err == nil {
			t.Fatal("missing effect evidence materialized")
		}

		if _, err = effects.RetainProviderEffectBinding(t.Context(), managed.ProviderEffectBindingRequest{Command: projectionCommand(t, s, key.Key), Original: pending.Original.Key}); err != nil {
			t.Fatal("exact provider evidence", err)
		}

		materialize.Command = memoryRecoveryCommand(t, s, key.Key, ref)

		root, err := repository.MaterializeManagedGuardedOriginal(t.Context(), materialize)
		if err != nil {
			t.Fatal("materialize original", err)
		}

		invocation := managed.ProviderInvocationKey{AttemptKey: key, Step: step, CommandID: pending.Original.Permission.CommandID}

		accepted, err := repository.ReadManagedProviderInvocation(t.Context(), invocation)
		if err != nil {
			t.Fatal(err)
		}

		current := memoryRecoveryCurrent(t, s, key, invocation.CommandID)

		premature := managed.ProviderInvocationCommitRequest{Command: memoryRecoveryCommand(t, s, key.Key, ref), Invocation: invocation, ExpectedCurrentDigest: current.Digest}
		if _, err = repository.CommitManagedProviderInvocation(t.Context(), premature); err == nil {
			t.Fatal("ordinal zero delegated without native binds")
		}

		permissionRef, proofRef := memoryRecoveryReference(t, s, key, accepted.Permission), memoryRecoveryReference(t, s, key, proof)
		native := []byte("bind " + invocation.CommandID.String())

		intent, err := repository.RecordManagedRecoveryNativeIntent(t.Context(), managed.RecoveryNativeIntentRequest{Command: memoryRecoveryCommand(t, s, key.Key, ref), Intent: managed.RecoveryNativeIntent{Binding: binding, Profile: managed.RemovalRecoveryProfile, Operation: "commit", RequestID: "bind-" + invocation.CommandID.String(), RequestDigest: managed.Digest(native), Command: native, CommandDigest: managed.Digest(native), Permission: &permissionRef, Proof: &proofRef}})
		if err != nil {
			t.Fatal("guarded original bind intent", err)
		}

		permissionProof := memoryRecoveryPermissionProof(t, s, key, invocation)

		permission, err := pending.Original.Permission.Value()
		if err != nil {
			t.Fatal(err)
		}

		permissionRaw, err := json.Marshal(permission)
		if err != nil {
			t.Fatal(err)
		}

		permissionDigest := managed.Digest(permissionRaw)

		facts := managed.ProviderNativeCommitFacts{Binding: binding, Invocation: invocation, Intent: memoryRecoveryReference(t, s, key, intent), PermissionDigest: permissionDigest, PermissionProof: memoryRecoveryReference(t, s, key, permissionProof), CurrentProof: proofRef, PermissionProofDigest: permissionProof.Digest, CurrentProofDigest: proof.Digest, GuardID: proof.Facts.GuardID, GuardRootDigest: proof.Facts.GuardRootDigest, NativeRequestID: intent.Facts.RequestID, NativeRequestDigest: intent.Facts.RequestDigest, NativeReceiptDigest: strings.Repeat("d", 64), NativeAcceptedAt: time.Now().UTC(), Export: memoryRecoveryExport()}
		if _, err = repository.RecordManagedProviderNativeCommit(t.Context(), managed.ProviderNativeCommitRequest{Command: memoryRecoveryCommand(t, s, key.Key, ref), Facts: facts}); err != nil {
			t.Fatal("guarded original native bind", err)
		}

		markerRequest := managed.ProviderInvocationCommitRequest{Command: memoryRecoveryCommand(t, s, key.Key, ref), Invocation: invocation, ExpectedCurrentDigest: current.Digest}

		grant, err := repository.CommitManagedProviderInvocation(t.Context(), markerRequest)
		if err != nil || !grant.MayDelegate {
			t.Fatal("first guarded original marker", err)
		}

		markerReplay, err := repository.CommitManagedProviderInvocation(t.Context(), markerRequest)
		if err != nil || markerReplay.MayDelegate {
			t.Fatal("marker replay repeated delegation", err)
		}

		current = memoryRecoveryCurrent(t, s, key, invocation.CommandID)
		result := managed.ProviderInvocationResult{Key: invocation, RootDigest: root.Digest, PermissionDigest: permissionDigest, Result: managed.WireProviderStepResult{Permission: pending.Original.Permission, Outcome: managed.PortTerminal, ResourceID: pending.Original.Permission.ResourceID, Evidence: []byte("exact positive provider response"), ObservedAt: time.Now().UTC()}}

		observation := managed.ProviderInvocationObservationRequest{Command: memoryRecoveryCommand(t, s, key.Key, ref), ExpectedCurrentDigest: current.Digest, Result: result}
		if err = repository.RecordManagedProviderInvocationResult(t.Context(), observation); err != nil {
			t.Fatal("positive guarded original result", err)
		}

		if err = repository.RecordManagedProviderInvocationResult(t.Context(), observation); err != nil {
			t.Fatal("positive result replay", err)
		}
	}

	memoryRecoveryCloseAndSettle(t, s, repository, key, ref, binding, proof, managed.Removed)
	grant := memoryRecoveryTerminalCommand(t, s, repository, key, ref, binding, managed.AttemptFinish)
	finish := managed.AttemptFinishRequest{AttemptRequest: memoryRecoveryAttemptRequest(t, s, key.Key, ref), ReservationID: binding.Key.ReservationID, Acceptance: memoryAttemptAcceptance(grant.Command)}

	receipt, err := repository.RecordManagedRemovalRecoveryFinish(t.Context(), finish)
	if err != nil {
		t.Fatal("full Finish", err)
	}

	replay, err := repository.RecordManagedRemovalRecoveryFinish(t.Context(), finish)
	if err != nil || receipt != replay {
		t.Fatal("Finish replay", err)
	}

	tombstone := memoryRecoveryAttemptRequest(t, s, key.Key, ref)

	receipt, err = repository.TombstoneManagedRemovalRecovery(t.Context(), tombstone)
	if err != nil {
		t.Fatal("full tombstone", err)
	}

	replay, err = repository.TombstoneManagedRemovalRecovery(t.Context(), tombstone)
	if err != nil || receipt != replay {
		t.Fatal("tombstone replay", err)
	}

	after, err := s.ReadManagedTarget(t.Context(), key.Key)
	if err != nil || after.Phase != managed.Deleted {
		t.Fatal("terminal target", err)
	}

	if _, err = repository.ReadManagedEffectiveRemovalCoverage(t.Context(), key); err != nil {
		t.Fatal("historical coverage after tombstone", err)
	}

	if host.proofs != 1 || host.commits != 3 || host.closes != 1 {
		t.Fatal("unexpected configured callbacks", host)
	}

	if _, err = s.ReadManagedRemovalAttempt(t.Context(), key); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal("legacy reader accepted schema two", err)
	}
}

type memoryRecoveryFaultBackend struct {
	managedBackend

	failAt    int
	failAfter bool
}
type memoryRecoveryFaultTransaction struct {
	managedstate.ControllerTransaction

	failAt, writes int
}

func (tx *memoryRecoveryFaultTransaction) WriteRecord(ctx context.Context, kind, scope, key string, raw []byte, insert bool) error {
	tx.writes++
	if tx.writes == tx.failAt {
		return errors.New("injected recovery row failure")
	}

	return tx.ControllerTransaction.WriteRecord(ctx, kind, scope, key, raw, insert)
}
func (b memoryRecoveryFaultBackend) ControllerAtomic(ctx context.Context, lock string, keys []managed.Key, change func(managedstate.ControllerTransaction) error) error {
	return b.managedBackend.ControllerAtomic(ctx, lock, keys, func(tx managedstate.ControllerTransaction) error {
		if err := change(&memoryRecoveryFaultTransaction{ControllerTransaction: tx, failAt: b.failAt}); err != nil {
			return err
		}

		if b.failAfter {
			return errors.New("injected post-receipt failure")
		}

		return nil
	})
}
func memoryRecoverySnapshot(t *testing.T, s *Store) []byte {
	t.Helper()

	s.mu.RLock()
	defer s.mu.RUnlock()

	raw, err := json.Marshal(struct {
		Targets  any `json:"targets"`
		Records  any `json:"records"`
		Receipts any `json:"receipts"`
	}{s.managedTargets, s.managedControllerRecords, s.managedReceipts})
	if err != nil {
		t.Fatal(err)
	}

	return raw
}
func TestManagedRecoveryPendingOriginalConcreteMemoryRollback(t *testing.T) {
	s, _, host, key, ref, binding, _ := memoryRecoverySetup(t)
	scope := memoryAttemptScope(t, key.Key)

	var descriptor managed.AttemptDescriptor

	s.mu.RLock()
	raw := s.managedControllerRecords["removal_attempt:"+scope][key.AttemptID.String()]
	s.mu.RUnlock()

	if err := json.Unmarshal(raw, &descriptor); err != nil {
		t.Fatal(err)
	}

	step := managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: key.Key, ActionID: descriptor.ActionID}, ProviderOperationID: binding.ProviderOperationID, Kind: managed.StepRemoveContainer, Service: "api"}

	for failure := 1; failure <= 6; failure++ {
		backend := memoryRecoveryFaultBackend{managedBackend: managedBackend{store: s}, failAt: failure, failAfter: failure == 6}
		identity := memoryRecoveryIdentity()

		repository, err := managedstate.New(backend).WithRemovalRecovery(managed.RemovalRecoveryHost{Revalidation: host, Commit: host, Close: host, Closure: host, Verifier: host, RevalidationIdentity: identity, CommitIdentity: identity, CloseIdentity: identity, ClosureVerifierID: "verifier"})
		if err != nil {
			t.Fatal(err)
		}

		request := managed.GuardedOriginalRequest{Command: memoryRecoveryCommand(t, s, key.Key, ref), Step: step}

		before := memoryRecoverySnapshot(t, s)
		if _, err = repository.PrepareManagedGuardedOriginal(t.Context(), request); err == nil {
			t.Fatal("injected pending failure accepted", failure)
		}

		if after := memoryRecoverySnapshot(t, s); !reflect.DeepEqual(before, after) {
			t.Fatal("partial concrete pending mutation", failure)
		}
	}
}

func TestManagedRecoveryOriginalMaterializationConcreteMemoryRollback(t *testing.T) {
	s, repository, host, key, ref, binding, _ := memoryRecoverySetup(t)
	scope := memoryAttemptScope(t, key.Key)

	var descriptor managed.AttemptDescriptor

	s.mu.RLock()
	raw := s.managedControllerRecords["removal_attempt:"+scope][key.AttemptID.String()]
	s.mu.RUnlock()

	if err := json.Unmarshal(raw, &descriptor); err != nil {
		t.Fatal(err)
	}

	step := managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: key.Key, ActionID: descriptor.ActionID}, ProviderOperationID: binding.ProviderOperationID, Kind: managed.StepRemoveContainer, Service: "api"}

	pending, err := repository.PrepareManagedGuardedOriginal(t.Context(), managed.GuardedOriginalRequest{Command: memoryRecoveryCommand(t, s, key.Key, ref), Step: step})
	if err != nil {
		t.Fatal(err)
	}

	target, err := s.ReadManagedTarget(t.Context(), key.Key)
	if err != nil {
		t.Fatal(err)
	}

	effectHost := &memoryRecoveryEffectHost{}
	identity := memoryRecoveryIdentity()

	effects, err := repository.WithProviderEffectBindings(managed.ProviderEffectBindingHost{Producer: effectHost, Verifier: effectHost, Identity: identity, Provider: target.Physical.Provider, BackendAuthority: "fixture-daemon"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err = effects.RetainProviderEffectBinding(t.Context(), managed.ProviderEffectBindingRequest{Command: projectionCommand(t, s, key.Key), Original: pending.Original.Key}); err != nil {
		t.Fatal(err)
	}
	// One binding writes eleven rows; the twelfth fault follows receipt acceptance
	// inside the concrete memory controller transaction and must roll it all back.
	for failure := 1; failure <= 12; failure++ {
		backend := memoryRecoveryFaultBackend{managedBackend: managedBackend{store: s}, failAt: failure, failAfter: failure == 12}

		faulted, configErr := managedstate.New(backend).WithRemovalRecovery(managed.RemovalRecoveryHost{Revalidation: host, Commit: host, Close: host, Closure: host, Verifier: host, RevalidationIdentity: identity, CommitIdentity: identity, CloseIdentity: identity, ClosureVerifierID: "verifier"})
		if configErr != nil {
			t.Fatal(configErr)
		}

		request := managed.GuardedOriginalMaterializationRequest{Command: memoryRecoveryCommand(t, s, key.Key, ref), Step: step, PendingDigest: pending.PendingDigest}

		before := memoryRecoverySnapshot(t, s)
		if _, err = faulted.MaterializeManagedGuardedOriginal(t.Context(), request); err == nil {
			t.Fatal("injected materialization failure accepted", failure)
		}

		if after := memoryRecoverySnapshot(t, s); !reflect.DeepEqual(before, after) {
			t.Fatal("partial concrete materialization", failure)
		}
	}
}
