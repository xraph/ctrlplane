package bootstrap_test

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/xraph/ctrlplane/bootstrap"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/store/memory"
)

// TestReconcile_DeclarativeOnly is the headline test: a datacenter
// with declarative BootstrapServices and no hooks → reconcile inserts
// + provisions the rows, ending in StateRunning with ProviderRef set.
func TestReconcile_DeclarativeOnly(t *testing.T) {
	t.Parallel()

	store := memory.New()
	prov := newFakeProvider("k8s")
	registry := provider.NewRegistry()
	registry.Register(prov.name, prov)

	hooks := bootstrap.NewRegistry()
	svc := bootstrap.NewService(store, registry, hooks, event.NewInMemoryBus())

	dc := makeDatacenterInfo(prov.name)
	declared := []bootstrap.BootstrapServiceSpec{
		{
			Name: "fluent-bit",
			Services: []provider.ServiceSpec{
				{Name: "main", Image: "fluent/fluent-bit:2.0", Role: provider.RoleMain},
			},
		},
		{
			Name: "node-exporter",
			Services: []provider.ServiceSpec{
				{Name: "main", Image: "prom/node-exporter:1.6", Role: provider.RoleMain},
			},
		},
	}

	if err := svc.Reconcile(context.Background(), dc, declared); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	rows, err := svc.ListByDatacenter(context.Background(), dc.ID)
	if err != nil {
		t.Fatalf("ListByDatacenter: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("rows: want 2, got %d", len(rows))
	}

	for _, bw := range rows {
		if bw.State != bootstrap.StateRunning {
			t.Fatalf("%s state: want running, got %s (err=%q)", bw.Name, bw.State, bw.LastError)
		}

		if bw.ProviderRef == "" {
			t.Fatalf("%s ProviderRef: want non-empty after Provision", bw.Name)
		}
	}

	if got := prov.provisions.Load(); got != 2 {
		t.Fatalf("Provision calls: want 2, got %d", got)
	}
}

// TestReconcile_HookOnly covers the programmatic-only path: no
// declarative services, but a registered hook contributes one. The
// reconciler reaches the same end state as the declarative case.
func TestReconcile_HookOnly(t *testing.T) {
	t.Parallel()

	store := memory.New()
	prov := newFakeProvider("k8s")
	registry := provider.NewRegistry()
	registry.Register(prov.name, prov)

	hooks := bootstrap.NewRegistry()
	hooks.Register(&fakeHook{
		name: "cert-manager-installer",
		specs: []bootstrap.BootstrapServiceSpec{
			{
				Name: "cert-manager",
				Services: []provider.ServiceSpec{
					{Name: "main", Image: "quay.io/jetstack/cert-manager:1.13", Role: provider.RoleMain},
				},
			},
		},
	})

	svc := bootstrap.NewService(store, registry, hooks, event.NewInMemoryBus())
	dc := makeDatacenterInfo(prov.name)

	if err := svc.Reconcile(context.Background(), dc, nil); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	rows, _ := svc.ListByDatacenter(context.Background(), dc.ID)
	if len(rows) != 1 {
		t.Fatalf("rows: want 1 (cert-manager), got %d", len(rows))
	}

	if rows[0].Name != "cert-manager" || rows[0].State != bootstrap.StateRunning {
		t.Fatalf("cert-manager row: name=%q state=%q", rows[0].Name, rows[0].State)
	}
}

// TestReconcile_DeclarativeAndHookMerge covers the union path:
// declarative declares one service, a hook adds another. Both end up
// running, deduped by Name.
func TestReconcile_DeclarativeAndHookMerge(t *testing.T) {
	t.Parallel()

	store := memory.New()
	prov := newFakeProvider("k8s")
	registry := provider.NewRegistry()
	registry.Register(prov.name, prov)

	hooks := bootstrap.NewRegistry()
	hooks.Register(&fakeHook{
		name: "cert-manager-installer",
		specs: []bootstrap.BootstrapServiceSpec{
			{Name: "cert-manager", Services: []provider.ServiceSpec{{Name: "main", Image: "cm:1", Role: provider.RoleMain}}},
		},
	})

	svc := bootstrap.NewService(store, registry, hooks, event.NewInMemoryBus())
	dc := makeDatacenterInfo(prov.name)
	declared := []bootstrap.BootstrapServiceSpec{
		{Name: "fluent-bit", Services: []provider.ServiceSpec{{Name: "main", Image: "fb:1", Role: provider.RoleMain}}},
	}

	if err := svc.Reconcile(context.Background(), dc, declared); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	rows, _ := svc.ListByDatacenter(context.Background(), dc.ID)
	names := namesOf(rows)

	if len(names) != 2 || !contains(names, "fluent-bit") || !contains(names, "cert-manager") {
		t.Fatalf("expected union {fluent-bit, cert-manager}, got %v", names)
	}
}

// TestReconcile_DeclaredWinsOnNameConflict covers the precedence
// rule: when a hook contributes a Name that collides with a
// declarative entry, the operator's declarative spec wins. Tests by
// installing different images under the same Name; the row's stored
// image is the declarative one.
func TestReconcile_DeclaredWinsOnNameConflict(t *testing.T) {
	t.Parallel()

	store := memory.New()
	prov := newFakeProvider("k8s")
	registry := provider.NewRegistry()
	registry.Register(prov.name, prov)

	hooks := bootstrap.NewRegistry()
	hooks.Register(&fakeHook{
		name: "default-cert-manager",
		specs: []bootstrap.BootstrapServiceSpec{
			{Name: "cert-manager", Services: []provider.ServiceSpec{{Name: "main", Image: "cm:hook-default", Role: provider.RoleMain}}},
		},
	})

	svc := bootstrap.NewService(store, registry, hooks, event.NewInMemoryBus())
	dc := makeDatacenterInfo(prov.name)
	declared := []bootstrap.BootstrapServiceSpec{
		// Operator overrides the hook's default image.
		{Name: "cert-manager", Services: []provider.ServiceSpec{{Name: "main", Image: "cm:operator-pinned", Role: provider.RoleMain}}},
	}

	if err := svc.Reconcile(context.Background(), dc, declared); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	rows, _ := svc.ListByDatacenter(context.Background(), dc.ID)
	if len(rows) != 1 {
		t.Fatalf("rows: want 1 (deduped by Name), got %d", len(rows))
	}

	gotImage := rows[0].Services[0].Image
	if gotImage != "cm:operator-pinned" {
		t.Fatalf("declarative should win on Name conflict: got %q (want cm:operator-pinned)", gotImage)
	}
}

// TestReconcile_Idempotent asserts re-running with no spec change
// produces no extra Provision calls. Critical for the every-tick
// reconciler shape — without this, every tick would re-provision
// every row indefinitely.
func TestReconcile_Idempotent(t *testing.T) {
	t.Parallel()

	store := memory.New()
	prov := newFakeProvider("k8s")
	registry := provider.NewRegistry()
	registry.Register(prov.name, prov)

	svc := bootstrap.NewService(store, registry, bootstrap.NewRegistry(), event.NewInMemoryBus())
	dc := makeDatacenterInfo(prov.name)
	declared := []bootstrap.BootstrapServiceSpec{
		{Name: "fluent-bit", Services: []provider.ServiceSpec{{Name: "main", Image: "fb:1", Role: provider.RoleMain}}},
	}

	for i := range 3 {
		if err := svc.Reconcile(context.Background(), dc, declared); err != nil {
			t.Fatalf("Reconcile #%d: %v", i, err)
		}
	}

	if got := prov.provisions.Load(); got != 1 {
		t.Fatalf("Provision calls across 3 ticks: want 1, got %d", got)
	}
}

// TestReconcile_HookSelfFilters asserts a hook that returns nil
// for a non-matching datacenter (e.g. a k8s-only hook against a
// docker dc) contributes nothing. The hook receives DatacenterInfo
// and decides; the reconciler does not pre-filter.
func TestReconcile_HookSelfFilters(t *testing.T) {
	t.Parallel()

	store := memory.New()
	prov := newFakeProvider("docker")
	registry := provider.NewRegistry()
	registry.Register(prov.name, prov)

	hooks := bootstrap.NewRegistry()
	hooks.Register(&fakeHook{
		name: "k8s-only-cert-manager",
		filter: func(dc bootstrap.DatacenterInfo) []bootstrap.BootstrapServiceSpec {
			if dc.ProviderName != "k8s" {
				return nil
			}

			return []bootstrap.BootstrapServiceSpec{
				{Name: "cert-manager", Services: []provider.ServiceSpec{{Name: "main", Image: "cm:1", Role: provider.RoleMain}}},
			}
		},
	})

	svc := bootstrap.NewService(store, registry, hooks, event.NewInMemoryBus())
	dc := makeDatacenterInfo("docker")

	if err := svc.Reconcile(context.Background(), dc, nil); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	rows, _ := svc.ListByDatacenter(context.Background(), dc.ID)
	if len(rows) != 0 {
		t.Fatalf("rows: want 0 (hook self-filtered out), got %d", len(rows))
	}
}

// TestReconcile_Retire asserts that removing a service from the
// declared set on a subsequent reconcile triggers the retire path —
// Deprovision is called and the row goes away.
func TestReconcile_Retire(t *testing.T) {
	t.Parallel()

	store := memory.New()
	prov := newFakeProvider("k8s")
	registry := provider.NewRegistry()
	registry.Register(prov.name, prov)

	svc := bootstrap.NewService(store, registry, bootstrap.NewRegistry(), event.NewInMemoryBus())
	dc := makeDatacenterInfo(prov.name)

	declaredV1 := []bootstrap.BootstrapServiceSpec{
		{Name: "fluent-bit", Services: []provider.ServiceSpec{{Name: "main", Image: "fb:1", Role: provider.RoleMain}}},
		{Name: "node-exporter", Services: []provider.ServiceSpec{{Name: "main", Image: "ne:1", Role: provider.RoleMain}}},
	}
	if err := svc.Reconcile(context.Background(), dc, declaredV1); err != nil {
		t.Fatalf("Reconcile v1: %v", err)
	}

	declaredV2 := []bootstrap.BootstrapServiceSpec{
		// node-exporter dropped — should retire on next reconcile.
		{Name: "fluent-bit", Services: []provider.ServiceSpec{{Name: "main", Image: "fb:1", Role: provider.RoleMain}}},
	}
	if err := svc.Reconcile(context.Background(), dc, declaredV2); err != nil {
		t.Fatalf("Reconcile v2: %v", err)
	}

	rows, _ := svc.ListByDatacenter(context.Background(), dc.ID)
	if len(rows) != 1 {
		t.Fatalf("rows after retire: want 1, got %d", len(rows))
	}

	if rows[0].Name != "fluent-bit" {
		t.Fatalf("surviving row: want fluent-bit, got %q", rows[0].Name)
	}

	if got := prov.deprovisions.Load(); got != 1 {
		t.Fatalf("Deprovision calls: want 1 (node-exporter retired), got %d", got)
	}
}

// TestReconcile_RetryOnTransientFailure asserts that a Provision
// failure leaves the row in StateFailed with Attempts incremented,
// and the next reconcile flips it to Running once Provision starts
// succeeding. Tracks the eventually-consistent contract.
func TestReconcile_RetryOnTransientFailure(t *testing.T) {
	t.Parallel()

	store := memory.New()
	prov := newFakeProvider("k8s")
	prov.failNext.Store(1) // first Provision call errors; subsequent succeed.

	registry := provider.NewRegistry()
	registry.Register(prov.name, prov)

	svc := bootstrap.NewService(store, registry, bootstrap.NewRegistry(), event.NewInMemoryBus())
	dc := makeDatacenterInfo(prov.name)
	declared := []bootstrap.BootstrapServiceSpec{
		{Name: "fluent-bit", Services: []provider.ServiceSpec{{Name: "main", Image: "fb:1", Role: provider.RoleMain}}},
	}

	if err := svc.Reconcile(context.Background(), dc, declared); err != nil {
		t.Fatalf("Reconcile #1: %v", err)
	}

	rows, _ := svc.ListByDatacenter(context.Background(), dc.ID)
	if len(rows) != 1 || rows[0].State != bootstrap.StateFailed {
		t.Fatalf("after first tick: want 1 row in StateFailed, got %+v", rows)
	}

	if rows[0].Attempts < 1 {
		t.Fatalf("Attempts: want >=1 after failure, got %d", rows[0].Attempts)
	}

	if !strings.Contains(rows[0].LastError, "boom") {
		t.Fatalf("LastError should reflect provider error, got %q", rows[0].LastError)
	}

	if err := svc.Reconcile(context.Background(), dc, declared); err != nil {
		t.Fatalf("Reconcile #2: %v", err)
	}

	rows, _ = svc.ListByDatacenter(context.Background(), dc.ID)
	if len(rows) != 1 || rows[0].State != bootstrap.StateRunning {
		t.Fatalf("after recovery tick: want 1 row in StateRunning, got %+v", rows)
	}
}

// --- helpers ---

func makeDatacenterInfo(providerName string) bootstrap.DatacenterInfo {
	return bootstrap.DatacenterInfo{
		ID:           id.New(id.PrefixDatacenter),
		ProviderName: providerName,
		Region:       "us-east-1",
		Zone:         "a",
	}
}

func namesOf(rows []*bootstrap.BootstrapWorkload) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Name)
	}

	return out
}

