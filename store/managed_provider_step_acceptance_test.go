package store_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/xraph/ctrlplane/managed"
)

func TestManagedProviderStepAcceptanceProvisionHistory(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s := controllerStore(t, backend)

			reader, ok := s.(managed.ProviderStepAcceptanceStore)
			if !ok {
				t.Fatal("missing additive acceptance reader")
			}

			action, f, projection := acceptedController(t, s)
			if _, err := s.ProjectManagedRows(t.Context(), projection); err != nil {
				t.Fatal(err)
			}

			steps := []struct {
				kind    managed.ProviderStepKind
				service string
			}{
				{managed.StepCreateNetwork, ""}, {managed.StepCreateContainer, "api"}, {managed.StepCreateContainer, "sidecar"}, {managed.StepStartContainer, "api"}, {managed.StepStartContainer, "sidecar"},
			}
			requests := make([]managed.ProviderStepAuthorization, 0, len(steps))

			accepted := make([]managed.ProviderStepAcceptance, 0, len(steps))
			for _, step := range steps {
				q := managed.ProviderStepAuthorization{Command: f.command(), Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: action.Key, ActionID: action.ID}, ProviderOperationID: action.NewTarget.ProvisionOperationID, Kind: step.kind, Service: step.service}}

				grant, err := s.AuthorizeManagedProviderStep(t.Context(), q)
				if err != nil || !grant.NewlyIssued {
					t.Fatal("fresh permission", err)
				}

				got, err := reader.ReadManagedProviderStepAcceptance(t.Context(), q)
				if err != nil || got.Permission != grant.Permission {
					t.Fatal("original permission changed", err)
				}

				kind := "provider_step"

				var input any = q
				if step.kind == managed.StepCreateNetwork {
					kind, input = "issue_provision", q.Command
				}

				raw, err := json.Marshal(struct {
					Kind  string `json:"kind"`
					Input any    `json:"input"`
				}{kind, input})
				if err != nil {
					t.Fatal(err)
				}

				if got.Receipt.ID != q.ID || got.Receipt.Digest != managed.Digest(raw) || got.Receipt.Digest == got.Permission.Digest || got.Receipt.Revision != q.Revision+1 || got.Receipt.LeaseEpoch != q.LeaseEpoch {
					t.Fatal("independent receipt lost", got)
				}

				requests = append(requests, q)
				accepted = append(accepted, got)

				resource := step.service + "-resource"
				if step.service == "" {
					resource = "network-resource"
				}

				if err = s.RecordManagedProviderStepResult(t.Context(), managed.ProviderStepResult{Permission: grant.Permission, Outcome: managed.PortTerminal, ResourceID: resource, Evidence: []byte("accepted provider response"), ObservedAt: controllerClock(t, s)}); err != nil {
					t.Fatal(err)
				}
			}
			// A real lease command advances the live revision/epoch; history stays exact.
			old := f.target()
			if _, err := s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: f.command(), Duration: managed.MaxLease}); err != nil {
				t.Fatal(err)
			}

			before := f.target()
			if before.Revision == old.Revision || before.LeaseEpoch == old.LeaseEpoch {
				t.Fatal("fixture did not change authority")
			}

			for i, q := range requests {
				got, err := reader.ReadManagedProviderStepAcceptance(t.Context(), q)
				if err != nil || got != accepted[i] {
					t.Fatal("historical acceptance changed", err)
				}

				replay, err := s.AuthorizeManagedProviderStep(t.Context(), q)
				if err != nil || replay.NewlyIssued || replay.Permission != got.Permission {
					t.Fatal("history became new send authority", err)
				}
			}

			if !reflect.DeepEqual(before, f.target()) {
				t.Fatal("historical read changed parent")
			}
		})
	}
}
