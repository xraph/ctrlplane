package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	dashauth "github.com/xraph/forge/extensions/dashboard/auth"
	dash "github.com/xraph/forge/extensions/dashboard/contract"
	"github.com/xraph/forge/extensions/dashboard/contract/dispatcher"
	"github.com/xraph/forge/extensions/dashboard/contract/transport"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/app"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/store/badger"
)

func harness(t *testing.T, opts ...app.Option) (*app.CtrlPlane, http.Handler) {
	t.Helper()

	s, err := badger.New(badger.Config{Path: t.TempDir(), SyncWrites: true})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})

	cp, err := app.New(append([]app.Option{app.WithStore(s), app.WithAuth(&auth.NoopProvider{})}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}

	reg := dash.NewRegistry()
	wreg := dash.NewWardenRegistry()

	d := dispatcher.New(dispatcher.NoopMetricsEmitter{})
	if err := Register(d, reg, wreg, cp); err != nil {
		t.Fatal(err)
	}

	return cp, transport.NewHandler(reg, wreg, d, nil)
}

func principal(tenant string, admin bool) *dashauth.UserInfo {
	user := &dashauth.UserInfo{Subject: "operator", Scopes: []string{"ctrlplane:read", "ctrlplane:write"}, Claims: map[string]any{"tenant_id": tenant}}
	if admin {
		user.Roles = []string{"system:admin"}
	}

	return user
}

func request(t *testing.T, h http.Handler, user *dashauth.UserInfo, kind dash.Kind, intent string, payload any) dash.Response {
	t.Helper()

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	envelope := dash.Request{Envelope: "v1", Kind: kind, Contributor: "ctrlplane", Intent: intent, IntentVersion: 1, Payload: data, CSRF: "test", IdempotencyKey: id.New(id.PrefixEvent).String()}

	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1", bytes.NewReader(body))
	if user != nil {
		r = r.WithContext(dashauth.WithUser(r.Context(), user))
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	var result dash.Response
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}

	return result
}

func TestPrincipalResolutionRefusesMalformedClaims(t *testing.T) {
	for _, value := range []any{"", nil, 123, []string{"alpha"}} {
		for _, admin := range []bool{false, true} {
			user := principal("alpha", admin)

			user.Claims["tenant_id"] = value
			if _, err := principalContext(context.Background(), dash.PrincipalFor(user)); err == nil {
				t.Fatalf("accepted invalid claim %#v, admin %v", value, admin)
			}
		}
	}

	if _, err := principalContext(context.Background(), dash.Principal{}); err == nil {
		t.Fatal("accepted anonymous principal")
	}

	user := principal("alpha", false)
	delete(user.Claims, "tenant_id")

	if _, err := principalContext(context.Background(), dash.PrincipalFor(user)); err == nil {
		t.Fatal("accepted tenantless non-admin")
	}
}

func TestHTTPIsolationForeignParentAndInvalidation(t *testing.T) {
	cp, h := harness(t)
	ctx := context.Background()
	own := &instance.Instance{Entity: ctrlplane.NewEntity(id.PrefixInstance), TenantID: "alpha", Name: "Alpha", Slug: "alpha", State: provider.StateRunning}

	foreign := &instance.Instance{Entity: ctrlplane.NewEntity(id.PrefixInstance), TenantID: "beta", Name: "Beta", Slug: "beta", State: provider.StateRunning}
	for _, row := range []*instance.Instance{own, foreign} {
		if err := cp.Store().Insert(ctx, row); err != nil {
			t.Fatal(err)
		}
	}

	user := principal("alpha", false)

	list := request(t, h, user, dash.KindQuery, "instances.list", map[string]any{})
	if !list.OK {
		t.Fatal("instance list refused")
	}

	var rows instance.ListResult
	if err := json.Unmarshal(list.Data, &rows); err != nil {
		t.Fatal(err)
	}

	if len(rows.Items) != 1 || rows.Items[0].ID != own.ID {
		t.Fatalf("tenant isolation: %#v", rows.Items)
	}

	for _, intent := range []string{"instances.detail", "domains.list", "secrets.list", "health.detail"} {
		result := request(t, h, user, dash.KindQuery, intent, map[string]any{"id": foreign.ID.String(), "instance_id": foreign.ID.String()})
		if result.OK {
			t.Fatalf("foreign read %s succeeded", intent)
		}
	}

	create := map[string]any{"instance_id": foreign.ID.String(), "path": "/", "port": 8080}
	if request(t, h, user, dash.KindCommand, "routes.create", create).OK {
		t.Fatal("foreign-parent route write succeeded")
	}

	create["instance_id"] = own.ID.String()
	create["service_name"] = "sidecar"
	create["hostname"] = "alpha.example.test"
	create["upstream_origin"] = "https://upstream.example.test"

	result := request(t, h, user, dash.KindCommand, "routes.create", create)
	if !result.OK || !slices.Contains(result.Meta.Invalidates, "routes.list") {
		t.Fatalf("write/invalidation: %#v", result)
	}

	read := request(t, h, user, dash.KindQuery, "routes.list", map[string]any{"instance_id": own.ID.String()})
	if !read.OK || !bytes.Contains(read.Data, []byte(`"service_name":"sidecar"`)) || !bytes.Contains(read.Data, []byte(`"tls_verify":true`)) {
		t.Fatalf("persisted proxy fields: %s", read.Data)
	}
}

