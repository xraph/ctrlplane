package worker

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/workload"
)

// TestGC_OrphanInstance_Reaped covers the headline case: an
// instance whose ctrlplane.workload label points at a workload row
// that no longer exists is deleted on the next tick.
func TestGC_OrphanInstance_Reaped(t *testing.T) {
	t.Parallel()

	tenants := newGCTenantStore(1)
	tid := tenants.tenantIDs()[0]

	wid := id.New(id.PrefixWorkload)
	inst := &instance.Instance{
		Entity:   ctrlplane.NewEntity(id.PrefixInstance),
		TenantID: tid,
		Labels:   map[string]string{workloadLabelKey: wid.String()},
	}
	// Backdate so the grace window doesn't protect it.
	inst.CreatedAt = time.Now().Add(-time.Hour)

	insts := newGCInstances(inst)
	wlds := newGCWorkloadStore() // empty — workload missing
	bus := event.NewInMemoryBus()

	gc := NewGarbageCollector(tenants, insts, wlds, bus, time.Minute, GCConfig{})

	if err := gc.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := insts.deletes.Load(); got != 1 {
		t.Fatalf("Delete calls: want 1, got %d", got)
	}
}

// TestGC_OrphanInstance_GraceWindowProtects asserts an instance
// younger than InstanceGracePeriod is left alone even when the
// parent workload is missing — catches mid-Provision races where
// the workload row is being inserted right after the instance.
func TestGC_OrphanInstance_GraceWindowProtects(t *testing.T) {
	t.Parallel()

	tenants := newGCTenantStore(1)
	tid := tenants.tenantIDs()[0]

	wid := id.New(id.PrefixWorkload)
	inst := &instance.Instance{
		Entity:   ctrlplane.NewEntity(id.PrefixInstance),
		TenantID: tid,
		Labels:   map[string]string{workloadLabelKey: wid.String()},
	}
	inst.CreatedAt = time.Now().Add(-1 * time.Minute) // way under default 15m

	insts := newGCInstances(inst)
	wlds := newGCWorkloadStore() // empty
	bus := event.NewInMemoryBus()

	gc := NewGarbageCollector(tenants, insts, wlds, bus, time.Minute, GCConfig{})

	if err := gc.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := insts.deletes.Load(); got != 0 {
		t.Fatalf("Delete calls: want 0 (grace window), got %d", got)
	}
}

// TestGC_NoLabel_NotReaped asserts an instance without a
// ctrlplane.workload label is treated as "not workload-owned" and
// left alone — manual / non-workload instances are out of scope.
func TestGC_NoLabel_NotReaped(t *testing.T) {
	t.Parallel()

	tenants := newGCTenantStore(1)
	tid := tenants.tenantIDs()[0]

	inst := &instance.Instance{
		Entity:   ctrlplane.NewEntity(id.PrefixInstance),
		TenantID: tid,
		// No workload label.
	}
	inst.CreatedAt = time.Now().Add(-time.Hour)

	insts := newGCInstances(inst)
	wlds := newGCWorkloadStore()
	bus := event.NewInMemoryBus()

	gc := NewGarbageCollector(tenants, insts, wlds, bus, time.Minute, GCConfig{})

	if err := gc.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := insts.deletes.Load(); got != 0 {
		t.Fatalf("Delete calls: want 0 (no workload label), got %d", got)
	}
}

// TestGC_LiveParent_NotReaped asserts an instance whose parent
// workload still exists is left alone. Sanity check for the
// happy path.
func TestGC_LiveParent_NotReaped(t *testing.T) {
	t.Parallel()

	tenants := newGCTenantStore(1)
	tid := tenants.tenantIDs()[0]

	wid := id.New(id.PrefixWorkload)
	inst := &instance.Instance{
		Entity:   ctrlplane.NewEntity(id.PrefixInstance),
		TenantID: tid,
		Labels:   map[string]string{workloadLabelKey: wid.String()},
	}
	inst.CreatedAt = time.Now().Add(-time.Hour)

	insts := newGCInstances(inst)
	wlds := newGCWorkloadStore(wid) // parent present
	bus := event.NewInMemoryBus()

	gc := NewGarbageCollector(tenants, insts, wlds, bus, time.Minute, GCConfig{})

	if err := gc.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := insts.deletes.Load(); got != 0 {
		t.Fatalf("Delete calls: want 0 (parent still exists), got %d", got)
	}
}

