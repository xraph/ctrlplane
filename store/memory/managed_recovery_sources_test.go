package memory

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/store/postgres"
)

var _ managed.ProviderInvocationSourcesStore = (*Store)(nil)
var _ managed.RemovalCutoffReader = (*Store)(nil)
var _ managed.ProviderInvocationSourcesStore = (*postgres.Store)(nil)
var _ managed.RemovalCutoffReader = (*postgres.Store)(nil)

func TestManagedRecoveryInvocationSourcesLegacyOriginal(t *testing.T) {
	s, repository, effects, host, authorization, original := ordinaryRemovalAcceptanceFixture(t)
	if _, err := effects.RetainProviderEffectBinding(t.Context(), managed.ProviderEffectBindingRequest{Command: projectionCommand(t, s, authorization.Key), Original: original.Key}); err != nil {
		t.Fatal(err)
	}

	if _, err := repository.ActivateManagedRemovalRecovery(t.Context(), managed.RemovalRecoveryActivationRequest{Command: memoryRecoveryCommand(t, s, authorization.Key, original.AttemptRef)}); err != nil {
		t.Fatal(err)
	}

	key := managed.ProviderInvocationKey{AttemptKey: original.Key.AttemptKey, Step: authorization.Step, CommandID: authorization.ID}
	before := memoryRecoverySnapshot(t, s)

	sources, err := repository.ReadManagedProviderInvocationSources(t.Context(), key)
	if err != nil || len(sources.PermissionProofs) != 0 || sources.Root.Facts.OriginalPermission != original.Permission || sources.Root.Facts.OriginalPermissionReceipt != original.Receipt {
		t.Fatal("legacy original source inspection", err)
	}

	if !reflect.DeepEqual(before, memoryRecoverySnapshot(t, s)) || host.calls != 1 {
		t.Fatal("source inspection wrote or invoked effect custody")
	}
}

func memoryRecoverySourcesFixture(t *testing.T) (*Store, *managedstate.Repository, *memoryRecoveryHost, managed.ProviderInvocationKey, managed.RecoveryAssociation, managed.RecoveryRecord[managed.RemovalRevalidationFacts]) {
	t.Helper()

	s, repository, host, key, ref, binding, proof := memoryRecoverySetup(t)

	target, err := s.ReadManagedTarget(t.Context(), key.Key)
	if err != nil {
		t.Fatal(err)
	}

	effects, err := repository.WithProviderEffectBindings(managed.ProviderEffectBindingHost{Producer: &memoryRecoveryEffectHost{}, Verifier: &memoryRecoveryEffectHost{}, Identity: memoryRecoveryIdentity(), Provider: target.Physical.Provider, BackendAuthority: "fixture-daemon"})
	if err != nil {
		t.Fatal(err)
	}

	var descriptor managed.AttemptDescriptor

	scope := memoryAttemptScope(t, key.Key)

	s.mu.RLock()
	raw := s.managedControllerRecords["removal_attempt:"+scope][key.AttemptID.String()]
	s.mu.RUnlock()

	if err = json.Unmarshal(raw, &descriptor); err != nil {
		t.Fatal(err)
	}

	step := managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: key.Key, ActionID: descriptor.ActionID}, ProviderOperationID: binding.ProviderOperationID, Kind: managed.StepRemoveContainer, Service: "api"}

	pending, err := repository.PrepareManagedGuardedOriginal(t.Context(), managed.GuardedOriginalRequest{Command: memoryRecoveryCommand(t, s, key.Key, ref), Step: step})
	if err != nil {
		t.Fatal(err)
	}

	if _, err = effects.RetainProviderEffectBinding(t.Context(), managed.ProviderEffectBindingRequest{Command: projectionCommand(t, s, key.Key), Original: pending.Original.Key}); err != nil {
		t.Fatal(err)
	}

	root, err := repository.MaterializeManagedGuardedOriginal(t.Context(), managed.GuardedOriginalMaterializationRequest{Command: memoryRecoveryCommand(t, s, key.Key, ref), Step: step, PendingDigest: pending.PendingDigest})
	if err != nil {
		t.Fatal(err)
	}

	return s, repository, host, managed.ProviderInvocationKey{AttemptKey: key, Step: step, CommandID: pending.Original.Permission.CommandID}, root, proof
}

func TestManagedRecoveryInvocationSourcesExactHistoricalProofs(t *testing.T) {
	s, repository, host, key, root, native := memoryRecoverySourcesFixture(t)
	original := memoryRecoveryPermissionProof(t, s, key.AttemptKey, key)

	memoryRecoverySourcesAdvanceLease(s, key.AttemptKey.Key)

	before := memoryRecoverySnapshot(t, s)
	calls := [3]int{host.proofs, host.commits, host.closes}

	sources, err := repository.ReadManagedProviderInvocationSources(t.Context(), key)
	if err != nil || len(sources.PermissionProofs) != 1 || !reflect.DeepEqual(sources.PermissionProofs[0], original) {
		t.Fatal("exact historical sources lost", err)
	}

	rootRef, err := managed.RecoveryRecordAssociation(key.AttemptKey, sources.Root)
	if err != nil || rootRef != root {
		t.Fatal("accepted root association changed", err)
	}

	reference, err := managed.RecoveryRecordAssociation(key.AttemptKey, native)
	if err != nil || sources.PermissionProofs[0].Facts.Proof != reference {
		t.Fatal("source selected a substitute native proof", err)
	}

	sources.PermissionProofs[0].Facts.NativeMaterialDigest = managed.Digest([]byte("caller mutation"))

	again, err := repository.ReadManagedProviderInvocationSources(t.Context(), key)
	if err != nil || !reflect.DeepEqual(again.PermissionProofs[0], original) || !reflect.DeepEqual(before, memoryRecoverySnapshot(t, s)) || calls != [3]int{host.proofs, host.commits, host.closes} {
		t.Fatal("inspection aliased storage, wrote, or called native producer", err)
	}
}

