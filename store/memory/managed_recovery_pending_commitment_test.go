package memory

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/managed"
)

func TestManagedRecoveryMissingPendingSelectorRefusesEveryEntry(t *testing.T) {
	for _, corruption := range []string{"missing", "replaced", "missing commitment", "padding commitment", "short commitment"} {
		t.Run(corruption, func(t *testing.T) {
			s, repository, host, key, ref, binding, proof := memoryRecoverySetup(t)
			scope := memoryAttemptScope(t, key.Key)

			var descriptor managed.AttemptDescriptor

			s.mu.RLock()
			raw := s.managedControllerRecords["removal_attempt:"+scope][key.AttemptID.String()]
			s.mu.RUnlock()

			if err := json.Unmarshal(raw, &descriptor); err != nil {
				t.Fatal(err)
			}

			step := managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: key.Key, ActionID: descriptor.ActionID}, ProviderOperationID: binding.ProviderOperationID, Kind: managed.StepRemoveContainer, Service: "api"}
			request := managed.GuardedOriginalRequest{Command: memoryRecoveryCommand(t, s, key.Key, ref), Step: step}

			pending, err := repository.PrepareManagedGuardedOriginal(t.Context(), request)
			if err != nil {
				t.Fatal("pending acceptance", err)
			}
			// Cutoff must preserve the pending commitment without creating coverage.
			cutoff, err := repository.CutoffManagedRemovalRecovery(t.Context(), memoryRecoveryCommand(t, s, key.Key, ref))
			if err != nil {
				t.Fatal("cutoff", err)
			}

			cutoffRef, proofRef := memoryRecoveryReference(t, s, key, cutoff), memoryRecoveryReference(t, s, key, proof)
			s.mu.Lock()

			bucket := s.managedControllerRecords["guarded_original_selection:"+scope]
			if len(bucket) != 1 {
				s.mu.Unlock()
				t.Fatal("expected one pending selector")
			}

			if corruption == "missing commitment" || corruption == "padding commitment" || corruption == "short commitment" {
				stateKey := key.AttemptID.String()
				states := s.managedControllerRecords["removal_attempt_state:"+scope]

				var state map[string]json.RawMessage
				if err = json.Unmarshal(states[stateKey], &state); err != nil {
					s.mu.Unlock()
					t.Fatal(err)
				}

				switch corruption {
				case "missing commitment":
					delete(state, "pending_originals")
				case "padding commitment":
					var slots []string
					if err = json.Unmarshal(state["pending_originals"], &slots); err != nil {
						s.mu.Unlock()
						t.Fatal(err)
					}

					slots[len(slots)-1] = strings.Repeat("f", 64)
					state["pending_originals"], err = json.Marshal(slots)
				case "short commitment":
					state["pending_originals"], err = json.Marshal([]string{pending.PendingDigest})
				}

				if err != nil {
					s.mu.Unlock()
					t.Fatal(err)
				}

				states[stateKey], err = json.Marshal(state)
				if err != nil {
					s.mu.Unlock()
					t.Fatal(err)
				}
			} else {
				for selected, raw := range bucket {
					if corruption == "missing" {
						delete(bucket, selected)

						continue
					}

					var changed map[string]json.RawMessage
					if err = json.Unmarshal(raw, &changed); err != nil {
						s.mu.Unlock()
						t.Fatal(err)
					}

					changed["pending_digest"], err = json.Marshal(strings.Repeat("e", 64))
					if err != nil {
						s.mu.Unlock()
						t.Fatal(err)
					}

					bucket[selected], err = json.Marshal(changed)
					if err != nil {
						s.mu.Unlock()
						t.Fatal(err)
					}
				}
			}
			s.mu.Unlock()
			before := memoryRecoverySnapshot(t, s)
			fresh := request
			fresh.Command = memoryRecoveryCommand(t, s, key.Key, ref)
			native := []byte("native close with missing pending custody")
			closeRequest := managed.RecoveryNativeIntentRequest{Command: memoryRecoveryCommand(t, s, key.Key, ref), Intent: managed.RecoveryNativeIntent{Binding: binding, Profile: managed.RemovalRecoveryProfile, Operation: "close", RequestID: "missing-pending-close", RequestDigest: managed.Digest(native), Command: native, CommandDigest: managed.Digest(native), Cutoff: &cutoffRef, Proof: &proofRef}}
			settlement := managed.AttemptSettlementRequest{AttemptRequest: memoryRecoveryAttemptRequest(t, s, key.Key, ref), Disposition: managed.SettledWithoutDeletion, ProducerProfile: managed.LocalNoDispatchProfile, VerifierID: "provider", Evidence: []byte("cannot settle missing custody"), SettledAt: time.Now().UTC()}

			calls := map[string]func() error{
				"read": func() error {
					_, e := repository.ReadManagedProviderInvocation(t.Context(), managed.ProviderInvocationKey{AttemptKey: key, Step: step, CommandID: pending.Original.Permission.CommandID})

					return e
				},
				"coverage": func() error {
					_, e := repository.ReadManagedEffectiveRemovalCoverage(t.Context(), key)

					return e
				},
				"close": func() error {
					_, e := repository.RecordManagedRecoveryNativeIntent(t.Context(), closeRequest)

					return e
				},
				"settle": func() error {
					_, e := repository.SettleManagedRemovalRecovery(t.Context(), settlement)

					return e
				},
				"prepare replay": func() error {
					_, e := repository.PrepareManagedGuardedOriginal(t.Context(), request)

					return e
				},
				"prepare fresh": func() error {
					_, e := repository.PrepareManagedGuardedOriginal(t.Context(), fresh)

					return e
				},
			}
			for name, call := range calls {
				if err = call(); !errors.Is(err, managed.ErrUnavailable) {
					t.Fatal(name, "missing accepted dependency did not refuse", err)
				}

				if !bytes.Equal(before, memoryRecoverySnapshot(t, s)) {
					t.Fatal(name, "changed authority on refusal")
				}
			}

			if host.closes != 0 || host.commits != 0 {
				t.Fatal("corruption reached configured callbacks", host)
			}
			// The accepted pending record and independent parent receipt are still present.
			s.mu.RLock()
			_, retained := s.managedControllerRecords["removal_guarded_original:"+scope][request.Command.ID.String()]
			_, receipted := s.managedReceipts[key.InstanceID.String()+":"+request.Command.ID.String()]
			s.mu.RUnlock()

			if !retained || !receipted || request.Command.ID.Prefix() != managed.PrefixOperation {
				t.Fatal("test removed more than selector")
			}
		})
	}
}
