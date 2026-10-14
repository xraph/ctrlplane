package memory

import (
	"errors"
	"reflect"
	"testing"

	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

func TestManagedRecoveryConfiguredAttemptInspection(t *testing.T) {
	s, repository, host, key, ref, expected, _ := memoryRecoverySetup(t)
	before := memoryRecoverySnapshot(t, s)
	calls := [3]int{host.proofs, host.commits, host.closes}

	attempt, err := repository.ReadManagedRemovalAttempt(t.Context(), key)
	if err != nil || attempt.State.SchemaVersion != "2" || attempt.State.AttemptRef != ref || attempt.Descriptor.Key != key.Key || attempt.State.AdmissionDigest != expected.AdmissionDigest {
		t.Fatal("configured schema two attempt inspection", err)
	}

	binding, err := repository.ReadManagedAttemptBinding(t.Context(), expected.Key)
	if err != nil || binding.Fence == nil || binding.Key != expected.Key || binding.DescriptorDigest != ref.DescriptorDigest || binding.Fence.NativeFenceDigest != expected.NativeFenceDigest || len(binding.Commands) == 0 {
		t.Fatal("configured schema two binding inspection", err)
	}

	binding.Commands[0].NativeCommand.Native[0] ^= 0xff

	again, err := repository.ReadManagedAttemptBinding(t.Context(), expected.Key)
	if err != nil || reflect.DeepEqual(again, binding) {
		t.Fatal("caller changed saved binding", err)
	}

	if _, err = s.ReadManagedRemovalAttempt(t.Context(), key); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal("legacy unconfigured attempt accepted schema two", err)
	}

	if _, err = s.ReadManagedAttemptBinding(t.Context(), expected.Key); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal("legacy unconfigured binding accepted schema two", err)
	}

	if !reflect.DeepEqual(before, memoryRecoverySnapshot(t, s)) || calls != [3]int{host.proofs, host.commits, host.closes} {
		t.Fatal("inspection wrote or called configured producers")
	}
}

func TestManagedRecoveryConfiguredAttemptInspectionCorruption(t *testing.T) {
	for _, kind := range []string{"activation", "activation-receipt", "descriptor", "original-command", "original-fence"} {
		t.Run(kind, func(t *testing.T) {
			s, repository, host, key, _, binding, _ := memoryRecoverySetup(t)

			activation, err := repository.ReadManagedRecoveryActivation(t.Context(), key)
			if err != nil {
				t.Fatal(err)
			}

			scope := memoryAttemptScope(t, key.Key)

			s.mu.Lock()
			switch kind {
			case "activation":
				delete(s.managedControllerRecords, string(managed.RecoveryActivationRecord)+":"+scope)
			case "activation-receipt":
				delete(s.managedReceipts, key.InstanceID.String()+":"+activation.Acceptance.OperationID.String())
			case "descriptor":
				delete(s.managedControllerRecords["removal_attempt:"+scope], key.AttemptID.String())
			case "original-command":
				delete(s.managedControllerRecords, "removal_command:"+scope)
			case "original-fence":
				delete(s.managedControllerRecords, "removal_fence:"+scope)
			}
			s.mu.Unlock()

			before := memoryRecoverySnapshot(t, s)
			calls := [3]int{host.proofs, host.commits, host.closes}

			if _, err = repository.ReadManagedRemovalAttempt(t.Context(), key); err == nil {
				t.Fatal("corrupt recovery attempt accepted")
			}

			if _, err = repository.ReadManagedAttemptBinding(t.Context(), binding.Key); err == nil {
				t.Fatal("corrupt recovery binding accepted")
			}

			if !reflect.DeepEqual(before, memoryRecoverySnapshot(t, s)) || calls != [3]int{host.proofs, host.commits, host.closes} {
				t.Fatal("failed inspection wrote or called configured producers")
			}
		})
	}
}

func TestManagedRecoveryConfiguredAttemptInspectionPreservesLegacy(t *testing.T) {
	s, action, key, rid := memoryAttemptFixture(t)
	memoryAttemptAdmit(t, s, action)

	want, err := s.ReadManagedRemovalAttempt(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}

	bindingKey := managed.AttemptBindingKey{AttemptKey: key, ReservationID: rid}

	wantBinding, err := s.ReadManagedAttemptBinding(t.Context(), bindingKey)
	if err != nil {
		t.Fatal(err)
	}

	host := &memoryRecoveryHost{}
	identity := memoryRecoveryIdentity()

	repository, err := managedstate.New(managedBackend{store: s}).WithRemovalRecovery(managed.RemovalRecoveryHost{Revalidation: host, Commit: host, Close: host, Closure: host, Verifier: host, RevalidationIdentity: identity, CommitIdentity: identity, CloseIdentity: identity, ClosureVerifierID: "verifier"})
	if err != nil {
		t.Fatal(err)
	}

	before := memoryRecoverySnapshot(t, s)

	actual, err := repository.ReadManagedRemovalAttempt(t.Context(), key)
	if err != nil || !reflect.DeepEqual(actual, want) {
		t.Fatal("legacy attempt changed", err)
	}

	actualBinding, err := repository.ReadManagedAttemptBinding(t.Context(), bindingKey)
	if err != nil || !reflect.DeepEqual(actualBinding, wantBinding) {
		t.Fatal("legacy binding changed", err)
	}

	if !reflect.DeepEqual(before, memoryRecoverySnapshot(t, s)) || host.proofs != 0 || host.commits != 0 || host.closes != 0 {
		t.Fatal("legacy inspection wrote or invoked producer")
	}
}
