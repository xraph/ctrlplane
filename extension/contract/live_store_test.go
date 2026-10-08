package contract

import (
	"context"
	"net/url"
	"os"
	"testing"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/mongodriver"
	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/store"
	"github.com/xraph/ctrlplane/store/mongo"
	"github.com/xraph/ctrlplane/store/postgres"
)

// liveStore uses a fresh database per test. Configured connection failures fail the test.
func liveStore(t *testing.T, backend string) store.Store {
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

		if err := persistent.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		// Running migrations again must retain the data and schema.
		if err := persistent.Migrate(ctx); err != nil {
			t.Fatal(err)
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
