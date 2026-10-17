package worker

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/bootstrap"
	"github.com/xraph/ctrlplane/datacenter"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/internal/obs/obstest"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/store/memory"
)

// telProvider provisions everything, except that a provision for a spec
// named "broken" fails with a message that quotes a secret.
type telProvider struct{}

func (telProvider) Info() provider.ProviderInfo              { return provider.ProviderInfo{Name: "fake"} }
func (telProvider) Capabilities() []provider.Capability      { return nil }
func (telProvider) Deprovision(context.Context, id.ID) error { return nil }
func (telProvider) Start(context.Context, id.ID) error       { return nil }
func (telProvider) Stop(context.Context, id.ID) error        { return nil }
func (telProvider) Restart(context.Context, id.ID) error     { return nil }
func (telProvider) Rollback(context.Context, id.ID, id.ID) error {
	return nil
}

func (telProvider) Scale(context.Context, id.ID, provider.ResourceSpec) error { return nil }

func (telProvider) Provision(_ context.Context, req provider.ProvisionRequest) (*provider.ProvisionResult, error) {
	if req.Name == "broken" {
		return nil, errors.New("registry token=hunter2 rejected for alice@example.com")
	}

	return &provider.ProvisionResult{ProviderRef: "ref"}, nil
}

func (telProvider) Status(context.Context, id.ID) (*provider.InstanceStatus, error) {
	return &provider.InstanceStatus{Ready: true}, nil
}

func (telProvider) Deploy(context.Context, provider.DeployRequest) (*provider.DeployResult, error) {
	return &provider.DeployResult{}, nil
}

func (telProvider) Resources(context.Context, id.ID) (*provider.ResourceUsage, error) {
	return &provider.ResourceUsage{}, nil
}

