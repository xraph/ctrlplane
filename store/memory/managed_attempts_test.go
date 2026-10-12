package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/store/internal/continuationtest"
)

func memoryAttemptRequest(t *testing.T, s *Store, action managed.ActionSubmission) managed.AttemptRequest {
	t.Helper()

	op, err := s.ReadManagedOperation(t.Context(), managed.OperationKey{Key: action.Key, ActionID: action.ID})
	if err != nil {
		t.Fatal(err)
	}

	if op.RemovalAttemptRoot == nil {
		t.Fatal("missing attempt anchor")
	}

	return managed.AttemptRequest{Command: projectionCommand(t, s, action.Key), AttemptID: op.RemovalAttemptRoot.AttemptID, Generation: "1"}
}
func memoryAttemptStart(t *testing.T, s *Store, action managed.ActionSubmission, rid id.ID) {
	t.Helper()

	q := managed.AttemptStartRequest{Command: projectionCommand(t, s, action.Key), ActionID: action.ID, AttemptID: id.New(managed.PrefixOperation), ProviderOperationID: id.New(managed.PrefixOperation), VerifierID: "provider"}
	if _, err := s.BeginManagedRemovalAttempt(t.Context(), q); err != nil {
		t.Fatal(err)
	}

	grant := memoryAttemptCommand(t, s, action, rid, managed.AttemptBegin)

	proof := []byte("fence")
	if _, err := s.RecordManagedAttemptFence(t.Context(), managed.AttemptFenceRequest{AttemptRequest: memoryAttemptRequest(t, s, action), ReservationID: rid, Acceptance: memoryAttemptAcceptance(grant.Command), Fence: managed.FenceWire(managed.Fence{Epoch: 1, Evidence: proof, Digest: managed.Digest(proof), CompatibilityEvidence: []byte("retired"), ValidUntil: time.Now().UTC().Add(time.Hour), Survivors: []string{"off-instance"}})}); err != nil {
		t.Fatal(err)
	}
}
func memoryAttemptCommand(t *testing.T, s *Store, action managed.ActionSubmission, rid id.ID, kind string) managed.AttemptCommandGrant {
	t.Helper()

	raw := []byte("native " + kind)
	q := managed.AttemptCommandRequest{AttemptRequest: memoryAttemptRequest(t, s, action), ReservationID: rid, Kind: kind, ExpectedNativeVersion: "1", NativeCommand: managed.PortCommand{SchemaVersion: "1", OperationID: id.New(managed.PrefixOperation), RequestID: "request-" + kind, Original: raw, OriginalDigest: managed.Digest(raw), Native: raw, NativeDigest: managed.Digest(raw)}}

	out, err := s.AuthorizeManagedAttemptCommand(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}

	return out
}
func memoryAttemptAcceptance(c managed.AttemptCommandRecord) managed.AttemptNativeAcceptance {
	raw := []byte("native receipt")
	proof := []byte("fresh binding proof")

	return managed.AttemptNativeAcceptance{NativeAcceptance: managed.AttemptNativeFacts{RequestID: c.NativeCommand.RequestID, RequestDigest: c.NativeCommand.NativeDigest, CommandDigest: managed.Digest([]byte("native command")), ResponseVersion: "1", BindingVersion: "1", RemovalEpoch: "1", AcceptedAt: time.Now().UTC()}, NativeFenceDigest: managed.Digest([]byte("native fence")), ReceiptID: "receipt", Receipt: raw, ReceiptDigest: managed.Digest(raw), OriginalBindingProof: proof, ProofDigest: managed.Digest(proof)}
}
func memoryAttemptFixture(t *testing.T) (*Store, managed.ActionSubmission, managed.AttemptKey, id.ID) {
	t.Helper()
	s, action, q := memoryQuiescenceProvider(t)
	q.Command = memoryOriginalCommand(t, s, action.Key, q.Command.ReservationID)
	q.Observation.ObservedAt = time.Now().UTC().Add(-time.Millisecond)
	q.Observation.ValidUntil = q.Observation.ObservedAt.Add(time.Minute)
	signMemoryQuiescence(t, &q)

	if _, err := s.AcceptManagedQuiescence(t.Context(), q); err != nil {
		t.Fatal(err)
	}

	request := memoryAttemptRequest(t, s, action)

	return s, action, managed.AttemptKey{Key: action.Key, AttemptID: request.AttemptID, Generation: "1"}, q.Command.ReservationID
}
func memoryAttemptScope(t *testing.T, k managed.Key) string {
	t.Helper()

	raw, err := json.Marshal(struct {
		Key managed.Key `json:"key"`
	}{k})
	if err != nil {
		t.Fatal(err)
	}

	return managed.Digest(append([]byte("ctrlplane.managed.attempt-scope.v1\n"), raw...))
}

