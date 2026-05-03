package instance

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// TestDelete_AlreadyDestroyed_SkipsDeprovision asserts that an
// instance already in StateDestroyed has its row removed without
// re-calling Deprovision — the resources are gone, the row is
// vestigial. Re-calling Deprovision against a half-destroyed
// instance can race with provider GC and produce spurious failures.
func TestDelete_AlreadyDestroyed_SkipsDeprovision(t *testing.T) {
	t.Parallel()

	store := newDelStore()
	prov := newDelProvider("kubernetes")
	registry := provider.NewRegistry()
	registry.Register(prov.info.Name, prov)

	inst := &Instance{
		Entity:       ctrlplane.NewEntity(id.PrefixInstance),
		TenantID:     "ten_test",
		Name:         "stale",
		ProviderName: prov.info.Name,
		State:        provider.StateDestroyed,
		Services:     []provider.ServiceSpec{{Name: "main", Image: "x", Role: provider.RoleMain}},
	}
	store.put(inst)

	svc := NewService(store, registry, event.NewInMemoryBus(), nil, nil)

	if err := svc.Delete(adminCtx(), inst.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if got := prov.deprovisions.Load(); got != 0 {
		t.Fatalf("Deprovision calls: want 0 (skipped for StateDestroyed), got %d", got)
	}

	if _, ok := store.workloads[inst.ID.String()]; ok {
		t.Fatal("instance row should have been removed")
	}
}

// TestDelete_ProviderResourcesAlreadyGone_Succeeds asserts that a
// provider that returns nil from Deprovision (because the underlying
// container/pod is already gone) lets Delete complete and remove the
// row. Mirrors the kubernetes Deprovision behaviour where 404 on
// both Deployment and StatefulSet collapses to nil.
func TestDelete_ProviderResourcesAlreadyGone_Succeeds(t *testing.T) {
	t.Parallel()

	store := newDelStore()
	prov := newDelProvider("kubernetes") // default: Deprovision returns nil
	registry := provider.NewRegistry()
	registry.Register(prov.info.Name, prov)

	inst := &Instance{
		Entity:       ctrlplane.NewEntity(id.PrefixInstance),
		TenantID:     "ten_test",
		Name:         "ghost",
		ProviderName: prov.info.Name,
		State:        provider.StateRunning,
		Services:     []provider.ServiceSpec{{Name: "main", Image: "x", Role: provider.RoleMain}},
	}
	store.put(inst)

	svc := NewService(store, registry, event.NewInMemoryBus(), nil, nil)

	if err := svc.Delete(adminCtx(), inst.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if got := prov.deprovisions.Load(); got != 1 {
		t.Fatalf("Deprovision calls: want 1, got %d", got)
	}

	if _, ok := store.workloads[inst.ID.String()]; ok {
		t.Fatal("instance row should have been removed")
	}
}

// TestDelete_MidProvision_Succeeds asserts that an instance stuck
// mid-Provision (StateStarting or StateProvisioning) is still
// deletable. Older code routed Delete through ValidateTransition
// which rejected several legitimate states and left orphan rows
// after a half-failed Provision.
func TestDelete_MidProvision_Succeeds(t *testing.T) {
	t.Parallel()

	for _, state := range []provider.InstanceState{
		provider.StateProvisioning,
		provider.StateStarting,
		provider.StateFailed,
	} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			store := newDelStore()
			prov := newDelProvider("kubernetes")
			registry := provider.NewRegistry()
			registry.Register(prov.info.Name, prov)

			inst := &Instance{
				Entity:       ctrlplane.NewEntity(id.PrefixInstance),
				TenantID:     "ten_test",
				Name:         "mid-" + string(state),
				ProviderName: prov.info.Name,
				State:        state,
				Services:     []provider.ServiceSpec{{Name: "main", Image: "x", Role: provider.RoleMain}},
			}
			store.put(inst)

			svc := NewService(store, registry, event.NewInMemoryBus(), nil, nil)

			if err := svc.Delete(adminCtx(), inst.ID); err != nil {
				t.Fatalf("Delete from %s: %v", state, err)
			}

			if got := prov.deprovisions.Load(); got != 1 {
				t.Fatalf("Deprovision calls: want 1, got %d", got)
			}

			if _, ok := store.workloads[inst.ID.String()]; ok {
				t.Fatalf("instance row should have been removed (state=%s)", state)
			}
		})
	}
}