func TestHTTPDenialsAndSecretRedaction(t *testing.T) {
	cp, h := harness(t)
	for _, user := range []*dashauth.UserInfo{nil, {Subject: "unsigned-scopes", Claims: map[string]any{"tenant_id": "alpha"}}, principal("alpha", false)} {
		if request(t, h, user, dash.KindQuery, "system.stats", struct{}{}).OK {
			t.Fatal("non-admin global stats succeeded")
		}
	}

	if request(t, h, nil, dash.KindCommand, "templates.create", map[string]any{"name": "anonymous"}).OK {
		t.Fatal("anonymous write succeeded")
	}

	reader := principal("alpha", false)

	reader.Scopes = []string{"ctrlplane:read"}
	if request(t, h, reader, dash.KindCommand, "templates.create", map[string]any{"name": "read-only"}).OK {
		t.Fatal("read-only write succeeded")
	}

	inst := &instance.Instance{Entity: ctrlplane.NewEntity(id.PrefixInstance), TenantID: "alpha", Name: "API", Slug: "api", State: provider.StateRunning}
	if err := cp.Store().Insert(context.Background(), inst); err != nil {
		t.Fatal(err)
	}

	user := principal("alpha", false)
	if !request(t, h, user, dash.KindCommand, "secrets.set", map[string]any{"instance_id": inst.ID.String(), "key": "TOKEN", "value": "never-return-this"}).OK {
		t.Fatal("set secret failed")
	}

	got := request(t, h, user, dash.KindQuery, "secrets.list", map[string]any{"instance_id": inst.ID.String()})
	if !got.OK || bytes.Contains(got.Data, []byte("never-return-this")) {
		t.Fatalf("secret metadata leaks: %s", got.Data)
	}

	if request(t, h, user, dash.KindQuery, "instances.detail", map[string]any{"id": id.New(id.PrefixTemplate).String()}).OK {
		t.Fatal("wrong TypeID prefix accepted")
	}
}

type refusalPolicy struct {
	auth.NoopProvider

	failure bool
}

func (p refusalPolicy) Authorize(context.Context, auth.AuthzRequest) (bool, error) {
	if p.failure {
		return false, errors.New("policy backend unavailable")
	}

	return false, nil
}
func TestConfiguredPolicyRefusalStopsReadsAndWrites(t *testing.T) {
	for _, failure := range []bool{false, true} {
		_, h := harness(t, app.WithAuth(&refusalPolicy{failure: failure}))
		for _, test := range []struct {
			kind   dash.Kind
			intent string
		}{{dash.KindQuery, "instances.list"}, {dash.KindCommand, "workloads.create"}} {
			response := request(t, h, principal("alpha", true), test.kind, test.intent, map[string]any{"name": "Orders"})
			if response.OK {
				t.Fatalf("policy refusal accepted: %+v", response)
			}
		}
	}
}
func TestRegisterRejectsMissingControlPlane(t *testing.T) {
	err := Register(dispatcher.New(dispatcher.NoopMetricsEmitter{}), dash.NewRegistry(), dash.NewWardenRegistry(), nil)
	if err == nil {
		t.Fatal("accepted nil control plane")
	}
}
