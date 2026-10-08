package badger

import (
	"context"
	"errors"
	"reflect"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/workload"
)

func TestWorkloadPersistenceAndTenantIsolation(t *testing.T) {
	ctx := context.Background()
	cfg := Config{Path: t.TempDir(), SyncWrites: true}

	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if s != nil {
			if err := s.Close(); err != nil {
				t.Error(err)
			}
		}
	})

	first := &workload.Workload{Entity: ctrlplane.NewEntity(id.PrefixWorkload), TenantID: "t1", Name: "API", Slug: "api", ProviderName: "demo", Region: "local", State: workload.StateActive, ReplicaCount: 2, Services: []provider.ServiceSpec{{Name: "main", Image: "api:v1", Env: map[string]string{"MODE": "demo"}}, {Name: "sidecar", Image: "proxy:v1"}}, Labels: map[string]string{"team": "platform"}, PreviousReplicas: 2}
	second := *first
	second.ID = id.New(id.PrefixWorkload)

	second.TenantID = "t2"
	for _, row := range []*workload.Workload{first, &second} {
		if err := s.InsertWorkload(ctx, row); err != nil {
			t.Fatal(err)
		}
	}

	dup := *first

	dup.ID = id.New(id.PrefixWorkload)
	if err := s.InsertWorkload(ctx, &dup); !errors.Is(err, ctrlplane.ErrAlreadyExists) {
		t.Fatalf("duplicate slug: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s = nil

	s, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.GetWorkloadByID(ctx, "t1", first.ID)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(got, first) {
		t.Fatalf("populated round trip changed: got %#v want %#v", got, first)
	}

	got.Services[0].Env["MODE"] = "changed"

	again, err := s.GetWorkloadByID(ctx, "t1", first.ID)
	if err != nil || again.Services[0].Env["MODE"] != "demo" {
		t.Fatalf("read aliases stored state: %v", err)
	}

	if _, err := s.GetWorkloadByID(ctx, "t2", first.ID); !errors.Is(err, ctrlplane.ErrNotFound) {
		t.Fatalf("foreign read: %v", err)
	}

	if err := s.DeleteWorkload(ctx, "t2", first.ID); !errors.Is(err, ctrlplane.ErrNotFound) {
		t.Fatalf("foreign delete: %v", err)
	}

	rows, err := s.ListWorkloads(ctx, "t1", workload.ListOptions{Limit: 1})
	if err != nil || len(rows.Items) != 1 || rows.Items[0].ID != first.ID {
		t.Fatalf("tenant list: %#v %v", rows, err)
	}
	// Empty tenant deliberately matches all rows. The contract must guard it.
	all, err := s.ListWorkloads(ctx, "", workload.ListOptions{Limit: 1})
	if err != nil || all.Total != 2 || len(all.Items) != 1 {
		t.Fatalf("bounded admin list: %#v %v", all, err)
	}

	first.ReplicaCount = 0

	first.State = workload.StatePaused
	if err := s.UpdateWorkload(ctx, first); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteWorkload(ctx, "t1", first.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := s.GetWorkloadByID(ctx, "t1", first.ID); !errors.Is(err, ctrlplane.ErrNotFound) {
		t.Fatalf("deleted row: %v", err)
	}
}