func (telProvider) Logs(context.Context, id.ID, provider.LogOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (telProvider) Exec(context.Context, id.ID, provider.ExecRequest) (*provider.ExecResult, error) {
	return &provider.ExecResult{}, nil
}

// bootCtx returns a context carrying an ended span, the way Forge hands
// Start a context holding the boot span.
func bootCtx(t *testing.T) (context.Context, trace.Span) {
	t.Helper()

	ctx, span := otel.Tracer("test").Start(context.Background(), "start ctrlplane")
	span.End()

	return ctx, span
}

func TestReconcilerTracesAPassAsItsOwnRoot(t *testing.T) {
	rec := obstest.Record(t)
	sink := obstest.NewSink()

	store := memory.New()
	registry := provider.NewRegistry()
	registry.Register("edge-cluster-alice", telProvider{})

	// A platform datacenter (no tenant), so the reconciler's cross
	// tenant list sees it. Its name still must not reach a span.
	dc := &datacenter.Datacenter{
		Entity:       ctrlplane.NewEntity(id.PrefixDatacenter),
		Name:         "alice@example.com",
		ProviderName: "edge-cluster-alice",
		BootstrapServices: []bootstrap.BootstrapServiceSpec{
			{Name: "ingress", Services: []provider.ServiceSpec{{Name: "proxy", Image: "traefik"}}},
			{Name: "broken", Services: []provider.ServiceSpec{{Name: "x", Image: "x"}}},
		},
	}
	if err := store.InsertDatacenter(context.Background(), dc); err != nil {
		t.Fatal(err)
	}

	bootstraps := bootstrap.NewService(store, registry, nil, event.NewInMemoryBus())
	bootstraps.SetMetrics(sink.Metrics())

	r := NewReconciler(store, store, bootstraps, registry, event.NewInMemoryBus(), time.Minute)

	ctx, boot := bootCtx(t)
	if err := r.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	roots := obstest.Named(rec, "reconcile datacenters")
	if len(roots) != 1 {
		t.Fatalf("want one reconcile root, got %d", len(roots))
	}

	root := roots[0]
	if root.Parent().IsValid() || root.SpanContext().TraceID() == boot.SpanContext().TraceID() {
		t.Fatal("the reconcile pass joined the boot trace")
	}

	perDC := obstest.Named(rec, "reconcile datacenter")
	if len(perDC) != 1 || perDC[0].Parent().SpanID() != root.SpanContext().SpanID() {
		t.Fatal("reconcile datacenter is not a child of the pass")
	}

	attrs := obstest.Attrs(perDC[0])
	if attrs["ctrlplane.datacenter.id"] != dc.ID.String() || attrs["ctrlplane.bootstrap.provisioned"] != "1" ||
		attrs["ctrlplane.bootstrap.failed"] != "1" || attrs["ctrlplane.bootstrap.desired"] != "2" {
		t.Fatalf("reconcile datacenter attributes = %v", attrs)
	}

	provisions := obstest.Named(rec, "provision instance")
	if len(provisions) != 2 {
		t.Fatalf("want 2 provider provision spans, got %d", len(provisions))
	}

	failed := obstest.Named(rec, "provision bootstrap workload")

	var sawFailure bool

	for _, s := range failed {
		if s.Status().Code == codes.Error {
			sawFailure = true

			if s.Status().Description != "*errors.errorString" {
				t.Fatalf("failure description = %q", s.Status().Description)
			}
		}
	}

	if !sawFailure {
		t.Fatal("the failed provision was not marked")
	}

	// The registry key names a customer's connection and the datacenter
	// a tenant; neither, nor the provider's error message, is on a span.
	obstest.AssertAbsent(t, rec.Ended(), "alice", "hunter2")

	if got := sink.Counter(t, "ctrlplane_datacenter_reconciles_total{result=ok}"); got != 1 {
		t.Fatalf("datacenter reconciles = %v", got)
	}

	if got := sink.Counter(t, "ctrlplane_bootstrap_actions_total{action=provision,result=error}"); got != 1 {
		t.Fatalf("failed bootstrap provisions = %v", got)
	}

	if got := sink.HistogramCount(t, "ctrlplane_provider_call_duration_seconds{operation=provision,provider=custom,result=ok}"); got != 1 {
		t.Fatalf("provider provision calls = %d", got)
	}
}

func TestReconcilerWithNothingToDoTracesNothing(t *testing.T) {
	rec := obstest.Record(t)

	store := memory.New()
	registry := provider.NewRegistry()
	r := NewReconciler(store, store, bootstrap.NewService(store, registry, nil, nil), registry, event.NewInMemoryBus(), time.Minute)

	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if n := len(rec.Ended()); n != 0 {
		t.Fatalf("an empty pass recorded %d spans", n)
	}
}

func TestGCTracesASweepAndCountsReaps(t *testing.T) {
	rec := obstest.Record(t)
	sink := obstest.NewSink()

	tenants := newGCTenantStore(1)
	tid := tenants.tenantIDs()[0]

	orphan := func() *instance.Instance {
		inst := &instance.Instance{
			Entity:   ctrlplane.NewEntity(id.PrefixInstance),
			TenantID: tid,
			Labels:   map[string]string{workloadLabelKey: id.New(id.PrefixWorkload).String()},
		}
		inst.CreatedAt = time.Now().Add(-time.Hour)

		return inst
	}

	reaped, stuck := orphan(), orphan()
	insts := newGCInstances(reaped, stuck)
	insts.deleteErrIDs = map[string]error{stuck.ID.String(): errors.New("provider down for alice@example.com")}

	gc := NewGarbageCollector(tenants, insts, newGCWorkloadStore(), event.NewInMemoryBus(), time.Minute, GCConfig{})
	gc.SetMetrics(sink.Metrics())

	ctx, boot := bootCtx(t)
	if err := gc.Run(ctx); err != nil {
		t.Fatal(err)
	}

	roots := obstest.Named(rec, "sweep orphan instances")
	if len(roots) != 1 || roots[0].Parent().IsValid() || roots[0].SpanContext().TraceID() == boot.SpanContext().TraceID() {
		t.Fatal("the sweep is not its own root")
	}

	attrs := obstest.Attrs(roots[0])
	if attrs["ctrlplane.gc.tenants"] != "1" || attrs["ctrlplane.gc.reaped"] != "1" || attrs["ctrlplane.gc.tenants_failed"] != "0" {
		t.Fatalf("sweep attributes = %v", attrs)
	}

	events := roots[0].Events()
	if len(events) != 1 || events[0].Name != "orphan delete failed" {
		t.Fatalf("sweep events = %+v", events)
	}

	obstest.AssertAbsent(t, rec.Ended(), "alice@example.com", tid)

	if got := sink.Counter(t, "ctrlplane_gc_instances_reaped_total{result=ok}"); got != 1 {
		t.Fatalf("reaped ok = %v", got)
	}

	if got := sink.Counter(t, "ctrlplane_gc_instances_reaped_total{result=error}"); got != 1 {
		t.Fatalf("reaped error = %v", got)
	}

	if got := sink.Counter(t, "ctrlplane_gc_tenant_sweeps_total{result=ok}"); got != 1 {
		t.Fatalf("tenant sweeps = %v", got)
	}
}

// signalWorker reports, on its first run, whether its context carried a
// valid span.
type signalWorker struct {
	ran chan bool
	err error
}

func (w *signalWorker) Name() string            { return "signal" }
func (w *signalWorker) Interval() time.Duration { return time.Millisecond }
func (w *signalWorker) Run(ctx context.Context) error {
	select {
	case w.ran <- trace.SpanContextFromContext(ctx).IsValid():
	default:
	}

	return w.err
}

func TestSchedulerTimesRunsOnADetachedContext(t *testing.T) {
	obstest.Record(t)

	sink := obstest.NewSink()
	w := &signalWorker{ran: make(chan bool, 1), err: errors.New("boom")}

	s := NewScheduler()
	s.Register(w)
	s.SetMetrics(sink.Metrics())

	ctx, _ := bootCtx(t)
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}

	if traced := <-w.ran; traced {
		t.Fatal("the worker ran under the boot span")
	}

	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := sink.HistogramCount(t, "ctrlplane_worker_run_duration_seconds{result=error,worker=signal}"); got == 0 {
		t.Fatal("no failed run was timed")
	}

	if got := sink.Gauge(t, "ctrlplane_scheduler_leader{}"); got != 0 {
		t.Fatalf("leader gauge after Stop = %v", got)
	}
}

