package badger

import (
	"context"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
)

func TestInstanceListScopesWorkloadAndDatacenter(t *testing.T) {
	s, err := New(Config{Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})

	dc := id.New(id.PrefixDatacenter)

	for _, name := range []string{"orders", "billing"} {
		inst := &instance.Instance{Entity: ctrlplane.NewEntity(id.PrefixInstance), TenantID: "alpha", Name: name, Slug: name, Labels: map[string]string{"ctrlplane.workload": name}}
		if name == "orders" {
			inst.DatacenterID = dc
		}

		if err := s.Insert(context.Background(), inst); err != nil {
			t.Fatal(err)
		}
	}

	for _, opts := range []instance.ListOptions{{Label: "ctrlplane.workload=orders"}, {Datacenter: dc.String()}, {Label: "ctrlplane.workload=orders", Datacenter: dc.String()}} {
		result, err := s.List(context.Background(), "alpha", opts)
		if err != nil {
			t.Fatal(err)
		}

		if result.Total != 1 || result.Items[0].Name != "orders" {
			t.Fatalf("incorrect scoped result: %+v", result)
		}
	}

	result, err := s.List(context.Background(), "beta", instance.ListOptions{Label: "ctrlplane.workload=orders"})
	if err != nil {
		t.Fatal(err)
	}

	if result.Total != 0 {
		t.Fatal("foreign tenant leaked")
	}
}
