package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xraph/forge/extensions/dashboard"
	dashauth "github.com/xraph/forge/extensions/dashboard/auth"
	dash "github.com/xraph/forge/extensions/dashboard/contract"
	"github.com/xraph/forge/extensions/dashboard/contract/dispatcher"
	"github.com/xraph/forge/extensions/dashboard/contract/idempotency"
	"github.com/xraph/forge/extensions/dashboard/contract/transport"

	"github.com/xraph/ctrlplane/app"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/store/memory"
	"github.com/xraph/ctrlplane/template"
)

// keyedRequest sends one contract request with the given idempotency key.
func keyedRequest(t *testing.T, h http.Handler, user *dashauth.UserInfo, kind dash.Kind, intent, key string, payload any) testResponse {
	t.Helper()

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(dash.Request{Envelope: "v1", Kind: kind, Contributor: "ctrlplane", Intent: intent, IntentVersion: 1, Payload: data, CSRF: "test", IdempotencyKey: key})
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequestWithContext(dashauth.WithUser(context.Background(), user), http.MethodPost, "/v1", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	var result testResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}

	return result
}

// TestCommandReplayFollowsTheIdempotencyStore pins the forge v1.12.9
// dispatcher behaviour behind the contract: with an idempotency store, a
// command repeated under the same key and payload replays the stored
// response without running again, and the same key with a different
// payload answers CONFLICT. A write sent with the query kind is refused.
func TestCommandReplayFollowsTheIdempotencyStore(t *testing.T) {
	cp, err := app.New(app.WithStore(memory.New()), app.WithAuth(&auth.NoopProvider{}))
	if err != nil {
		t.Fatal(err)
	}

	reg, wreg := dash.NewRegistry(), dash.NewWardenRegistry()
	d := dispatcher.NewWithOptions(dispatcher.NoopMetricsEmitter{},
		dispatcher.WithIdempotencyStore(dashboard.AdaptIdempotencyStore(idempotency.NewInMemoryStore())))

	if err := Register(d, reg, wreg, cp); err != nil {
		t.Fatal(err)
	}

	h := transport.NewHandler(reg, wreg, d, nil)
	user := principal("alpha", false)

	templates := func() int {
		t.Helper()

		ctx := auth.WithClaims(context.Background(), &auth.Claims{SubjectID: "operator", TenantID: "alpha"})

		res, err := cp.Templates.List(ctx, template.ListOptions{Limit: 100})
		if err != nil {
			t.Fatal(err)
		}

		return len(res.Items)
	}

	const key = "replay-key-1"

	payload := func(name string) map[string]any {
		return map[string]any{"name": name, "services": []map[string]any{{"name": "web", "image": "nginx"}}}
	}

	first := keyedRequest(t, h, user, dash.KindCommand, "templates.create", key, payload("Orders"))
	if !first.OK {
		t.Fatalf("first create failed: %+v", first.Error)
	}

	replay := keyedRequest(t, h, user, dash.KindCommand, "templates.create", key, payload("Orders"))
	if !replay.OK || !bytes.Equal(replay.Data, first.Data) {
		t.Fatalf("replay did not return the stored response: %+v %s", replay.Error, replay.Data)
	}

	if n := templates(); n != 1 {
		t.Fatalf("a replay ran the command again: %d templates", n)
	}

	conflict := keyedRequest(t, h, user, dash.KindCommand, "templates.create", key, payload("Billing"))
	if conflict.OK || conflict.Error == nil || conflict.Error.Code != dash.CodeConflict {
		t.Fatalf("a changed payload under a used key = %+v", conflict)
	}

	if n := templates(); n != 1 {
		t.Fatalf("a conflicting replay ran the command: %d templates", n)
	}

	// A write sent with the query kind is refused before it runs.
	asQuery := keyedRequest(t, h, user, dash.KindQuery, "templates.create", "replay-key-2", payload("Shadow"))
	if asQuery.OK || asQuery.Error == nil || asQuery.Error.Code != dash.CodeBadRequest {
		t.Fatalf("a command sent as a query = %+v", asQuery)
	}

	if n := templates(); n != 1 {
		t.Fatalf("a command sent as a query ran: %d templates", n)
	}
}