// TestGC_TenantErrorIsolation asserts that a List failure on one
// tenant does not blind-spot the next tenant's sweep — the GC
// emits a tenant_failed event and continues.
func TestGC_TenantErrorIsolation(t *testing.T) {
	t.Parallel()

	tenants := newGCTenantStore(2)
	ids := tenants.tenantIDs()
	brokenTID, goodTID := ids[0], ids[1]

	wid := id.New(id.PrefixWorkload)

	goodInst := &instance.Instance{
		Entity:   ctrlplane.NewEntity(id.PrefixInstance),
		TenantID: goodTID,
		Labels:   map[string]string{workloadLabelKey: wid.String()},
	}
	goodInst.CreatedAt = time.Now().Add(-time.Hour)

	insts := newGCInstances(goodInst)
	insts.tenantErr = map[string]error{brokenTID: errors.New("store offline")}
	wlds := newGCWorkloadStore() // empty — orphan
	bus := event.NewInMemoryBus()

	gc := NewGarbageCollector(tenants, insts, wlds, bus, time.Minute, GCConfig{})

	if err := gc.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := insts.deletes.Load(); got != 1 {
		t.Fatalf("Delete calls: want 1 (good tenant swept despite broken tenant), got %d", got)
	}
}

// TestGC_MalformedLabel_NotReaped asserts a corrupt label value
// (e.g. a string that doesn't parse as a TypeID with the workload
// prefix) is skipped rather than causing the sweep to error or
// reaping the row aggressively.
func TestGC_MalformedLabel_NotReaped(t *testing.T) {
	t.Parallel()

	tenants := newGCTenantStore(1)
	tid := tenants.tenantIDs()[0]

	inst := &instance.Instance{
		Entity:   ctrlplane.NewEntity(id.PrefixInstance),
		TenantID: tid,
		Labels:   map[string]string{workloadLabelKey: "not-a-real-id"},
	}
	inst.CreatedAt = time.Now().Add(-time.Hour)

	insts := newGCInstances(inst)
	wlds := newGCWorkloadStore()
	bus := event.NewInMemoryBus()

	gc := NewGarbageCollector(tenants, insts, wlds, bus, time.Minute, GCConfig{})

	if err := gc.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := insts.deletes.Load(); got != 0 {
		t.Fatalf("Delete calls: want 0 (malformed label), got %d", got)
	}
}

// TestGC_DeleteFailureNonFatal asserts that when instance.Service.Delete
// returns a real error (provider unreachable, etc.) the sweep
// continues to the next instance — the convergent Delete is
// retry-safe so the next tick will pick up the failure.
func TestGC_DeleteFailureNonFatal(t *testing.T) {
	t.Parallel()

	tenants := newGCTenantStore(1)
	tid := tenants.tenantIDs()[0]

	wid := id.New(id.PrefixWorkload)

	stuck := &instance.Instance{
		Entity:   ctrlplane.NewEntity(id.PrefixInstance),
		TenantID: tid,
		Labels:   map[string]string{workloadLabelKey: wid.String()},
	}
	stuck.CreatedAt = time.Now().Add(-time.Hour)

	healthy := &instance.Instance{
		Entity:   ctrlplane.NewEntity(id.PrefixInstance),
		TenantID: tid,
		Labels:   map[string]string{workloadLabelKey: wid.String()},
	}
	healthy.CreatedAt = time.Now().Add(-time.Hour)

	insts := newGCInstances(stuck, healthy)
	insts.deleteErrIDs = map[string]error{stuck.ID.String(): errors.New("provider down")}
	wlds := newGCWorkloadStore()
	bus := event.NewInMemoryBus()

	gc := NewGarbageCollector(tenants, insts, wlds, bus, time.Minute, GCConfig{})

	if err := gc.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Both instances had Delete attempted; one errored, one succeeded.
	if got := insts.deleteAttempts.Load(); got != 2 {
		t.Fatalf("Delete attempts: want 2 (one stuck + one healthy), got %d", got)
	}

	if got := insts.deletes.Load(); got != 1 {
		t.Fatalf("Successful deletes: want 1, got %d", got)
	}
}

