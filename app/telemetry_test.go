package app

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"

	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/deploy"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/internal/obs"
	"github.com/xraph/ctrlplane/internal/obs/obstest"
	"github.com/xraph/ctrlplane/observability"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/store/memory"
)

// telemetryProvider is an asynchronous provider: it implements
// ReadinessWaiter, so every provision and deploy is followed by a status
// poll. Start fails with a message that quotes a tenant's email.
type telemetryProvider struct{}

func (telemetryProvider) Info() provider.ProviderInfo         { return provider.ProviderInfo{Name: "fake"} }
func (telemetryProvider) Capabilities() []provider.Capability { return nil }

func (telemetryProvider) ReadinessPolicy() provider.ReadinessPolicy {
	return provider.ReadinessPolicy{Timeout: time.Second, Interval: time.Millisecond}
}

func (telemetryProvider) Provision(context.Context, provider.ProvisionRequest) (*provider.ProvisionResult, error) {
	return &provider.ProvisionResult{ProviderRef: "ref"}, nil
}

func (telemetryProvider) Deprovision(context.Context, id.ID) error { return nil }
func (telemetryProvider) Stop(context.Context, id.ID) error        { return nil }
func (telemetryProvider) Restart(context.Context, id.ID) error     { return nil }

func (telemetryProvider) Start(context.Context, id.ID) error {
	return errors.New("quota exhausted for alice@example.com")
}

func (telemetryProvider) Status(context.Context, id.ID) (*provider.InstanceStatus, error) {
	return &provider.InstanceStatus{State: provider.StateRunning, Ready: true}, nil
}

func (telemetryProvider) Deploy(context.Context, provider.DeployRequest) (*provider.DeployResult, error) {
	return &provider.DeployResult{}, nil
}

func (telemetryProvider) Rollback(context.Context, id.ID, id.ID) error { return nil }

func (telemetryProvider) Scale(context.Context, id.ID, provider.ResourceSpec) error { return nil }

func (telemetryProvider) Resources(context.Context, id.ID) (*provider.ResourceUsage, error) {
	return &provider.ResourceUsage{}, nil
}

