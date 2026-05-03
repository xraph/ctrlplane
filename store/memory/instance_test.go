package memory

import (
	"context"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
)

// TestInstanceList_LabelFilter regresses the workload-replicas
// counting bug. Before this fix the memory store ignored
// ListOptions.Label and returned every tenant instance, so
// workload.ListInstances couldn't tell which instances were its
// own replicas.
func TestInstanceList_LabelFilter(t *testing.T) {
	t.Parallel()

	s := New()
	ctx := context.Background()

	// Insert three instances under the same tenant, varying labels.
	wid := id.New(id.PrefixWorkload).String()
	other := id.New(id.PrefixWorkload).String()

	mustInsert := func(slug string, labels map[string]string) {
		t.Helper()

		inst := &instance.Instance{
			Entity:   ctrlplane.NewEntity(id.PrefixInstance),
			TenantID: "tenant-x",
			Slug:     slug,
			Labels:   labels,
		}
		if err := s.Insert(ctx, inst); err != nil {
			t.Fatalf("insert %s: %v", slug, err)
		}
	}
	mustInsert("ours-0", map[string]string{"ctrlplane.workload": wid, "ctrlplane.replica_index": "0"})
	mustInsert("ours-1", map[string]string{"ctrlplane.workload": wid, "ctrlplane.replica_index": "1"})
	mustInsert("other-0", map[string]string{"ctrlplane.workload": other, "ctrlplane.replica_index": "0"})
	mustInsert("none", nil) // no labels

	// Without filter — returns all 4 (sanity check).
	all, err := s.List(ctx, "tenant-x", instance.ListOptions{})
	if err != nil {
		t.Fatalf("List(no filter): %v", err)
	}

	if len(all.Items) != 4 {
		t.Fatalf("unfiltered list: want 4 items, got %d", len(all.Items))
	}

	// With filter — should return only the two for `wid`.
	filtered, err := s.List(ctx, "tenant-x", instance.ListOptions{
		Label: "ctrlplane.workload=" + wid,
	})
	if err != nil {
		t.Fatalf("List(label filter): %v", err)
	}

	if len(filtered.Items) != 2 {
		t.Fatalf("filtered list: want 2 items, got %d", len(filtered.Items))
	}

	for _, inst := range filtered.Items {
		if inst.Labels["ctrlplane.workload"] != wid {
			t.Fatalf("filter leaked an item with workload=%q", inst.Labels["ctrlplane.workload"])
		}
	}
}

// TestInstanceList_LabelFilterIgnoredWhenNoEquals asserts a
// malformed Label string (no "=") doesn't accidentally narrow
// the result set — better to return everything than to silently
// hide rows on a programming mistake.
func TestInstanceList_LabelFilterIgnoredWhenNoEquals(t *testing.T) {
	t.Parallel()

	s := New()
	ctx := context.Background()

	if err := s.Insert(ctx, &instance.Instance{
		Entity:   ctrlplane.NewEntity(id.PrefixInstance),
		TenantID: "tenant-x",
		Slug:     "only-one",
		Labels:   map[string]string{"key": "v"},
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	res, err := s.List(ctx, "tenant-x", instance.ListOptions{Label: "no-equals"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(res.Items) != 1 {
		t.Fatalf("malformed label should be ignored; want 1 item, got %d", len(res.Items))
	}
}