// --- helpers ---

// gcTenantStore is a minimal admin.Store that returns a fixed
// list of tenants and stubs every other method. The GC only calls
// ListTenants today; the rest are stubbed.
//
// Construct with newGCTenantStore(n) to get n tenants with fresh
// TypeIDs. tenantIDs() returns the generated tenant ID strings in
// the order they were created — use these as the TenantID stamp
// on test instances so the GC's per-tenant context resolves to
// the same key the fake instance store filters on.
type gcTenantStore struct {
	tenants []*admin.Tenant
}

func newGCTenantStore(count int) *gcTenantStore {
	out := make([]*admin.Tenant, 0, count)

	for range count {
		t := &admin.Tenant{}
		t.ID = id.New(id.PrefixTenant)
		out = append(out, t)
	}

	return &gcTenantStore{tenants: out}
}

func (s *gcTenantStore) tenantIDs() []string {
	out := make([]string, 0, len(s.tenants))

	for _, t := range s.tenants {
		out = append(out, t.ID.String())
	}

	return out
}

func (s *gcTenantStore) ListTenants(_ context.Context, _ admin.ListTenantsOptions) (*admin.TenantListResult, error) {
	return &admin.TenantListResult{Items: s.tenants, Total: len(s.tenants)}, nil
}

func (s *gcTenantStore) InsertTenant(context.Context, *admin.Tenant) error { return nil }
func (s *gcTenantStore) GetTenant(context.Context, string) (*admin.Tenant, error) {
	return nil, ctrlplane.ErrNotFound
}

func (s *gcTenantStore) GetTenantBySlug(context.Context, string) (*admin.Tenant, error) {
	return nil, ctrlplane.ErrNotFound
}

func (s *gcTenantStore) GetTenantByExternalID(context.Context, string) (*admin.Tenant, error) {
	return nil, ctrlplane.ErrNotFound
}

func (s *gcTenantStore) UpdateTenant(context.Context, *admin.Tenant) error { return nil }
func (s *gcTenantStore) DeleteTenant(context.Context, string) error        { return nil }
func (s *gcTenantStore) CountTenants(context.Context) (int, error)         { return len(s.tenants), nil }

func (s *gcTenantStore) CountTenantsByStatus(context.Context, admin.TenantStatus) (int, error) {
	return 0, nil
}

func (s *gcTenantStore) InsertAuditEntry(context.Context, *admin.AuditEntry) error { return nil }

func (s *gcTenantStore) QueryAuditLog(context.Context, admin.AuditQuery) (*admin.AuditResult, error) {
	return &admin.AuditResult{}, nil
}

// gcInstances is a fake instance.Service that lists pre-seeded
// instances and tracks Delete invocations. Filters by the tenantID
// stamped on each instance — the GC's per-tenant context carries
// claims that route List + Delete here.
type gcInstances struct {
	mu             sync.Mutex
	rows           map[string]*instance.Instance // keyed by instance ID
	deletes        atomic.Int32                  // successful deletes only
	deleteAttempts atomic.Int32                  // every Delete call (success + failure)
	listCalls      atomic.Int32
	tenantErr      map[string]error // tenantID → List error
	deleteErrIDs   map[string]error // instanceID.String() → Delete error
}

func newGCInstances(seeds ...*instance.Instance) *gcInstances {
	rows := make(map[string]*instance.Instance, len(seeds))
	for _, s := range seeds {
		rows[s.ID.String()] = s
	}

	return &gcInstances{rows: rows}
}