func (telemetryProvider) Logs(context.Context, id.ID, provider.LogOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (telemetryProvider) Exec(context.Context, id.ID, provider.ExecRequest) (*provider.ExecResult, error) {
	return &provider.ExecResult{}, nil
}

func TestTelemetryCoversAnInstanceLifecycle(t *testing.T) {
	rec := obstest.Record(t)
	sink := obstest.NewSink()

	cp, err := New(
		WithStore(memory.New()),
		WithProvider("alice-cloud", telemetryProvider{}),
		WithDefaultProvider("alice-cloud"),
		WithMetrics(sink.Metrics()),
	)
	if err != nil {
		t.Fatal(err)
	}

	const pii = "alice@example.com"

	ctx := auth.WithClaims(context.Background(), &auth.Claims{SubjectID: pii, TenantID: pii, Roles: []string{"system:admin"}})
	ctx, request := otel.Tracer("test").Start(ctx, "POST /instances")

	inst, err := cp.Instances.Create(ctx, instance.CreateRequest{
		Name:     pii,
		Services: []provider.ServiceSpec{{Name: "web", Image: "nginx"}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := cp.Deploys.Deploy(ctx, deploy.DeployRequest{
		InstanceID: inst.ID,
		Services:   []provider.ServiceDeploySpec{{Name: "web", Image: "nginx:2"}},
	}); err != nil {
		t.Fatalf("Deploy: %v", err)
	}

	if err := cp.Instances.Stop(ctx, inst.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if err := cp.Instances.Start(ctx, inst.ID); err == nil {
		t.Fatal("Start should fail")
	}

	request.End()

	requestID := request.SpanContext().SpanID()

	// Provider calls made straight from a service hang off the request.
	for _, name := range []string{"render deployment source", "provision instance", "execute deployment", "stop instance", "start instance"} {
		spans := obstest.Named(rec, name)
		if len(spans) != 1 || spans[0].Parent().SpanID() != requestID {
			t.Fatalf("%s: want one child of the request, got %d", name, len(spans))
		}
	}

	waits := obstest.Named(rec, "wait for instance readiness")
	if len(waits) != 2 {
		t.Fatalf("want a readiness wait after provision and after deploy, got %d", len(waits))
	}

	for _, w := range waits {
		if obstest.Attrs(w)["ctrlplane.readiness.outcome"] != provider.ReadinessReady {
			t.Fatalf("readiness wait attributes = %v", obstest.Attrs(w))
		}
	}

	for _, s := range obstest.Named(rec, "read instance status") {
		if s.Parent().SpanID() != waits[0].SpanContext().SpanID() && s.Parent().SpanID() != waits[1].SpanContext().SpanID() {
			t.Fatal("a status poll is not under its readiness wait")
		}
	}

	rollout := obstest.Named(rec, "execute deployment")[0]

	deploys := obstest.Named(rec, "deploy instance")
	if len(deploys) != 1 || deploys[0].Parent().SpanID() != rollout.SpanContext().SpanID() {
		t.Fatal("the provider deploy is not under the rollout")
	}

	if a := obstest.Attrs(rollout); a["ctrlplane.deployment.strategy"] != "rolling" || a["ctrlplane.provider.type"] != "fake" ||
		a["ctrlplane.instance.id"] != inst.ID.String() {
		t.Fatalf("rollout attributes = %v", a)
	}

	start := obstest.Named(rec, "start instance")[0]
	if start.Status().Code != codes.Error || start.Status().Description != "*errors.errorString" {
		t.Fatalf("failed start status = %+v", start.Status())
	}

	// No tenant, subject, instance name, registry key or provider error
	// message reaches any span.
	obstest.AssertAbsent(t, rec.Ended(), pii, "alice", "quota exhausted")

	for key, want := range map[string]uint64{
		"ctrlplane_provider_call_duration_seconds{operation=provision,provider=custom,result=ok}": 1,
		"ctrlplane_provider_call_duration_seconds{operation=deploy,provider=custom,result=ok}":    1,
		"ctrlplane_provider_call_duration_seconds{operation=start,provider=custom,result=error}":  1,
		"ctrlplane_readiness_wait_duration_seconds{result=ready}":                                 2,
		"ctrlplane_deployment_duration_seconds{action=deploy,result=ok,strategy=rolling}":         1,
	} {
		if got := sink.HistogramCount(t, key); got != want {
			t.Fatalf("%s = %d, want %d", key, got, want)
		}
	}

	for _, key := range []string{
		"ctrlplane_lifecycle_events_total{event=instance.created}",
		"ctrlplane_lifecycle_events_total{event=deploy.started}",
		"ctrlplane_lifecycle_events_total{event=deploy.succeeded}",
		"ctrlplane_lifecycle_events_total{event=instance.stopped}",
	} {
		if got := sink.Counter(t, key); got != 1 {
			t.Fatalf("%s = %v", key, got)
		}
	}

	// Labels are bounded enums: no series is keyed by anything tenant
	// supplied.
	for _, series := range sink.Series() {
		if strings.Contains(series, "alice") {
			t.Fatalf("series %s carries a tenant value", series)
		}
	}
}

// TestWithMetricsReachesEverySubsystem checks the fan-out itself: every
// subsystem that records metrics ends up holding a sink, and the lifecycle
// event plugin is registered once. A subsystem dropped from wireMetrics
// keeps a nil *obs.Metrics and fails here.
func TestWithMetricsReachesEverySubsystem(t *testing.T) {
	t.Parallel()

	cp, err := New(WithStore(memory.New()), WithMetrics(obstest.NewSink().Metrics()))
	if err != nil {
		t.Fatal(err)
	}

	targets := map[string]any{
		"providers": cp.providers, "scheduler": cp.scheduler, "gc": cp.gc, "bootstraps": cp.Bootstraps,
		"instances": cp.Instances, "deploys": cp.Deploys, "health": cp.Health, "metrics": cp.Metrics,
		"provider health": cp.ProviderHealth, "admin": cp.Admin, "plugins": cp.extensions,
	}

	for name, target := range targets {
		if !holdsSink(target) {
			t.Errorf("%s (%T) did not receive the metrics sink", name, target)
		}
	}

	if n := countMetricsExtensions(cp); n != 1 {
		t.Fatalf("want one lifecycle event plugin, got %d", n)
	}

	// Without WithMetrics nothing holds a sink.
	bare, err := New(WithStore(memory.New()))
	if err != nil {
		t.Fatal(err)
	}

	if holdsSink(bare.Instances) || countMetricsExtensions(bare) != 0 {
		t.Fatal("a control plane built without WithMetrics records metrics")
	}
}

// TestWithMetricsDoesNotDoubleCountAHostPlugin registers the lifecycle
// event plugin by hand as well as passing WithMetrics, and checks each
// event is counted once.
func TestWithMetricsDoesNotDoubleCountAHostPlugin(t *testing.T) {
	t.Parallel()

	sink := obstest.NewSink()

	cp, err := New(
		WithStore(memory.New()),
		WithMetrics(sink.Metrics()),
		WithExtension(observability.NewMetricsExtension(sink.Metrics())),
	)
	if err != nil {
		t.Fatal(err)
	}

	if n := countMetricsExtensions(cp); n != 1 {
		t.Fatalf("want one lifecycle event plugin, got %d", n)
	}

	if err := cp.Events().Publish(context.Background(), event.NewEvent(event.TenantCreated, "")); err != nil {
		t.Fatal(err)
	}

	if got := sink.Counter(t, "ctrlplane_lifecycle_events_total{event=tenant.created}"); got != 1 {
		t.Fatalf("tenant.created counted %v times", got)
	}
}

// holdsSink reports whether target, or the struct it points at, has a
// non-nil *obs.Metrics field.
func holdsSink(target any) bool {
	v := reflect.ValueOf(target)
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return false
		}

		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return false
	}

	want := reflect.TypeFor[*obs.Metrics]()

	for i := range v.NumField() {
		if f := v.Field(i); f.Type() == want && !f.IsNil() {
			return true
		}
	}

	return false
}

func countMetricsExtensions(cp *CtrlPlane) int {
	n := 0

	for _, ext := range cp.extensions.Extensions() {
		if _, ok := ext.(*observability.MetricsExtension); ok {
			n++
		}
	}

	return n
}
