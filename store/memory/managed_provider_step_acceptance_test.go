package memory

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

func TestManagedProviderStepAcceptanceRefusesCorruptHistory(t *testing.T) {
	for _, mutation := range []string{"missing receipt", "receipt digest", "receipt command", "receipt revision", "receipt epoch", "receipt clock", "missing permission", "permission digest", "request owner", "request revision", "request epoch", "request command", "request action", "request parent", "request tenant"} {
		t.Run(mutation, func(t *testing.T) {
			s, action, projection := projectionFixture(t)
			if _, err := s.ProjectManagedRows(t.Context(), projection); err != nil {
				t.Fatal(err)
			}

			q := managed.ProviderStepAuthorization{Command: projectionCommand(t, s, action.Key), Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: action.Key, ActionID: action.ID}, ProviderOperationID: action.NewTarget.ProvisionOperationID, Kind: managed.StepCreateNetwork}}
			if _, err := s.AuthorizeManagedProviderStep(t.Context(), q); err != nil {
				t.Fatal(err)
			}

			s.mu.Lock()
			receiptKey := action.InstanceID.String() + ":" + q.ID.String()
			receipt := s.managedReceipts[receiptKey]

			switch mutation {
			case "missing receipt":
				delete(s.managedReceipts, receiptKey)
			case "receipt digest":
				receipt.Digest = strings.Repeat("f", 64)
				s.managedReceipts[receiptKey] = receipt
			case "receipt command":
				receipt.ID = id.New(managed.PrefixOperation)
				s.managedReceipts[receiptKey] = receipt
			case "receipt revision":
				receipt.Revision++
				s.managedReceipts[receiptKey] = receipt
			case "receipt epoch":
				receipt.LeaseEpoch++
				s.managedReceipts[receiptKey] = receipt
			case "receipt clock":
				receipt.LeaseUntil = receipt.AcceptedAt
				s.managedReceipts[receiptKey] = receipt
			case "missing permission", "permission digest":
				bucket := s.managedControllerRecords["step:"+action.TenantID]
				for key, raw := range bucket {
					if mutation == "missing permission" {
						delete(bucket, key)

						continue
					}

					var record managed.ProviderStepRecord
					if err := json.Unmarshal(raw, &record); err != nil {
						s.mu.Unlock()
						t.Fatal(err)
					}

					record.Permission.Digest = strings.Repeat("e", 64)

					changed, err := json.Marshal(record)
					if err != nil {
						s.mu.Unlock()
						t.Fatal(err)
					}

					bucket[key] = changed
				}
			case "request owner":
				q.Owner = "another-owner"
			case "request revision":
				q.Revision++
			case "request epoch":
				q.LeaseEpoch++
			case "request command":
				q.ID = id.New(managed.PrefixOperation)
			case "request action":
				q.Step.ActionID = id.New(managed.PrefixOperation)
			case "request parent":
				q.InstanceID = id.New(id.PrefixInstance)
				q.Step.InstanceID = q.InstanceID
			case "request tenant":
				q.TenantID = "other-tenant"
				q.Step.TenantID = q.TenantID
			}
			s.mu.Unlock()
			before := projectionSnapshot(t, s)

			got, err := s.ReadManagedProviderStepAcceptance(t.Context(), q)
			if err == nil || got != (managed.ProviderStepAcceptance{}) {
				t.Fatal("corrupt acceptance returned", got, err)
			}

			if before != projectionSnapshot(t, s) {
				t.Fatal("refusal mutated authority")
			}
		})
	}
}

