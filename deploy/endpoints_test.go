package deploy_test

import (
	"context"
	"reflect"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/deploy/strategies"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/store/memory"
)

// endpointProvider answers every Deploy with the endpoints in next, nil
// included. Only Info and Deploy are implemented.
type endpointProvider struct {
	provider.Provider

	next []provider.Endpoint
}

func (p *endpointProvider) Info() provider.ProviderInfo { return provider.ProviderInfo{Name: "eps"} }

func (p *endpointProvider) Deploy(context.Context, provider.DeployRequest) (*provider.DeployResult, error) {
	return &provider.DeployResult{Status: "deployed", Endpoints: p.next}, nil
}

var provisionedEndpoints = []provider.Endpoint{{ServiceName: "main", URL: "http://main:8080", Port: 8080, Protocol: "http"}}

// endpointFixture stores one running instance with provisionedEndpoints
// and returns a deploy service backed by prov, with every built-in
// strategy registered.
func endpointFixture(t *testing.T, prov *endpointProvider) (deploy.Service, *memory.Store, *instance.Instance) {
	t.Helper()

	store := memory.New()
	registry := provider.NewRegistry()
	registry.Register("eps", prov)

	inst := &instance.Instance{
		Entity:       ctrlplane.NewEntity(id.PrefixInstance),
		TenantID:     "ten_test",
		Name:         "api-1",
		ProviderName: "eps",
		Kind:         provider.KindDeployment,
		State:        provider.StateRunning,
		Services:     []provider.ServiceSpec{{Name: "main", Image: "api:1", Role: provider.RoleMain}},
		Endpoints:    provisionedEndpoints,
	}
	if err := store.Insert(adminCtxDeploy(), inst); err != nil {
		t.Fatalf("insert instance: %v", err)
	}

	svc := deploy.NewService(store, store, registry, event.NewInMemoryBus(), &auth.NoopProvider{}, nil)
	svc.RegisterStrategy(strategies.NewRolling())
	svc.RegisterStrategy(strategies.NewRecreate())
	svc.RegisterStrategy(strategies.NewCanary())
	svc.RegisterStrategy(strategies.NewBlueGreen())

	return svc, store, inst
}

func storedEndpoints(t *testing.T, store *memory.Store, inst *instance.Instance) []provider.Endpoint {
	t.Helper()

	got, err := store.GetByID(adminCtxDeploy(), inst.TenantID, inst.ID)
	if err != nil {
		t.Fatalf("get instance: %v", err)
	}

	return got.Endpoints
}

// TestDeploy_StoresReturnedEndpoints checks that endpoints a provider
// returns from Deploy replace the instance's stored ones, under every
// built-in strategy.
func TestDeploy_StoresReturnedEndpoints(t *testing.T) {
	t.Parallel()

	moved := []provider.Endpoint{
		{ServiceName: "main", URL: "http://main:9090", Port: 9090, Protocol: "http"},
		{ServiceName: "main", URL: "http://localhost:32001", Port: 32001, Protocol: "http", Public: true},
	}

	for _, strategy := range []string{"rolling", "recreate", "canary", "blue-green"} {
		t.Run(strategy, func(t *testing.T) {
			t.Parallel()

			svc, store, inst := endpointFixture(t, &endpointProvider{next: moved})

			_, err := svc.Deploy(adminCtxDeploy(), deploy.DeployRequest{
				InstanceID: inst.ID,
				Strategy:   strategy,
				Services:   []provider.ServiceDeploySpec{{Name: "main", Image: "api:2", Ports: []provider.PortSpec{{Container: 9090}}}},
			})
			if err != nil {
				t.Fatalf("Deploy: %v", err)
			}

			if got := storedEndpoints(t, store, inst); !reflect.DeepEqual(got, moved) {
				t.Fatalf("endpoints = %+v, want %+v", got, moved)
			}
		})
	}
}

// TestDeploy_NilEndpointsKeepStored checks that a provider returning no
// endpoints leaves the instance's stored endpoints alone.
func TestDeploy_NilEndpointsKeepStored(t *testing.T) {
	t.Parallel()

	svc, store, inst := endpointFixture(t, &endpointProvider{})

	if _, err := svc.Deploy(adminCtxDeploy(), deploy.DeployRequest{
		InstanceID: inst.ID,
		Services:   []provider.ServiceDeploySpec{{Name: "main", Image: "api:2"}},
	}); err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	if got := storedEndpoints(t, store, inst); !reflect.DeepEqual(got, provisionedEndpoints) {
		t.Fatalf("endpoints = %+v, want the provisioned ones %+v", got, provisionedEndpoints)
	}
}

// TestRollback_StoresReturnedEndpoints checks that a rollback, which
// runs through the same strategy path, stores returned endpoints too,
// and that a nil result there also keeps them.
func TestRollback_StoresReturnedEndpoints(t *testing.T) {
	t.Parallel()

	prov := &endpointProvider{}
	svc, store, inst := endpointFixture(t, prov)
	ctx := adminCtxDeploy()

	v1, err := svc.RecordInitial(ctx, inst.ID)
	if err != nil {
		t.Fatalf("RecordInitial: %v", err)
	}

	if _, err := svc.Deploy(ctx, deploy.DeployRequest{
		InstanceID: inst.ID,
		Services:   []provider.ServiceDeploySpec{{Name: "main", Image: "api:2"}},
	}); err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	if _, err := svc.Rollback(ctx, inst.ID, v1.ID); err != nil {
		t.Fatalf("Rollback (nil endpoints): %v", err)
	}

	if got := storedEndpoints(t, store, inst); !reflect.DeepEqual(got, provisionedEndpoints) {
		t.Fatalf("after nil rollback endpoints = %+v, want %+v", got, provisionedEndpoints)
	}

	restored := []provider.Endpoint{{ServiceName: "main", URL: "http://main:7070", Port: 7070, Protocol: "http"}}
	prov.next = restored

	if _, err := svc.Rollback(ctx, inst.ID, v1.ID); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	if got := storedEndpoints(t, store, inst); !reflect.DeepEqual(got, restored) {
		t.Fatalf("after rollback endpoints = %+v, want %+v", got, restored)
	}
}
