package memory

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/store/postgres"
)

var _ managed.RemovalCoverageSealer = (*Store)(nil)
var _ managed.RemovalCoverageSealer = (*postgres.Store)(nil)

func TestManagedRecoveryCoverageSealBeforeCloseAndReplay(t *testing.T) {
	s, repository, host, key, ref, binding, proof := memoryRecoverySetup(t)

	before := memoryRecoverySnapshot(t, s)
	if _, err := repository.SealManagedEffectiveRemovalCoverage(t.Context(), memoryRecoveryCommand(t, s, key.Key, ref)); err == nil || !reflect.DeepEqual(before, memoryRecoverySnapshot(t, s)) {
		t.Fatal("pre-cutoff seal changed state", err)
	}

	cutoff, err := repository.CutoffManagedRemovalRecovery(t.Context(), memoryRecoveryCommand(t, s, key.Key, ref))
	if err != nil {
		t.Fatal(err)
	}

	command := memoryRecoveryCommand(t, s, key.Key, ref)

	parentBefore, err := s.ReadManagedTarget(t.Context(), key.Key)
	if err != nil {
		t.Fatal(err)
	}

	scope := memoryAttemptScope(t, key.Key)
	stateBefore := append([]byte(nil), s.managedControllerRecords["removal_attempt_state:"+scope][key.AttemptID.String()]...)
	calls := [3]int{host.proofs, host.commits, host.closes}

	coverage, err := repository.SealManagedEffectiveRemovalCoverage(t.Context(), command)
	if err != nil {
		t.Fatal("seal", err)
	}

	parentAfter, err := s.ReadManagedTarget(t.Context(), key.Key)
	if err != nil || !reflect.DeepEqual(parentBefore, parentAfter) || !reflect.DeepEqual(stateBefore, s.managedControllerRecords["removal_attempt_state:"+scope][key.AttemptID.String()]) || coverage.Acceptance.Receipt.Revision != command.Revision {
		t.Fatal("nonissuing seal consumed counters or changed target", err)
	}

	before = memoryRecoverySnapshot(t, s)

	replay, err := repository.SealManagedEffectiveRemovalCoverage(t.Context(), command)
	if err != nil || !reflect.DeepEqual(replay, coverage) || !reflect.DeepEqual(before, memoryRecoverySnapshot(t, s)) || calls != [3]int{host.proofs, host.commits, host.closes} {
		t.Fatal("seal replay changed authority", err)
	}

	for _, mutate := range []func(*managed.RecoveryCommand){
		func(q *managed.RecoveryCommand) { q.ID = id.New(managed.PrefixOperation) },
		func(q *managed.RecoveryCommand) { q.Owner = "changed-owner" },
		func(q *managed.RecoveryCommand) { q.Revision = "1" },
	} {
		bad := command
		mutate(&bad)

		if _, err = repository.SealManagedEffectiveRemovalCoverage(t.Context(), bad); err == nil || !reflect.DeepEqual(before, memoryRecoverySnapshot(t, s)) {
			t.Fatal("different seal command accepted", err)
		}
	}

	coverageRef := memoryRecoveryReference(t, s, key, coverage)
	cutoffRef := memoryRecoveryReference(t, s, key, cutoff)
	proofRef := memoryRecoveryReference(t, s, key, proof)
	native := []byte("native close bound to accepted coverage:" + coverage.Digest)
	request := managed.RecoveryNativeIntentRequest{Command: memoryRecoveryCommand(t, s, key.Key, ref), Intent: managed.RecoveryNativeIntent{Binding: binding, Profile: managed.RemovalRecoveryProfile, Operation: "close", RequestID: "sealed-close", RequestDigest: managed.Digest(native), Command: native, CommandDigest: managed.Digest(native), Coverage: &coverageRef, Cutoff: &cutoffRef, Proof: &proofRef}}

	intent, err := repository.RecordManagedRecoveryNativeIntent(t.Context(), request)
	if err != nil || intent.Facts.Coverage == nil || *intent.Facts.Coverage != coverageRef {
		t.Fatal("close failed to bind accepted seal", err)
	}

	retained, err := repository.ReadManagedEffectiveRemovalCoverageExact(t.Context(), managed.RecoveryExactRequest{Key: key, Association: coverageRef})
	if err != nil || !reflect.DeepEqual(retained, coverage) {
		t.Fatal("close rewrote seal", err)
	}

	memoryRecoverySourcesAdvanceLease(s, key.Key)
	before = memoryRecoverySnapshot(t, s)

	replay, err = repository.SealManagedEffectiveRemovalCoverage(t.Context(), command)
	if err != nil || !reflect.DeepEqual(replay, coverage) || !reflect.DeepEqual(before, memoryRecoverySnapshot(t, s)) {
		t.Fatal("historical seal replay issued writes", err)
	}
}

