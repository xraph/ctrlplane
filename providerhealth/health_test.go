package providerhealth

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// TestCache_SweepCallsHealthCheckPerProvider asserts a single
// CheckNow ticks every registered provider's HealthCheck and
// stores the result in the cache.
func TestCache_SweepCallsHealthCheckPerProvider(t *testing.T) {
	t.Parallel()

	registry := provider.NewRegistry()
	a := &fakeProvider{name: "alpha", healthy: true, message: "alpha ok"}
	b := &fakeProvider{name: "beta", healthy: false, message: "beta down"}

	registry.Register("alpha", a)
	registry.Register("beta", b)

	cache := NewCache(registry, Config{PollInterval: time.Hour, CheckTimeout: 100 * time.Millisecond})
	cache.CheckNow(context.Background())

	if got := a.calls.Load(); got != 1 {
		t.Fatalf("alpha HealthCheck calls: want 1, got %d", got)
	}

	if got := b.calls.Load(); got != 1 {
		t.Fatalf("beta HealthCheck calls: want 1, got %d", got)
	}

	if status, ok := cache.Get("alpha"); !ok || !status.Healthy {
		t.Fatalf("alpha cache entry: ok=%v healthy=%v", ok, status.Healthy)
	}

	if status, ok := cache.Get("beta"); !ok || status.Healthy {
		t.Fatalf("beta cache entry: ok=%v healthy=%v message=%q", ok, status.Healthy, status.Message)
	}
}

// TestCache_NonHealthCheckerProviderReportedAsUnknownButHealthy
// asserts the fallback path: a provider that doesn't implement
// HealthChecker is recorded as Healthy=true with an "unknown"
// message so consumers can distinguish "no probe" from "probe
// passed".
func TestCache_NonHealthCheckerProviderReportedAsUnknownButHealthy(t *testing.T) {
	t.Parallel()

	registry := provider.NewRegistry()
	registry.Register("plain", &plainProvider{})

	cache := NewCache(registry, DefaultConfig())
	cache.CheckNow(context.Background())

	status, ok := cache.Get("plain")
	if !ok {
		t.Fatal("plain provider not in cache")
	}

	if !status.Healthy {
		t.Fatalf("plain provider should default healthy: %+v", status)
	}

	if status.Message == "" {
		t.Fatalf("plain provider should carry a clarifying message")
	}
}

// TestCache_HealthCheckErrorRecordedAsUnhealthy covers the case
// where HealthCheck returns an error (network refused, timeout,
// bad TLS). The cache marks it unhealthy with the error string in
// Message so the dashboard surfaces a useful diagnostic.
func TestCache_HealthCheckErrorRecordedAsUnhealthy(t *testing.T) {
	t.Parallel()

	registry := provider.NewRegistry()
	registry.Register("k8s", &fakeProvider{name: "k8s", err: errors.New("api server unreachable")})

	cache := NewCache(registry, DefaultConfig())
	cache.CheckNow(context.Background())

	status, ok := cache.Get("k8s")
	if !ok {
		t.Fatal("k8s provider not in cache")
	}

	if status.Healthy {
		t.Fatal("k8s provider should be unhealthy after error")
	}

	if status.Message == "" {
		t.Fatal("error message should be carried into Message")
	}
}

// TestCache_SweepBoundedByDeadlineWhenProviderHangs asserts the
// safety property that protects startup: even if a provider's
// HealthCheck blocks indefinitely (ignoring ctx), the sweep
// itself returns within a bounded time. The hung provider gets
// recorded as "did not complete" so the cache is still useful.
func TestCache_SweepBoundedByDeadlineWhenProviderHangs(t *testing.T) {
	t.Parallel()

	registry := provider.NewRegistry()
	registry.Register("good", &fakeProvider{name: "good", healthy: true, message: "ok"})
	registry.Register("bad", &hangingProvider{})

	cfg := Config{
		PollInterval: 24 * 60 * 60,
		CheckTimeout: 50 * time.Millisecond, // sweep deadline = 100ms
	}
	cache := NewCache(registry, cfg)

	start := time.Now()

	cache.CheckNow(context.Background())

	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Fatalf("sweep took %v — expected to be bounded around 2 × CheckTimeout", elapsed)
	}

	// Good provider's status should land normally.
	if status, ok := cache.Get("good"); !ok || !status.Healthy {
		t.Fatalf("good provider missing or unhealthy: ok=%v status=%+v", ok, status)
	}
	// Hung provider should be recorded as "did not complete" with
	// Healthy=false so consumers know.
	if status, ok := cache.Get("bad"); !ok {
		t.Fatal("hung provider not recorded in cache")
	} else if status.Healthy {
		t.Fatalf("hung provider recorded as healthy: %+v", status)
	}
}

// TestCache_RunStopsOnContextCancel asserts the run loop exits
// promptly when ctx cancels. Run synchronously waits for the
// initial sweep, so we cancel and check the goroutine returned.
func TestCache_RunStopsOnContextCancel(t *testing.T) {
	t.Parallel()

	registry := provider.NewRegistry()
	registry.Register("alpha", &fakeProvider{name: "alpha", healthy: true})

	cache := NewCache(registry, Config{PollInterval: 50 * time.Millisecond, CheckTimeout: 50 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		cache.Run(ctx)
		close(done)
	}()

	time.Sleep(80 * time.Millisecond) // let it tick at least once
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return within 1s of ctx cancel")
	}
}

// --- fakes ---