func (g *gcInstances) List(ctx context.Context, _ instance.ListOptions) (*instance.ListResult, error) {
	g.listCalls.Add(1)

	tenantID := tenantFromCtx(ctx)
	if tenantID == "" {
		return nil, errors.New("test: missing tenant in context")
	}

	if g.tenantErr != nil {
		if e, ok := g.tenantErr[tenantID]; ok {
			return nil, e
		}
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	out := make([]*instance.Instance, 0)

	for _, r := range g.rows {
		if r.TenantID == tenantID {
			clone := *r
			out = append(out, &clone)
		}
	}

	return &instance.ListResult{Items: out, Total: len(out)}, nil
}

func (g *gcInstances) Delete(_ context.Context, instanceID id.ID) error {
	g.deleteAttempts.Add(1)

	if g.deleteErrIDs != nil {
		if e, ok := g.deleteErrIDs[instanceID.String()]; ok {
			return e
		}
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	delete(g.rows, instanceID.String())
	g.deletes.Add(1)

	return nil
}

func (g *gcInstances) Get(context.Context, id.ID) (*instance.Instance, error) {
	return nil, ctrlplane.ErrNotFound
}

func (g *gcInstances) GetBySlug(context.Context, string) (*instance.Instance, error) {
	return nil, ctrlplane.ErrNotFound
}

func (g *gcInstances) Create(context.Context, instance.CreateRequest) (*instance.Instance, error) {
	return nil, errors.New("not used in gc tests")
}

func (g *gcInstances) Update(context.Context, id.ID, instance.UpdateRequest) (*instance.Instance, error) {
	return nil, errors.New("not used in gc tests")
}

func (g *gcInstances) Start(context.Context, id.ID) error   { return nil }
func (g *gcInstances) Stop(context.Context, id.ID) error    { return nil }
func (g *gcInstances) Restart(context.Context, id.ID) error { return nil }
func (g *gcInstances) Scale(context.Context, id.ID, instance.ScaleRequest) error {
	return nil
}
func (g *gcInstances) Suspend(context.Context, id.ID, string) error { return nil }
func (g *gcInstances) Unsuspend(context.Context, id.ID) error       { return nil }

func (g *gcInstances) Logs(context.Context, id.ID, instance.LogsOptions) (io.ReadCloser, error) {
	return nil, errors.New("not used in gc tests")
}

func (g *gcInstances) Resources(context.Context, id.ID) (*provider.ResourceUsage, error) {
	return &provider.ResourceUsage{}, nil
}

// gcWorkloadStore is a fake workload.Store that returns
// ErrNotFound for every workload ID except the seeded ones.
type gcWorkloadStore struct {
	present map[string]struct{}
}

func newGCWorkloadStore(present ...id.ID) *gcWorkloadStore {
	s := &gcWorkloadStore{present: make(map[string]struct{}, len(present))}

	for _, p := range present {
		s.present[p.String()] = struct{}{}
	}

	return s
}

func (s *gcWorkloadStore) GetWorkloadByID(_ context.Context, _ string, wid id.ID) (*workload.Workload, error) {
	if _, ok := s.present[wid.String()]; ok {
		return &workload.Workload{Entity: ctrlplane.Entity{ID: wid}}, nil
	}

	return nil, ctrlplane.ErrNotFound
}

func (s *gcWorkloadStore) InsertWorkload(context.Context, *workload.Workload) error { return nil }

func (s *gcWorkloadStore) GetWorkloadBySlug(context.Context, string, string) (*workload.Workload, error) {
	return nil, ctrlplane.ErrNotFound
}

func (s *gcWorkloadStore) ListWorkloads(context.Context, string, workload.ListOptions) (*workload.ListResult, error) {
	return &workload.ListResult{}, nil
}

func (s *gcWorkloadStore) UpdateWorkload(context.Context, *workload.Workload) error { return nil }

func (s *gcWorkloadStore) DeleteWorkload(context.Context, string, id.ID) error { return nil }

// tenantFromCtx pulls the synthesized claim out of the worker's
// system-claims context. Mirrors what auth.RequireClaims does in
// production but doesn't fail-open the tests on a missing claim.
func tenantFromCtx(ctx context.Context) string {
	c := auth.ClaimsFrom(ctx)
	if c == nil {
		return ""
	}

	return c.TenantID
}