func contains(haystack []string, needle string) bool {
	return slices.Contains(haystack, needle)
}

// fakeHook is a tiny test double for bootstrap.Hook. Either
// returns the static `specs` slice on every call, or — when filter
// is non-nil — delegates to filter for per-DatacenterInfo decisions.
type fakeHook struct {
	name   string
	specs  []bootstrap.BootstrapServiceSpec
	filter func(dc bootstrap.DatacenterInfo) []bootstrap.BootstrapServiceSpec
}

func (f *fakeHook) Name() string { return f.name }

func (f *fakeHook) Services(_ context.Context, dc bootstrap.DatacenterInfo) ([]bootstrap.BootstrapServiceSpec, error) {
	if f.filter != nil {
		return f.filter(dc), nil
	}

	return f.specs, nil
}

// fakeProvider is a tracking test double for provider.Provider that
// records Provision/Deprovision calls and (when failNext > 0) errors
// the next Provision before flipping back to success.
type fakeProvider struct {
	name         string
	provisions   atomic.Int32
	deprovisions atomic.Int32
	failNext     atomic.Int32
}

func newFakeProvider(name string) *fakeProvider {
	return &fakeProvider{name: name}
}

func (p *fakeProvider) Info() provider.ProviderInfo {
	return provider.ProviderInfo{Name: p.name, Version: "test"}
}

