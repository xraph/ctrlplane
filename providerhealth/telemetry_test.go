package providerhealth

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"

	"github.com/xraph/ctrlplane/internal/obs/obstest"
	"github.com/xraph/ctrlplane/provider"
)

func TestScheduledSweepIsARootTrace(t *testing.T) {
	rec := obstest.Record(t)
	sink := obstest.NewSink()

	registry := provider.NewRegistry()
	// Registry keys can name a customer's cloud connection; only the
	// provider type may reach a span or a label.
	registry.Register("alice-prod-cluster", &fakeProvider{name: "kubernetes", healthy: true})
	registry.Register("alice-staging", &fakeProvider{name: "docker", healthy: false, message: "daemon at alice@example.com down"})
	registry.Register("alice-broken", &fakeProvider{name: "nomad", err: errors.New("token hunter2 expired")})

	cache := NewCache(registry, Config{PollInterval: time.Hour, CheckTimeout: time.Second})
	cache.SetMetrics(sink.Metrics())

	bootCtx, boot := otel.Tracer("test").Start(context.Background(), "start ctrlplane")
	boot.End()

	cache.sweepRoot(bootCtx)

	roots := obstest.Named(rec, "check providers")
	if len(roots) != 1 || roots[0].Parent().IsValid() || roots[0].SpanContext().TraceID() == boot.SpanContext().TraceID() {
		t.Fatal("the scheduled sweep is not its own root")
	}

	attrs := obstest.Attrs(roots[0])
	if attrs["ctrlplane.providers.checked"] != "3" || attrs["ctrlplane.providers.unhealthy"] != "2" {
		t.Fatalf("sweep attributes = %v", attrs)
	}

	checks := obstest.Named(rec, "check provider health")
	if len(checks) != 3 {
		t.Fatalf("want 3 provider checks, got %d", len(checks))
	}

	for _, s := range checks {
		if s.Parent().SpanID() != roots[0].SpanContext().SpanID() {
			t.Fatal("a provider check is not under the sweep")
		}

		a := obstest.Attrs(s)

		switch a["ctrlplane.provider.type"] {
		case "kubernetes":
			if s.Status().Code == codes.Error || a["ctrlplane.provider.healthy"] != "true" {
				t.Fatalf("healthy check = %v %v", s.Status(), a)
			}
		case "docker":
			if s.Status().Description != "unhealthy" {
				t.Fatalf("unhealthy check status = %+v", s.Status())
			}
		case "nomad":
			if s.Status().Description != "*errors.errorString" {
				t.Fatalf("failed check status = %+v", s.Status())
			}
		default:
			t.Fatalf("unexpected provider type %q", a["ctrlplane.provider.type"])
		}
	}

	obstest.AssertAbsent(t, rec.Ended(), "alice", "hunter2")

	for key, want := range map[string]uint64{
		"ctrlplane_provider_health_check_duration_seconds{provider=kubernetes,result=healthy}": 1,
		"ctrlplane_provider_health_check_duration_seconds{provider=docker,result=unhealthy}":   1,
		"ctrlplane_provider_health_check_duration_seconds{provider=nomad,result=error}":        1,
	} {
		if got := sink.HistogramCount(t, key); got != want {
			t.Fatalf("%s = %d, want %d", key, got, want)
		}
	}
}

func TestSweepWithNoProbeTracesNothing(t *testing.T) {
	rec := obstest.Record(t)

	registry := provider.NewRegistry()
	registry.Register("plain", &plainProvider{})

	cache := NewCache(registry, DefaultConfig())
	cache.sweepRoot(context.Background())

	if n := len(rec.Ended()); n != 0 {
		t.Fatalf("a sweep with nothing to probe recorded %d spans", n)
	}
}

func TestCheckNowNestsUnderTheCaller(t *testing.T) {
	rec := obstest.Record(t)

	registry := provider.NewRegistry()
	registry.Register("a", &fakeProvider{name: "docker", healthy: true})

	cache := NewCache(registry, DefaultConfig())

	ctx, parent := otel.Tracer("test").Start(context.Background(), "POST /providers/refresh")
	cache.CheckNow(ctx)
	parent.End()

	sweeps := obstest.Named(rec, "check providers")
	if len(sweeps) != 1 || sweeps[0].Parent().SpanID() != parent.SpanContext().SpanID() {
		t.Fatal("CheckNow did not nest under the caller's span")
	}
}
