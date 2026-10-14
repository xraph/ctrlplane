package memory

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

// This authority models independent fixture custody, not a production provider.
type ordinaryRemovalEffectHost struct {
	memoryRecoveryEffectHost

	original managed.ProviderEffectOriginal
	calls    int
}

func (h *ordinaryRemovalEffectHost) RecoverProviderEffectBinding(ctx context.Context, original managed.ProviderEffectOriginal) (managed.ProviderEffectBindingFacts, error) {
	h.calls++
	if !reflect.DeepEqual(original, h.original) {
		return managed.ProviderEffectBindingFacts{}, managed.ErrAuthority
	}

	return h.memoryRecoveryEffectHost.RecoverProviderEffectBinding(ctx, original)
}
func (h *ordinaryRemovalEffectHost) VerifyProviderEffectBinding(_ context.Context, original managed.ProviderEffectOriginal, facts managed.ProviderEffectBindingFacts) error {
	if !reflect.DeepEqual(original, h.original) || !reflect.DeepEqual(facts.Original, h.original) {
		return managed.ErrAuthority
	}

	return nil
}

func ordinaryRemovalAcceptanceFixture(t *testing.T) (*Store, *managedstate.Repository, *managedstate.ProviderEffectBindingRepository, *ordinaryRemovalEffectHost, managed.ProviderStepAuthorization, managed.ProviderEffectOriginal) {
	t.Helper()
	s, action, key, _ := memoryAttemptFixture(t)
	attempt := memoryAttemptAdmit(t, s, action)
	q := managed.ProviderStepAuthorization{Command: projectionCommand(t, s, key.Key), Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: key.Key, ActionID: action.ID}, ProviderOperationID: attempt.Descriptor.ProviderOperationID, Kind: managed.StepRemoveContainer, Service: "api"}}

	grant, err := s.AuthorizeManagedProviderStep(t.Context(), q)
	if err != nil || !grant.NewlyIssued {
		t.Fatal("real original authorization", err)
	}

	s.mu.RLock()
	receipt := s.managedReceipts[key.InstanceID.String()+":"+q.ID.String()]
	s.mu.RUnlock()

	envelope, err := json.Marshal(struct {
		Kind  string `json:"kind"`
		Input any    `json:"input"`
	}{"provider_step", q})
	if err != nil {
		t.Fatal(err)
	}

	if receipt.Digest != managed.Digest(envelope) || receipt.Digest == grant.Permission.Digest || !receipt.AcceptedAt.Equal(grant.Permission.IssuedAt) {
		t.Fatal("fixture did not retain independent real apply receipt")
	}

	original := managed.ProviderEffectOriginal{Key: managed.ProviderEffectBindingKey{AttemptKey: key, Step: q.Step, OriginalCommandID: q.ID}, AttemptRef: attempt.State.AttemptRef, Permission: managed.ProviderPermissionWire(grant.Permission), Receipt: managed.ReceiptWire(receipt)}
	host := &memoryRecoveryHost{}
	identity := memoryRecoveryIdentity()

	repository, err := managedstate.New(managedBackend{store: s}).WithRemovalRecovery(managed.RemovalRecoveryHost{Revalidation: host, Commit: host, Close: host, Closure: host, Verifier: host, RevalidationIdentity: identity, CommitIdentity: identity, CloseIdentity: identity, ClosureVerifierID: "verifier"})
	if err != nil {
		t.Fatal(err)
	}

	effectHost := &ordinaryRemovalEffectHost{original: original}

	effects, err := repository.WithProviderEffectBindings(managed.ProviderEffectBindingHost{Producer: effectHost, Verifier: effectHost, Identity: identity, Provider: grant.Permission.Physical.Provider, BackendAuthority: "fixture-daemon"})
	if err != nil {
		t.Fatal(err)
	}

	return s, repository, effects, effectHost, q, original
}

