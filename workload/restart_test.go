package workload

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/provider"
)

// TestWorkloadRestart_PerReplicaInstanceRestart asserts Restart
// loops instance.Restart per replica — no Delete / Create calls
// (which would reproduce the "restart deletes my workspace" bug).
func TestWorkloadRestart_PerReplicaInstanceRestart(t *testing.T) {
	t.Parallel()

	wid := id.New(id.PrefixWorkload)
	insts := newRestartFakeInstances(wid, 3)

	store := newRestartFakeStore()
	store.put(seedWorkload(wid, 3))

	svc := &service{store: store, instances: insts, events: event.NewInMemoryBus()}

	if err := svc.Restart(adminCtxRestart(), wid); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	if got := insts.restarts.Load(); got != 3 {
		t.Fatalf("Restart calls: want 3 (one per replica), got %d", got)
	}

	if got := insts.deletes.Load(); got != 0 {
		t.Fatalf("Delete calls: want 0 (in-place restart), got %d", got)
	}

	if got := insts.creates.Load(); got != 0 {
		t.Fatalf("Create calls: want 0, got %d", got)
	}

	got := store.get(wid)
	if got.ReplicaCount != 3 {
		t.Fatalf("ReplicaCount: want 3 (preserved), got %d", got.ReplicaCount)
	}
}

// TestWorkloadRestart_FailsFastOnReplicaError covers the error
// path: second replica's Restart errors → method returns; the third
// replica is not touched.
func TestWorkloadRestart_FailsFastOnReplicaError(t *testing.T) {
	t.Parallel()

	wid := id.New(id.PrefixWorkload)
	insts := newRestartFakeInstances(wid, 3)
	insts.restartErr = func(call int32) error {
		if call == 2 {
			return errors.New("boom")
		}

		return nil
	}

	store := newRestartFakeStore()
	store.put(seedWorkload(wid, 3))

	svc := &service{store: store, instances: insts, events: event.NewInMemoryBus()}

	err := svc.Restart(adminCtxRestart(), wid)
	if err == nil {
		t.Fatal("Restart should propagate replica error")
	}

	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Restart error should wrap underlying cause; got %q", err)
	}

	if got := insts.restarts.Load(); got != 2 {
		t.Fatalf("Restart calls: want 2 (fail-fast), got %d", got)
	}
}

// TestPauseResume_RoundTripsReplicaCount asserts the previous bug:
// Pause → Resume on a 3-replica workload restored exactly 3, not 1.
func TestPauseResume_RoundTripsReplicaCount(t *testing.T) {
	t.Parallel()

	wid := id.New(id.PrefixWorkload)
	insts := newRestartFakeInstances(wid, 3)

	store := newRestartFakeStore()
	store.put(seedWorkload(wid, 3))

	svc := &service{store: store, instances: insts, events: event.NewInMemoryBus()}

	if err := svc.Pause(adminCtxRestart(), wid); err != nil {
		t.Fatalf("Pause: %v", err)
	}

	got := store.get(wid)
	if got.ReplicaCount != 0 {
		t.Fatalf("after Pause, ReplicaCount: want 0, got %d", got.ReplicaCount)
	}

	if got.PreviousReplicas != 3 {
		t.Fatalf("after Pause, PreviousReplicas: want 3 (stamped), got %d", got.PreviousReplicas)
	}

	if err := svc.Resume(adminCtxRestart(), wid); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	got = store.get(wid)
	if got.ReplicaCount != 3 {
		t.Fatalf("after Resume, ReplicaCount: want 3 (restored), got %d", got.ReplicaCount)
	}
}

// --- helpers ---

func adminCtxRestart() context.Context {
	return adminCtxStream() // reuse streaming_test.go's admin claims
}

// seedWorkload returns a populated Workload that maps cleanly to
// the conventions the restart tests rely on.
func seedWorkload(wid id.ID, replicas int) *Workload {
	w := NewWorkload()
	w.ID = wid
	w.TenantID = "test-tenant"
	w.Slug = "test-workload"
	w.Services = []provider.ServiceSpec{{
		Name:  "main",
		Image: "alpine:latest",
		Role:  provider.RoleMain,
	}}
	w.ReplicaCount = replicas
	w.State = StateActive

	return w
}

// restartFakeStore is a minimal in-memory workload.Store for tests.
type restartFakeStore struct {
	mu        sync.Mutex
	workloads map[string]*Workload
}

func newRestartFakeStore() *restartFakeStore {
	return &restartFakeStore{workloads: make(map[string]*Workload)}
}

