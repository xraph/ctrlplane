package obs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/internal/obs/obstest"
	"github.com/xraph/ctrlplane/managed"
)

type customError struct{ msg string }

func (e *customError) Error() string { return e.msg }

func TestErrorType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"ctrlplane sentinel", fmt.Errorf("get instance alice@example.com: %w", ctrlplane.ErrNotFound), "ctrlplane.ErrNotFound"},
		{"managed sentinel", fmt.Errorf("reserve: %w", managed.ErrConflict), "managed.ErrConflict"},
		{"deadline", fmt.Errorf("call: %w", context.DeadlineExceeded), "context.DeadlineExceeded"},
		{"typed error keeps its type", fmt.Errorf("wrap: %w", &customError{msg: "token=s3cret"}), "*obs.customError"},
		{"plain error", errors.New("dial tcp 10.0.0.1: refused"), "*errors.errorString"},
		{"joined follows first branch", errors.Join(&customError{msg: "x"}, errors.New("y")), "*obs.customError"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := ErrorType(tt.err); got != tt.want {
				t.Fatalf("ErrorType = %q, want %q", got, tt.want)
			}
		})
	}

	extra := Sentinel{Err: errors.New("not ready"), Name: "provider.ErrNotReady"}
	if got := ErrorType(fmt.Errorf("wait: %w", extra.Err), extra); got != extra.Name {
		t.Fatalf("ErrorType with extra = %q", got)
	}
}

func TestStartChildOnlyUnderASpan(t *testing.T) {
	rec := obstest.Record(t)

	ctx, span := StartChild(context.Background(), "orphan")
	span.End()

	if trace.SpanContextFromContext(ctx).IsValid() || len(rec.Ended()) != 0 {
		t.Fatalf("StartChild without a parent recorded %d spans", len(rec.Ended()))
	}

	parentCtx, parent := Start(context.Background(), "parent")
	_, child := StartChild(parentCtx, "child")
	child.End()
	parent.End()

	ended := rec.Ended()
	if len(ended) != 2 || ended[0].Name() != "child" || ended[0].Parent().SpanID() != parent.SpanContext().SpanID() {
		t.Fatalf("child span not parented: %+v", ended)
	}

	if ended[0].InstrumentationScope().Name != InstrumentationName {
		t.Fatalf("scope = %q", ended[0].InstrumentationScope().Name)
	}
}

func TestStartRootAndDetachLeaveTheBootSpan(t *testing.T) {
	rec := obstest.Record(t)

	bootCtx, boot := Start(context.Background(), "start ctrlplane")
	boot.End()

	_, root := StartRoot(bootCtx, "sweep")
	root.End()

	if trace.SpanContextFromContext(Detach(bootCtx)).IsValid() {
		t.Fatal("Detach kept the span")
	}

	ended := rec.Ended()
	if ended[1].Parent().IsValid() || ended[1].SpanContext().TraceID() == boot.SpanContext().TraceID() {
		t.Fatal("StartRoot joined the boot trace")
	}
}

func TestFailKeepsMessagesOffTheSpan(t *testing.T) {
	rec := obstest.Record(t)

	_, span := Start(context.Background(), "op")
	Fail(span, fmt.Errorf("tenant alice@example.com: %w", &customError{msg: "key sk_live_123"}))
	span.End()

	s := rec.Ended()[0]
	if s.Status().Code != codes.Error || s.Status().Description != "*obs.customError" {
		t.Fatalf("status = %+v", s.Status())
	}

	if len(s.Events()) != 1 || s.Events()[0].Name != "exception" {
		t.Fatalf("events = %+v", s.Events())
	}

	ev := s.Events()[0]
	if len(ev.Attributes) != 1 || ev.Attributes[0].Key != "exception.type" {
		t.Fatalf("exception event attributes = %v", ev.Attributes)
	}

	obstest.AssertAbsent(t, rec.Ended(), "alice@example.com", "sk_live_123")
}