func TestManagedOrdinaryRemovalIndependentReceiptRecovery(t *testing.T) {
	s, repository, effects, host, q, original := ordinaryRemovalAcceptanceFixture(t)

	replay, replayErr := s.AuthorizeManagedProviderStep(t.Context(), q)
	if replayErr != nil || replay.NewlyIssued || managed.ProviderPermissionWire(replay.Permission) != original.Permission {
		t.Fatal("original replay changed permission", replayErr)
	}

	request := managed.ProviderEffectBindingRequest{Command: projectionCommand(t, s, q.Key), Original: original.Key}

	binding, err := effects.RetainProviderEffectBinding(t.Context(), request)
	if err != nil || !reflect.DeepEqual(binding.Facts.Original, original) {
		t.Fatal("real original binding", err)
	}

	activation := managed.RemovalRecoveryActivationRequest{Command: memoryRecoveryCommand(t, s, q.Key, original.AttemptRef)}
	if _, err = repository.ActivateManagedRemovalRecovery(t.Context(), activation); err != nil {
		t.Fatal("ordinary original activation", err)
	}

	before := memoryRecoverySnapshot(t, s)
	invocation := managed.ProviderInvocationKey{AttemptKey: original.Key.AttemptKey, Step: q.Step, CommandID: q.ID}

	accepted, err := repository.ReadManagedProviderInvocation(t.Context(), invocation)
	if err != nil || accepted.Permission.Facts.Ordinal != "0" || accepted.Permission.Facts.Permission != original.Permission {
		t.Fatal("historical initial invocation", err)
	}

	again, err := repository.ReadManagedProviderInvocation(t.Context(), invocation)
	if err != nil || !reflect.DeepEqual(accepted, again) {
		t.Fatal("historical replay", err)
	}

	if !reflect.DeepEqual(before, memoryRecoverySnapshot(t, s)) || host.calls != 1 {
		t.Fatal("historical read mutated state or called provider")
	}

	grant, err := s.AuthorizeManagedProviderStep(t.Context(), q)
	if !errors.Is(err, managed.ErrUnsupported) || grant.NewlyIssued {
		t.Fatal("active recovery accepted legacy original replay", err)
	}
}

func TestManagedOrdinaryRemovalReceiptCorruptionRefuses(t *testing.T) {
	for _, stage := range []string{"before binding", "after activation"} {
		for _, mutation := range []string{"missing receipt", "changed receipt", "changed permission"} {
			t.Run(stage+"/"+mutation, func(t *testing.T) {
				s, repository, effects, host, q, original := ordinaryRemovalAcceptanceFixture(t)
				if stage == "after activation" {
					if _, err := effects.RetainProviderEffectBinding(t.Context(), managed.ProviderEffectBindingRequest{Command: projectionCommand(t, s, q.Key), Original: original.Key}); err != nil {
						t.Fatal(err)
					}

					if _, err := repository.ActivateManagedRemovalRecovery(t.Context(), managed.RemovalRecoveryActivationRequest{Command: memoryRecoveryCommand(t, s, q.Key, original.AttemptRef)}); err != nil {
						t.Fatal(err)
					}
				}

				command := projectionCommand(t, s, q.Key)
				s.mu.Lock()
				receiptKey := q.InstanceID.String() + ":" + q.ID.String()

				switch mutation {
				case "missing receipt":
					delete(s.managedReceipts, receiptKey)
				case "changed receipt":
					receipt := s.managedReceipts[receiptKey]
					receipt.Digest = strings.Repeat("9", 64)
					s.managedReceipts[receiptKey] = receipt
				case "changed permission":
					found := false

					for key, raw := range s.managedControllerRecords["step:"+q.TenantID] {
						var record managed.ProviderStepRecord
						if err := json.Unmarshal(raw, &record); err != nil {
							s.mu.Unlock()
							t.Fatal(err)
						}

						if record.Permission.CommandID != q.ID {
							continue
						}

						record.Permission.ResourceID = "different-valid-resource"

						changed, err := json.Marshal(record)
						if err != nil {
							s.mu.Unlock()
							t.Fatal(err)
						}

						s.managedControllerRecords["step:"+q.TenantID][key] = changed
						found = true
					}

					if !found {
						s.mu.Unlock()
						t.Fatal("missing original fixture permission")
					}
				}
				s.mu.Unlock()

				before, calls := memoryRecoverySnapshot(t, s), host.calls
				if stage == "before binding" {
					if _, err := effects.RetainProviderEffectBinding(t.Context(), managed.ProviderEffectBindingRequest{Command: command, Original: original.Key}); err == nil {
						t.Fatal("corrupt original bound")
					}
				} else {
					if _, err := repository.ReadManagedProviderInvocation(t.Context(), managed.ProviderInvocationKey{AttemptKey: original.Key.AttemptKey, Step: q.Step, CommandID: q.ID}); err == nil {
						t.Fatal("corrupt original read")
					}
				}

				if !reflect.DeepEqual(before, memoryRecoverySnapshot(t, s)) || host.calls != calls {
					t.Fatal("refusal mutated state or invoked producer")
				}
			})
		}
	}
}
