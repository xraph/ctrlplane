package instance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// asyncProvider accepts Provision immediately and reports the workload
// ready only after a few status polls, like Cloud Run or ECS.
type asyncProvider struct {
	*delProvider

	statuses []provider.InstanceStatus
	polls    int
}

func (p *asyncProvider) ReadinessPolicy() provider.ReadinessPolicy {
	return provider.ReadinessPolicy{Timeout: time.Second, Interval: time.Millisecond}
}

func (p *asyncProvider) Status(context.Context, id.ID) (*provider.InstanceStatus, error) {
	st := p.statuses[min(p.polls, len(p.statuses)-1)]
	p.polls++

	return &st, nil
}

func TestCreate_WaitsForAsyncProvider(t *testing.T) {
	t.Parallel()

	url := provider.Endpoint{URL: "https://api-xyz.run.app", Port: 443, Protocol: "https", Public: true}

	tests := []struct {
		name      string
		statuses  []provider.InstanceStatus
		wantErr   bool
		wantState provider.InstanceState
	}{
		{
			name: "becomes ready",
			statuses: []provider.InstanceStatus{
				{State: provider.StateStarting},
				{State: provider.StateStarting},
				{State: provider.StateRunning, Ready: true, Endpoints: []provider.Endpoint{url}},
			},
			wantState: provider.StateRunning,
		},
		{
			name: "fails to start",
			statuses: []provider.InstanceStatus{
				{State: provider.StateStarting},
				{State: provider.StateFailed, Message: "container exited with code 1"},
			},
			wantErr:   true,
			wantState: provider.StateFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newDelStore()
			prov := &asyncProvider{delProvider: newDelProvider("cloudrun"), statuses: tt.statuses}
			registry := provider.NewRegistry()
			registry.Register("cloudrun", prov)

			svc := NewService(store, registry, event.NewInMemoryBus(), &guardAuthorizer{}, nil)

			inst, err := svc.Create(srcCtx(), CreateRequest{
				Name:         "api",
				ProviderName: "cloudrun",
				Services:     []provider.ServiceSpec{{Name: "api", Image: "api:1", Role: provider.RoleMain}},
			})

			if prov.polls != len(tt.statuses) {
				t.Fatalf("status polled %d times, want %d", prov.polls, len(tt.statuses))
			}

			var stored *Instance
			for _, row := range store.workloads {
				stored = row
			}

			if stored == nil || stored.State != tt.wantState {
				t.Fatalf("stored instance = %+v, want state %s", stored, tt.wantState)
			}

			if tt.wantErr {
				if !errors.Is(err, provider.ErrNotReady) {
					t.Fatalf("Create error = %v, want ErrNotReady", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("Create: %v", err)
			}

			if len(inst.Endpoints) != 1 || inst.Endpoints[0].URL != url.URL {
				t.Fatalf("Endpoints = %+v, want the URL the status reported", inst.Endpoints)
			}
		})
	}
}