func TestTransport(t *testing.T) {
	rec := obstest.Record(t)

	// forge installs tracecontext plus baggage; Record restores the
	// previous propagator when the test ends.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	var gotTraceparent, gotBaggage []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTraceparent = append(gotTraceparent, r.Header.Get("Traceparent"))
		gotBaggage = append(gotBaggage, r.Header.Get("Baggage"))

		if strings.HasSuffix(r.URL.Path, "/broken") {
			w.WriteHeader(http.StatusBadGateway)
		}
	}))
	t.Cleanup(srv.Close)

	client := &http.Client{Transport: Transport(nil)}

	do := func(ctx context.Context, path string) {
		t.Helper()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		_ = resp.Body.Close()
	}

	// No span in the context: the request goes out untouched.
	do(context.Background(), "/api/v1/namespaces/tenant-alice/pods/web")

	if len(rec.Ended()) != 0 || gotTraceparent[0] != "" {
		t.Fatal("an untraced request was traced")
	}

	// A client's inbound baggage rides on the request context.
	member, err := baggage.NewMember("user_id", "alice")
	if err != nil {
		t.Fatal(err)
	}

	bag, err := baggage.New(member)
	if err != nil {
		t.Fatal(err)
	}

	ctx, parent := Start(baggage.ContextWithBaggage(context.Background(), bag), "deploy instance")
	do(ctx, "/api/v1/namespaces/tenant-alice/pods/web")
	do(ctx, "/broken")
	parent.End()

	if gotTraceparent[1] == "" {
		t.Fatal("traceparent was not injected under a span")
	}

	if slices.ContainsFunc(gotBaggage, func(b string) bool { return b != "" }) {
		t.Fatalf("inbound baggage reached the upstream: %q", gotBaggage)
	}

	ended := rec.Ended()
	if len(ended) != 3 {
		t.Fatalf("want 2 client spans and the parent, got %d", len(ended))
	}

	ok, broken := ended[0], ended[1]
	if ok.Name() != http.MethodGet || ok.SpanKind() != trace.SpanKindClient || ok.Parent().SpanID() != parent.SpanContext().SpanID() {
		t.Fatalf("client span = %q kind %v", ok.Name(), ok.SpanKind())
	}

	attrs := map[attribute.Key]attribute.Value{}
	for _, kv := range ok.Attributes() {
		attrs[kv.Key] = kv.Value
	}

	if attrs["http.request.method"].AsString() != http.MethodGet || attrs["http.response.status_code"].AsInt64() != http.StatusOK ||
		attrs["server.address"].AsString() != "127.0.0.1" || attrs["server.port"].AsInt64() == 0 {
		t.Fatalf("client span attributes = %v", ok.Attributes())
	}

	obstest.AssertAbsent(t, ended, "tenant-alice", "/pods/")

	if broken.Status().Code != codes.Error || broken.Status().Description != "502" {
		t.Fatalf("5xx status = %+v", broken.Status())
	}

	if Transport(Transport(nil)) == nil {
		t.Fatal("Transport of a Transport returned nil")
	}
}

func TestMetricsRecordLabelledSeries(t *testing.T) {
	f := obstest.NewSink()
	m := NewMetrics(f.Metrics())

	m.WorkerRun("gc", 2*time.Second, nil)
	m.WorkerRun("gc", time.Second, errors.New("x"))
	m.WorkerSkipped("reconciler")
	m.LeaderCheck(true, nil)
	m.LeaderCheck(false, errors.New("lock"))
	m.BootstrapAction(ActionProvision, nil)
	m.GCInstanceReaped(nil)
	m.DeploymentFinished("rolling", ActionDeploy, time.Second, nil)
	m.ProviderHealthChecked("docker", HealthUnhealthy, time.Millisecond)
	m.ResourceSampled(SampleEmpty)
	m.SetResourcePollers(3)
	m.LifecycleEvent("instance.created")
	m.PluginHookFailed("OnInstanceCreated")

	_, call := m.StartProviderCall(context.Background(), "docker", OpProvision)
	call.End(nil)

	if got := f.HistogramCount(t, "ctrlplane_worker_run_duration_seconds{result=ok,worker=gc}"); got != 1 {
		t.Fatalf("ok runs = %d", got)
	}

	if got := f.HistogramCount(t, "ctrlplane_worker_run_duration_seconds{result=error,worker=gc}"); got != 1 {
		t.Fatalf("error runs = %d", got)
	}

	for key, want := range map[string]float64{
		"ctrlplane_worker_runs_skipped_total{worker=reconciler}":        1,
		"ctrlplane_leader_checks_total{result=leader}":                  1,
		"ctrlplane_leader_checks_total{result=error}":                   1,
		"ctrlplane_bootstrap_actions_total{action=provision,result=ok}": 1,
		"ctrlplane_gc_instances_reaped_total{result=ok}":                1,
		"ctrlplane_resource_samples_total{result=empty}":                1,
		"ctrlplane_lifecycle_events_total{event=instance.created}":      1,
		"ctrlplane_plugin_hook_errors_total{hook=OnInstanceCreated}":    1,
	} {
		if got := f.Counter(t, key); got != want {
			t.Fatalf("%s = %v, want %v", key, got, want)
		}
	}

	for _, key := range []string{
		"ctrlplane_deployment_duration_seconds{action=deploy,result=ok,strategy=rolling}",
		"ctrlplane_provider_health_check_duration_seconds{provider=docker,result=unhealthy}",
		"ctrlplane_provider_call_duration_seconds{operation=provision,provider=docker,result=ok}",
	} {
		if got := f.HistogramCount(t, key); got != 1 {
			t.Fatalf("%s count = %d", key, got)
		}
	}

	if got := f.Gauge(t, "ctrlplane_scheduler_leader{}"); got != 0 {
		t.Fatalf("leader gauge after an election error = %v", got)
	}

	if got := f.Gauge(t, "ctrlplane_resource_pollers{}"); got != 3 {
		t.Fatalf("pollers gauge = %v", got)
	}
}