func (p *fakeProvider) Capabilities() []provider.Capability { return nil }

func (p *fakeProvider) Provision(_ context.Context, req provider.ProvisionRequest) (*provider.ProvisionResult, error) {
	p.provisions.Add(1)

	if p.failNext.Load() > 0 {
		p.failNext.Add(-1)

		return nil, errors.New("boom")
	}

	return &provider.ProvisionResult{
		ProviderRef: "ref-" + req.InstanceID.String(),
		ServiceRefs: map[string]string{},
	}, nil
}

func (p *fakeProvider) Deprovision(_ context.Context, _ id.ID) error {
	p.deprovisions.Add(1)

	return nil
}

func (p *fakeProvider) Start(context.Context, id.ID) error   { return nil }
func (p *fakeProvider) Stop(context.Context, id.ID) error    { return nil }
func (p *fakeProvider) Restart(context.Context, id.ID) error { return nil }

func (p *fakeProvider) Status(context.Context, id.ID) (*provider.InstanceStatus, error) {
	return &provider.InstanceStatus{}, nil
}

func (p *fakeProvider) Deploy(context.Context, provider.DeployRequest) (*provider.DeployResult, error) {
	return &provider.DeployResult{}, nil
}

func (p *fakeProvider) Rollback(context.Context, id.ID, id.ID) error { return nil }

func (p *fakeProvider) Scale(context.Context, id.ID, provider.ResourceSpec) error { return nil }

func (p *fakeProvider) Resources(context.Context, id.ID) (*provider.ResourceUsage, error) {
	return &provider.ResourceUsage{}, nil
}

func (p *fakeProvider) Logs(context.Context, id.ID, provider.LogOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (p *fakeProvider) Exec(context.Context, id.ID, provider.ExecRequest) (*provider.ExecResult, error) {
	return &provider.ExecResult{}, nil
}