// TestDelete_ProviderDeconfigured_DropsRow asserts that when the
// configured provider is no longer registered (operator reconfig,
// renamed provider, etc.) the row is dropped anyway. Pinning a row
// to a vanished provider is worse than the operator manually
// reaping any orphan runtime resources.
func TestDelete_ProviderDeconfigured_DropsRow(t *testing.T) {
	t.Parallel()

	store := newDelStore()
	registry := provider.NewRegistry() // empty — no providers registered

	inst := &Instance{
		Entity:       ctrlplane.NewEntity(id.PrefixInstance),
		TenantID:     "ten_test",
		Name:         "orphan",
		ProviderName: "vanished",
		State:        provider.StateRunning,
		Services:     []provider.ServiceSpec{{Name: "main", Image: "x", Role: provider.RoleMain}},
	}
	store.put(inst)

	svc := NewService(store, registry, event.NewInMemoryBus(), nil, nil)

	if err := svc.Delete(adminCtx(), inst.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, ok := store.workloads[inst.ID.String()]; ok {
		t.Fatal("instance row should have been removed even with missing provider")
	}
}

// TestDelete_DeprovisionRealFailure_LeavesFailedRow asserts that a
// genuine provider error (network down, auth error, etc.) leaves the
// row in StateFailed for operator retry — not silently dropped.
// This is the inverse of the "already gone" case: a real error
// must surface, not collapse to success.
func TestDelete_DeprovisionRealFailure_LeavesFailedRow(t *testing.T) {
	t.Parallel()

	store := newDelStore()
	prov := newDelProvider("kubernetes")
	prov.deprovisionErr = errors.New("api server unreachable")
	registry := provider.NewRegistry()
	registry.Register(prov.info.Name, prov)

	inst := &Instance{
		Entity:       ctrlplane.NewEntity(id.PrefixInstance),
		TenantID:     "ten_test",
		Name:         "stuck",
		ProviderName: prov.info.Name,
		State:        provider.StateRunning,
		Services:     []provider.ServiceSpec{{Name: "main", Image: "x", Role: provider.RoleMain}},
	}
	store.put(inst)

	svc := NewService(store, registry, event.NewInMemoryBus(), nil, nil)

	err := svc.Delete(adminCtx(), inst.ID)
	if err == nil {
		t.Fatal("Delete should propagate real provider error")
	}

	if !strings.Contains(err.Error(), "api server unreachable") {
		t.Fatalf("error should wrap underlying cause; got %q", err)
	}

	got, ok := store.workloads[inst.ID.String()]
	if !ok {
		t.Fatal("row should remain after Deprovision failure for retry")
	}

	if got.State != provider.StateFailed {
		t.Fatalf("State after failed Deprovision: want StateFailed, got %s", got.State)
	}
}

// --- helpers ---

func adminCtx() context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{
		SubjectID: "test-user",
		TenantID:  "ten_test",
		Roles:     []string{"system:admin"},
	})
}

// delStore is a minimal in-memory instance.Store for delete tests.
// Keyed by ID string (not multi-tenant index) since the test fixture
// only ever uses one tenant.
type delStore struct {
	workloads map[string]*Instance
}

func newDelStore() *delStore {
	return &delStore{workloads: make(map[string]*Instance)}
}

func (s *delStore) put(inst *Instance) {
	clone := *inst
	s.workloads[inst.ID.String()] = &clone
}

func (s *delStore) Insert(_ context.Context, inst *Instance) error {
	s.put(inst)

	return nil
}

func (s *delStore) GetByID(_ context.Context, _ string, instanceID id.ID) (*Instance, error) {
	w, ok := s.workloads[instanceID.String()]
	if !ok {
		return nil, ctrlplane.ErrNotFound
	}

	clone := *w

	return &clone, nil
}

func (s *delStore) GetBySlug(_ context.Context, _, _ string) (*Instance, error) {
	return nil, ctrlplane.ErrNotFound
}

func (s *delStore) List(_ context.Context, _ string, _ ListOptions) (*ListResult, error) {
	return &ListResult{}, nil
}

func (s *delStore) Update(_ context.Context, inst *Instance) error {
	s.put(inst)

	return nil
}

func (s *delStore) Delete(_ context.Context, _ string, instanceID id.ID) error {
	delete(s.workloads, instanceID.String())

	return nil
}

func (s *delStore) CountByTenant(_ context.Context, _ string) (int, error) {
	return len(s.workloads), nil
}

// delProvider is a tracking fake provider for Delete tests. It
// counts Deprovision invocations and, optionally, returns a fixed
// error from Deprovision to simulate runtime failures.
type delProvider struct {
	info           provider.ProviderInfo
	deprovisions   atomic.Int32
	deprovisionErr error
}

func newDelProvider(name string) *delProvider {
	return &delProvider{info: provider.ProviderInfo{Name: name, Version: "test"}}
}

func (p *delProvider) Info() provider.ProviderInfo         { return p.info }
func (p *delProvider) Capabilities() []provider.Capability { return nil }

func (p *delProvider) Provision(context.Context, provider.ProvisionRequest) (*provider.ProvisionResult, error) {
	return &provider.ProvisionResult{}, nil
}

func (p *delProvider) Deprovision(_ context.Context, _ id.ID) error {
	p.deprovisions.Add(1)

	return p.deprovisionErr
}

func (p *delProvider) Start(context.Context, id.ID) error   { return nil }
func (p *delProvider) Stop(context.Context, id.ID) error    { return nil }
func (p *delProvider) Restart(context.Context, id.ID) error { return nil }

func (p *delProvider) Status(context.Context, id.ID) (*provider.InstanceStatus, error) {
	return &provider.InstanceStatus{}, nil
}

func (p *delProvider) Deploy(context.Context, provider.DeployRequest) (*provider.DeployResult, error) {
	return &provider.DeployResult{}, nil
}

func (p *delProvider) Rollback(context.Context, id.ID, id.ID) error { return nil }

func (p *delProvider) Scale(context.Context, id.ID, provider.ResourceSpec) error { return nil }

func (p *delProvider) Resources(context.Context, id.ID) (*provider.ResourceUsage, error) {
	return &provider.ResourceUsage{}, nil
}

func (p *delProvider) Logs(context.Context, id.ID, provider.LogOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (p *delProvider) Exec(context.Context, id.ID, provider.ExecRequest) (*provider.ExecResult, error) {
	return &provider.ExecResult{}, nil
}