func TestProviderLabelIsBounded(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"docker": "docker", "kubernetes": "kubernetes", "nomad": "nomad",
		"cloudrun": ProviderCustom, "acme-cloud-connection-7": ProviderCustom, "": ProviderCustom,
	} {
		if got := ProviderLabel(in); got != want {
			t.Fatalf("ProviderLabel(%q) = %q, want %q", in, got, want)
		}
	}

	f := obstest.NewSink()
	m := NewMetrics(f.Metrics())

	_, call := m.StartProviderCall(context.Background(), "acme-cloud", OpDeploy)
	call.End(nil)
	m.ProviderHealthChecked("acme-cloud", HealthHealthy, time.Millisecond)

	for _, key := range []string{
		"ctrlplane_provider_call_duration_seconds{operation=deploy,provider=custom,result=ok}",
		"ctrlplane_provider_health_check_duration_seconds{provider=custom,result=healthy}",
	} {
		if got := f.HistogramCount(t, key); got != 1 {
			t.Fatalf("%s = %d", key, got)
		}
	}
}

func TestProviderCallsDropInboundBaggage(t *testing.T) {
	t.Parallel()

	member, err := baggage.NewMember("user_id", "alice")
	if err != nil {
		t.Fatal(err)
	}

	bag, err := baggage.New(member)
	if err != nil {
		t.Fatal(err)
	}

	ctx, call := NewMetrics(nil).StartProviderCall(baggage.ContextWithBaggage(context.Background(), bag), "cloudrun", OpProvision)
	defer call.End(nil)

	if n := baggage.FromContext(ctx).Len(); n != 0 {
		t.Fatalf("the provider call context kept %d baggage members", n)
	}
}

type notFoundError struct{}

func (notFoundError) Error() string { return "no such container" }
func (notFoundError) NotFound()     {}

func TestIsNotFound(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		err  error
		want bool
	}{
		{fmt.Errorf("status: %w", ctrlplane.ErrNotFound), true},
		{fmt.Errorf("inspect: %w", notFoundError{}), true},
		{errors.New("service not found"), false},
		{context.DeadlineExceeded, false},
	} {
		if got := IsNotFound(tt.err); got != tt.want {
			t.Fatalf("IsNotFound(%v) = %v", tt.err, got)
		}
	}
}

func TestNilMetricsRecordNothing(t *testing.T) {
	t.Parallel()

	var m *Metrics

	if NewMetrics(nil) != nil {
		t.Fatal("NewMetrics(nil) built a sink")
	}

	m.WorkerRun("gc", time.Second, nil)
	m.LeaderCheck(true, nil)
	m.SetLeader(true)
	m.SetResourcePollers(1)

	_, call := m.StartProviderCall(context.Background(), "docker", OpStart)
	call.End(errors.New("boom"))
}

func TestTracedAndMethodSpanName(t *testing.T) {
	rec := obstest.Record(t)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1.47/containers/inst_123/start", nil)
	if Traced(req) {
		t.Fatal("an untraced request reported traced")
	}

	ctx, span := Start(context.Background(), "start instance")
	defer span.End()

	if !Traced(req.WithContext(ctx)) {
		t.Fatal("a traced request reported untraced")
	}

	if got := MethodSpanName("", req); got != http.MethodPost {
		t.Fatalf("MethodSpanName = %q", got)
	}

	_ = rec
}

func TestRedactedTracerProviderKeepsOnlyTheAllowlist(t *testing.T) {
	rec := obstest.Record(t)

	tracer := RedactedTracerProvider().Tracer("go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp")

	_, span := tracer.Start(context.Background(), http.MethodGet,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("http.request.method", http.MethodGet),
			attribute.String("url.full", "http://docker/v1.47/images/registry.example.com/alice/app/json?token=hunter2"),
		))
	span.SetAttributes(attribute.String("error.type", "*url.Error"), attribute.String("url.path", "/containers/alice"))
	span.RecordError(&customError{msg: "dial http://docker/v1.47/containers/alice/json"})
	span.AddEvent("read", trace.WithAttributes(attribute.String("url.query", "token=hunter2")))
	span.SetStatus(codes.Error, `Get "http://docker/v1.47/containers/alice/json": dial tcp: refused`)
	span.End()

	s := rec.Ended()[0]
	if s.SpanKind() != trace.SpanKindClient || s.Status().Code != codes.Error || s.Status().Description != "*url.Error" {
		t.Fatalf("span = kind %v status %+v", s.SpanKind(), s.Status())
	}

	attrs := obstest.Attrs(s)
	if len(attrs) != 2 || attrs["http.request.method"] != http.MethodGet || attrs["error.type"] != "*url.Error" {
		t.Fatalf("attributes = %v", attrs)
	}

	obstest.AssertAbsent(t, rec.Ended(), "alice", "hunter2", "registry.example.com", "/v1.47")
}