func TestManagedRecoveryCoverageSealMissingCustodyRefuses(t *testing.T) {
	for _, kind := range []string{"receipt", "removal_attempt_event", "removal_recovery_acceptance", "removal_effective_binding", "command"} {
		t.Run(kind, func(t *testing.T) {
			s, repository, _, key, ref, _, _ := memoryRecoverySetup(t)
			if _, err := repository.CutoffManagedRemovalRecovery(t.Context(), memoryRecoveryCommand(t, s, key.Key, ref)); err != nil {
				t.Fatal(err)
			}

			command := memoryRecoveryCommand(t, s, key.Key, ref)

			coverage, err := repository.SealManagedEffectiveRemovalCoverage(t.Context(), command)
			if err != nil {
				t.Fatal(err)
			}

			scope := memoryAttemptScope(t, key.Key)
			reference := memoryRecoveryReference(t, s, key, coverage)
			s.mu.Lock()
			switch kind {
			case "receipt":
				delete(s.managedReceipts, key.InstanceID.String()+":"+command.ID.String())
			case "removal_attempt_event":
				delete(s.managedControllerRecords[kind+":"+scope], command.ID.String())
			case "removal_recovery_acceptance":
				delete(s.managedControllerRecords[kind+":"+scope], reference.Key)
			case "command":
				raw := s.managedControllerRecords["removal_coverage_selection:"+scope][key.AttemptID.String()]

				var selection map[string]json.RawMessage
				if err = json.Unmarshal(raw, &selection); err != nil {
					t.Fatal(err)
				}

				changed := command
				changed.Owner = "rewritten"

				selection["command"], err = json.Marshal(changed)
				if err != nil {
					t.Fatal(err)
				}

				raw, err = json.Marshal(selection)
				if err != nil {
					t.Fatal(err)
				}

				s.managedControllerRecords["removal_coverage_selection:"+scope][key.AttemptID.String()] = raw
			default:
				rows := s.managedControllerRecords[string(managed.RecoveryCoverageBindingRecord)+":"+scope]
				for row := range rows {
					delete(rows, row)

					break
				}
			}
			s.mu.Unlock()

			before := memoryRecoverySnapshot(t, s)
			if _, err = repository.ReadManagedEffectiveRemovalCoverage(t.Context(), key); err == nil {
				t.Fatal("missing seal custody accepted")
			}

			if _, err = repository.SealManagedEffectiveRemovalCoverage(t.Context(), command); err == nil || !reflect.DeepEqual(before, memoryRecoverySnapshot(t, s)) {
				t.Fatal("corrupt seal replay repaired or wrote", err)
			}
		})
	}
}

func TestManagedRecoveryCoverageSealIncompleteAndRollback(t *testing.T) {
	t.Run("incomplete", func(t *testing.T) {
		s, repository, _, invocation, _, _ := memoryRecoverySourcesFixture(t)
		key := invocation.AttemptKey

		sources, err := repository.ReadManagedProviderInvocationSources(t.Context(), invocation)
		if err != nil {
			t.Fatal(err)
		}

		ref := sources.Root.Facts.AttemptRef
		if _, err = repository.CutoffManagedRemovalRecovery(t.Context(), memoryRecoveryCommand(t, s, key.Key, ref)); err != nil {
			t.Fatal(err)
		}

		before := memoryRecoverySnapshot(t, s)
		if _, err = repository.SealManagedEffectiveRemovalCoverage(t.Context(), memoryRecoveryCommand(t, s, key.Key, ref)); err == nil || !reflect.DeepEqual(before, memoryRecoverySnapshot(t, s)) {
			t.Fatal("unresolved permission sealed", err)
		}
	})
	t.Run("rollback", func(t *testing.T) {
		s, repository, host, key, ref, _, _ := memoryRecoverySetup(t)
		if _, err := repository.CutoffManagedRemovalRecovery(t.Context(), memoryRecoveryCommand(t, s, key.Key, ref)); err != nil {
			t.Fatal(err)
		}

		for failure := 1; failure <= 7; failure++ {
			backend := memoryRecoveryFaultBackend{managedBackend: managedBackend{store: s}, failAt: failure, failAfter: failure == 7}
			identity := memoryRecoveryIdentity()

			failing, err := managedstate.New(backend).WithRemovalRecovery(managed.RemovalRecoveryHost{Revalidation: host, Commit: host, Close: host, Closure: host, Verifier: host, RevalidationIdentity: identity, CommitIdentity: identity, CloseIdentity: identity, ClosureVerifierID: "verifier"})
			if err != nil {
				t.Fatal(err)
			}

			before := memoryRecoverySnapshot(t, s)
			if _, err = failing.SealManagedEffectiveRemovalCoverage(t.Context(), memoryRecoveryCommand(t, s, key.Key, ref)); err == nil || !reflect.DeepEqual(before, memoryRecoverySnapshot(t, s)) {
				t.Fatal("partial seal survived failure", failure, err)
			}
		}
	})
}
