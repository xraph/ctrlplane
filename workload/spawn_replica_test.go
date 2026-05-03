package workload

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/provider"
)

// TestSpawnReplica_ReusesOwnOrphan asserts spawnReplica adopts an
// existing instance row whose slug matches the would-be replica
// slug and whose ctrlplane.workload label matches us. This is the
// recovery path for a workload stuck in StateFailed because a
// prior Provision errored after the row was inserted — Scale
// retries should heal, not collide.
func TestSpawnReplica_ReusesOwnOrphan(t *testing.T) {
	t.Parallel()

	wid := id.New(id.PrefixWorkload)
	w := seedWorkload(wid, 1)

	// Pre-existing row at slug "test-workload-0" labelled with our
	// workload ID — simulates the orphan from a half-failed prior
	// scale.
	orphanSlug := "test-workload-0"
	orphan := &instance.Instance{
		Entity: ctrlplane.NewEntity(id.PrefixInstance),
		Slug:   orphanSlug,
		Labels: map[string]string{
			"ctrlplane.workload":      wid.String(),
			"ctrlplane.replica_index": "0",
		},
	}

	insts := &spawnFakeInstances{
		bySlug: map[string]*instance.Instance{orphanSlug: orphan},
	}
	svc := &service{instances: insts, events: event.NewInMemoryBus()}

	got, err := svc.spawnReplica(context.Background(), w, 0)
	if err != nil {
		t.Fatalf("spawnReplica: %v", err)
	}

	if got != orphan {
		t.Fatalf("expected to reuse orphan, got fresh instance %v", got)
	}

	if insts.creates != 0 {
		t.Fatalf("Create should not have been called when orphan was reusable, got %d calls", insts.creates)
	}
}

// TestSpawnReplica_AdoptsLabellessLegacyOrphan asserts that an
// existing row with no ctrlplane.workload label is treated as
// ours by construction (no other workload could have produced the
// slug under our tenant). Covers the in-flight upgrade path:
// rows written before the mongo store persisted Labels were
// invisible to ListInstances and would have collided forever
// without this adoption.
func TestSpawnReplica_AdoptsLabellessLegacyOrphan(t *testing.T) {
	t.Parallel()

	wid := id.New(id.PrefixWorkload)
	w := seedWorkload(wid, 1)

	orphan := &instance.Instance{
		Entity: ctrlplane.NewEntity(id.PrefixInstance),
		Slug:   "test-workload-0",
		Labels: nil, // simulates row from before label persistence
	}
	insts := &spawnFakeInstances{
		bySlug: map[string]*instance.Instance{"test-workload-0": orphan},
	}
	svc := &service{instances: insts, events: event.NewInMemoryBus()}

	got, err := svc.spawnReplica(context.Background(), w, 0)
	if err != nil {
		t.Fatalf("spawnReplica: %v", err)
	}

	if got != orphan {
		t.Fatalf("expected to adopt labelless orphan, got %v", got)
	}
}

// TestSpawnReplica_RejectsAlienOwner asserts that an existing row
// at the target slug owned by a different workload is rejected
// rather than overwritten. Exists primarily so a future bug that
// produces colliding slugs across workloads gets a loud error
// instead of silent data corruption.
func TestSpawnReplica_RejectsAlienOwner(t *testing.T) {
	t.Parallel()

	myWid := id.New(id.PrefixWorkload)
	otherWid := id.New(id.PrefixWorkload)
	w := seedWorkload(myWid, 1)

	alien := &instance.Instance{
		Entity: ctrlplane.NewEntity(id.PrefixInstance),
		Slug:   "test-workload-0",
		Labels: map[string]string{
			"ctrlplane.workload": otherWid.String(),
		},
	}
	insts := &spawnFakeInstances{
		bySlug: map[string]*instance.Instance{"test-workload-0": alien},
	}
	svc := &service{instances: insts, events: event.NewInMemoryBus()}

	_, err := svc.spawnReplica(context.Background(), w, 0)
	if err == nil {
		t.Fatal("spawnReplica: want error on alien-owned slug, got nil")
	}

	if !strings.Contains(err.Error(), "already owned by workload") {
		t.Fatalf("error message should call out alien ownership, got %v", err)
	}
}

// TestSpawnReplica_NoCollisionFreshCreate asserts the happy path:
// no existing row, GetBySlug returns ErrNotFound, Create is called.
func TestSpawnReplica_NoCollisionFreshCreate(t *testing.T) {
	t.Parallel()

	wid := id.New(id.PrefixWorkload)
	w := seedWorkload(wid, 1)

	insts := &spawnFakeInstances{}
	svc := &service{instances: insts, events: event.NewInMemoryBus()}

	got, err := svc.spawnReplica(context.Background(), w, 0)
	if err != nil {
		t.Fatalf("spawnReplica: %v", err)
	}

	if got == nil {
		t.Fatal("expected fresh instance, got nil")
	}

	if insts.creates != 1 {
		t.Fatalf("Create should have been called once, got %d calls", insts.creates)
	}
}

// spawnFakeInstances is a minimal instance.Service for the
// spawnReplica tests. Tracks Create call count and answers
// GetBySlug from a slug→instance map.
type spawnFakeInstances struct {
	bySlug  map[string]*instance.Instance
	creates int
}

func (f *spawnFakeInstances) GetBySlug(_ context.Context, slug string) (*instance.Instance, error) {
	if inst, ok := f.bySlug[slug]; ok {
		return inst, nil
	}

	return nil, ctrlplane.ErrNotFound
}

func (f *spawnFakeInstances) Create(_ context.Context, req instance.CreateRequest) (*instance.Instance, error) {
	f.creates++

	return &instance.Instance{
		Entity: ctrlplane.NewEntity(id.PrefixInstance),
		Slug:   slugify(req.Name),
		Name:   req.Name,
		Labels: req.Labels,
	}, nil
}

func (f *spawnFakeInstances) Get(context.Context, id.ID) (*instance.Instance, error) {
	panic("not used")
}
func (f *spawnFakeInstances) List(context.Context, instance.ListOptions) (*instance.ListResult, error) {
	return &instance.ListResult{}, nil
}
func (f *spawnFakeInstances) Update(context.Context, id.ID, instance.UpdateRequest) (*instance.Instance, error) {
	panic("not used")
}
func (f *spawnFakeInstances) Delete(context.Context, id.ID) error          { return nil }
func (f *spawnFakeInstances) Start(context.Context, id.ID) error           { return nil }
func (f *spawnFakeInstances) Stop(context.Context, id.ID) error            { return nil }
func (f *spawnFakeInstances) Restart(context.Context, id.ID) error         { return nil }
func (f *spawnFakeInstances) Suspend(context.Context, id.ID, string) error { return nil }
func (f *spawnFakeInstances) Unsuspend(context.Context, id.ID) error       { return nil }
func (f *spawnFakeInstances) ResolveProvider(context.Context, id.ID) (string, error) {
	return "", nil
}
func (f *spawnFakeInstances) Scale(context.Context, id.ID, instance.ScaleRequest) error {
	return nil
}
func (f *spawnFakeInstances) Logs(context.Context, id.ID, instance.LogsOptions) (io.ReadCloser, error) {
	panic("not used")
}
func (f *spawnFakeInstances) Resources(context.Context, id.ID) (*provider.ResourceUsage, error) {
	return nil, nil
}

// Compile-time guard.
var _ instance.Service = (*spawnFakeInstances)(nil)

// silence unused import warning when only one branch fires.
var _ = errors.Is