func (s *restartFakeStore) put(w *Workload) {
	s.mu.Lock()
	defer s.mu.Unlock()

	clone := *w
	s.workloads[w.ID.String()] = &clone
}

func (s *restartFakeStore) get(wid id.ID) *Workload {
	s.mu.Lock()
	defer s.mu.Unlock()

	w, ok := s.workloads[wid.String()]
	if !ok {
		return nil
	}

	clone := *w

	return &clone
}

func (s *restartFakeStore) InsertWorkload(_ context.Context, w *Workload) error {
	s.put(w)

	return nil
}

func (s *restartFakeStore) GetWorkloadByID(_ context.Context, _ string, wid id.ID) (*Workload, error) {
	w := s.get(wid)
	if w == nil {
		return nil, ctrlplane.ErrNotFound
	}

	return w, nil
}

func (s *restartFakeStore) GetWorkloadBySlug(_ context.Context, _, _ string) (*Workload, error) {
	return nil, ctrlplane.ErrNotFound
}

func (s *restartFakeStore) ListWorkloads(_ context.Context, _ string, _ ListOptions) (*ListResult, error) {
	return &ListResult{}, nil
}

func (s *restartFakeStore) UpdateWorkload(_ context.Context, w *Workload) error {
	s.put(w)

	return nil
}

func (s *restartFakeStore) DeleteWorkload(_ context.Context, _ string, wid id.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.workloads, wid.String())

	return nil
}

// restartFakeInstances tracks per-method call counts and simulates
// a replica set that grows / shrinks via Create / Delete.
type restartFakeInstances struct {
	wid               id.ID
	restarts          atomic.Int32
	deletes           atomic.Int32
	creates           atomic.Int32
	restartErr        func(call int32) error
	simulatedReplicas atomic.Int32
}

func newRestartFakeInstances(wid id.ID, initialReplicas int) *restartFakeInstances {
	f := &restartFakeInstances{wid: wid}
	f.simulatedReplicas.Store(int32(initialReplicas))

	return f
}

func (f *restartFakeInstances) Restart(_ context.Context, _ id.ID) error {
	n := f.restarts.Add(1)
	if f.restartErr != nil {
		return f.restartErr(n)
	}

	return nil
}

func (f *restartFakeInstances) List(_ context.Context, _ instance.ListOptions) (*instance.ListResult, error) {
	n := f.simulatedReplicas.Load()

	out := make([]*instance.Instance, 0, n)
	for i := range n {
		out = append(out, newReplica(f.wid, int(i)))
	}

	return &instance.ListResult{Items: out, Total: len(out)}, nil
}

func (f *restartFakeInstances) Delete(_ context.Context, _ id.ID) error {
	f.deletes.Add(1)
	f.simulatedReplicas.Add(-1)

	return nil
}

func (f *restartFakeInstances) Create(_ context.Context, _ instance.CreateRequest) (*instance.Instance, error) {
	f.creates.Add(1)
	f.simulatedReplicas.Add(1)

	return &instance.Instance{Entity: ctrlplane.NewEntity(id.PrefixInstance)}, nil
}

func (f *restartFakeInstances) Logs(context.Context, id.ID, instance.LogsOptions) (io.ReadCloser, error) {
	panic("not used in restart tests")
}
func (f *restartFakeInstances) Get(context.Context, id.ID) (*instance.Instance, error) {
	panic("not used in restart tests")
}
func (f *restartFakeInstances) GetBySlug(context.Context, string) (*instance.Instance, error) {
	return nil, ctrlplane.ErrNotFound
}
func (f *restartFakeInstances) Update(context.Context, id.ID, instance.UpdateRequest) (*instance.Instance, error) {
	panic("not used in restart tests")
}
func (f *restartFakeInstances) Start(context.Context, id.ID) error { return nil }
func (f *restartFakeInstances) Stop(context.Context, id.ID) error  { return nil }
func (f *restartFakeInstances) Scale(context.Context, id.ID, instance.ScaleRequest) error {
	return nil
}
func (f *restartFakeInstances) Suspend(context.Context, id.ID, string) error { return nil }
func (f *restartFakeInstances) Unsuspend(context.Context, id.ID) error       { return nil }
func (f *restartFakeInstances) ResolveProvider(context.Context, id.ID) (string, error) {
	return "", nil
}
func (f *restartFakeInstances) Resources(context.Context, id.ID) (*provider.ResourceUsage, error) {
	return nil, nil
}