type fakeProvider struct {
	name    string
	healthy bool
	message string
	err     error
	calls   atomic.Int32
}

func (f *fakeProvider) HealthCheck(_ context.Context) (*provider.HealthStatus, error) {
	f.calls.Add(1)

	if f.err != nil {
		return nil, f.err
	}

	return &provider.HealthStatus{
		Healthy:   f.healthy,
		Message:   f.message,
		Latency:   time.Millisecond,
		CheckedAt: time.Now().UTC(),
	}, nil
}

func (f *fakeProvider) Info() provider.ProviderInfo {
	return provider.ProviderInfo{Name: f.name}
}
func (f *fakeProvider) Capabilities() []provider.Capability { return nil }
func (f *fakeProvider) Provision(context.Context, provider.ProvisionRequest) (*provider.ProvisionResult, error) {
	return nil, nil
}
func (f *fakeProvider) Deprovision(context.Context, id.ID) error { return nil }
func (f *fakeProvider) Start(context.Context, id.ID) error       { return nil }
func (f *fakeProvider) Stop(context.Context, id.ID) error        { return nil }
func (f *fakeProvider) Restart(context.Context, id.ID) error     { return nil }
func (f *fakeProvider) Status(context.Context, id.ID) (*provider.InstanceStatus, error) {
	return nil, nil
}
func (f *fakeProvider) Deploy(context.Context, provider.DeployRequest) (*provider.DeployResult, error) {
	return nil, nil
}
func (f *fakeProvider) Rollback(context.Context, id.ID, id.ID) error              { return nil }
func (f *fakeProvider) Scale(context.Context, id.ID, provider.ResourceSpec) error { return nil }
func (f *fakeProvider) Resources(context.Context, id.ID) (*provider.ResourceUsage, error) {
	return nil, nil
}
func (f *fakeProvider) Logs(context.Context, id.ID, provider.LogOptions) (io.ReadCloser, error) {
	return nil, nil
}
func (f *fakeProvider) Exec(context.Context, id.ID, provider.ExecRequest) (*provider.ExecResult, error) {
	return nil, nil
}

// hangingProvider's HealthCheck never returns. Exercises the
// sweep-deadline safety net.
type hangingProvider struct{}

func (hangingProvider) HealthCheck(_ context.Context) (*provider.HealthStatus, error) {
	// Block forever, ignoring ctx to simulate a misbehaving
	// upstream client (e.g. older k8s Discovery().ServerVersion()).
	select {}
}
func (hangingProvider) Info() provider.ProviderInfo         { return provider.ProviderInfo{Name: "hang"} }
func (hangingProvider) Capabilities() []provider.Capability { return nil }
func (hangingProvider) Provision(context.Context, provider.ProvisionRequest) (*provider.ProvisionResult, error) {
	return nil, nil
}
func (hangingProvider) Deprovision(context.Context, id.ID) error { return nil }
func (hangingProvider) Start(context.Context, id.ID) error       { return nil }
func (hangingProvider) Stop(context.Context, id.ID) error        { return nil }
func (hangingProvider) Restart(context.Context, id.ID) error     { return nil }
func (hangingProvider) Status(context.Context, id.ID) (*provider.InstanceStatus, error) {
	return nil, nil
}
func (hangingProvider) Deploy(context.Context, provider.DeployRequest) (*provider.DeployResult, error) {
	return nil, nil
}
func (hangingProvider) Rollback(context.Context, id.ID, id.ID) error              { return nil }
func (hangingProvider) Scale(context.Context, id.ID, provider.ResourceSpec) error { return nil }
func (hangingProvider) Resources(context.Context, id.ID) (*provider.ResourceUsage, error) {
	return nil, nil
}
func (hangingProvider) Logs(context.Context, id.ID, provider.LogOptions) (io.ReadCloser, error) {
	return nil, nil
}
func (hangingProvider) Exec(context.Context, id.ID, provider.ExecRequest) (*provider.ExecResult, error) {
	return nil, nil
}

// plainProvider doesn't implement HealthChecker — exercises the
// fallback path in checkOne.
type plainProvider struct{}

func (plainProvider) Info() provider.ProviderInfo         { return provider.ProviderInfo{Name: "plain"} }
func (plainProvider) Capabilities() []provider.Capability { return nil }
func (plainProvider) Provision(context.Context, provider.ProvisionRequest) (*provider.ProvisionResult, error) {
	return nil, nil
}
func (plainProvider) Deprovision(context.Context, id.ID) error { return nil }
func (plainProvider) Start(context.Context, id.ID) error       { return nil }
func (plainProvider) Stop(context.Context, id.ID) error        { return nil }
func (plainProvider) Restart(context.Context, id.ID) error     { return nil }
func (plainProvider) Status(context.Context, id.ID) (*provider.InstanceStatus, error) {
	return nil, nil
}
func (plainProvider) Deploy(context.Context, provider.DeployRequest) (*provider.DeployResult, error) {
	return nil, nil
}
func (plainProvider) Rollback(context.Context, id.ID, id.ID) error              { return nil }
func (plainProvider) Scale(context.Context, id.ID, provider.ResourceSpec) error { return nil }
func (plainProvider) Resources(context.Context, id.ID) (*provider.ResourceUsage, error) {
	return nil, nil
}
func (plainProvider) Logs(context.Context, id.ID, provider.LogOptions) (io.ReadCloser, error) {
	return nil, nil
}
func (plainProvider) Exec(context.Context, id.ID, provider.ExecRequest) (*provider.ExecResult, error) {
	return nil, nil
}
