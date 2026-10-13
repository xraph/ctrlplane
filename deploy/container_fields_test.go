package deploy_test

import (
	"context"
	"slices"
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

// deployRecorder keeps every DeployRequest it receives. Only Info and
// Deploy are implemented; the embedded nil interface panics on anything
// else.
type deployRecorder struct {
	provider.Provider

	deploys []provider.DeployRequest
}

func (p *deployRecorder) Info() provider.ProviderInfo { return provider.ProviderInfo{Name: "rec"} }

func (p *deployRecorder) Deploy(_ context.Context, req provider.DeployRequest) (*provider.DeployResult, error) {
	p.deploys = append(p.deploys, req)

	return &provider.DeployResult{Status: "deployed"}, nil
}

// TestDeploy_CarriesContainerFields deploys a new image with a new size,
// then rolls back, and checks command, args, ports and resources reach the
// provider and survive in the Release snapshots.
func TestDeploy_CarriesContainerFields(t *testing.T) {
	t.Parallel()

	ctx := adminCtxDeploy()
	store := memory.New()
	rec := &deployRecorder{}

	registry := provider.NewRegistry()
	registry.Register("rec", rec)

	inst := &instance.Instance{
		Entity:       ctrlplane.NewEntity(id.PrefixInstance),
		TenantID:     "ten_test",
		Name:         "api-1",
		ProviderName: "rec",
		Kind:         provider.KindDeployment,
		State:        provider.StateRunning,
		Services: []provider.ServiceSpec{{
			Name:      "main",
			Image:     "api:1",
			Role:      provider.RoleMain,
			Command:   []string{"/bin/api"},
			Args:      []string{"serve"},
			Ports:     []provider.PortSpec{{Container: 8080, Protocol: "tcp"}},
			Resources: provider.ResourceSpec{CPUMillis: 500, MemoryMB: 512, Replicas: 1},
		}},
	}
	if err := store.Insert(ctx, inst); err != nil {
		t.Fatalf("insert instance: %v", err)
	}

	svc := deploy.NewService(store, store, registry, event.NewInMemoryBus(), &auth.NoopProvider{}, nil)
	svc.RegisterStrategy(strategies.NewRolling())
	svc.RegisterStrategy(strategies.NewRecreate())

	v1, err := svc.RecordInitial(ctx, inst.ID)
	if err != nil {
		t.Fatalf("RecordInitial: %v", err)
	}

	v1Main := findSnapshot(v1.Services, "main")
	if v1Main == nil || v1Main.Resources == nil || v1Main.Resources.CPUMillis != 500 || !slices.Equal(v1Main.Command, []string{"/bin/api"}) {
		t.Fatalf("v1 snapshot = %+v, want command and resources recorded", v1Main)
	}

	// Change the image and the size only.
	bigger := &provider.ResourceSpec{CPUMillis: 1000, MemoryMB: 1024, Replicas: 1}

	dep, err := svc.Deploy(ctx, deploy.DeployRequest{
		InstanceID: inst.ID,
		Services:   []provider.ServiceDeploySpec{{Name: "main", Image: "api:2", Resources: bigger}},
	})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	sent := rec.deploys[0].Services[0]
	if sent.Resources == nil || sent.Resources.CPUMillis != 1000 || sent.Resources.MemoryMB != 1024 {
		t.Fatalf("provider got resources %+v, want 1000m / 1024MB", sent.Resources)
	}

	v2, err := svc.GetRelease(ctx, dep.ReleaseID)
	if err != nil {
		t.Fatalf("GetRelease: %v", err)
	}

	v2Main := findSnapshot(v2.Services, "main")
	if v2Main == nil || v2Main.Image != "api:2" || v2Main.Resources.CPUMillis != 1000 {
		t.Fatalf("v2 snapshot = %+v, want api:2 at 1000m", v2Main)
	}

	// Fields the deploy left unset carry over from v1.
	if !slices.Equal(v2Main.Command, []string{"/bin/api"}) || !slices.Equal(v2Main.Args, []string{"serve"}) ||
		len(v2Main.Ports) != 1 || v2Main.Ports[0].Container != 8080 {
		t.Fatalf("v2 snapshot = %+v, want command, args and port inherited from v1", v2Main)
	}

	if _, err := svc.Rollback(ctx, inst.ID, v1.ID); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	restored := rec.deploys[len(rec.deploys)-1].Services[0]
	if restored.Image != "api:1" || restored.Resources == nil || restored.Resources.CPUMillis != 500 ||
		!slices.Equal(restored.Command, []string{"/bin/api"}) || len(restored.Ports) != 1 {
		t.Fatalf("rollback sent %+v, want v1 image, command, port and 500m", restored)
	}
}
