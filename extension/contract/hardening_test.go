package contract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"

	dash "github.com/xraph/forge/extensions/dashboard/contract"
	"github.com/xraph/forge/extensions/dashboard/contract/dispatcher"
	"github.com/xraph/forge/extensions/dashboard/contract/transport"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/app"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/template"
)

func TestLazyReadinessAndResolution(t *testing.T) {
	cp, _ := harness(t)

	var current *app.CtrlPlane

	ready := false

	d, reg, wreg := dispatcher.New(dispatcher.NoopMetricsEmitter{}), dash.NewRegistry(), dash.NewWardenRegistry()
	if err := RegisterWithResolver(d, reg, wreg, Deps{ControlPlane: func() *app.CtrlPlane { return current }, Ready: func() bool { return ready }}); err != nil {
		t.Fatal(err)
	}

	h := transport.NewHandler(reg, wreg, d, nil)

	for _, stage := range []string{"uninitialized", "not started", "ready", "stopped"} {
		switch stage {
		case "not started":
			current = cp
		case "ready":
			ready = true
		case "stopped":
			ready = false
		}

		got := request(t, h, principal("alpha", false), dash.KindQuery, "instances.list", struct{}{})
		if stage == "ready" {
			if !got.OK {
				t.Fatalf("ready query failed: %+v", got.Error)
			}

			continue
		}

		if got.OK || got.Error.Code != dash.CodeUnavailable || !got.Error.Retryable {
			t.Fatalf("%s must be retryable unavailable: %+v", stage, got.Error)
		}
	}
}
func TestBindingValidation(t *testing.T) {
	for _, test := range []struct {
		name    string
		intents []dash.Intent
		bound   map[string]binding
	}{
		{"missing", []dash.Intent{{Name: "a", Kind: dash.IntentKindQuery, Version: 1}}, map[string]binding{}},
		{"kind", []dash.Intent{{Name: "a", Kind: dash.IntentKindCommand, Version: 1}}, map[string]binding{"a": {kind: dash.KindQuery}}},
		{"version", []dash.Intent{{Name: "a", Kind: dash.IntentKindQuery, Version: 2}}, map[string]binding{"a": {kind: dash.KindQuery}}},
		{"unknown dependency", []dash.Intent{{Name: "a", Kind: dash.IntentKindCommand, Version: 1, Invalidates: []string{"missing"}}}, map[string]binding{"a": {kind: dash.KindCommand}}},
		{"command dependency", []dash.Intent{{Name: "a", Kind: dash.IntentKindCommand, Version: 1, Invalidates: []string{"a"}}}, map[string]binding{"a": {kind: dash.KindCommand}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateBindings(test.intents, &bindings{handlers: test.bound}); err == nil {
				t.Fatal("accepted invalid contract")
			}
		})
	}
}
func TestErrorsDoNotExposeBackendDetails(t *testing.T) {
	for _, cause := range []error{errors.New("password=private-marker"), fmt.Errorf("postgres://private-marker: %w", ctrlplane.ErrNotFound), fmt.Errorf("provider token private-marker: %w", ctrlplane.ErrProviderUnavail)} {
		got := contractError(cause)
		if strings.Contains(got.Error(), "private-marker") {
			t.Fatal("wire error exposes backend detail")
		}
	}

	cp, h := harness(t)
	cp.Templates = failingTemplates{Service: cp.Templates}

	got := request(t, h, principal("alpha", false), dash.KindQuery, "templates.list", struct{}{})
	if got.OK || got.Error.Code != dash.CodeInternal || strings.Contains(got.Error.Message, "private-marker") {
		t.Fatal("HTTP error exposes backend detail")
	}

	var diagnostic bytes.Buffer

	b := &bindings{deps: Deps{Logger: slog.New(slog.NewTextHandler(&diagnostic, nil))}}
	_ = b.failure(context.Background(), "templates.list", errors.New("diagnostic-marker"))

	if !bytes.Contains(diagnostic.Bytes(), []byte("diagnostic-marker")) {
		t.Fatal("missing server diagnostic")
	}
}

type failingTemplates struct{ template.Service }

func (f failingTemplates) List(context.Context, template.ListOptions) (*template.ListResult, error) {
	return nil, errors.New("database password private-marker")
}
func TestCommandInvalidationIsSpecific(t *testing.T) {
	_, h := harness(t)

	got := request(t, h, principal("alpha", false), dash.KindCommand, "templates.create", map[string]any{"name": "Blueprint", "services": []map[string]any{{"name": "api", "image": "orders:1", "role": "main"}}})
	if !got.OK {
		t.Fatalf("create: %+v", got.Error)
	}

	for _, want := range []string{"templates.list", "templates.detail", "audit.list", "events.list"} {
		if !slices.Contains(got.Meta.Invalidates, want) {
			t.Fatalf("missing %s", want)
		}
	}

	for _, unrelated := range []string{"instances.list", "workers.list", "config.detail", "providers.list"} {
		if slices.Contains(got.Meta.Invalidates, unrelated) {
			t.Fatalf("unrelated invalidation %s", unrelated)
		}
	}
}

// noProbeProvider supplies metadata without pretending to support connectivity checks.
type noProbeProvider struct{ provider.Provider }

func (noProbeProvider) Info() provider.ProviderInfo         { return provider.ProviderInfo{Name: "no-probe"} }
func (noProbeProvider) Capabilities() []provider.Capability { return nil }

func TestProviderWithoutProbeStaysUnknown(t *testing.T) {
	cp, h := harness(t, app.WithProvider("no-probe", noProbeProvider{}))
	cp.ProviderHealth.CheckNow(context.Background())

	got := request(t, h, principal("alpha", true), dash.KindQuery, "providers.list", struct{}{})
	if !got.OK || !bytes.Contains(got.Data, []byte(`"healthy":null`)) {
		t.Fatalf("synthetic health observation: %s", got.Data)
	}

	tested := request(t, h, principal("alpha", true), dash.KindCommand, "providers.test", map[string]any{"name": "no-probe"})
	if tested.OK || tested.Error.Code != dash.CodeUnavailable {
		t.Fatal("unsupported probe returned success")
	}
}
