package contract

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	dash "github.com/xraph/forge/extensions/dashboard/contract"
	"github.com/xraph/forge/extensions/dashboard/contract/dispatcher"
	"github.com/xraph/forge/extensions/dashboard/contract/transport"
	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/sqlitedriver"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/app"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/network"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/store"
	"github.com/xraph/ctrlplane/store/badger"
	"github.com/xraph/ctrlplane/store/memory"
	"github.com/xraph/ctrlplane/store/sqlite"
	"github.com/xraph/ctrlplane/template"
	"github.com/xraph/ctrlplane/vars"
)

func storageHarness(t *testing.T, backend string) (*app.CtrlPlane, http.Handler) {
	t.Helper()

	var s store.Store

	switch backend {
	case "memory":
		s = memory.New()
	case "badger":
		db, err := badger.New(badger.Config{Path: t.TempDir(), SyncWrites: true})
		if err != nil {
			t.Fatal(err)
		}

		s = db
	case "sqlite":
		driver := sqlitedriver.New()
		if err := driver.Open(context.Background(), filepath.Join(t.TempDir(), "contract.db")); err != nil {
			t.Fatal(err)
		}

		db, err := grove.Open(driver)
		if err != nil {
			t.Fatal(err)
		}

		persistent := sqlite.New(db)
		s = persistent

		t.Cleanup(func() {
			if err := persistent.Close(); err != nil {
				t.Error(err)
			}
		})

		if err := persistent.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown backend %s", backend)
	}

	if backend != "sqlite" {
		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Error(err)
			}
		})
	}

	cp, err := app.New(app.WithStore(s), app.WithAuth(&auth.NoopProvider{}))
	if err != nil {
		t.Fatal(err)
	}

	reg, wreg := dash.NewRegistry(), dash.NewWardenRegistry()

	d := dispatcher.New(dispatcher.NoopMetricsEmitter{})
	if err := Register(d, reg, wreg, cp); err != nil {
		t.Fatal(err)
	}

	return cp, transport.NewHandler(reg, wreg, d, nil)
}
func TestPersistentPartialUpdates(t *testing.T) {
	for _, backend := range []string{"memory", "badger", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			cp, h := storageHarness(t, backend)
			user := principal("alpha", false)

			row := &template.Template{Entity: ctrlplane.NewEntity(id.PrefixTemplate), TenantID: "alpha", Name: "Before", Description: "keep description", DefaultKind: provider.KindDeployment, DefaultStrategy: "rolling", Labels: map[string]string{"team": "platform"}, Notes: "keep notes", Services: []provider.ServiceSpec{{Name: "api", Image: "api:1", Role: provider.RoleMain, Env: map[string]string{"MODE": "prod"}, Ports: []provider.PortSpec{{Container: 8080, Protocol: "tcp"}}}}, Source: provider.DeploymentSource{Type: provider.SourceHelm, Helm: &provider.HelmSource{Chart: "app", Version: "1.2.3", Values: map[string]any{"replicas": float64(3)}}}, Variables: []vars.Definition{{Name: "replicas", Type: vars.TypeInt, Default: float64(3)}}}
			if err := cp.Store().InsertTemplate(context.Background(), row); err != nil {
				t.Fatal(err)
			}

			before, err := cp.Store().GetTemplate(context.Background(), "alpha", row.ID)
			if err != nil {
				t.Fatal(err)
			}

			result := request(t, h, user, dash.KindCommand, "templates.update", map[string]any{"id": row.ID.String(), "request": map[string]any{"name": "After"}})
			if !result.OK {
				t.Fatalf("update: %+v", result.Error)
			}

			got, err := cp.Store().GetTemplate(context.Background(), "alpha", row.ID)
			if err != nil {
				t.Fatal(err)
			}

			before.Name = "After"

			before.UpdatedAt = got.UpdatedAt
			if !reflect.DeepEqual(before, got) {
				t.Fatalf("name-only update lost fields: before=%+v after=%+v", before, got)
			}

			if request(t, h, principal("beta", false), dash.KindCommand, "templates.update", map[string]any{"id": row.ID.String(), "request": map[string]any{"name": "Foreign"}}).OK {
				t.Fatal("foreign template update succeeded")
			}

			inst := &instance.Instance{Entity: ctrlplane.NewEntity(id.PrefixInstance), TenantID: "alpha", Name: "API", Slug: "api", State: provider.StateRunning}
			if err := cp.Store().Insert(context.Background(), inst); err != nil {
				t.Fatal(err)
			}

			route := &network.Route{Entity: ctrlplane.NewEntity(id.PrefixRoute), TenantID: "alpha", InstanceID: inst.ID, ServiceName: "api", Hostname: "app.example.test", Path: "/app", Port: 8080, Protocol: "http", Weight: 10, StripPrefix: true, RewriteRedirects: true, RewriteCookiePath: true, UpstreamOrigin: "https://upstream.example.test", TLSVerify: true}
			if err := cp.Store().InsertRoute(context.Background(), route); err != nil {
				t.Fatal(err)
			}

			original, err := cp.Store().GetRoute(context.Background(), "alpha", route.ID)
			if err != nil {
				t.Fatal(err)
			}

			result = request(t, h, user, dash.KindCommand, "routes.update", map[string]any{"id": route.ID.String(), "request": map[string]any{"weight": 20}})
			if !result.OK {
				t.Fatalf("route update: %+v", result.Error)
			}

			updated, err := cp.Store().GetRoute(context.Background(), "alpha", route.ID)
			if err != nil {
				t.Fatal(err)
			}

			original.Weight = 20

			original.UpdatedAt = updated.UpdatedAt
			if !reflect.DeepEqual(original, updated) {
				t.Fatalf("weight-only update lost routing fields: before=%+v after=%+v", original, updated)
			}
		})
	}
}
func TestHTTPTemplateContinuation(t *testing.T) {
	for _, backend := range []string{"memory", "badger", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			cp, h := storageHarness(t, backend)
			stamp := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)

			for i := range 5 {
				row := &template.Template{Entity: ctrlplane.NewEntity(id.PrefixTemplate), TenantID: "alpha", Name: fmt.Sprintf("Template %d", i)}

				row.CreatedAt = stamp
				if err := cp.Store().InsertTemplate(context.Background(), row); err != nil {
					t.Fatal(err)
				}
			}

			foreign := &template.Template{Entity: ctrlplane.NewEntity(id.PrefixTemplate), TenantID: "beta", Name: "Private"}
			if err := cp.Store().InsertTemplate(context.Background(), foreign); err != nil {
				t.Fatal(err)
			}

			cursor := ""
			seen := map[id.ID]bool{}

			for turn := range 4 {
				result := request(t, h, principal("alpha", false), dash.KindQuery, "templates.list", map[string]any{"limit": 2, "cursor": cursor})
				if !result.OK {
					t.Fatalf("list: %+v", result.Error)
				}

				var page template.ListResult
				if err := json.Unmarshal(result.Data, &page); err != nil {
					t.Fatal(err)
				}

				if page.Total != 5 || len(page.Items) > 2 {
					t.Fatalf("count/limit: %+v", page)
				}

				for _, row := range page.Items {
					if seen[row.ID] || row.TenantID != "alpha" {
						t.Fatal("duplicate or foreign continuation row")
					}

					seen[row.ID] = true
				}

				if turn == 0 {
					newer := &template.Template{Entity: ctrlplane.NewEntity(id.PrefixTemplate), TenantID: "alpha", Name: "Inserted after first page"}

					newer.CreatedAt = stamp.Add(time.Hour)
					if err := cp.Store().InsertTemplate(context.Background(), newer); err != nil {
						t.Fatal(err)
					}

					if err := cp.Store().DeleteTemplate(context.Background(), "alpha", page.Items[len(page.Items)-1].ID); err != nil {
						t.Fatal(err)
					}
				}

				if page.NextCursor == "" {
					break
				}

				if page.NextCursor == cursor {
					t.Fatal("cursor did not advance")
				}

				cursor = page.NextCursor
			}

			if len(seen) != 5 {
				t.Fatalf("continuation skipped rows: %d", len(seen))
			}

			invalid := request(t, h, principal("alpha", false), dash.KindQuery, "templates.list", map[string]any{"cursor": "invalid"})
			if invalid.OK || invalid.Error.Code != dash.CodeBadRequest {
				t.Fatal("malformed cursor was ignored")
			}
		})
	}
}
