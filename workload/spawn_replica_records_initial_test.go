package workload

import (
	"context"
	"sync/atomic"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
)

// TestSpawnReplica_RecordsInitialRelease covers the headline fix:
// after a fresh-create spawnReplica path, the deploy service's
// RecordInitial is invoked so the dashboard's Deployments page is
// populated immediately and rollback has a v1 target.
func TestSpawnReplica_RecordsInitialRelease(t *testing.T) {
	t.Parallel()

	wid := id.New(id.PrefixWorkload)
	w := seedWorkload(wid, 1)

	insts := &spawnFakeInstances{}
	deploys := &recordInitialFakeDeploys{}

	svc := &service{
		instances: insts,
		deploys:   deploys,
		events:    event.NewInMemoryBus(),
	}

	if _, err := svc.spawnReplica(context.Background(), w, 0); err != nil {
		t.Fatalf("spawnReplica: %v", err)
	}

	if got := deploys.recordInitialCalls.Load(); got != 1 {
		t.Fatalf("RecordInitial calls: want 1 (one per fresh replica), got %d", got)
	}
}

// TestSpawnReplica_RecordInitialFailureNotFatal asserts that a
// failure inside RecordInitial does not abort spawnReplica — the
// already-provisioned instance must not leak just because the
// release-recording side-effect failed. Operators can re-trigger
// by calling Deploy explicitly; the cron's reconciler will catch
// up over time.
func TestSpawnReplica_RecordInitialFailureNotFatal(t *testing.T) {
	t.Parallel()

	wid := id.New(id.PrefixWorkload)
	w := seedWorkload(wid, 1)

	insts := &spawnFakeInstances{}
	deploys := &recordInitialFakeDeploys{shouldErr: true}

	svc := &service{
		instances: insts,
		deploys:   deploys,
		events:    event.NewInMemoryBus(),
	}

	got, err := svc.spawnReplica(context.Background(), w, 0)
	if err != nil {
		t.Fatalf("spawnReplica should not propagate RecordInitial error: %v", err)
	}

	if got == nil {
		t.Fatal("expected instance to be returned despite RecordInitial failure")
	}
}

// TestSpawnReplica_NilDeploysSafe asserts that a workload service
// constructed without a deploy.Service (legacy callers, partial
// integration tests) doesn't panic on the new RecordInitial call —
// the wiring degrades gracefully to no-op.
func TestSpawnReplica_NilDeploysSafe(t *testing.T) {
	t.Parallel()

	wid := id.New(id.PrefixWorkload)
	w := seedWorkload(wid, 1)

	svc := &service{
		instances: &spawnFakeInstances{},
		// deploys deliberately nil
		events: event.NewInMemoryBus(),
	}

	got, err := svc.spawnReplica(context.Background(), w, 0)
	if err != nil {
		t.Fatalf("spawnReplica with nil deploys: %v", err)
	}

	if got == nil {
		t.Fatal("expected instance, got nil")
	}
}

// --- helpers ---

// recordInitialFakeDeploys is a minimal deploy.Service whose only
// production-relevant method is RecordInitial — the rest panic so
// any accidental new usage from spawnReplica surfaces immediately.
type recordInitialFakeDeploys struct {
	recordInitialCalls atomic.Int32
	shouldErr          bool
}

func (f *recordInitialFakeDeploys) RecordInitial(_ context.Context, _ id.ID) (*deploy.Release, error) {
	f.recordInitialCalls.Add(1)

	if f.shouldErr {
		return nil, ctrlplane.ErrAlreadyExists
	}

	return &deploy.Release{Entity: ctrlplane.NewEntity(id.PrefixRelease)}, nil
}

func (f *recordInitialFakeDeploys) Deploy(context.Context, deploy.DeployRequest) (*deploy.Deployment, error) {
	panic("Deploy not used in spawnReplica record-initial tests")
}

func (f *recordInitialFakeDeploys) Rollback(context.Context, id.ID, id.ID) (*deploy.Deployment, error) {
	panic("Rollback not used")
}

func (f *recordInitialFakeDeploys) Cancel(context.Context, id.ID) error { panic("Cancel not used") }

func (f *recordInitialFakeDeploys) GetDeployment(context.Context, id.ID) (*deploy.Deployment, error) {
	panic("GetDeployment not used")
}

func (f *recordInitialFakeDeploys) ListDeployments(context.Context, id.ID, deploy.ListOptions) (*deploy.DeployListResult, error) {
	panic("ListDeployments not used")
}

func (f *recordInitialFakeDeploys) GetRelease(context.Context, id.ID) (*deploy.Release, error) {
	panic("GetRelease not used")
}

func (f *recordInitialFakeDeploys) ListReleases(context.Context, id.ID, deploy.ListOptions) (*deploy.ReleaseListResult, error) {
	panic("ListReleases not used")
}

// Compile-time check: the fake satisfies deploy.Service.
var _ deploy.Service = (*recordInitialFakeDeploys)(nil)
