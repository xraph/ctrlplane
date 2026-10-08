package contract

import (
	"context"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/mongodriver"
	"github.com/xraph/grove/drivers/pgdriver"
	"github.com/xraph/grove/drivers/pgdriver/pgmigrate"
	"github.com/xraph/grove/migrate"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/store"
	"github.com/xraph/ctrlplane/store/mongo"
	"github.com/xraph/ctrlplane/store/postgres"
	"github.com/xraph/ctrlplane/workload"
)

// liveStore uses a fresh database per test. Configured connection failures fail the test.
func liveStore(t *testing.T, backend string) store.Store {
	t.Helper()

	return liveStoreAtVersion(t, backend, "")
}

func liveStoreAtVersion(t *testing.T, backend, version string) store.Store {
	t.Helper()

	ctx := context.Background()
	name := "ctrlplane_test_" + id.New(id.PrefixTenant).String()

	if backend == "postgres" {
		dsn := os.Getenv("CTRLPLANE_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("CTRLPLANE_TEST_POSTGRES_DSN is unset")
		}

		root := pgdriver.New()
		if err := root.Open(ctx, dsn); err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() {
			if err := root.Close(); err != nil {
				t.Error(err)
			}
		})

		if _, err := root.Exec(ctx, "CREATE DATABASE "+name); err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() {
			if _, err := root.Exec(ctx, "DROP DATABASE "+name); err != nil {
				t.Error(err)
			}
		})

		parsed, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}

		parsed.Path = "/" + name

		driver := pgdriver.New()
		if err := driver.Open(ctx, parsed.String()); err != nil {
			t.Fatal(err)
		}

		db, err := grove.Open(driver)
		if err != nil {
			t.Fatal(err)
		}

		persistent := postgres.New(db)

		t.Cleanup(func() {
			if err := persistent.Close(); err != nil {
				t.Error(err)
			}
		})

		if version != "" {
			group := migrate.NewGroup(postgres.Migrations.Name())
			for _, migration := range postgres.Migrations.Migrations() {
				if migration.Version <= version {
					if err := group.Register(migration); err != nil {
						t.Fatal(err)
					}
				}
			}

			if _, err := migrate.NewOrchestrator(pgmigrate.New(driver), group).Migrate(ctx); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := persistent.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			// Running migrations again must retain the data and schema.
			if err := persistent.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
		}

		return persistent
	}

	uri := os.Getenv("CTRLPLANE_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("CTRLPLANE_TEST_MONGO_URI is unset")
	}

	driver := mongodriver.New()
	if err := driver.Open(ctx, uri, mongodriver.WithDatabase(name)); err != nil {
		t.Fatal(err)
	}

	db, err := grove.Open(driver)
	if err != nil {
		t.Fatal(err)
	}

	persistent := mongo.New(db)

	t.Cleanup(func() {
		if err := driver.Database().Drop(ctx); err != nil {
			t.Error(err)
		}

		if err := persistent.Close(); err != nil {
			t.Error(err)
		}
	})

	if err := persistent.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	if err := persistent.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	return persistent
}

func TestPostgresPlacementMigrationPreservesExistingRows(t *testing.T) {
	s := liveStoreAtVersion(t, "postgres", "20240101000027").(*postgres.Store)
	ctx := context.Background()
	driver := pgdriver.Unwrap(s.DB())
	target := id.New(id.PrefixInstance)

	stamp := time.Date(2026, 10, 8, 0, 0, 0, 123000000, time.UTC)
	if _, err := driver.Exec(ctx, `INSERT INTO cp_instances (id,tenant_id,slug,name,state,provider_name,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, target.String(), "alpha", "legacy", "Legacy", "stopped", "demo", stamp, stamp); err != nil {
		t.Fatal(err)
	}

	w := workload.NewWorkload()
	w.TenantID = "alpha"

	w.Slug = "legacy-workload"
	if err := s.InsertWorkload(ctx, w); err != nil {
		t.Fatal(err)
	}

	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	old, err := s.GetByID(ctx, "alpha", target)
	if err != nil {
		t.Fatal(err)
	}

	if old.Name != "Legacy" || !old.DatacenterID.IsNil() || !old.CurrentRelease.IsNil() || old.SuspendedAt != nil {
		t.Fatalf("legacy row changed: %#v", old)
	}

	old.DatacenterID = id.New(id.PrefixDatacenter)
	old.CurrentRelease = id.New(id.PrefixRelease)

	old.SuspendedAt = &stamp
	if err := s.Update(ctx, old); err != nil {
		t.Fatal(err)
	}

	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetByID(ctx, "alpha", target)
	if err != nil {
		t.Fatal(err)
	}

	old.UpdatedAt = got.UpdatedAt
	assertStoredValue(t, got, old)

	persisted, err := s.GetWorkloadByID(ctx, "alpha", w.ID)
	if err != nil {
		t.Fatal(err)
	}

	w.CreatedAt = persisted.CreatedAt
	w.UpdatedAt = persisted.UpdatedAt
	assertStoredValue(t, persisted, w)
}