func TestManagedRecoveryInvocationSourcesMissingExactDependenciesRefuse(t *testing.T) {
	for _, kind := range []string{string(managed.RecoveryPermissionProofRecord), "removal_recovery_acceptance", "removal_recovery_request", "receipt"} {
		t.Run(kind, func(t *testing.T) {
			s, repository, host, key, _, native := memoryRecoverySourcesFixture(t)
			proof := memoryRecoveryPermissionProof(t, s, key.AttemptKey, key)

			proofRef, err := managed.RecoveryRecordAssociation(key.AttemptKey, proof)
			if err != nil {
				t.Fatal(err)
			}

			scope := memoryAttemptScope(t, key.AttemptKey.Key)
			rowKey := proofRef.Key

			if kind == "removal_recovery_request" {
				ref, refErr := managed.RecoveryRecordAssociation(key.AttemptKey, native)
				if refErr != nil {
					t.Fatal(refErr)
				}

				rowKey = ref.Key
			}

			s.mu.Lock()
			if kind == "receipt" {
				delete(s.managedReceipts, key.AttemptKey.InstanceID.String()+":"+proof.Acceptance.OperationID.String())
			} else {
				delete(s.managedControllerRecords[kind+":"+scope], rowKey)
			}
			s.mu.Unlock()

			before := memoryRecoverySnapshot(t, s)
			calls := [3]int{host.proofs, host.commits, host.closes}

			out, readErr := repository.ReadManagedProviderInvocationSources(t.Context(), key)
			if readErr == nil || !reflect.DeepEqual(out, managed.ProviderInvocationSources{}) || !reflect.DeepEqual(before, memoryRecoverySnapshot(t, s)) || calls != [3]int{host.proofs, host.commits, host.closes} {
				t.Fatal("missing dependency returned partial sources or changed authority", readErr)
			}
		})
	}
}

func TestManagedRecoveryCutoffExactHistoricalInspection(t *testing.T) {
	s, repository, host, key, ref, _, _ := memoryRecoverySetup(t)

	cutoff, err := repository.CutoffManagedRemovalRecovery(t.Context(), memoryRecoveryCommand(t, s, key.Key, ref))
	if err != nil {
		t.Fatal(err)
	}

	association, err := managed.RecoveryRecordAssociation(key, cutoff)
	if err != nil {
		t.Fatal(err)
	}

	memoryRecoverySourcesAdvanceLease(s, key.Key)

	request := managed.RecoveryExactRequest{Key: key, Association: association}
	before := memoryRecoverySnapshot(t, s)
	calls := [3]int{host.proofs, host.commits, host.closes}

	out, err := repository.ReadManagedRemovalCutoffExact(t.Context(), request)
	if err != nil || !reflect.DeepEqual(out, cutoff) {
		t.Fatal("exact cutoff receipt/time lost", err)
	}

	for _, mutate := range []func(*managed.RecoveryExactRequest){
		func(q *managed.RecoveryExactRequest) { q.Association.Key = managed.Digest([]byte("wrong key")) },
		func(q *managed.RecoveryExactRequest) { q.Association.Digest = managed.Digest([]byte("wrong digest")) },
		func(q *managed.RecoveryExactRequest) { q.Association.OperationID = id.New(managed.PrefixOperation) },
		func(q *managed.RecoveryExactRequest) {
			q.Association.ReceiptDigest = managed.Digest([]byte("wrong receipt"))
		},
		func(q *managed.RecoveryExactRequest) { q.Association.Kind = managed.RecoveryProofRecord },
		func(q *managed.RecoveryExactRequest) { q.Key.AttemptID = id.New(managed.PrefixOperation) },
	} {
		bad := request
		mutate(&bad)

		if rejected, readErr := repository.ReadManagedRemovalCutoffExact(t.Context(), bad); readErr == nil || !reflect.DeepEqual(rejected, managed.RecoveryRecord[managed.RemovalCutoffFacts]{}) {
			t.Fatal("inexact cutoff accepted", readErr)
		}
	}

	if !reflect.DeepEqual(before, memoryRecoverySnapshot(t, s)) || calls != [3]int{host.proofs, host.commits, host.closes} {
		t.Fatal("cutoff inspection changed authority")
	}

	s.mu.Lock()
	delete(s.managedReceipts, key.InstanceID.String()+":"+cutoff.Acceptance.OperationID.String())
	s.mu.Unlock()

	if _, err = repository.ReadManagedRemovalCutoffExact(t.Context(), request); err == nil {
		t.Fatal("missing independent cutoff receipt accepted")
	}
}

// Change only the fixture's current lease/revision. Legacy lease issuance is
// intentionally disabled after schema-2 activation; this tests historical
// inspection independently of that admission path.
func memoryRecoverySourcesAdvanceLease(s *Store, key managed.Key) {
	s.mu.Lock()
	defer s.mu.Unlock()

	target := s.managedTargets[key.InstanceID.String()]
	target.Revision++
	target.LeaseEpoch++
	target.LeaseOwner = "historical-reader-successor"
	target.LeaseUntil = time.Now().UTC().Add(-time.Minute)
}