// signalElector never leads and reports each time it is asked.
type signalElector struct{ asked chan struct{} }

func (e *signalElector) IsLeader(context.Context) (bool, error) {
	select {
	case e.asked <- struct{}{}:
	default:
	}

	return false, nil
}

func (e *signalElector) Resign(context.Context) error { return nil }

func TestSchedulerCountsSkippedTicks(t *testing.T) {
	sink := obstest.NewSink()
	elector := &signalElector{asked: make(chan struct{}, 1)}

	s := NewScheduler()
	s.Register(&signalWorker{ran: make(chan bool, 1)})
	s.SetLeaderElector(elector)
	s.SetMetrics(sink.Metrics())

	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	<-elector.asked

	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := sink.Counter(t, "ctrlplane_worker_runs_skipped_total{worker=signal}"); got == 0 {
		t.Fatal("no skipped tick was counted")
	}

	if got := sink.Counter(t, "ctrlplane_leader_checks_total{result=follower}"); got == 0 {
		t.Fatal("no follower answer was counted")
	}
}

// spanProbeDatacenters records whether each ListDatacenters call ran under
// a valid span.
type spanProbeDatacenters struct {
	datacenter.Store

	traced chan bool
}

func (p *spanProbeDatacenters) ListDatacenters(ctx context.Context, tenantID string, opts datacenter.ListOptions) (*datacenter.ListResult, error) {
	select {
	case p.traced <- trace.SpanContextFromContext(ctx).IsValid():
	default:
	}

	return p.Store.ListDatacenters(ctx, tenantID, opts)
}

// spanProbeTenants records whether each ListTenants call ran under a valid
// span.
type spanProbeTenants struct {
	admin.Store

	traced chan bool
}

func (p *spanProbeTenants) ListTenants(ctx context.Context, opts admin.ListTenantsOptions) (*admin.TenantListResult, error) {
	select {
	case p.traced <- trace.SpanContextFromContext(ctx).IsValid():
	default:
	}

	return p.Store.ListTenants(ctx, opts)
}

// TestIdleTickQueriesLeaveTheBootTrace starts the scheduler under a context
// that carries a span, the way Forge starts an extension, and checks the
// queries each idle tick makes before any root span exists run with no
// valid parent. grove would otherwise trace them under the ended boot span.
func TestIdleTickQueriesLeaveTheBootTrace(t *testing.T) {
	obstest.Record(t)

	store := memory.New()
	registry := provider.NewRegistry()
	datacenters := &spanProbeDatacenters{Store: store, traced: make(chan bool, 1)}
	tenants := &spanProbeTenants{Store: newGCTenantStore(0), traced: make(chan bool, 1)}

	s := NewScheduler()
	s.Register(NewReconciler(store, datacenters, bootstrap.NewService(store, registry, nil, nil), registry, event.NewInMemoryBus(), time.Millisecond))
	s.Register(NewGarbageCollector(tenants, newGCInstances(), newGCWorkloadStore(), event.NewInMemoryBus(), time.Millisecond, GCConfig{}))

	ctx, span := otel.Tracer("test").Start(context.Background(), "start ctrlplane")
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}

	span.End()

	reconcilerTraced, gcTraced := <-datacenters.traced, <-tenants.traced

	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	if reconcilerTraced || gcTraced {
		t.Fatalf("idle tick queries ran under the boot span: reconciler=%v gc=%v", reconcilerTraced, gcTraced)
	}
}
