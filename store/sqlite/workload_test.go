package sqlite

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/sqlitedriver"
	"github.com/xraph/grove/drivers/sqlitedriver/sqlitemigrate"
	"github.com/xraph/grove/migrate"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/workload"
)

func openPersistentTestStore(t *testing.T, path string) *Store {
	t.Helper()

	driver := sqlitedriver.New()
	if err := driver.Open(context.Background(), path); err != nil {
		t.Fatal(err)
	}

	db, err := grove.Open(driver)
	if err != nil {
		t.Fatal(err)
	}

	return New(db)
}

func TestWorkloadPersistsAcrossReopenAndMigrationUpgrade(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "workloads.db")
	s := openPersistentTestStore(t, path)
	t.Cleanup(func() {
		if s != nil {
			if err := s.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	// Apply the schema shipped before workload persistence, then upgrade in place.
	legacy := migrate.NewGroup(Migrations.Name())
	for _, migration := range Migrations.Migrations() {
		if migration.Version <= "20240101000022" {
			if err := legacy.Register(migration); err != nil {
				t.Fatal(err)
			}
		}
	}

	if _, err := migrate.NewOrchestrator(sqlitemigrate.New(s.sdb), legacy).Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	oldID := id.New(id.PrefixInstance)

	stamp := time.Date(2026, 10, 8, 0, 0, 0, 123456789, time.UTC)
	if _, err := s.sdb.Exec(ctx, `INSERT INTO cp_instances (id,tenant_id,slug,name,state,provider_name,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?)`, oldID.String(), "alpha", "legacy", "Legacy", "running", "demo", stamp, stamp); err != nil {
		t.Fatal(err)
	}

	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	old, err := s.GetByID(ctx, "alpha", oldID)
	if err != nil {
		t.Fatal(err)
	}

	if old.Name != "Legacy" || !old.DatacenterID.IsNil() || !old.CurrentRelease.IsNil() || old.SuspendedAt != nil {
		t.Fatalf("legacy row changed: %#v", old)
	}

	paused := stamp.Add(time.Hour)

	w := &workload.Workload{
		Entity:   ctrlplane.Entity{ID: id.New(id.PrefixWorkload), CreatedAt: stamp, UpdatedAt: stamp},
		TenantID: "alpha", Name: "API", Slug: "api", DatacenterID: id.New(id.PrefixDatacenter), ProviderName: "demo", Region: "local", Kind: provider.KindStatefulSet,
		Services: []provider.ServiceSpec{
			{Name: "main", Image: "api:v1", Role: provider.RoleMain, Env: map[string]string{"MODE": "test"}, Resources: provider.ResourceSpec{CPUMillis: 250, MemoryMB: 512, Replicas: 1}, Secrets: []provider.SecretRef{{Key: "DB_PASSWORD"}}},
			{Name: "proxy", Image: "proxy:v1", Role: provider.RoleSidecar, DependsOn: []string{"main"}},
		}, Labels: map[string]string{"team": "platform"}, TemplateID: id.New(id.PrefixTemplate), CurrentReleaseID: id.New(id.PrefixRelease), ReplicaCount: 0, PreviousReplicas: 3, State: workload.StatePaused, PausedAt: &paused,
	}
	if err := s.InsertWorkload(ctx, w); err != nil {
		t.Fatal(err)
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s = nil

	s = openPersistentTestStore(t, path)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetWorkloadByID(ctx, "alpha", w.ID)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(got, w) {
		t.Fatalf("reopened workload changed:\ngot %#v\nwant %#v", got, w)
	}

	got.ReplicaCount = got.PreviousReplicas
	got.State = workload.StateActive

	got.PausedAt = nil
	if err := s.UpdateWorkload(ctx, got); err != nil {
		t.Fatal(err)
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s = nil
	s = openPersistentTestStore(t, path)

	resumed, err := s.GetWorkloadByID(ctx, "alpha", w.ID)
	if err != nil {
		t.Fatal(err)
	}

	if resumed.ReplicaCount != 3 || resumed.State != workload.StateActive || resumed.PausedAt != nil || !reflect.DeepEqual(resumed.Services, w.Services) {
		t.Fatalf("resumed workload changed: %#v", resumed)
	}
}
