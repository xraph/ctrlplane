package workload

import (
	"context"
	"strconv"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
)

// TestListInstances_FiltersToWorkloadEvenWhenStorePollutes is the
// regression for "workspace /health reports 28 replicas per
// component". Every store backend silently ignored
// ListOptions.Label until recently — and the stub backends
// (pg/sqlite/badger) still do. workload.ListInstances must filter
// the returned slice locally so callers (GetHealth, the metrics
// fan-in, the log fan-in) see only the workload's own replicas.
func TestListInstances_FiltersToWorkloadEvenWhenStorePollutes(t *testing.T) {
	t.Parallel()

	wid := id.New(id.PrefixWorkload)
	otherWID := id.New(id.PrefixWorkload)

	// Three replicas of OUR workload + three orphans (different
	// workload, no workload label, wrong workload).
	insts := &pollutingInstances{
		all: []*instance.Instance{
			labeledReplica(wid, 0),
			labeledReplica(wid, 1),
			labeledReplica(wid, 2),
			labeledReplica(otherWID, 0), // belongs to another workload
			{Entity: ctrlplane.NewEntity(id.PrefixInstance), Labels: nil},                 // no labels at all
			{Entity: ctrlplane.NewEntity(id.PrefixInstance), Labels: map[string]string{}}, // empty labels
		},
	}
	store := newRestartFakeStore()
	store.put(seedWorkload(wid, 3))

	svc := &service{store: store, instances: insts, events: event.NewInMemoryBus()}

	got, err := svc.ListInstances(context.Background(), wid)
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("len(got): want 3 (only our workload's replicas), got %d", len(got))
	}

	for i, inst := range got {
		if inst.Labels["ctrlplane.workload"] != wid.String() {
			t.Fatalf("item %d: belongs to a different workload (label=%q)", i, inst.Labels["ctrlplane.workload"])
		}
	}
	// Also verify the sort: replica indices 0, 1, 2 in order.
	for i, inst := range got {
		if inst.Labels["ctrlplane.replica_index"] != strconv.Itoa(i) {
			t.Fatalf("sort: want index %d at position %d, got %q", i, i, inst.Labels["ctrlplane.replica_index"])
		}
	}
}

// labeledReplica is a small constructor. Mirrors what spawnReplica
// would label a real replica with.
func labeledReplica(wid id.ID, idx int) *instance.Instance {
	return &instance.Instance{
		Entity: ctrlplane.NewEntity(id.PrefixInstance),
		Labels: map[string]string{
			"ctrlplane.workload":      wid.String(),
			"ctrlplane.replica_index": strconv.Itoa(idx),
		},
	}
}

// pollutingInstances simulates a store backend that ignores
// ListOptions.Label and returns everything.
type pollutingInstances struct {
	restartFakeInstances // for the rest of the interface

	all []*instance.Instance
}

func (p *pollutingInstances) List(_ context.Context, _ instance.ListOptions) (*instance.ListResult, error) {
	out := append([]*instance.Instance(nil), p.all...)

	return &instance.ListResult{Items: out, Total: len(out)}, nil
}