func memoryAttemptAdmit(t *testing.T, s *Store, action managed.ActionSubmission) managed.AttemptRecord {
	t.Helper()

	q := memoryAttemptRequest(t, s, action)
	if _, err := s.AdmitManagedRemovalAttempt(t.Context(), managed.AttemptAdmissionRequest{AttemptRequest: q, VerifierID: "provider"}); err != nil {
		t.Fatal(err)
	}

	rec, err := s.ReadManagedRemovalAttempt(t.Context(), managed.AttemptKey{Key: action.Key, AttemptID: q.AttemptID, Generation: "1"})
	if err != nil {
		t.Fatal(err)
	}

	return rec
}
func memoryAttemptStep(t *testing.T, s *Store, action managed.ActionSubmission, record managed.AttemptRecord, service string) managed.ProviderStepGrant {
	t.Helper()

	kind := managed.StepRemoveContainer
	if service == "" {
		kind = managed.StepRemoveNetwork
	}

	q := managed.ProviderStepAuthorization{Command: projectionCommand(t, s, action.Key), Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: action.Key, ActionID: action.ID}, ProviderOperationID: record.Descriptor.ProviderOperationID, Kind: kind, Service: service}}

	grant, err := s.AuthorizeManagedProviderStep(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}

	return grant
}
func memoryAttemptFailure(t *testing.T, s *Store, record managed.AttemptRecord, grant managed.ProviderStepGrant) managed.ProviderStepFailure {
	t.Helper()

	witness := []byte("hook refused before transport delegate")

	return managed.ProviderStepFailure{SchemaVersion: "1", AttemptRef: record.State.AttemptRef, Permission: grant.Permission, InvocationID: grant.Permission.CommandID, ProducerProfile: managed.LocalNoDispatchProfile, VerifierID: "hook", Class: "local_no_dispatch", Phase: "before_transport_delegate", Reason: "authority_refused", ObservedAt: time.Now().UTC(), Witness: witness, WitnessDigest: managed.Digest(witness)}
}
func memoryAttemptScenario(t *testing.T, scenario string) (*Store, managed.ActionSubmission, managed.AttemptKey) {
	t.Helper()

	s, action, key, rid := memoryAttemptFixture(t)
	if scenario == "abort" {
		if _, err := s.RevokeManagedRemovalAttempt(t.Context(), memoryAttemptRequest(t, s, action)); err != nil {
			t.Fatal(err)
		}

		binding, err := s.ReadManagedAttemptBinding(t.Context(), managed.AttemptBindingKey{AttemptKey: key, ReservationID: rid})
		if err != nil {
			t.Fatal(err)
		}

		raw, err := json.Marshal(struct {
			Kind  string `json:"kind"`
			Value any    `json:"value"`
		}{"removal_fence", *binding.Fence})
		if err != nil {
			t.Fatal(err)
		}

		hash := managed.Digest(append([]byte("ctrlplane.managed.attempt-fact.v1\n"), raw...))
		if _, err = s.RecordManagedAttemptBindingSettlement(t.Context(), managed.AttemptBindingSettlementRequest{AttemptRequest: memoryAttemptRequest(t, s, action), ReservationID: rid, FenceFactDigest: hash}); err != nil {
			t.Fatal(err)
		}

		grant := memoryAttemptCommand(t, s, action, rid, managed.AttemptAbort)
		if _, err = s.RecordManagedAttemptAbort(t.Context(), managed.AttemptAbortRequest{AttemptRequest: memoryAttemptRequest(t, s, action), ReservationID: rid, Acceptance: memoryAttemptAcceptance(grant.Command), ProofVerifiedAt: time.Now().UTC(), ProofValidUntil: time.Now().UTC().Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}

		return s, action, key
	}

	record := memoryAttemptAdmit(t, s, action)

	grant := memoryAttemptStep(t, s, action, record, "api")
	if scenario == "issued" {
		return s, action, key
	}

	uncertainty := managed.ProviderStepResult{Permission: grant.Permission, Outcome: managed.PortUnavailable, Evidence: []byte("timeout after possible send"), ObservedAt: time.Now().UTC()}
	if err := s.RecordManagedProviderStepResult(t.Context(), uncertainty); err != nil {
		t.Fatal(err)
	}

	if scenario == "uncertainty" {
		return s, action, key
	}

	if scenario == "failure" {
		failure := memoryAttemptFailure(t, s, record, grant)
		if err := s.RecordManagedProviderStepFailure(t.Context(), failure); err != nil {
			t.Fatal(err)
		}

		if _, err := s.SettleManagedRemovalAttempt(t.Context(), managed.AttemptSettlementRequest{AttemptRequest: memoryAttemptRequest(t, s, action), Disposition: managed.SettledWithoutDeletion, ProducerProfile: managed.LocalNoDispatchProfile, VerifierID: "provider", Evidence: []byte("retained qualified failure"), SettledAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}

		return s, action, key
	}

	positive := func(g managed.ProviderStepGrant) {
		t.Helper()

		if err := s.RecordManagedProviderStepResult(t.Context(), managed.ProviderStepResult{Permission: g.Permission, Outcome: managed.PortTerminal, ResourceID: g.Permission.ResourceID, Evidence: []byte("removed"), ObservedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	positive(grant)
	positive(memoryAttemptStep(t, s, action, record, "sidecar"))
	positive(memoryAttemptStep(t, s, action, record, ""))

	if _, err := s.SettleManagedRemovalAttempt(t.Context(), managed.AttemptSettlementRequest{AttemptRequest: memoryAttemptRequest(t, s, action), Disposition: managed.Removed, ProducerProfile: "ctrlplane.managed.remove.v1", VerifierID: "provider", Evidence: []byte("all original terminal results"), SettledAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	if scenario == "settled" {
		return s, action, key
	}

	finish := memoryAttemptCommand(t, s, action, rid, managed.AttemptFinish)
	if _, err := s.RecordManagedAttemptFinish(t.Context(), managed.AttemptFinishRequest{AttemptRequest: memoryAttemptRequest(t, s, action), ReservationID: rid, Acceptance: memoryAttemptAcceptance(finish.Command)}); err != nil {
		t.Fatal(err)
	}

	return s, action, key
}

func TestManagedAttemptMemoryFiniteCorruption(t *testing.T) {
	for _, scenario := range []string{"issued", "uncertainty", "failure", "removed", "abort"} {
		t.Run(scenario, func(t *testing.T) {
			s, action, key := memoryAttemptScenario(t, scenario)
			scope := memoryAttemptScope(t, key.Key)
			stateBucket := "removal_attempt_state:" + scope
			refuse := func(t *testing.T) {
				t.Helper()

				before := projectionSnapshot(t, s)
				if _, err := s.ReadManagedRemovalAttempt(t.Context(), key); err == nil {
					t.Fatal("corrupt full-set read accepted")
				}

				request := managed.AttemptRequest{Command: projectionCommand(t, s, action.Key), AttemptID: key.AttemptID, Generation: "1"}
				if _, err := s.RevokeManagedRemovalAttempt(t.Context(), request); err == nil {
					t.Fatal("corruption classified as unissued")
				}

				if after := projectionSnapshot(t, s); after != before {
					t.Fatal("corruption repaired or mutated")
				}
			}
			// Remove every retained association and original acceptance separately, then restore only the test fixture.
			for bucket, rows := range s.managedControllerRecords {
				if !strings.HasPrefix(bucket, "removal_") && !strings.HasPrefix(bucket, "step:") && !strings.HasPrefix(bucket, "step_history:") && !strings.HasPrefix(bucket, "step_failure:") {
					continue
				}

				for rowKey, raw := range rows {
					t.Run("missing/"+bucket+"/"+rowKey, func(t *testing.T) {
						delete(s.managedControllerRecords[bucket], rowKey)
						defer func() { s.managedControllerRecords[bucket][rowKey] = raw }()

						refuse(t)
					})
				}
			}

			requiredReceipts := map[string]bool{}
			for operation := range s.managedControllerRecords["removal_attempt_event:"+scope] {
				requiredReceipts[key.InstanceID.String()+":"+operation] = true
			}

			for _, raw := range s.managedControllerRecords["removal_provider_slot:"+scope] {
				var slot struct {
					Receipt managed.WireReceipt `json:"receipt"`
				}
				if err := json.Unmarshal(raw, &slot); err != nil {
					t.Fatal(err)
				}

				requiredReceipts[key.InstanceID.String()+":"+slot.Receipt.ID.String()] = true
			}

			for receiptKey, receipt := range s.managedReceipts {
				if !requiredReceipts[receiptKey] {
					continue
				}

				t.Run("receipt/"+receiptKey, func(t *testing.T) {
					delete(s.managedReceipts, receiptKey)
					defer func() { s.managedReceipts[receiptKey] = receipt }()

					refuse(t)
				})
			}

			original := s.managedControllerRecords[stateBucket][key.AttemptID.String()]

			var state managed.AttemptState
			if err := json.Unmarshal(original, &state); err != nil {
				t.Fatal(err)
			}

			for _, field := range []string{"binding", "provider"} {
				limit := 7
				if field == "provider" {
					limit = 4
				}

				for bit := range limit {
					t.Run(field+"/toggle/"+strconv.Itoa(bit), func(t *testing.T) {
						var value managed.AttemptState
						if err := json.Unmarshal(original, &value); err != nil {
							t.Fatal(err)
						}

						if field == "binding" {
							value.BindingSlots[0] ^= 1 << bit
						} else {
							value.ProviderSlots[0] ^= 1 << bit
						}

						raw, err := json.Marshal(value)
						if err != nil {
							t.Fatal(err)
						}

						s.managedControllerRecords[stateBucket][key.AttemptID.String()] = raw
						defer func() { s.managedControllerRecords[stateBucket][key.AttemptID.String()] = original }()

						refuse(t)
					})
				}
			}

			for _, mode := range []string{"binding-short", "binding-null", "binding-unused", "binding-unknown", "provider-short", "provider-null", "provider-unused", "provider-unknown"} {
				t.Run(mode, func(t *testing.T) {
					var value managed.AttemptState
					if err := json.Unmarshal(original, &value); err != nil {
						t.Fatal(err)
					}

					switch mode {
					case "binding-short":
						value.BindingSlots = value.BindingSlots[:63]
					case "binding-null":
						value.BindingSlots = nil
					case "binding-unused":
						value.BindingSlots[63] = 1
					case "binding-unknown":
						value.BindingSlots[0] = 128
					case "provider-short":
						value.ProviderSlots = value.ProviderSlots[:32]
					case "provider-null":
						value.ProviderSlots = nil
					case "provider-unused":
						value.ProviderSlots[32] = 1
					case "provider-unknown":
						value.ProviderSlots[0] = 128
					}

					raw, err := json.Marshal(value)
					if err != nil {
						t.Fatal(err)
					}

					s.managedControllerRecords[stateBucket][key.AttemptID.String()] = raw
					defer func() { s.managedControllerRecords[stateBucket][key.AttemptID.String()] = original }()

					refuse(t)
				})
			}

			for rowKey, raw := range s.managedControllerRecords["removal_provider_slot:"+scope] {
				for _, field := range []string{"index", "attempt_ref", "permission_digest", "positive_digest", "failure_digest", "last_uncertainty_digest", "receipt"} {
					t.Run("slot/"+field+"/"+rowKey, func(t *testing.T) {
						var value memoryAttemptProviderAssociation
						if err := json.Unmarshal(raw, &value); err != nil {
							t.Fatal(err)
						}

						switch field {
						case "index":
							value.Index = "32"
						case "receipt":
							value.Receipt.Revision = "99999"
						case "attempt_ref":
							value.AttemptRef.DescriptorDigest = strings.Repeat("f", 64)
						case "permission_digest":
							value.PermissionDigest = strings.Repeat("f", 64)
						case "positive_digest":
							value.PositiveDigest = strings.Repeat("f", 64)
						case "failure_digest":
							value.FailureDigest = strings.Repeat("f", 64)
						case "last_uncertainty_digest":
							value.LastUncertaintyDigest = strings.Repeat("f", 64)
						}

						changed, err := json.Marshal(value)
						if err != nil {
							t.Fatal(err)
						}

						s.managedControllerRecords["removal_provider_slot:"+scope][rowKey] = changed
						defer func() { s.managedControllerRecords["removal_provider_slot:"+scope][rowKey] = raw }()

						refuse(t)
					})
				}
			}
		})
	}
}

type attemptFault struct {
	fail    int
	actions []string
}

func (f *attemptFault) hit(action string) error {
	f.actions = append(f.actions, action)
	if len(f.actions) == f.fail {
		return errors.New("injected attempt write failure")
	}

	return nil
}

type attemptFaultBackend struct {
	managedBackend

	fault *attemptFault
}
type attemptFaultTransaction struct {
	managedstate.ControllerTransaction

	fault *attemptFault
}

func (b attemptFaultBackend) ControllerAtomic(ctx context.Context, serial string, keys []managed.Key, change func(managedstate.ControllerTransaction) error) error {
	return b.managedBackend.ControllerAtomic(ctx, serial, keys, func(tx managedstate.ControllerTransaction) error {
		before := len(b.fault.actions)
		if err := change(attemptFaultTransaction{tx, b.fault}); err != nil {
			return err
		}

		if len(b.fault.actions) > before {
			return b.fault.hit("commit")
		}

		return nil
	})
}
func (tx attemptFaultTransaction) WriteRecord(ctx context.Context, kind, scope, key string, raw []byte, insert bool) error {
	if err := tx.ControllerTransaction.WriteRecord(ctx, kind, scope, key, raw, insert); err != nil {
		return err
	}

	return tx.fault.hit("row:" + kind)
}
func (tx attemptFaultTransaction) Atomic(ctx context.Context, key managed.Key, operation id.ID, digest string, change func(*managed.Target, time.Time) (managed.Receipt, error)) (managed.Receipt, error) {
	fresh := false

	receipt, err := tx.ControllerTransaction.Atomic(ctx, key, operation, digest, func(target *managed.Target, now time.Time) (managed.Receipt, error) {
		fresh = true

		return change(target, now)
	})
	if err == nil && fresh {
		err = tx.fault.hit("parent-and-receipt")
	}

	return receipt, err
}
func memoryAttemptRollbackOperation(t *testing.T, kind string) (*Store, func(*managedstate.Repository) error) {
	t.Helper()

	if kind == "start" {
		s, action, _ := memoryQuiescenceProviderSetup(t, false)
		q := managed.AttemptStartRequest{Command: projectionCommand(t, s, action.Key), ActionID: action.ID, AttemptID: id.New(managed.PrefixOperation), ProviderOperationID: id.New(managed.PrefixOperation), VerifierID: "provider"}

		return s, func(r *managedstate.Repository) error {
			_, err := r.BeginManagedRemovalAttempt(t.Context(), q)

			return err
		}
	}

	if kind == "finish-command" || kind == "finish" {
		s, action, key := memoryAttemptScenario(t, "settled")

		target, err := s.ReadManagedTarget(t.Context(), key.Key)
		if err != nil {
			t.Fatal(err)
		}

		rid := target.Reservations[0].ID

		if kind == "finish-command" {
			raw := []byte("native finish")
			q := managed.AttemptCommandRequest{AttemptRequest: memoryAttemptRequest(t, s, action), ReservationID: rid, Kind: managed.AttemptFinish, ExpectedNativeVersion: "1", NativeCommand: managed.PortCommand{SchemaVersion: "1", OperationID: id.New(managed.PrefixOperation), RequestID: "finish", Original: raw, OriginalDigest: managed.Digest(raw), Native: raw, NativeDigest: managed.Digest(raw)}}

			return s, func(r *managedstate.Repository) error {
				_, err := r.AuthorizeManagedAttemptCommand(t.Context(), q)

				return err
			}
		}

		grant := memoryAttemptCommand(t, s, action, rid, managed.AttemptFinish)
		q := managed.AttemptFinishRequest{AttemptRequest: memoryAttemptRequest(t, s, action), ReservationID: rid, Acceptance: memoryAttemptAcceptance(grant.Command)}

		return s, func(r *managedstate.Repository) error {
			_, err := r.RecordManagedAttemptFinish(t.Context(), q)

			return err
		}
	}

	s, action, key, rid := memoryAttemptFixture(t)

	switch kind {
	case "admit":
		q := managed.AttemptAdmissionRequest{AttemptRequest: memoryAttemptRequest(t, s, action), VerifierID: "provider"}

		return s, func(r *managedstate.Repository) error {
			_, err := r.AdmitManagedRemovalAttempt(t.Context(), q)

			return err
		}
	case "revoke":
		q := memoryAttemptRequest(t, s, action)

		return s, func(r *managedstate.Repository) error {
			_, err := r.RevokeManagedRemovalAttempt(t.Context(), q)

			return err
		}
	case "recovery":
		command, err := s.ReadManagedAttemptCommand(t.Context(), managed.AttemptCommandKey{AttemptBindingKey: managed.AttemptBindingKey{AttemptKey: key, ReservationID: rid}, Kind: managed.AttemptBegin})
		if err != nil {
			t.Fatal(err)
		}

		raw, err := json.Marshal(command)
		if err != nil {
			t.Fatal(err)
		}

		q := managed.AttemptRecoveryRequest{AttemptRequest: memoryAttemptRequest(t, s, action), ReservationID: rid, Kind: managed.AttemptBegin, CommandDigest: managed.Digest(append([]byte("ctrlplane.managed.attempt-command.v1\n"), raw...))}

		return s, func(r *managedstate.Repository) error {
			_, err := r.AuthorizeManagedAttemptRecovery(t.Context(), q)

			return err
		}
	}

	record := memoryAttemptAdmit(t, s, action)
	if kind == "permission" {
		q := managed.ProviderStepAuthorization{Command: projectionCommand(t, s, action.Key), Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: action.Key, ActionID: action.ID}, ProviderOperationID: record.Descriptor.ProviderOperationID, Kind: managed.StepRemoveContainer, Service: "api"}}

		return s, func(r *managedstate.Repository) error {
			_, err := r.AuthorizeManagedProviderStep(t.Context(), q)

			return err
		}
	}

	grant := memoryAttemptStep(t, s, action, record, "api")
	if kind == "failure" {
		q := memoryAttemptFailure(t, s, record, grant)

		return s, func(r *managedstate.Repository) error { return r.RecordManagedProviderStepFailure(t.Context(), q) }
	}

	if kind == "settlement-cutoff" {
		if err := s.RecordManagedProviderStepFailure(t.Context(), memoryAttemptFailure(t, s, record, grant)); err != nil {
			t.Fatal(err)
		}

		q := managed.AttemptSettlementRequest{AttemptRequest: memoryAttemptRequest(t, s, action), Disposition: managed.SettledWithoutDeletion, ProducerProfile: managed.LocalNoDispatchProfile, VerifierID: "provider", Evidence: []byte("qualified failure"), SettledAt: time.Now().UTC()}

		return s, func(r *managedstate.Repository) error {
			_, err := r.SettleManagedRemovalAttempt(t.Context(), q)

			return err
		}
	}

	outcome := managed.PortTerminal
	if kind == "uncertainty" {
		outcome = managed.PortUnavailable
	}

	q := managed.ProviderStepResult{Permission: grant.Permission, Outcome: outcome, ResourceID: grant.Permission.ResourceID, Evidence: []byte("original provider response"), ObservedAt: time.Now().UTC()}

	return s, func(r *managedstate.Repository) error { return r.RecordManagedProviderStepResult(t.Context(), q) }
}
func TestManagedAttemptMemoryAtomicWrites(t *testing.T) {
	for _, kind := range []string{"start", "admit", "revoke", "permission", "positive", "uncertainty", "failure", "settlement-cutoff", "finish-command", "finish", "recovery"} {
		t.Run(kind, func(t *testing.T) {
			s, run := memoryAttemptRollbackOperation(t, kind)
			stateRevisions := func() map[string]managed.Decimal {
				out := map[string]managed.Decimal{}

				for bucket, rows := range s.managedControllerRecords {
					if !strings.HasPrefix(bucket, "removal_attempt_state:") {
						continue
					}

					for key, raw := range rows {
						var state managed.AttemptState
						if err := json.Unmarshal(raw, &state); err != nil {
							t.Fatal(err)
						}

						out[bucket+":"+key] = state.Revision
					}
				}

				return out
			}
			stateBefore := stateRevisions()
			before := projectionSnapshot(t, s)

			var parentBefore uint64
			for _, target := range s.managedTargets {
				parentBefore = target.Revision
			}

			for n := 1; n < 32; n++ {
				fault := &attemptFault{fail: n}
				repo := managedstate.New(attemptFaultBackend{managedBackend: managedBackend{store: s}, fault: fault})

				err := run(repo)
				if err == nil {
					var parentAfter uint64
					for _, target := range s.managedTargets {
						parentAfter = target.Revision
					}

					t.Logf("actual nested writes %s: %v, parent revision delta=%d", kind, fault.actions, parentAfter-parentBefore)

					if kind == "failure" || kind == "positive" || kind == "uncertainty" || kind == "recovery" {
						if parentAfter != parentBefore || !reflect.DeepEqual(stateBefore, stateRevisions()) {
							t.Fatal("nonadvancing fact consumed parent or state revision")
						}
					}

					return
				}

				if !strings.Contains(err.Error(), "injected attempt write failure") {
					t.Fatalf("unexpected failure at %d: %v", n, err)
				}

				if after := projectionSnapshot(t, s); after != before {
					t.Fatalf("write boundary %d leaked parent, receipt or record", n)
				}
			}

			t.Fatal("no successful write boundary for " + kind)
		})
	}
}

func TestManagedAttemptMemoryTerminalReserve(t *testing.T) {
	s, action, q := memoryQuiescenceProviderSetup(t, false)
	q.Command = memoryOriginalCommand(t, s, action.Key, q.Command.ReservationID)
	q.Observation.ObservedAt = time.Now().UTC().Add(-time.Millisecond)
	q.Observation.ValidUntil = q.Observation.ObservedAt.Add(time.Minute)
	signMemoryQuiescence(t, &q)

	if _, err := s.AcceptManagedQuiescence(t.Context(), q); err != nil {
		t.Fatal(err)
	}

	const reserve = 7 + 3 + 8

	s.managedTargets[action.InstanceID.String()].Revision = math.MaxInt64 - reserve - 1
	memoryAttemptStart(t, s, action, q.Command.ReservationID)

	operation, err := s.ReadManagedOperation(t.Context(), managed.OperationKey{Key: action.Key, ActionID: action.ID})
	if err != nil {
		t.Fatal(err)
	}

	if _, err = s.AdvanceManagedOperation(t.Context(), managed.OperationAdvance{Command: projectionCommand(t, s, action.Key), ActionID: action.ID, ExpectedOperationRevision: operation.Revision, Observation: managed.OperationObservation{Kind: "retry", ObservedAt: time.Now().UTC(), RetryAt: time.Now().UTC().Add(time.Minute), Diagnostic: "retry"}}); err != nil {
		t.Fatal(err)
	}

	before := projectionSnapshot(t, s)
	if _, err = s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: projectionCommand(t, s, action.Key), Duration: managed.MaxLease}); !errors.Is(err, managed.ErrCapacity) {
		t.Fatalf("lease metadata consumed terminal reserve: %v", err)
	}

	if after := projectionSnapshot(t, s); after != before {
		t.Fatal("capacity refusal changed retained state")
	}

	record := memoryAttemptAdmit(t, s, action)
	for _, service := range []string{"api", "sidecar", ""} {
		grant := memoryAttemptStep(t, s, action, record, service)
		if err = s.RecordManagedProviderStepResult(t.Context(), managed.ProviderStepResult{Permission: grant.Permission, Outcome: managed.PortTerminal, ResourceID: grant.Permission.ResourceID, Evidence: []byte("removed"), ObservedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err = s.SettleManagedRemovalAttempt(t.Context(), managed.AttemptSettlementRequest{AttemptRequest: memoryAttemptRequest(t, s, action), Disposition: managed.Removed, ProducerProfile: "ctrlplane.managed.remove.v1", VerifierID: "provider", Evidence: []byte("terminal"), SettledAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	finish := memoryAttemptCommand(t, s, action, q.Command.ReservationID, managed.AttemptFinish)
	if _, err = s.RecordManagedAttemptFinish(t.Context(), managed.AttemptFinishRequest{AttemptRequest: memoryAttemptRequest(t, s, action), ReservationID: q.Command.ReservationID, Acceptance: memoryAttemptAcceptance(finish.Command)}); err != nil {
		t.Fatal(err)
	}

	if _, err = s.TombstoneManagedRemovalAttempt(t.Context(), memoryAttemptRequest(t, s, action)); err != nil {
		t.Fatal(err)
	}

	target, err := s.ReadManagedTarget(t.Context(), action.Key)
	if err != nil || target.Phase != managed.Deleted {
		t.Fatal("near-limit terminal completion failed", err)
	}

	t.Logf("near-limit initial reserve=%d actual parent increments=%d unused signed headroom=%d", reserve, target.Revision-(math.MaxInt64-reserve-1), math.MaxInt64-target.Revision)
}

func memoryAttemptFullSetup(t *testing.T) (*Store, managed.ActionSubmission, []managed.QuiescenceAcceptanceRequest) {
	t.Helper()
	_, template := memoryOriginal(t)
	action := projectionAction(t)
	member := action.NewTarget.Manifest.Members[0]
	action.NewTarget.Manifest.Members = nil
	spec := managed.ProvisionSpec{Version: "1"}
	roles := []managed.RuntimeRole{}

	for n := range 32 {
		name := "service" + strconv.Itoa(n)
		m := member
		m.Name = name
		m.Service = name
		m.Namespace = "namespace" + strconv.Itoa(n)
		action.NewTarget.Manifest.Members = append(action.NewTarget.Manifest.Members, m)

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

	action.NewTarget.Manifest.Spec, err = json.Marshal(managed.ManagedSpec{Profile: "ctrlplane.managed.v1", RuntimeRoles: roles, Provision: raw})
	if err != nil {
		t.Fatal(err)
	}

	action.NewTarget.Manifest.SpecDigest = managed.Digest(action.NewTarget.Manifest.Spec)
	signProjectionAction(t, &action)

	s := New()
	if _, err = s.SubmitManagedAction(t.Context(), action); err != nil {
		t.Fatal(err)
	}

	projection := projectionLease(t, s, action.Key, action.ID)
	if _, err = s.ProjectManagedRows(t.Context(), projection); err != nil {
		t.Fatal(err)
	}

	if _, err = s.IssueManagedProvision(t.Context(), projectionCommand(t, s, action.Key)); err != nil {
		t.Fatal(err)
	}

	resources := map[string]string{"network": "network"}
	for n := range spec.Services {
		resources[spec.Services[n].Name] = "resource-" + strconv.Itoa(n)
	}

	if _, err = s.BindManagedResources(t.Context(), managed.ResourcesRequest{Command: projectionCommand(t, s, action.Key), OperationID: action.NewTarget.ProvisionOperationID, Receipt: []byte("provisioned"), Resources: resources}); err != nil {
		t.Fatal(err)
	}

	observations := []managed.QuiescenceAcceptanceRequest{}

	for n, member := range action.NewTarget.Manifest.Members {
		for copyIndex := range 2 {
			rid := id.New(managed.PrefixReservation)
			runtime := "runtime-" + strconv.Itoa(n) + "-" + strconv.Itoa(copyIndex)
			body := []byte(runtime)

			registration := managed.Registration{ID: rid, Member: member.Name, RuntimeID: runtime, InstanceID: action.InstanceID.String(), HostID: "host", RequestID: "request-" + runtime, Command: body, CommandDigest: managed.Digest(body)}
			if _, err = s.ReserveManagedRegistration(t.Context(), managed.ReserveRequest{Command: projectionCommand(t, s, action.Key), Registration: registration}); err != nil {
				t.Fatal(err)
			}

			if _, err = s.IssueManagedRegistration(t.Context(), managed.ReservationRequest{Command: projectionCommand(t, s, action.Key), ReservationID: rid}); err != nil {
				t.Fatal(err)
			}

			original := template
			original.Command = memoryOriginalCommand(t, s, action.Key, rid)
			original.Facts.Member = member.Name
			original.Facts.StoredCommandDigest = registration.CommandDigest
			original.Facts.Native.RequestID = registration.RequestID
			original.Facts.Process.RuntimeID = runtime
			original.Facts.Process.InstanceID = registration.InstanceID
			original.Facts.Process.HostID = registration.HostID
			original.Facts.Process.Installation = member.Installation
			original.Facts.Process.Namespace = member.Namespace
			original.Facts.Process.Queue = member.Queue
			original.Facts.Process.Build = member.Build
			original.Facts.Process.ArtifactDigest = member.ArtifactDigest
			original.Facts.Process.ConfigurationDigest = member.ConfigurationDigest
			original.Facts.Process.Descriptor = spec.Services[n].Startup
			original.Facts.Process.MaximumProofValidityNS = "60000000000"

			drain := memoryOriginalDrain(t, s, original)
			if _, err = s.RecordManagedOriginalDrain(t.Context(), drain); err != nil {
				t.Fatal(err)
			}

			regRecord, err := s.ReadManagedOriginalLifecycle(t.Context(), managed.OriginalLifecycleKey{Key: action.Key, Kind: managed.OriginalRegistration, OperationID: original.Command.ID})
			if err != nil {
				t.Fatal(err)
			}

			drainRecord, err := s.ReadManagedOriginalLifecycle(t.Context(), managed.OriginalLifecycleKey{Key: action.Key, Kind: managed.OriginalDrain, OperationID: drain.Command.ID})
			if err != nil {
				t.Fatal(err)
			}

			ref, err := managed.NewQuiescenceReference(regRecord, drainRecord)
			if err != nil {
				t.Fatal(err)
			}

			observations = append(observations, managed.QuiescenceAcceptanceRequest{Command: memoryOriginalCommand(t, s, action.Key, rid), Observation: managed.QuiescenceObservation{QuiescenceLeaf: managed.QuiescenceLeaf{Reference: ref, AdmissionClosed: true, Quiescent: true, Complete: true, ActiveClaims: "0", UnknownClaims: "0", ObservationID: "observation", ChallengeID: "challenge", VerifierID: ref.Process.ProducerVerifier}, Attestation: []byte("synthetic")}})
		}
	}

	if _, err = s.SealManagedTarget(t.Context(), projectionCommand(t, s, action.Key)); err != nil {
		t.Fatal(err)
	}

	return s, action, observations
}
func TestManagedAttemptMemoryInitialReserve64(t *testing.T) {
	s, action, _ := memoryAttemptFullSetup(t)

	const reserve = 7*64 + 33 + 8

	s.managedTargets[action.InstanceID.String()].Revision = math.MaxInt64 - reserve

	q := managed.AttemptStartRequest{Command: projectionCommand(t, s, action.Key), ActionID: action.ID, AttemptID: id.New(managed.PrefixOperation), ProviderOperationID: id.New(managed.PrefixOperation), VerifierID: "provider"}

	before := projectionSnapshot(t, s)
	if _, err := s.BeginManagedRemovalAttempt(t.Context(), q); !errors.Is(err, managed.ErrCapacity) {
		t.Fatal("exact489 start did not refuse", err)
	}

	if after := projectionSnapshot(t, s); after != before {
		t.Fatal("exact489 refusal changed retained state")
	}

	if _, exists := s.managedReceipts[action.InstanceID.String()+":"+q.ID.String()]; exists {
		t.Fatal("refused allocation retained receipt")
	}

	s.managedTargets[action.InstanceID.String()].Revision--

	q.Command = projectionCommand(t, s, action.Key)
	if _, err := s.BeginManagedRemovalAttempt(t.Context(), q); err != nil {
		t.Fatal("exact490 boundary rejected", err)
	}

	target, err := s.ReadManagedTarget(t.Context(), action.Key)
	if err != nil {
		t.Fatal(err)
	}

	if math.MaxInt64-target.Revision != reserve {
		t.Fatal("exact reserve lost")
	}
}

func TestManagedAttemptMemoryPositiveOnlyReserveRelease(t *testing.T) {
	for _, outcome := range []string{"none", "uncertainty", "failure", "positive"} {
		t.Run(outcome, func(t *testing.T) {
			s, action, q := memoryQuiescenceProviderSetup(t, false)
			q.Command = memoryOriginalCommand(t, s, action.Key, q.Command.ReservationID)
			q.Observation.ObservedAt = time.Now().UTC()
			q.Observation.ValidUntil = q.Observation.ObservedAt.Add(time.Minute)
			signMemoryQuiescence(t, &q)

			if _, err := s.AcceptManagedQuiescence(t.Context(), q); err != nil {
				t.Fatal(err)
			}

			s.managedTargets[action.InstanceID.String()].Revision = math.MaxInt64 - (7 + 3 + 8) - 1
			memoryAttemptStart(t, s, action, q.Command.ReservationID)
			record := memoryAttemptAdmit(t, s, action)

			grant := memoryAttemptStep(t, s, action, record, "api")
			if outcome == "failure" {
				if err := s.RecordManagedProviderStepFailure(t.Context(), memoryAttemptFailure(t, s, record, grant)); err != nil {
					t.Fatal(err)
				}
			}

			if outcome == "uncertainty" || outcome == "positive" {
				value := managed.PortUnavailable
				if outcome == "positive" {
					value = managed.PortTerminal
				}

				if err := s.RecordManagedProviderStepResult(t.Context(), managed.ProviderStepResult{Permission: grant.Permission, Outcome: value, ResourceID: grant.Permission.ResourceID, Evidence: []byte("exact retained response"), ObservedAt: time.Now().UTC()}); err != nil {
					t.Fatal(err)
				}
			}

			allowed := 0
			if outcome == "positive" {
				allowed = 3
			}

			for n := range allowed {
				if _, err := s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: projectionCommand(t, s, action.Key), Duration: managed.MaxLease}); err != nil {
					t.Fatalf("released absent branch slot %d: %v", n, err)
				}
			}

			before := projectionSnapshot(t, s)
			if _, err := s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: projectionCommand(t, s, action.Key), Duration: managed.MaxLease}); !errors.Is(err, managed.ErrCapacity) {
				t.Fatalf("unproven or double-released terminal capacity: %v", err)
			}

			if before != projectionSnapshot(t, s) {
				t.Fatal("capacity refusal changed state")
			}
		})
	}
}

type memoryAttemptProviderAssociation struct {
	SchemaVersion         managed.Decimal     `json:"schema_version"`
	AttemptRef            managed.AttemptRef  `json:"attempt_ref"`
	ProviderOperationID   id.ID               `json:"provider_operation_id"`
	Index                 managed.Decimal     `json:"index"`
	PermissionDigest      string              `json:"permission_digest"`
	Receipt               managed.WireReceipt `json:"receipt"`
	PositiveDigest        string              `json:"positive_digest,omitempty"`
	FailureDigest         string              `json:"failure_digest,omitempty"`
	LastUncertaintyDigest string              `json:"last_uncertainty_digest,omitempty"`
}

type attemptAmbiguousBackend struct{ managedBackend }

func (b attemptAmbiguousBackend) ControllerAtomic(ctx context.Context, serial string, keys []managed.Key, change func(managedstate.ControllerTransaction) error) error {
	if err := b.managedBackend.ControllerAtomic(ctx, serial, keys, change); err != nil {
		return err
	}

	return managed.ErrUnavailable
}
func TestManagedAttemptMemoryAmbiguousGrantCommit(t *testing.T) {
	t.Run("command", func(t *testing.T) {
		s, action, _ := memoryAttemptScenario(t, "settled")

		target, err := s.ReadManagedTarget(t.Context(), action.Key)
		if err != nil {
			t.Fatal(err)
		}

		raw := []byte("finish")
		q := managed.AttemptCommandRequest{AttemptRequest: memoryAttemptRequest(t, s, action), ReservationID: target.Reservations[0].ID, Kind: managed.AttemptFinish, ExpectedNativeVersion: "1", NativeCommand: managed.PortCommand{SchemaVersion: "1", OperationID: id.New(managed.PrefixOperation), RequestID: "finish", Original: raw, OriginalDigest: managed.Digest(raw), Native: raw, NativeDigest: managed.Digest(raw)}}
		r := managedstate.New(attemptAmbiguousBackend{managedBackend{store: s}})

		grant, err := r.AuthorizeManagedAttemptCommand(t.Context(), q)
		if err == nil || grant.NewlyIssued || !grant.Command.AuthorizationID.IsNil() {
			t.Fatal("ambiguous commit leaked native send authority", err)
		}

		replay, err := s.AuthorizeManagedAttemptCommand(t.Context(), q)
		if err != nil || replay.NewlyIssued || replay.Command.AuthorizationID != q.ID {
			t.Fatal("committed original command replay changed", err)
		}
	})
	t.Run("recovery", func(t *testing.T) {
		s, action, key, rid := memoryAttemptFixture(t)
		q := memoryRecoveryInput(t, s, action, key, rid)
		r := managedstate.New(attemptAmbiguousBackend{managedBackend{store: s}})

		grant, err := r.AuthorizeManagedAttemptRecovery(t.Context(), q)
		if err == nil || grant.NewlyAuthorized || !grant.Command.AuthorizationID.IsNil() {
			t.Fatal("ambiguous commit leaked native recovery authority", err)
		}

		replay, err := s.AuthorizeManagedAttemptRecovery(t.Context(), q)
		if err != nil || replay.NewlyAuthorized || replay.Command.Key.ReservationID != rid {
			t.Fatal("committed recovery replay changed", err)
		}
	})
}
func memoryRecoveryInput(t *testing.T, s *Store, action managed.ActionSubmission, key managed.AttemptKey, rid id.ID) managed.AttemptRecoveryRequest {
	t.Helper()

	command, err := s.ReadManagedAttemptCommand(t.Context(), managed.AttemptCommandKey{AttemptBindingKey: managed.AttemptBindingKey{AttemptKey: key, ReservationID: rid}, Kind: managed.AttemptBegin})
	if err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}

	return managed.AttemptRecoveryRequest{AttemptRequest: memoryAttemptRequest(t, s, action), ReservationID: rid, Kind: managed.AttemptBegin, CommandDigest: managed.Digest(append([]byte("ctrlplane.managed.attempt-command.v1\n"), raw...))}
}
func TestManagedAttemptMemoryRecoveryAssociationsAndActualExpiry(t *testing.T) {
	for _, mode := range []string{"missing-event", "missing-receipt", "actual-expiry"} {
		t.Run(mode, func(t *testing.T) {
			s, action, key, rid := memoryAttemptFixture(t)
			q := memoryRecoveryInput(t, s, action, key, rid)

			if mode == "actual-expiry" {
				expiry := time.Now().UTC().Add(time.Second)
				s.managedTargets[action.InstanceID.String()].LeaseUntil = expiry
				paused := false
				skip := 0
				r := managedstate.New(quiescencePauseBackend{managedBackend: managedBackend{store: s}, t: t, operation: q.ID, expiry: expiry, paused: &paused, skipCalls: &skip})
				before := projectionSnapshot(t, s)

				grant, err := r.AuthorizeManagedAttemptRecovery(t.Context(), q)
				if err == nil || grant.NewlyAuthorized || !paused {
					t.Fatal("actual expired callback returned native recovery", err)
				}

				if before != projectionSnapshot(t, s) {
					t.Fatal("expiry refusal mutated history")
				}

				return
			}

			if _, err := s.AuthorizeManagedAttemptRecovery(t.Context(), q); err != nil {
				t.Fatal(err)
			}

			if mode == "missing-event" {
				delete(s.managedControllerRecords["removal_attempt_event:"+memoryAttemptScope(t, key.Key)], q.ID.String())
			} else {
				delete(s.managedReceipts, key.InstanceID.String()+":"+q.ID.String())
			}

			before := projectionSnapshot(t, s)

			grant, err := s.AuthorizeManagedAttemptRecovery(t.Context(), q)
			if err == nil || grant.NewlyAuthorized {
				t.Fatal("lost recovery association accepted", err)
			}

			if before != projectionSnapshot(t, s) {
				t.Fatal("missing recovery association repaired")
			}
		})
	}
}

func TestManagedAttemptMemoryOrphanBeginReceipt(t *testing.T) {
	s, action, _ := memoryQuiescenceProviderSetup(t, false)
	q := managed.AttemptStartRequest{Command: projectionCommand(t, s, action.Key), ActionID: action.ID, AttemptID: id.New(managed.PrefixOperation), ProviderOperationID: id.New(managed.PrefixOperation), VerifierID: "provider"}

	raw, err := json.Marshal(struct {
		Kind  string `json:"kind"`
		Value any    `json:"value"`
	}{"begin_attempt", q})
	if err != nil {
		t.Fatal(err)
	}

	fingerprint := managed.Digest(append([]byte("ctrlplane.managed.attempt-fact.v1\n"), raw...))
	s.managedReceipts[action.InstanceID.String()+":"+q.ID.String()] = managed.Receipt{ID: q.ID, Digest: fingerprint, Revision: q.Revision, LeaseEpoch: q.LeaseEpoch, AcceptedAt: time.Now().UTC()}
	before := projectionSnapshot(t, s)

	receipt, err := s.BeginManagedRemovalAttempt(t.Context(), q)
	if !errors.Is(err, managed.ErrUnavailable) || !receipt.AttemptID.IsNil() {
		t.Fatal("orphan begin receipt accepted", err)
	}

	if before != projectionSnapshot(t, s) {
		t.Fatal("orphan begin history repaired")
	}
}

func TestManagedAttemptMemoryReceiptProbe(t *testing.T) {
	s, action, _, _ := memoryAttemptFixture(t)
	before := projectionSnapshot(t, s)
	operation := id.New(managed.PrefixOperation)
	digest := managed.Digest([]byte("exact held receipt"))
	stop := errors.New("rollback probe")

	err := (managedBackend{store: s}).ControllerAtomic(t.Context(), "probe", []managed.Key{action.Key}, func(tx managedstate.ControllerTransaction) error {
		if _, err := tx.Read(t.Context(), action.Key); err != nil {
			return err
		}

		check := func(ctx context.Context, key managed.Key, op id.ID, hash string, want error) {
			t.Helper()

			if _, err := tx.ReadReceipt(ctx, key, op, hash); !errors.Is(err, want) {
				t.Fatalf("probe expected %v got %v", want, err)
			}
		}
		check(t.Context(), action.Key, operation, digest, managed.ErrNotFound)

		receipt, err := tx.Atomic(t.Context(), action.Key, operation, digest, func(target *managed.Target, now time.Time) (managed.Receipt, error) {
			target.Revision++

			return managed.Receipt{ID: operation, Digest: digest, Revision: target.Revision, AcceptedAt: now}, nil
		})
		if err != nil {
			return err
		}

		found, err := tx.ReadReceipt(t.Context(), action.Key, operation, digest)
		if err != nil || found != receipt {
			t.Fatal("same transaction receipt not visible", err)
		}

		wrong := action.Key
		wrong.TenantID = "wrong-tenant"
		check(t.Context(), wrong, operation, digest, managed.ErrAuthority)

		absent := action.Key
		absent.InstanceID = id.New(id.PrefixInstance)
		check(t.Context(), absent, operation, digest, managed.ErrNotFound)
		check(t.Context(), action.Key, id.New(managed.PrefixOperation), digest, managed.ErrNotFound)
		check(t.Context(), action.Key, operation, managed.Digest([]byte("wrong")), managed.ErrConflict)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		check(ctx, action.Key, operation, digest, context.Canceled)

		held := tx.(managedControllerTransaction)
		receiptKey := action.InstanceID.String() + ":" + operation.String()
		corrupt := receipt
		corrupt.ID = id.New(managed.PrefixOperation)
		held.store.managedReceipts[receiptKey] = corrupt

		check(t.Context(), action.Key, operation, digest, managed.ErrUnavailable)
		delete(held.store.managedReceipts, receiptKey)
		check(t.Context(), action.Key, operation, digest, managed.ErrNotFound)

		held.store.managedReceipts[receiptKey] = receipt

		found, err = tx.ReadReceipt(t.Context(), action.Key, operation, digest)
		if err != nil || found != receipt {
			t.Fatal("absence cached across write", err)
		}

		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}

	if before != projectionSnapshot(t, s) {
		t.Fatal("probe transaction rollback leaked writes")
	}
}

func TestManagedAttemptMemoryRecoveryAtCounterLimit(t *testing.T) {
	s, action, key, rid := memoryAttemptFixture(t)
	s.managedTargets[action.InstanceID.String()].Revision = math.MaxInt64
	q := memoryRecoveryInput(t, s, action, key, rid)
	before := *s.managedTargets[action.InstanceID.String()]

	grant, err := s.AuthorizeManagedAttemptRecovery(t.Context(), q)
	if err != nil || !grant.NewlyAuthorized || grant.Receipt.Receipt.Revision != math.MaxInt64 {
		t.Fatal("existing recovery lost at counter exhaustion", err)
	}

	if s.managedTargets[action.InstanceID.String()].Revision != before.Revision {
		t.Fatal("counter-limit recovery advanced parent")
	}

	replay, err := s.AuthorizeManagedAttemptRecovery(t.Context(), q)
	if err != nil || replay.NewlyAuthorized {
		t.Fatal("counter-limit replay issued", err)
	}
}

func TestManagedAttemptMemoryAbsentBitPresentBrokenRow(t *testing.T) {
	for _, missing := range []string{"event", "receipt"} {
		t.Run(missing, func(t *testing.T) {
			s, action, key, rid := memoryAttemptFixture(t)

			command, err := s.ReadManagedAttemptCommand(t.Context(), managed.AttemptCommandKey{AttemptBindingKey: managed.AttemptBindingKey{AttemptKey: key, ReservationID: rid}, Kind: managed.AttemptBegin})
			if err != nil {
				t.Fatal(err)
			}

			scope := memoryAttemptScope(t, key.Key)

			var state managed.AttemptState
			if err = json.Unmarshal(s.managedControllerRecords["removal_attempt_state:"+scope][key.AttemptID.String()], &state); err != nil {
				t.Fatal(err)
			}

			state.BindingSlots[0] = 0

			raw, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}

			s.managedControllerRecords["removal_attempt_state:"+scope][key.AttemptID.String()] = raw
			if missing == "event" {
				delete(s.managedControllerRecords["removal_attempt_event:"+scope], command.AuthorizationID.String())
			} else {
				delete(s.managedReceipts, action.InstanceID.String()+":"+command.AuthorizationID.String())
			}

			before := projectionSnapshot(t, s)
			if _, err = s.ReadManagedRemovalAttempt(t.Context(), key); !errors.Is(err, managed.ErrUnavailable) {
				t.Fatal("broken present row became optional absence", err)
			}

			if before != projectionSnapshot(t, s) {
				t.Fatal("broken row repaired")
			}
		})
	}
}

func TestManagedAttemptMemoryWitnessAndAbortCallbackTime(t *testing.T) {
	s, action, key, rid := memoryAttemptFixture(t)

	revocation, err := s.RevokeManagedRemovalAttempt(t.Context(), memoryAttemptRequest(t, s, action))
	if err != nil {
		t.Fatal(err)
	}

	bindingKey := managed.AttemptBindingKey{AttemptKey: key, ReservationID: rid}

	binding, err := s.ReadManagedAttemptBinding(t.Context(), bindingKey)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(struct {
		Kind  string `json:"kind"`
		Value any    `json:"value"`
	}{"removal_fence", *binding.Fence})
	if err != nil {
		t.Fatal(err)
	}

	q := managed.AttemptBindingSettlementRequest{AttemptRequest: memoryAttemptRequest(t, s, action), ReservationID: rid, FenceFactDigest: managed.Digest(append([]byte("ctrlplane.managed.attempt-fact.v1\n"), raw...))}
	paused, skip := false, 0
	threshold := time.Now().UTC().Add(time.Second)

	repo := managedstate.New(quiescencePauseBackend{managedBackend: managedBackend{store: s}, t: t, operation: q.ID, expiry: threshold, paused: &paused, skipCalls: &skip})
	if _, err = repo.RecordManagedAttemptBindingSettlement(t.Context(), q); err != nil || !paused {
		t.Fatal(err)
	}

	binding, err = s.ReadManagedAttemptBinding(t.Context(), bindingKey)
	if err != nil || binding.Witness == nil || binding.Witness.WitnessedAt.Before(threshold) {
		t.Fatal("witness used controller entry time", err)
	}

	history, err := s.ReadManagedAttemptHistoricalSettlement(t.Context(), managed.AttemptSettlementReadRequest{AttemptBindingKey: bindingKey, ProviderOperationID: memoryAttemptRequest(t, s, action).AttemptID, NativeFenceDigest: binding.Fence.NativeFenceDigest})
	// The provider ID is separate from the attempt ID. Wrong identity must refuse.
	if !errors.Is(err, managed.ErrAuthority) {
		t.Fatal("attempt ID aliased provider identity", history, err)
	}

	record, err := s.ReadManagedRemovalAttempt(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}

	history, err = s.ReadManagedAttemptHistoricalSettlement(t.Context(), managed.AttemptSettlementReadRequest{AttemptBindingKey: bindingKey, ProviderOperationID: record.Descriptor.ProviderOperationID, NativeFenceDigest: binding.Fence.NativeFenceDigest})
	if err != nil || !history.Revocation.RevokedAt.Equal(revocation.AcceptedAt) {
		t.Fatal("witness rewrote revocation", err)
	}

	grant := memoryAttemptCommand(t, s, action, rid, managed.AttemptAbort)
	abort := managed.AttemptAbortRequest{AttemptRequest: memoryAttemptRequest(t, s, action), ReservationID: rid, Acceptance: memoryAttemptAcceptance(grant.Command), ProofVerifiedAt: time.Now().UTC(), ProofValidUntil: time.Now().UTC().Add(time.Second)}
	paused, skip = false, 0
	repo = managedstate.New(quiescencePauseBackend{managedBackend: managedBackend{store: s}, t: t, operation: abort.ID, expiry: abort.ProofValidUntil, paused: &paused, skipCalls: &skip})

	before := projectionSnapshot(t, s)
	if _, err = repo.RecordManagedAttemptAbort(t.Context(), abort); !errors.Is(err, managed.ErrBlocked) || !paused {
		t.Fatal("expired callback proof accepted", err)
	}

	if before != projectionSnapshot(t, s) {
		t.Fatal("expiry refusal changed history")
	}

	abort.ProofVerifiedAt = time.Now().UTC()

	abort.ProofValidUntil = abort.ProofVerifiedAt.Add(time.Minute)
	if _, err = s.RecordManagedAttemptAbort(t.Context(), abort); err != nil {
		t.Fatal(err)
	}
}

func TestManagedAttemptMemoryAmbiguousProviderPermission(t *testing.T) {
	s, action, _, _ := memoryAttemptFixture(t)
	record := memoryAttemptAdmit(t, s, action)
	q := managed.ProviderStepAuthorization{Command: projectionCommand(t, s, action.Key), Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: action.Key, ActionID: action.ID}, ProviderOperationID: record.Descriptor.ProviderOperationID, Kind: managed.StepRemoveContainer, Service: "api"}}
	repo := managedstate.New(attemptAmbiguousBackend{managedBackend{store: s}})

	grant, err := repo.AuthorizeManagedProviderStep(t.Context(), q)
	if err == nil || grant.NewlyIssued || !grant.Permission.CommandID.IsNil() {
		t.Fatal("ambiguous provider commit leaked send permission", err)
	}

	replay, err := s.AuthorizeManagedProviderStep(t.Context(), q)
	if err != nil || replay.NewlyIssued || replay.Permission.CommandID != q.ID {
		t.Fatal("original provider replay changed", err)
	}
}

func TestManagedAttemptMemoryImmutableBatches(t *testing.T) {
	s, action, _, _ := memoryAttemptFixture(t)
	scope := managed.Digest([]byte("batch scope"))
	rowKey := managed.Digest([]byte("batch row"))
	keys := []managedstate.ControllerRecordKey{{Kind: "removal_command", Scope: scope, Key: rowKey}, {Kind: "removal_fence", Scope: scope, Key: rowKey}, {Kind: "removal_command", Scope: managed.Digest([]byte("other scope")), Key: rowKey}}
	operation := id.New(managed.PrefixOperation)
	missing := id.New(managed.PrefixOperation)
	stop := errors.New("batch rollback")

	err := (managedBackend{store: s}).ControllerAtomic(t.Context(), "batch", []managed.Key{action.Key}, func(tx managedstate.ControllerTransaction) error {
		rows, err := tx.ReadImmutableRecords(t.Context(), keys)
		if err != nil || len(rows) != 3 {
			t.Fatal("missing exact records", err)
		}

		for _, row := range rows {
			if row.Found || row.Size != 0 || len(row.Data) != 0 {
				t.Fatal("missing was present")
			}
		}

		for n, k := range keys {
			if err = tx.WriteRecord(t.Context(), k.Kind, k.Scope, k.Key, []byte{byte(n + 1)}, true); err != nil {
				return err
			}
		}

		rows, err = tx.ReadImmutableRecords(t.Context(), keys)
		if err != nil {
			return err
		}

		for n, row := range rows {
			if row.ControllerRecordKey != keys[n] || !row.Found || row.Size != 1 || len(row.Data) != 1 || row.Data[0] != byte(n+1) {
				t.Fatal("tuple or staged visibility lost", err)
			}
		}

		rows[0].Data[0] = 99

		again, err := tx.ReadImmutableRecords(t.Context(), keys)
		if err != nil || again[0].Data[0] != 1 {
			t.Fatal("batch bytes alias store", err)
		}

		for _, size := range []int{0, managed.MaxAttemptCommand, managed.MaxAttemptCommand + 1} {
			k := keys[0]
			if err = tx.WriteRecord(t.Context(), k.Kind, k.Scope, k.Key, []byte(strings.Repeat("x", size)), false); err != nil {
				return err
			}

			found, e := tx.ReadImmutableRecords(t.Context(), keys[:1])
			if e != nil || !found[0].Found || found[0].Size != size {
				t.Fatal("empty/oversized became absent", e)
			}

			if size > managed.MaxAttemptCommand && len(found[0].Data) != 0 {
				t.Fatal("oversized payload crossed cap")
			}

			if size <= managed.MaxAttemptCommand && len(found[0].Data) != size {
				t.Fatal("bounded payload lost")
			}
		}

		maximumKeys := make([]managedstate.ControllerRecordKey, 64)
		maximumPayload := []byte(strings.Repeat("m", managed.MaxAttemptFence))

		for n := range maximumKeys {
			maximumKeys[n] = managedstate.ControllerRecordKey{Kind: "removal_fence", Scope: managed.Digest([]byte("maximum batch")), Key: managed.Digest([]byte(strconv.Itoa(n)))}

			k := maximumKeys[n]
			if e := tx.WriteRecord(t.Context(), k.Kind, k.Scope, k.Key, maximumPayload, true); e != nil {
				return e
			}
		}

		maximumRows, e := tx.ReadImmutableRecords(t.Context(), maximumKeys)
		if e != nil || len(maximumRows) != 64 {
			t.Fatal("maximum batch unavailable", e)
		}

		total := 0

		for _, row := range maximumRows {
			if !row.Found || row.Size != managed.MaxAttemptFence || len(row.Data) != row.Size {
				t.Fatal("maximum batch truncated")
			}

			total += len(row.Data)
		}

		if total != 3145728 {
			t.Fatal("maximum payload accounting", total)
		}

		t.Logf("actual64-key maximum record transport bytes=%d", total)

		if result, e := tx.ReadImmutableRecords(t.Context(), append(keys, keys[0])); !errors.Is(e, managed.ErrInvalid) || result != nil {
			t.Fatal("duplicate batch accepted", e)
		}

		large := make([]managedstate.ControllerRecordKey, 65)
		for n := range large {
			large[n] = keys[0]
			large[n].Key = managed.Digest([]byte(strconv.Itoa(n)))
		}

		if result, e := tx.ReadImmutableRecords(t.Context(), large); !errors.Is(e, managed.ErrInvalid) || result != nil {
			t.Fatal("oversized batch accepted", e)
		}

		forbidden := keys[0]

		forbidden.Kind = "step"
		if _, e := tx.ReadImmutableRecords(t.Context(), []managedstate.ControllerRecordKey{forbidden}); !errors.Is(e, managed.ErrInvalid) {
			t.Fatal("mutable allowlist bypass", e)
		}

		stamp := time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC)
		expected := managed.Receipt{ID: operation, Digest: strings.Repeat("f", 64), Revision: math.MaxInt64, LeaseEpoch: math.MaxInt64, AcceptedAt: stamp, LeaseUntil: stamp}

		encoded, e := json.Marshal(expected)
		if e != nil || len(encoded) > managed.MaxAttemptReceipt {
			t.Fatal("actual maximal A receipt exceeds cap", len(encoded), e)
		}

		t.Logf("actual maximal A receipt codec bytes=%d cap=%d", len(encoded), managed.MaxAttemptReceipt)

		if _, e = tx.Atomic(t.Context(), action.Key, operation, expected.Digest, func(*managed.Target, time.Time) (managed.Receipt, error) { return expected, nil }); e != nil {
			return e
		}

		receipts, e := tx.ReadImmutableReceipts(t.Context(), action.Key, []id.ID{operation, missing})
		if e != nil || len(receipts) != 2 || !receipts[0].Found || receipts[0].Receipt != expected || receipts[1].Found {
			t.Fatal("receipt staged visibility", e)
		}

		if result, e := tx.ReadImmutableReceipts(t.Context(), action.Key, []id.ID{operation, operation}); !errors.Is(e, managed.ErrInvalid) || result != nil {
			t.Fatal("duplicate receipt accepted", e)
		}

		wrong := action.Key

		wrong.TenantID = "another-tenant"
		if result, e := tx.ReadImmutableReceipts(t.Context(), wrong, []id.ID{operation}); !errors.Is(e, managed.ErrAuthority) || result != nil {
			t.Fatal("receipt tenant escaped", e)
		}

		wrong = action.Key

		wrong.InstanceID = id.New(id.PrefixInstance)
		if result, e := tx.ReadImmutableReceipts(t.Context(), wrong, []id.ID{operation}); !errors.Is(e, managed.ErrNotFound) || result != nil {
			t.Fatal("receipt parent absent", e)
		}

		canceled, cancel := context.WithCancel(t.Context())
		cancel()

		if result, e := tx.ReadImmutableRecords(canceled, keys); !errors.Is(e, context.Canceled) || result != nil {
			t.Fatal("record cancellation ignored", e)
		}

		if result, e := tx.ReadImmutableReceipts(canceled, action.Key, []id.ID{operation}); !errors.Is(e, context.Canceled) || result != nil {
			t.Fatal("receipt cancellation ignored", e)
		}

		held := tx.(managedControllerTransaction)
		bad := expected
		bad.ID = missing

		held.store.managedReceipts[action.InstanceID.String()+":"+operation.String()] = bad
		if result, e := tx.ReadImmutableReceipts(t.Context(), action.Key, []id.ID{operation}); !errors.Is(e, managed.ErrUnavailable) || result != nil {
			t.Fatal("wrong payload operation accepted", e)
		}

		held.store.managedReceipts[action.InstanceID.String()+":"+operation.String()] = expected

		held.store.managedTargets[action.InstanceID.String()].InstanceID = id.New(id.PrefixInstance)
		if result, e := tx.ReadImmutableReceipts(t.Context(), action.Key, []id.ID{operation}); !errors.Is(e, managed.ErrUnavailable) || result != nil {
			t.Fatal("wrong parent payload identity accepted", e)
		}

		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}

	err = (managedBackend{store: s}).ControllerAtomic(t.Context(), "batch rollback", []managed.Key{action.Key}, func(tx managedstate.ControllerTransaction) error {
		rows, e := tx.ReadImmutableRecords(t.Context(), keys)
		if e != nil {
			return e
		}

		for _, row := range rows {
			if row.Found {
				t.Fatal("rolled back record survived")
			}
		}

		receipts, e := tx.ReadImmutableReceipts(t.Context(), action.Key, []id.ID{operation})
		if e != nil || receipts[0].Found {
			t.Fatal("rolled back receipt survived", e)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestManagedAttemptMemoryFullFiniteSetNearLimit(t *testing.T) {
	requireMemoryMaximum(t, "individual")
	testMemoryFiniteSetNearLimit(t, false)
}
func testMemoryFiniteSetNearLimit(t *testing.T, batch bool) {
	testMemoryFiniteSetBudget(t, batch, false)
}
func testMemoryFiniteSetBudget(t *testing.T, batch, positiveReserve bool) {
	boundaries := []bool{true, false}
	if positiveReserve {
		boundaries = []bool{false}
	}

	for _, boundaryOnly := range boundaries {
		t.Run(strconv.FormatBool(boundaryOnly), func(t *testing.T) {
			started := time.Now()

			s, action, observations := memoryAttemptFullSetup(t)
			if len(observations) != 64 {
				t.Fatal("incomplete native set")
			}

			const reserve = 7*64 + 33 + 8

			setRevision := func(value uint64) {
				s.mu.Lock()
				s.managedTargets[action.InstanceID.String()].Revision = value
				s.mu.Unlock()
			}
			setRevision(math.MaxInt64 - reserve)

			start := managed.AttemptStartRequest{Command: projectionCommand(t, s, action.Key), ActionID: action.ID, AttemptID: id.New(managed.PrefixOperation), ProviderOperationID: id.New(managed.PrefixOperation), VerifierID: "provider"}

			before := projectionSnapshot(t, s)
			if _, err := s.BeginManagedRemovalAttempt(t.Context(), start); !errors.Is(err, managed.ErrCapacity) || projectionSnapshot(t, s) != before {
				t.Fatal("489 boundary failed atomic refusal", err)
			}

			headroom := uint64(reserve + 65 + 1)
			if batch {
				headroom = memoryBatchCompletionHeadroom
			}

			if boundaryOnly {
				headroom = reserve + 1
			}

			initial := uint64(math.MaxInt64) - headroom
			setRevision(initial)

			start.Command = projectionCommand(t, s, action.Key)

			accepted, err := s.BeginManagedRemovalAttempt(t.Context(), start)
			if err != nil {
				t.Fatal(err)
			}

			if boundaryOnly {
				if s.managedTargets[action.InstanceID.String()].Revision != math.MaxInt64-reserve {
					t.Fatal("490 boundary changed reserve")
				}

				return
			}

			key := managed.AttemptKey{Key: action.Key, AttemptID: accepted.AttemptID, Generation: "1"}
			must := func(_ managed.Receipt, err error) {
				t.Helper()

				if err != nil {
					t.Fatal(err)
				}
			}
			leases := 0
			renewals := newMemoryBatchRenewalBudget()

			t.Cleanup(func() { renewals.log(t) })

			claimLease := func() {
				t.Helper()

				if batch {
					renewals.leaseAttempt(t)
				}

				must(s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: projectionCommand(t, s, action.Key), Duration: managed.MaxLease}))

				leases++
				if batch {
					renewals.leases++
				}
			}

			renewSetupLease := func(force bool) {
				t.Helper()

				target, e := s.ReadManagedTarget(t.Context(), action.Key)
				if e != nil {
					t.Fatal(e)
				}

				if !force && time.Until(target.LeaseUntil) >= time.Minute {
					return
				}

				if !batch && leases != 0 {
					t.Fatal("setup needs more than single budgeted lease renewal")
				}

				claimLease()
			}
			for _, observation := range observations {
				renewSetupLease(false)

				rid := observation.Command.ReservationID
				grant := memoryAttemptCommand(t, s, action, rid, managed.AttemptBegin)
				evidence := []byte("fence-" + rid.String())
				must(s.RecordManagedAttemptFence(t.Context(), managed.AttemptFenceRequest{AttemptRequest: memoryAttemptRequest(t, s, action), ReservationID: rid, Acceptance: memoryAttemptAcceptance(grant.Command), Fence: managed.FenceWire(managed.Fence{Epoch: 1, Evidence: evidence, Digest: managed.Digest(evidence), CompatibilityEvidence: []byte("retired"), ValidUntil: time.Now().UTC().Add(time.Hour), Survivors: []string{"off-instance"}})}))
			}

			renewSetupLease(leases == 0)

			updates := 0
			aggregates := 0

			var batchHistory []managed.QuiescenceBatchAcceptanceRequest

			refreshBatch := func() {
				t.Helper()
				renewals.batchAttempt(t)
				observations = memoryRefreshBatch(t, s, action, key, observations, &batchHistory)
				renewals.batches++
				updates += 64
				aggregates++
			}

			renewLease := func() {
				t.Helper()

				target, e := s.ReadManagedTarget(t.Context(), action.Key)
				if e != nil {
					t.Fatal(e)
				}

				if time.Until(target.LeaseUntil) < time.Minute {
					claimLease()
				}
			}

			refresh := func(index int) {
				t.Helper()

				q := observations[index]
				q.Command = memoryOriginalCommand(t, s, action.Key, q.Command.ReservationID)
				q.Observation.ObservedAt = time.Now().UTC()
				q.Observation.ValidUntil = q.Observation.ObservedAt.Add(time.Minute)
				signMemoryQuiescence(t, &q)
				must(s.AcceptManagedQuiescence(t.Context(), q))
				observations[index] = q
				updates++
			}

			if batch {
				refreshBatch()
			} else {
				for index := range observations {
					refresh(index)
				}
			}

			must(s.AdmitManagedRemovalAttempt(t.Context(), managed.AttemptAdmissionRequest{AttemptRequest: memoryAttemptRequest(t, s, action), VerifierID: "provider"}))

			record, err := s.ReadManagedRemovalAttempt(t.Context(), key)
			if err != nil {
				t.Fatal(err)
			}

			minimum := time.Minute

			for n := range 33 {
				renewLease()

				for renewed := 0; ; renewed++ {
					oldest := 0
					for index := range observations {
						if observations[index].Observation.ValidUntil.Before(observations[oldest].Observation.ValidUntil) {
							oldest = index
						}
					}

					if time.Until(observations[oldest].Observation.ValidUntil) >= 5*time.Second {
						break
					}

					if batch && renewed >= 1 {
						t.Fatal("one batch refresh did not establish the required proof margin")
					}

					if renewed >= 64 {
						t.Fatal("one full renewal cannot establish proof margin")
					}

					if batch {
						refreshBatch()
					} else {
						refresh(oldest)
					}
				}

				service := "service" + strconv.Itoa(n)
				if n == 32 {
					service = ""
				}

				grant := memoryAttemptStep(t, s, action, record, service)
				for _, q := range observations {
					if margin := q.Observation.ValidUntil.Sub(grant.Permission.IssuedAt); margin < minimum {
						minimum = margin
					}
				}

				if minimum <= 0 {
					t.Fatal("late proof authorized send")
				}

				if err = s.RecordManagedProviderStepResult(t.Context(), managed.ProviderStepResult{Permission: grant.Permission, Outcome: managed.PortTerminal, ResourceID: grant.Permission.ResourceID, Evidence: []byte(strings.Repeat("x", managed.MaxEvidence)), ObservedAt: time.Now().UTC()}); err != nil {
					t.Fatal(err)
				}

				if positiveReserve && n == 23 {
					assertMemoryPositiveBatchReserve(t, s, action, key, observations)

					return
				}
			}

			must(s.SettleManagedRemovalAttempt(t.Context(), managed.AttemptSettlementRequest{AttemptRequest: memoryAttemptRequest(t, s, action), Disposition: managed.Removed, ProducerProfile: "ctrlplane.managed.remove.v1", VerifierID: "provider", Evidence: []byte("all33 positive original results"), SettledAt: time.Now().UTC()}))

			for _, q := range observations {
				renewLease()

				rid := q.Command.ReservationID
				grant := memoryAttemptCommand(t, s, action, rid, managed.AttemptFinish)
				must(s.RecordManagedAttemptFinish(t.Context(), managed.AttemptFinishRequest{AttemptRequest: memoryAttemptRequest(t, s, action), ReservationID: rid, Acceptance: memoryAttemptAcceptance(grant.Command)}))
			}

			must(s.TombstoneManagedRemovalAttempt(t.Context(), memoryAttemptRequest(t, s, action)))

			target, err := s.ReadManagedTarget(t.Context(), action.Key)
			if err != nil {
				t.Fatal(err)
			}

			record, err = s.ReadManagedRemovalAttempt(t.Context(), key)
			if err != nil {
				t.Fatal(err)
			}

			if target.Phase != managed.Deleted || target.Revision-initial != uint64(293+updates+leases+aggregates) {
				t.Fatal("incorrect full near-limit lifecycle delta")
			}

			for _, mask := range record.State.BindingSlots {
				if mask != 1|4|8|32 {
					t.Fatal("incomplete native slot closure")
				}
			}

			for _, mask := range record.State.ProviderSlots {
				if mask != 1|2 {
					t.Fatal("incomplete provider closure")
				}
			}

			revision, err := record.State.Revision.Uint64()
			if err != nil {
				t.Fatal(err)
			}

			raw, err := json.Marshal(target)
			if err != nil {
				t.Fatal(err)
			}

			state, err := json.Marshal(record.State)
			if err != nil {
				t.Fatal(err)
			}

			if batch {
				beforeReplay := projectionSnapshot(t, s)
				for _, q := range batchHistory {
					saved, e := s.ReadManagedQuiescenceBatch(t.Context(), managed.QuiescenceBatchKey{Key: key.Key, BatchID: q.Command.ID})
					if e != nil {
						t.Fatal("terminal batch history", e)
					}

					replay, e := s.AcceptManagedQuiescenceBatch(t.Context(), q)
					if e != nil || replay != saved {
						t.Fatal("terminal batch replay", e)
					}

					base, _ := q.Command.Revision.Uint64()
					for _, i := range []int{0, 63} {
						entry := q.Entries[i]
						child := managed.QuiescenceAcceptanceRequest{Command: managed.OriginalCommand{Key: key.Key, ID: entry.OperationID, Revision: managed.Decimal(strconv.FormatUint(base+uint64(i), 10)), LeaseEpoch: q.Command.LeaseEpoch, Owner: q.Command.Owner, ReservationID: entry.Observation.Reference.ReservationID}, Observation: entry.Observation}

						record, e := s.ReadManagedQuiescence(t.Context(), managed.QuiescenceKey{Key: key.Key, ReservationID: child.Command.ReservationID, OperationID: child.Command.ID})
						if e != nil {
							t.Fatal(e)
						}

						receipt, e := s.AcceptManagedQuiescence(t.Context(), child)
						if e != nil || managed.OriginalReceipt(receipt) != record.Receipt {
							t.Fatal("terminal individual replay", e)
						}
					}
				}

				if afterReplay := projectionSnapshot(t, s); beforeReplay != afterReplay {
					t.Fatal("terminal replay wrote")
				}
			}

			if batch {
				renewals.complete(t)

				if aggregates != renewals.batches || leases != renewals.leases || updates != 64*aggregates || target.Revision-initial != uint64(293+65*aggregates+leases) {
					t.Fatal("batch renewal accounting mismatch")
				}

				t.Logf("finite batch envelope headroom=%d Bmax=%d Lmax=%d U=0 B=%d L=%d", headroom, renewals.maxBatches, renewals.maxLeases, aggregates, leases)
			}

			if batch && revision-1 != 292 {
				t.Fatal("batch changed attempt counter")
			}

			t.Logf("full64/33 near-limit parent delta=%d required293 proof_updates=%d lease_updates=%d aggregate_updates=%d attempt_delta=%d unused_headroom=%d target_bytes=%d state_bytes=%d minimum authorization margin=%s elapsed=%s", target.Revision-initial, updates, leases, aggregates, revision-1, uint64(math.MaxInt64)-target.Revision, len(raw), len(state), minimum, time.Since(started))
		})
	}
}

func TestManagedAttemptMemoryUntrustedDiscovery(t *testing.T) {
	for _, mode := range []string{"unknown", "duplicate", "noncanonical"} {
		t.Run(mode, func(t *testing.T) {
			s, action, key, _ := memoryAttemptFixture(t)
			scope := memoryAttemptScope(t, key.Key)

			rows := s.managedControllerRecords["removal_command:"+scope]
			for slot, raw := range rows {
				var command managed.AttemptCommandRecord
				if err := json.Unmarshal(raw, &command); err != nil {
					t.Fatal(err)
				}

				switch mode {
				case "unknown":
					raw = append([]byte(`{"unknown":true,`), raw[1:]...)
				case "duplicate":
					raw = append([]byte(`{"authorization_id":"`+command.AuthorizationID.String()+`",`), raw[1:]...)
				case "noncanonical":
					raw = append([]byte(" "), raw...)
				}

				rows[slot] = raw
			}

			before := projectionSnapshot(t, s)
			if _, err := s.ReadManagedRemovalAttempt(t.Context(), key); !errors.Is(err, managed.ErrUnavailable) {
				t.Fatal("discovery projection bypassed canonical authority", err)
			}

			if _, err := s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: projectionCommand(t, s, action.Key), Duration: managed.MaxLease}); !errors.Is(err, managed.ErrUnavailable) {
				t.Fatal("fresh transition accepted corrupt discovery row", err)
			}

			if before != projectionSnapshot(t, s) {
				t.Fatal("corrupt discovery mutated rows")
			}
		})
	}
}

func requireMemoryMaximum(t *testing.T, mode string) {
	t.Helper()

	selected := os.Getenv("CTRLPLANE_MANAGED_MAXIMUM")
	if selected == "" {
		t.Skip("required explicit maximum campaign: set CTRLPLANE_MANAGED_MAXIMUM=" + mode + " with its documented test selection")
	}

	if selected != mode {
		t.Fatalf("incorrect maximum campaign selector: want %s, got %s", mode, selected)
	}
}

func TestManagedAttemptMemoryInvalidCommand(t *testing.T) {
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
					s, action, observation := memoryQuiescenceProviderSetup(t, false)
					rid := observation.Command.ReservationID
					start := managed.AttemptStartRequest{Command: projectionCommand(t, s, action.Key), ActionID: action.ID, AttemptID: id.New(managed.PrefixOperation), ProviderOperationID: id.New(managed.PrefixOperation), VerifierID: "provider"}

					attemptKey := managed.AttemptKey{Key: start.Key, AttemptID: start.AttemptID, Generation: "1"}
					if entry != "begin" {
						if _, err := s.BeginManagedRemovalAttempt(t.Context(), start); err != nil {
							t.Fatal(err)
						}
					}

					freshCommand := func() managed.Command { return projectionCommand(t, s, action.Key) }
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

					before := projectionSnapshot(t, s)

					got, err := call(invalid)
					if !errors.Is(err, tc.want) {
						t.Errorf("invalid command: got %v, want %v", err, tc.want)
					}

					if !reflect.DeepEqual(got, reflect.Zero(reflect.TypeOf(got)).Interface()) {
						t.Error("refused command returned receipt or grant", got)
					}

					if !reflect.DeepEqual(before, projectionSnapshot(t, s)) {
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

					after := projectionSnapshot(t, s)

					replay, err := call(valid)
					if err != nil || !reflect.DeepEqual(accepted, replay) {
						t.Fatal("exact replay changed", err)
					}

					if !reflect.DeepEqual(after, projectionSnapshot(t, s)) {
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

func memoryContinuationInput(t *testing.T) (*Store, managed.ContinuationRequest) {
	t.Helper()
	s, action, _ := memoryQuiescenceProviderSetup(t, false)
	start := managed.AttemptStartRequest{Command: projectionCommand(t, s, action.Key), ActionID: action.ID, AttemptID: id.New(managed.PrefixOperation), ProviderOperationID: id.New(managed.PrefixOperation), VerifierID: "provider"}

	first, err := s.BeginManagedRemovalAttempt(t.Context(), start)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = s.RevokeManagedRemovalAttempt(t.Context(), managed.AttemptRequest{Command: projectionCommand(t, s, action.Key), AttemptID: first.AttemptID, Generation: "1"}); err != nil {
		t.Fatal(err)
	}

	action.Command = projectionCommand(t, s, action.Key)
	action.Kind = managed.ActionContinue
	action.NewTarget = nil
	action.ReleaseID = id.Nil
	action.RollbackReleaseID = id.Nil
	action.CanonicalDigest = ""

	target, err := s.ReadManagedTarget(t.Context(), action.Key)
	if err != nil {
		t.Fatal(err)
	}

	action.ExpectedDesiredRevision = target.DesiredRevision
	action.OriginalCommand = []byte("continue")
	action.OriginalDigest = managed.Digest(action.OriginalCommand)
	q := managed.ContinuationRequest{Action: action, Predecessor: managed.AttemptRef{AttemptID: first.AttemptID, Generation: first.Generation, DescriptorDigest: first.DescriptorDigest}, SuccessorAttemptID: id.New(managed.PrefixOperation), ProviderOperationID: id.New(managed.PrefixOperation)}

	q.Action.CanonicalDigest, err = managed.RemovalContinuationDigest(q)
	if err != nil {
		t.Fatal(err)
	}

	return s, q
}

func TestManagedRemovalContinuationMemoryAtomicity(t *testing.T) {
	for n := 1; n <= 32; n++ {
		s, q := memoryContinuationInput(t)
		before := projectionSnapshot(t, s)
		fault := &attemptFault{fail: n}
		repo := managedstate.New(attemptFaultBackend{managedBackend{store: s}, fault})

		result, err := repo.BeginManagedRemovalContinuation(t.Context(), q)
		if err == nil {
			t.Logf("checked %d write/commit boundaries: %v", n-1, fault.actions)

			return
		}

		if !reflect.DeepEqual(result, managed.ContinuationReceipt{}) {
			t.Fatal("failed commit exposed receipt")
		}

		if after := projectionSnapshot(t, s); after != before {
			t.Fatalf("boundary %d leaked a graph member", n)
		}

		if _, err = s.BeginManagedRemovalContinuation(t.Context(), q); err != nil {
			t.Fatalf("same request after rollback: %v", err)
		}
	}

	t.Fatal("no successful boundary")
}

func TestManagedRemovalContinuationMemoryAmbiguousCommit(t *testing.T) {
	t.Run("persisted", func(t *testing.T) {
		s, q := memoryContinuationInput(t)
		repo := managedstate.New(attemptAmbiguousBackend{managedBackend{store: s}})

		result, err := repo.BeginManagedRemovalContinuation(t.Context(), q)
		if !errors.Is(err, managed.ErrUnavailable) || !reflect.DeepEqual(result, managed.ContinuationReceipt{}) {
			t.Fatalf("ambiguous commit exposed result: %+v %v", result, err)
		}

		saved, err := s.ReadManagedRemovalContinuation(t.Context(), managed.ContinuationKey{Key: q.Action.Key, ActionID: q.Action.ID})
		if err != nil {
			t.Fatal(err)
		}

		before := projectionSnapshot(t, s)

		replay, err := s.BeginManagedRemovalContinuation(t.Context(), q)
		if err != nil || !reflect.DeepEqual(replay, saved.Receipt) || projectionSnapshot(t, s) != before {
			t.Fatalf("reconciliation mutated graph: %v", err)
		}
	})
	t.Run("absent", func(t *testing.T) {
		s, q := memoryContinuationInput(t)
		before := projectionSnapshot(t, s)
		repo := managedstate.New(continuationAbsentCommitBackend{managedBackend{store: s}})

		result, err := repo.BeginManagedRemovalContinuation(t.Context(), q)
		if err == nil || !reflect.DeepEqual(result, managed.ContinuationReceipt{}) || projectionSnapshot(t, s) != before {
			t.Fatal("failed acceptance leaked result or graph")
		}

		if _, err = s.ReadManagedRemovalContinuation(t.Context(), managed.ContinuationKey{Key: q.Action.Key, ActionID: q.Action.ID}); !errors.Is(err, managed.ErrNotFound) {
			t.Fatalf("absent graph: %v", err)
		}

		if _, err = s.BeginManagedRemovalContinuation(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	})
}

func TestManagedRemovalContinuationMemoryAbsentStaleAuthority(t *testing.T) {
	s, q := memoryContinuationInput(t)
	repo := managedstate.New(continuationAbsentCommitBackend{managedBackend{store: s}})

	result, err := repo.BeginManagedRemovalContinuation(t.Context(), q)
	if !errors.Is(err, managed.ErrUnavailable) || !reflect.DeepEqual(result, managed.ContinuationReceipt{}) {
		t.Fatal("uncertain absence result", err)
	}

	if _, err = s.ReadManagedRemovalContinuation(t.Context(), managed.ContinuationKey{Key: q.Action.Key, ActionID: q.Action.ID}); !errors.Is(err, managed.ErrNotFound) {
		t.Fatal("authoritative absence", err)
	}

	if _, err = s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: projectionCommand(t, s, q.Action.Key), Duration: managed.MaxLease}); err != nil {
		t.Fatal(err)
	}

	before := projectionSnapshot(t, s)
	if _, err = s.BeginManagedRemovalContinuation(t.Context(), q); !errors.Is(err, managed.ErrConflict) {
		t.Fatal("same-ID stale authority accepted", err)
	}

	if projectionSnapshot(t, s) != before {
		t.Fatal("stale uncertain retry mutated graph")
	}
}

func TestManagedRemovalContinuationMemoryCorruption(t *testing.T) {
	s, q := memoryContinuationInput(t)

	receipt, err := s.BeginManagedRemovalContinuation(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}

	scope := memoryAttemptScope(t, q.Action.Key)

	cases := [][3]string{{"removal_continuation", scope, q.Action.ID.String()}, {"removal_attempt_closure", scope, q.Predecessor.AttemptID.String()}, {"removal_attempt", scope, q.SuccessorAttemptID.String()}, {"removal_attempt_state", scope, q.Predecessor.AttemptID.String()}, {"removal_attempt_current", scope, "current"}, {"removal_identity", "", q.ProviderOperationID.String()}, {"removal_attempt_event", scope, q.Action.ID.String()}, {"action", "", q.Action.ID.String()}, {"operation", q.Action.TenantID, q.Action.ID.String()}}
	for _, entry := range cases {
		t.Run(entry[0], func(t *testing.T) {
			rows := s.managedControllerRecords[entry[0]+":"+entry[1]]
			old := rows[entry[2]]

			delete(rows, entry[2])
			defer func() { rows[entry[2]] = old }()

			before := projectionSnapshot(t, s)
			if _, e := s.ReadManagedRemovalContinuation(t.Context(), managed.ContinuationKey{Key: q.Action.Key, ActionID: q.Action.ID}); e == nil {
				t.Fatal("partial graph read accepted")
			}

			if _, e := s.BeginManagedRemovalContinuation(t.Context(), q); e == nil {
				t.Fatal("partial graph replay accepted")
			}

			if _, e := s.ReadManagedRemovalAttempt(t.Context(), managed.AttemptKey{Key: q.Action.Key, AttemptID: receipt.Successor.AttemptID, Generation: receipt.Successor.Generation}); e == nil {
				t.Fatal("broken current lineage accepted")
			}

			if projectionSnapshot(t, s) != before {
				t.Fatal("partial graph repaired")
			}
		})
	}
}

// continuationAbsentCommitBackend returns the same uncertainty class after staging
// a complete graph, while the backing transaction rolls it back.
type continuationAbsentCommitBackend struct{ managedBackend }

func (b continuationAbsentCommitBackend) ControllerAtomic(ctx context.Context, serial string, keys []managed.Key, change func(managedstate.ControllerTransaction) error) error {
	return b.managedBackend.ControllerAtomic(ctx, serial, keys, func(tx managedstate.ControllerTransaction) error {
		if err := change(tx); err != nil {
			return err
		}

		return managed.ErrUnavailable
	})
}

func TestManagedRemovalContinuationMemoryMissingHistoricalLink(t *testing.T) {
	s, q := memoryContinuationInput(t)

	var (
		selected managed.AttemptKey
		replay   managed.ContinuationRequest
	)

	for n := 2; n <= 5; n++ {
		got, err := s.BeginManagedRemovalContinuation(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}

		key := managed.AttemptKey{Key: q.Action.Key, AttemptID: got.Successor.AttemptID, Generation: got.Successor.Generation}
		if n == 3 {
			selected = key
			replay = q
		}

		if n == 5 {
			break
		}

		if _, err = s.RevokeManagedRemovalAttempt(t.Context(), managed.AttemptRequest{Command: projectionCommand(t, s, key.Key), AttemptID: key.AttemptID, Generation: key.Generation}); err != nil {
			t.Fatal(err)
		}

		q.Action.Command = projectionCommand(t, s, key.Key)
		q.Predecessor = got.Successor
		q.SuccessorAttemptID = id.New(managed.PrefixOperation)
		q.ProviderOperationID = id.New(managed.PrefixOperation)
		q.Action.CanonicalDigest = ""

		q.Action.CanonicalDigest, err = managed.RemovalContinuationDigest(q)
		if err != nil {
			t.Fatal(err)
		}
	}

	if _, err := s.ReadManagedRemovalAttempt(t.Context(), selected); err != nil {
		t.Fatal("valid history", err)
	}

	rows := s.managedControllerRecords["removal_attempt_state:"+memoryAttemptScope(t, selected.Key)]

	var state managed.AttemptState
	if err := json.Unmarshal(rows[selected.AttemptID.String()], &state); err != nil {
		t.Fatal(err)
	}

	state.Successor = nil
	state.ClosureDigest = ""

	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}

	rows[selected.AttemptID.String()] = raw

	before := projectionSnapshot(t, s)
	if _, err = s.ReadManagedRemovalAttempt(t.Context(), selected); !errors.Is(err, managed.ErrUnavailable) {
		t.Error("missing selected closure accepted", err)
	}

	if got, e := s.BeginManagedRemovalContinuation(t.Context(), replay); !errors.Is(e, managed.ErrUnavailable) || !reflect.DeepEqual(got, managed.ContinuationReceipt{}) {
		t.Error("damaged historical replay accepted", e)
	}

	if projectionSnapshot(t, s) != before {
		t.Fatal("damaged graph repaired")
	}
}

func TestManagedRemovalContinuationMemoryParentBoundary(t *testing.T) {
	s, action, _ := memoryAttemptFullSetup(t)
	if _, err := s.BeginManagedRemovalAttempt(t.Context(), managed.AttemptStartRequest{Command: projectionCommand(t, s, action.Key), ActionID: action.ID, AttemptID: id.New(managed.PrefixOperation), ProviderOperationID: id.New(managed.PrefixOperation), VerifierID: "provider"}); err != nil {
		t.Fatal(err)
	}

	first := memoryAttemptRequest(t, s, action)
	if _, err := s.RevokeManagedRemovalAttempt(t.Context(), first); err != nil {
		t.Fatal(err)
	}

	record, err := s.ReadManagedRemovalAttempt(t.Context(), managed.AttemptKey{Key: action.Key, AttemptID: first.AttemptID, Generation: first.Generation})
	if err != nil {
		t.Fatal(err)
	}

	for _, headroom := range []uint64{489, 490} {
		s.managedTargets[action.InstanceID.String()].Revision = math.MaxInt64 - headroom
		action.Command = projectionCommand(t, s, action.Key)
		action.Kind = managed.ActionContinue
		action.NewTarget = nil
		action.ReleaseID = id.Nil
		action.RollbackReleaseID = id.Nil
		action.CanonicalDigest = ""
		action.ExpectedDesiredRevision = s.managedTargets[action.InstanceID.String()].DesiredRevision
		action.OriginalCommand = []byte("continue")
		action.OriginalDigest = managed.Digest(action.OriginalCommand)
		q := managed.ContinuationRequest{Action: action, Predecessor: record.State.AttemptRef, SuccessorAttemptID: id.New(managed.PrefixOperation), ProviderOperationID: id.New(managed.PrefixOperation)}

		q.Action.CanonicalDigest, err = managed.RemovalContinuationDigest(q)
		if err != nil {
			t.Fatal(err)
		}

		before := projectionSnapshot(t, s)

		got, e := s.BeginManagedRemovalContinuation(t.Context(), q)
		if headroom == 489 {
			if !errors.Is(e, managed.ErrCapacity) || !reflect.DeepEqual(got, managed.ContinuationReceipt{}) || projectionSnapshot(t, s) != before {
				t.Fatal("489 boundary", e)
			}
		} else if e != nil || got.Receipt.Revision != math.MaxInt64-489 || got.Successor.Generation != "2" {
			t.Fatal("490 boundary", e)
		}

		t.Logf("synthetic parent seed=%d headroom=%d N=64 S=33 accepted=%t", math.MaxInt64-headroom, headroom, e == nil)
	}
}

func TestManagedRemovalContinuationMemoryCounterBounds(t *testing.T) {
	s, q := memoryContinuationInput(t)

	got, err := s.BeginManagedRemovalContinuation(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}

	key := managed.AttemptKey{Key: q.Action.Key, AttemptID: got.Successor.AttemptID, Generation: got.Successor.Generation}
	opKey := managed.OperationKey{Key: key.Key, ActionID: q.Action.ID}

	op, err := s.ReadManagedOperation(t.Context(), opKey)
	if err != nil {
		t.Fatal(err)
	}

	op.Revision = math.MaxInt64 - 1

	raw, err := json.Marshal(op)
	if err != nil {
		t.Fatal(err)
	}

	s.managedControllerRecords["operation:"+key.TenantID][q.Action.ID.String()] = raw
	if _, err = s.ReadManagedOperation(t.Context(), opKey); err != nil {
		t.Fatal("seeded journal invalid", err)
	}

	advance := managed.OperationAdvance{Command: projectionCommand(t, s, key.Key), ActionID: q.Action.ID, ExpectedOperationRevision: op.Revision, Observation: managed.OperationObservation{Kind: "pending", ObservedAt: time.Now().UTC()}}

	before := projectionSnapshot(t, s)
	if _, err = s.AdvanceManagedOperation(t.Context(), advance); !errors.Is(err, managed.ErrCapacity) || projectionSnapshot(t, s) != before {
		t.Fatal("journal consumed final slot", err)
	}

	advance.Observation.Kind = "completed"

	terminal, err := s.AdvanceManagedOperation(t.Context(), advance)
	if err != nil || terminal.Revision != math.MaxInt64 {
		t.Fatal("final journal slot", err)
	}

	before = projectionSnapshot(t, s)

	replay, err := s.AdvanceManagedOperation(t.Context(), advance)
	if err != nil || !reflect.DeepEqual(replay, terminal) || projectionSnapshot(t, s) != before {
		t.Fatal("terminal replay", err)
	}

	advance.Command = projectionCommand(t, s, key.Key)

	advance.ExpectedOperationRevision = math.MaxInt64
	if _, err = s.AdvanceManagedOperation(t.Context(), advance); !errors.Is(err, managed.ErrCapacity) || projectionSnapshot(t, s) != before {
		t.Fatal("journal overflow", err)
	}

	if _, err = s.RevokeManagedRemovalAttempt(t.Context(), managed.AttemptRequest{Command: projectionCommand(t, s, key.Key), AttemptID: key.AttemptID, Generation: key.Generation}); err != nil {
		t.Fatal(err)
	}

	rows := s.managedControllerRecords["removal_attempt_state:"+memoryAttemptScope(t, key.Key)]

	var state managed.AttemptState
	if err = json.Unmarshal(rows[key.AttemptID.String()], &state); err != nil {
		t.Fatal(err)
	}

	state.Revision = managed.Decimal(strconv.FormatInt(math.MaxInt64, 10))

	raw, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}

	rows[key.AttemptID.String()] = raw
	if _, err = s.ReadManagedRemovalAttempt(t.Context(), key); err != nil {
		t.Fatal("seeded attempt invalid", err)
	}

	q.Action.Command = projectionCommand(t, s, key.Key)
	q.Predecessor = got.Successor
	q.SuccessorAttemptID = id.New(managed.PrefixOperation)
	q.ProviderOperationID = id.New(managed.PrefixOperation)
	q.Action.CanonicalDigest = ""

	q.Action.CanonicalDigest, err = managed.RemovalContinuationDigest(q)
	if err != nil {
		t.Fatal(err)
	}

	before = projectionSnapshot(t, s)
	if _, err = s.BeginManagedRemovalContinuation(t.Context(), q); !errors.Is(err, managed.ErrCapacity) || projectionSnapshot(t, s) != before {
		t.Fatal("attempt overflow", err)
	}

	t.Log("synthetic journal M-1 reserves terminal; completed reaches M; attempt state M refuses Continue")
}

func continuationMemoryRows(t *testing.T, s *Store, key managed.Key) continuationtest.Storage {
	t.Helper()

	return continuationtest.Storage{Rows: func() []continuationtest.Row {
		var out []continuationtest.Row

		for bucket, rows := range s.managedControllerRecords {
			kind, scope, ok := strings.Cut(bucket, ":")
			if !ok {
				t.Fatal("invalid fixture bucket")
			}

			for k, v := range rows {
				out = append(out, continuationtest.Row{Kind: kind, Scope: scope, Key: k, Data: append([]byte{}, v...)})
			}
		}

		sort.Slice(out, func(i, j int) bool {
			a, b := out[i], out[j]

			return a.Kind+":"+a.Scope+":"+a.Key < b.Kind+":"+b.Scope+":"+b.Key
		})

		return out
	}, Put: func(r continuationtest.Row) {
		bucket := r.Kind + ":" + r.Scope
		if s.managedControllerRecords[bucket] == nil {
			s.managedControllerRecords[bucket] = map[string][]byte{}
		}

		s.managedControllerRecords[bucket][r.Key] = append([]byte{}, r.Data...)
	}, Receipt: func(r managed.Receipt) { s.managedReceipts[key.InstanceID.String()+":"+r.ID.String()] = r }}
}
func TestManagedRemovalContinuationMemoryGenerationBoundary(t *testing.T) {
	s, q := memoryContinuationInput(t)
	root := managed.AttemptKey{Key: q.Action.Key, AttemptID: q.Predecessor.AttemptID, Generation: "1"}

	var current managed.AttemptKey

	for n := 2; n <= 5; n++ {
		got, err := s.BeginManagedRemovalContinuation(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}

		current = managed.AttemptKey{Key: q.Action.Key, AttemptID: got.Successor.AttemptID, Generation: got.Successor.Generation}
		if _, err = s.RevokeManagedRemovalAttempt(t.Context(), managed.AttemptRequest{Command: projectionCommand(t, s, current.Key), AttemptID: current.AttemptID, Generation: current.Generation}); err != nil {
			t.Fatal(err)
		}

		q.Action.Command = projectionCommand(t, s, current.Key)
		q.Predecessor = got.Successor
		q.SuccessorAttemptID = id.New(managed.PrefixOperation)
		q.ProviderOperationID = id.New(managed.PrefixOperation)
		q.Action.CanonicalDigest = ""

		q.Action.CanonicalDigest, err = managed.RemovalContinuationDigest(q)
		if err != nil {
			t.Fatal(err)
		}
	}

	target, readErr := s.ReadManagedTarget(t.Context(), current.Key)
	if readErr != nil {
		t.Fatal(readErr)
	}

	current = continuationtest.LateGeneration(t, continuationMemoryRows(t, s, current.Key), current, target)

	seeded, err := s.ReadManagedRemovalAttempt(t.Context(), current)
	if err != nil {
		t.Fatal("seeded current validation", err)
	}

	if _, err = s.ReadManagedRemovalAttempt(t.Context(), root); err != nil {
		t.Fatal("seeded historical root validation", err)
	}

	q.Action.Command = projectionCommand(t, s, current.Key)
	q.Predecessor = seeded.State.AttemptRef
	q.Action.CanonicalDigest = ""

	q.Action.CanonicalDigest, err = managed.RemovalContinuationDigest(q)
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.BeginManagedRemovalContinuation(t.Context(), q)
	if err != nil || got.Successor.Generation != managed.Decimal(strconv.FormatInt(math.MaxInt64, 10)) {
		t.Fatal("final generation", err)
	}

	current.Generation = got.Successor.Generation

	current.AttemptID = got.Successor.AttemptID
	if _, err = s.ReadManagedRemovalAttempt(t.Context(), current); err != nil {
		t.Fatal("final current validation", err)
	}

	q.Action.Command = projectionCommand(t, s, current.Key)
	q.Predecessor = got.Successor
	q.SuccessorAttemptID = id.New(managed.PrefixOperation)
	q.ProviderOperationID = id.New(managed.PrefixOperation)
	q.Action.CanonicalDigest = ""

	before := projectionSnapshot(t, s)
	if _, err = s.BeginManagedRemovalContinuation(t.Context(), q); !errors.Is(err, managed.ErrInvalid) || projectionSnapshot(t, s) != before {
		t.Fatal("generation/pointer M overflow", err)
	}

	t.Log("generation and pointer M-1 accepted final M; next request rejected by digest generation bound before writes")
}

func memoryContinuationTerminalInput(t *testing.T) (*Store, managed.ContinuationRequest, managed.QuiescenceObservation) {
	t.Helper()
	s, action, observation := memoryQuiescenceProviderSetup(t, false)
	start := managed.AttemptStartRequest{Command: projectionCommand(t, s, action.Key), ActionID: action.ID, AttemptID: id.New(managed.PrefixOperation), ProviderOperationID: id.New(managed.PrefixOperation), VerifierID: "provider"}

	first, err := s.BeginManagedRemovalAttempt(t.Context(), start)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = s.RevokeManagedRemovalAttempt(t.Context(), managed.AttemptRequest{Command: projectionCommand(t, s, action.Key), AttemptID: first.AttemptID, Generation: "1"}); err != nil {
		t.Fatal(err)
	}

	action.Command = projectionCommand(t, s, action.Key)
	action.Kind = managed.ActionContinue
	action.NewTarget = nil
	action.ReleaseID = id.Nil
	action.RollbackReleaseID = id.Nil
	action.CanonicalDigest = ""

	target, err := s.ReadManagedTarget(t.Context(), action.Key)
	if err != nil {
		t.Fatal(err)
	}

	action.ExpectedDesiredRevision = target.DesiredRevision
	action.OriginalCommand = []byte("continue")
	action.OriginalDigest = managed.Digest(action.OriginalCommand)
	q := managed.ContinuationRequest{Action: action, Predecessor: managed.AttemptRef{AttemptID: first.AttemptID, Generation: first.Generation, DescriptorDigest: first.DescriptorDigest}, SuccessorAttemptID: id.New(managed.PrefixOperation), ProviderOperationID: id.New(managed.PrefixOperation)}

	q.Action.CanonicalDigest, err = managed.RemovalContinuationDigest(q)
	if err != nil {
		t.Fatal(err)
	}

	return s, q, observation.Observation
}

func TestManagedRemovalContinuationMemoryExpiredFinish(t *testing.T) {
	s, q, observation := memoryContinuationTerminalInput(t)

	got, err := s.BeginManagedRemovalContinuation(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}

	key := managed.AttemptKey{Key: q.Action.Key, AttemptID: got.Successor.AttemptID, Generation: got.Successor.Generation}
	continuationtest.ExpiredFinish(t, s, continuationMemoryRows(t, s, key.Key), key, []managed.QuiescenceObservation{observation}, time.Now)
}

func TestManagedRemovalContinuationMemoryStorageCaps(t *testing.T) {
	s, q := memoryContinuationInput(t)

	got, err := s.BeginManagedRemovalContinuation(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}

	scope := memoryAttemptScope(t, q.Action.Key)
	for _, entry := range []struct {
		kind, scope, key string
		limit            int
	}{
		{"removal_continuation", scope, q.Action.ID.String(), managed.MaxRemovalContinuationMaterial},
		{"removal_attempt_closure", scope, q.Predecessor.AttemptID.String(), managed.MaxRemovalContinuationClosure},
		{"removal_attempt", scope, got.Successor.AttemptID.String(), managed.MaxAttemptDescriptor},
		{"removal_attempt_state", scope, got.Successor.AttemptID.String(), managed.MaxAttemptFact},
		{"removal_attempt_current", scope, "current", managed.MaxAttemptPointer},
		{"removal_identity", "", q.ProviderOperationID.String(), managed.MaxAttemptPointer},
		{"removal_attempt_event", scope, q.Action.ID.String(), managed.MaxAttemptFact},
	} {
		t.Run(entry.kind, func(t *testing.T) {
			records := s.managedControllerRecords[entry.kind+":"+entry.scope]

			saved := records[entry.key]
			defer func() { records[entry.key] = saved }()

			for _, n := range []int{entry.limit, entry.limit + 1} {
				records[entry.key] = append(append([]byte{}, saved...), bytes.Repeat([]byte(" "), n-len(saved))...)
				before := projectionSnapshot(t, s)

				_, e := s.ReadManagedRemovalContinuation(t.Context(), managed.ContinuationKey{Key: q.Action.Key, ActionID: q.Action.ID})
				if e == nil || projectionSnapshot(t, s) != before {
					t.Fatal("oversized retained input accepted/repaired", e)
				}

				t.Logf("bytes=%d cap=%d refusal=%v; padded noncanonical raw input", n, entry.limit, e)
			}
		})
	}

	records := s.managedControllerRecords["operation:"+q.Action.TenantID]
	saved := records[q.Action.ID.String()]

	var op managed.OperationState
	if err = json.Unmarshal(saved, &op); err != nil {
		t.Fatal(err)
	}

	op.ContinuationAssociation.MaterialDigest = string(bytes.Repeat([]byte("x"), managed.MaxRemovalContinuationAssociation+1))

	raw, err := json.Marshal(op)
	if err != nil {
		t.Fatal(err)
	}

	records[q.Action.ID.String()] = raw

	before := projectionSnapshot(t, s)
	if _, err = s.ReadManagedRemovalContinuation(t.Context(), managed.ContinuationKey{Key: q.Action.Key, ActionID: q.Action.ID}); err == nil || projectionSnapshot(t, s) != before {
		t.Fatal("oversized association accepted/repaired", err)
	}

	records[q.Action.ID.String()] = saved

	t.Log("association oversized digest rejected by exact retained association comparison; public record derives from bounded validated components")
}

func TestManagedAttemptMemoryHeldBatches(t *testing.T) {
	s, action, _, _ := memoryAttemptFixture(t)

	before, err := s.ReadManagedTarget(t.Context(), action.Key)
	if err != nil {
		t.Fatal(err)
	}

	beforeRaw, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}

	err = (managedBackend{store: s}).ControllerAtomic(t.Context(), "held-batch", []managed.Key{action.Key}, func(tx managedstate.ControllerTransaction) error {
		keys := []managedstate.ControllerRecordKey{}

		for i, kind := range []string{"step", "removal_provider_slot", "step_failure", "step_history"} {
			scope := action.TenantID
			if kind == "removal_provider_slot" {
				scope = managed.Digest([]byte("scope"))
			}

			keys = append(keys, managedstate.ControllerRecordKey{Kind: kind, Scope: scope, Key: managed.Digest([]byte(strconv.Itoa(i)))})
		}

		rows, e := tx.ReadHeldRecords(t.Context(), keys)
		if e != nil || len(rows) != 4 {
			t.Fatal(e)
		}

		for _, row := range rows {
			if row.Found || row.Size != 0 || len(row.Data) != 0 {
				t.Fatal("absence lost")
			}
		}

		for i, selected := range keys {
			raw := []byte(strings.Repeat("x", managedstate.HeldRecordLimit(selected.Kind)))
			if e = tx.WriteRecord(t.Context(), selected.Kind, selected.Scope, selected.Key, raw, true); e != nil {
				return e
			}

			rows, e = tx.ReadHeldRecords(t.Context(), keys)
			if e != nil || !rows[i].Found || string(rows[i].Data) != string(raw) || rows[i].ControllerRecordKey != selected {
				t.Fatal("held mutation/identity/bytes", e)
			}

			rows[i].Data[0] = 'Y'

			again, readErr := tx.ReadHeldRecords(t.Context(), keys)
			if readErr != nil || string(again[i].Data) != string(raw) {
				t.Fatal("borrowed bytes", readErr)
			}
		}

		if _, e = tx.ReadHeldRecords(t.Context(), append(keys, keys[0])); !errors.Is(e, managed.ErrInvalid) {
			t.Fatal("duplicate", e)
		}

		over := make([]managedstate.ControllerRecordKey, 65)
		if _, e = tx.ReadHeldRecords(t.Context(), over); !errors.Is(e, managed.ErrInvalid) {
			t.Fatal("bound", e)
		}

		bad := keys[0]

		bad.Kind = "removal_attempt_state"
		if _, e = tx.ReadHeldRecords(t.Context(), []managedstate.ControllerRecordKey{bad}); !errors.Is(e, managed.ErrInvalid) {
			t.Fatal("kind", e)
		}

		canceled, cancel := context.WithCancel(t.Context())
		cancel()

		if _, e = tx.ReadHeldRecords(canceled, keys); !errors.Is(e, context.Canceled) {
			t.Fatal("cancel", e)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	after, err := s.ReadManagedTarget(t.Context(), action.Key)
	if err != nil {
		t.Fatal(err)
	}

	afterRaw, err := json.Marshal(after)
	if err != nil || string(afterRaw) != string(beforeRaw) {
		t.Fatal("parent changed", err)
	}
}

func TestManagedAttemptMemoryUnexpectedProviderErrorIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		size       int
	}{
		{"malformed-association", "removal_provider_slot", 2},
		{"oversized-association", "removal_provider_slot", managed.MaxAttemptAssociation + 1},
		{"oversized-failure", "step_failure", managed.MaxAttemptSettlement + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, key, _ := memoryAttemptFixture(t)

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

			scope := memoryAttemptScope(t, key.Key)
			if tc.kind == "step_failure" {
				scope = key.TenantID
			}

			raw := []byte(`{}`)
			if tc.size > len(raw) {
				raw = bytes.Repeat([]byte("x"), tc.size)
			}

			bucket := tc.kind + ":" + scope
			if s.managedControllerRecords[bucket] == nil {
				s.managedControllerRecords[bucket] = make(map[string][]byte)
			}

			s.managedControllerRecords[bucket][slot] = raw
			before := projectionSnapshot(t, s)

			_, err = s.ReadManagedRemovalAttempt(t.Context(), key)
			if before != projectionSnapshot(t, s) {
				t.Fatal("refused read mutated retained state")
			}

			if !errors.Is(err, managed.ErrUnavailable) || errors.Is(err, managed.ErrInvalid) || errors.Is(err, managed.ErrCapacity) {
				t.Fatal("unexpected provider presence must refuse as unavailable", err)
			}
		})
	}
}