// The changed start resource remains a canonical record with matching result
// permission copies. Its independent apply receipt is deliberately untouched.
func TestManagedProviderStepAcceptanceBindsOriginalStartResource(t *testing.T) {
	for _, mutation := range []string{"start resource", "missing create", "missing create result", "create unavailable", "create result resource", "create result permission", "create parent", "create revision", "start result permission", "start result clock"} {
		t.Run(mutation, func(t *testing.T) {
			s, action, projection := projectionFixture(t)
			if _, err := s.ProjectManagedRows(t.Context(), projection); err != nil {
				t.Fatal(err)
			}

			steps := []struct {
				kind    managed.ProviderStepKind
				service string
			}{
				{managed.StepCreateNetwork, ""}, {managed.StepCreateContainer, "api"}, {managed.StepCreateContainer, "sidecar"}, {managed.StepStartContainer, "api"},
			}

			var start managed.ProviderStepAuthorization

			for _, step := range steps {
				q := managed.ProviderStepAuthorization{Command: projectionCommand(t, s, action.Key), Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: action.Key, ActionID: action.ID}, ProviderOperationID: action.NewTarget.ProvisionOperationID, Kind: step.kind, Service: step.service}}

				grant, err := s.AuthorizeManagedProviderStep(t.Context(), q)
				if err != nil || !grant.NewlyIssued {
					t.Fatal("fixture permission", err)
				}

				resource := step.service + "-original"
				if step.service == "" {
					resource = "network-original"
				}

				if err = s.RecordManagedProviderStepResult(t.Context(), managed.ProviderStepResult{Permission: grant.Permission, Outcome: managed.PortTerminal, ResourceID: resource, Evidence: []byte("original create response"), ObservedAt: grant.Permission.IssuedAt}); err != nil {
					t.Fatal(err)
				}

				if step.kind == managed.StepStartContainer {
					start = q
				}
			}

			accepted, err := s.ReadManagedProviderStepAcceptance(t.Context(), start)
			if err != nil || accepted.Permission.ResourceID != "api-original" {
				t.Fatal("positive start acceptance", err)
			}

			s.mu.Lock()
			changed := false

			for key, raw := range s.managedControllerRecords["step:"+action.TenantID] {
				var record managed.ProviderStepRecord
				if err = json.Unmarshal(raw, &record); err != nil {
					s.mu.Unlock()
					t.Fatal(err)
				}

				kind := managed.StepCreateContainer
				if strings.HasPrefix(mutation, "start ") {
					kind = managed.StepStartContainer
				}

				if record.Permission.Step.Kind != kind || record.Permission.Step.Service != "api" {
					continue
				}

				switch mutation {
				case "start resource":
					record.Permission.ResourceID = "another-container"
					record.Result.Permission = record.Permission
					record.Result.ResourceID = "another-container"
				case "missing create":
					delete(s.managedControllerRecords["step:"+action.TenantID], key)

					changed = true

					continue
				case "missing create result":
					record.Result = nil
				case "create unavailable":
					record.Result.Outcome = managed.PortUnavailable
				case "create result resource":
					record.Result.ResourceID = "another-container"
				case "create result permission", "start result permission":
					record.Result.Permission.CommandID = id.New(managed.PrefixOperation)
				case "create parent":
					record.Permission.Step.ActionID = id.New(managed.PrefixOperation)
					record.Result.Permission = record.Permission
				case "create revision":
					record.Permission.Revision = accepted.Permission.Revision
					record.Result.Permission = record.Permission
				case "start result clock":
					record.Result.ObservedAt = record.Permission.IssuedAt.Add(-1)
				}

				if mutation == "start resource" {
					if _, err = managed.ProviderStepWire(record).Value(); err != nil {
						s.mu.Unlock()
						t.Fatal("substitution did not reach association predicate", err)
					}
				}

				encoded, encodeErr := json.Marshal(record)
				if encodeErr != nil {
					s.mu.Unlock()
					t.Fatal(encodeErr)
				}

				s.managedControllerRecords["step:"+action.TenantID][key] = encoded
				changed = true
			}
			s.mu.Unlock()

			if !changed {
				t.Fatal("mutation missed original record")
			}

			before := projectionSnapshot(t, s)
			if got, readErr := s.ReadManagedProviderStepAcceptance(t.Context(), start); readErr == nil || got != (managed.ProviderStepAcceptance{}) {
				t.Fatal("rewritten association accepted", got, readErr)
			}

			if before != projectionSnapshot(t, s) {
				t.Fatal("refused read mutated retained history")
			}
		})
	}
}
