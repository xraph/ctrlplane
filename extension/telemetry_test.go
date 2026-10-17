package extension

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/xraph/forge"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/obs"
	"github.com/xraph/ctrlplane/provider"
)

// probeProvider answers health checks and nothing else; the embedded nil
// interface panics on any other call.
type probeProvider struct {
	provider.Provider
}

func (probeProvider) Info() provider.ProviderInfo { return provider.ProviderInfo{Name: "docker"} }

func (probeProvider) HealthCheck(context.Context) (*provider.HealthStatus, error) {
	return &provider.HealthStatus{Healthy: true, CheckedAt: time.Now()}, nil
}

func (probeProvider) Status(context.Context, id.ID) (*provider.InstanceStatus, error) {
	return &provider.InstanceStatus{}, nil
}

func scrape(t *testing.T, app forge.App) string {
	t.Helper()

	p, ok := app.Metrics().(interface{ PrometheusHandler() http.Handler })
	if !ok {
		t.Fatalf("app metrics %T have no Prometheus handler", app.Metrics())
	}

	rr := httptest.NewRecorder()
	p.PrometheusHandler().ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/_/metrics", nil))

	body, err := io.ReadAll(rr.Body)
	if err != nil {
		t.Fatal(err)
	}

	return string(body)
}

// TestExtensionRecordsOnTheForgeApp registers the extension on a real
// forge app and checks ctrlplane's spans reach forge's tracer provider and
// its metrics land on /_/metrics under the forge_ namespace.
func TestExtensionRecordsOnTheForgeApp(t *testing.T) {
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()

	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})

	app := forge.New(forge.WithAppName("ctrlplane-telemetry-test"), forge.WithAppLogger(forge.NewNoopLogger()))

	rec := tracetest.NewSpanRecorder()
	if err := forge.RegisterSpanProcessor(app, rec); err != nil {
		t.Fatalf("RegisterSpanProcessor: %v", err)
	}

	ext := New(WithProvider("customer-cluster-7", probeProvider{}), WithDisableRoutes())
	if err := ext.Register(app); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, span := forge.StartSpan(t.Context(), "POST /ctrlplane/providers/refresh")
	ext.CtrlPlane().ProviderHealth.CheckNow(ctx)
	span.End()

	var checks int

	for _, s := range rec.Ended() {
		if s.InstrumentationScope().Name != obs.InstrumentationName {
			continue
		}

		if s.Name() == "check provider health" {
			checks++
		}
	}

	if checks != 1 {
		t.Fatalf("want one provider health span on forge's provider, got %d", checks)
	}

	body := scrape(t, app)

	var found bool

	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, "forge_ctrlplane_provider_health_check_duration_seconds_count{") &&
			strings.Contains(line, `provider="docker"`) && strings.Contains(line, `result="healthy"`) {
			found = true
		}

		if strings.Contains(line, "customer-cluster-7") {
			t.Fatalf("a registry key reached the scrape: %s", line)
		}
	}

	if !found {
		t.Fatalf("no forge_ctrlplane_provider_health_check_duration_seconds series on the scrape:\n%s", grepCtrlplane(body))
	}
}

func grepCtrlplane(body string) string {
	var out []string

	for line := range strings.SplitSeq(body, "\n") {
		if strings.Contains(line, "ctrlplane") {
			out = append(out, line)
		}
	}

	return strings.Join(out, "\n")
}
