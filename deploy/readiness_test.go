package deploy_test

import (
	"context"
	"errors"
	"testing"
	"time"

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

// asyncDeployer accepts Deploy at once and then reports the status it is
// given, so the test controls whether the rollout ever becomes ready.
type asyncDeployer struct {
	deployRecorder

	status provider.InstanceStatus
	polls  int
}

func (p *asyncDeployer) ReadinessPolicy() provider.ReadinessPolicy {
	return provider.ReadinessPolicy{Timeout: 50 * time.Millisecond, Interval: time.Millisecond}
}

func (p *asyncDeployer) Status(context.Context, id.ID) (*provider.InstanceStatus, error) {
	p.polls++
	st := p.status

	return &st, nil
}

func TestDeploy_WaitsForAsyncProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		status   provider.InstanceStatus
		wantErr  bool
		wantDep  deploy.DeployState
		wantProg string
	}{
		{
			name:     "ready",
			status:   provider.InstanceStatus{State: provider.StateRunning, Ready: true},
			wantDep:  deploy.DeploySucceeded,
			wantProg: deploy.ServiceStateSucceeded,
		},
		{
			name:     "new revision never becomes ready",
			status:   provider.InstanceStatus{State: provider.StateStarting},
			wantErr:  true,
			wantDep:  deploy.DeployFailed,
			wantProg: deploy.ServiceStateFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := adminCtxDeploy()
			store := memory.New()
			prov := &asyncDeployer{status: tt.status}

			registry := provider.NewRegistry()
			registry.Register("ecs", prov)

			inst := &instance.Instance{
				Entity:       ctrlplane.NewEntity(id.PrefixInstance),
				TenantID:     "ten_test",
				Name:         "api-1",
				ProviderName: "ecs",
				Kind:         provider.KindDeployment,
				State:        provider.StateRunning,
				Services:     []provider.ServiceSpec{{Name: "main", Image: "api:1", Role: provider.RoleMain}},
			}
			if err := store.Insert(ctx, inst); err != nil {
				t.Fatalf("insert instance: %v", err)
			}

			svc := deploy.NewService(store, store, registry, event.NewInMemoryBus(), &auth.NoopProvider{}, nil)
			svc.RegisterStrategy(strategies.NewRolling())

			dep, err := svc.Deploy(ctx, deploy.DeployRequest{
				InstanceID: inst.ID,
				Services:   []provider.ServiceDeploySpec{{Name: "main", Image: "api:2"}},
			})

			if prov.polls == 0 {
				t.Fatal("status was never polled after Deploy returned")
			}

			if tt.wantErr && !errors.Is(err, ctrlplane.ErrDeploymentFailed) {
				t.Fatalf("Deploy error = %v, want ErrDeploymentFailed", err)
			}

			if !tt.wantErr && err != nil {
				t.Fatalf("Deploy: %v", err)
			}

			if dep.State != tt.wantDep || dep.ServiceProgress["main"] != tt.wantProg {
				t.Fatalf("deployment state %s, progress %v; want %s and %s", dep.State, dep.ServiceProgress, tt.wantDep, tt.wantProg)
			}
		})
	}
}
