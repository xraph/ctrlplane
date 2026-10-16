// Package obstest holds the test doubles for Ctrl Plane's telemetry: a span
// recorder installed as the global tracer provider, a metrics sink that
// remembers every series by name and labels, and a check that sensitive
// values never reach a span.
package obstest

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	gometrics "github.com/xraph/go-utils/metrics"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// Record installs a span recorder as the global tracer provider for the
// rest of the test and restores the previous provider and propagator when
// the test ends. Tests that call it must not run in parallel.
func Record(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()

	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())

		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})

	return rec
}

// Named returns the ended spans called name, in the order they ended.
func Named(rec *tracetest.SpanRecorder, name string) []sdktrace.ReadOnlySpan {
	var out []sdktrace.ReadOnlySpan

	for _, s := range rec.Ended() {
		if s.Name() == name {
			out = append(out, s)
		}
	}

	return out
}

// Attrs returns a span's attributes as strings, keyed by attribute name.
func Attrs(s sdktrace.ReadOnlySpan) map[string]string {
	out := make(map[string]string, len(s.Attributes()))
	for _, kv := range s.Attributes() {
		out[string(kv.Key)] = kv.Value.String()
	}

	return out
}

// AssertAbsent fails when any of values shows up in a span's name, status
// description, attributes, or event names and attributes.
func AssertAbsent(t *testing.T, spans []sdktrace.ReadOnlySpan, values ...string) {
	t.Helper()

	for _, s := range spans {
		fields := []string{s.Name(), s.Status().Description}
		for _, kv := range s.Attributes() {
			fields = append(fields, kv.Value.String())
		}

		for _, ev := range s.Events() {
			fields = append(fields, ev.Name)
			for _, kv := range ev.Attributes {
				fields = append(fields, kv.Value.String())
			}
		}

		for _, field := range fields {
			for _, v := range values {
				if strings.Contains(field, v) {
					t.Fatalf("span %q carries %q in %q", s.Name(), v, field)
				}
			}
		}
	}
}

// Sink is a go-utils metrics sink that remembers every series it hands out,
// keyed as name{label=value,...} with the labels sorted.
type Sink struct {
	mu         sync.Mutex
	counters   map[string]gometrics.Counter
	gauges     map[string]gometrics.Gauge
	histograms map[string]gometrics.Histogram
	mock       *gometrics.MockMetrics
}

// NewSink returns an empty sink.
func NewSink() *Sink {
	s := &Sink{
		counters:   map[string]gometrics.Counter{},
		gauges:     map[string]gometrics.Gauge{},
		histograms: map[string]gometrics.Histogram{},
		mock:       gometrics.NewMockMetrics(),
	}

	s.mock.CounterFunc = s.counter
	s.mock.GaugeFunc = s.gauge
	s.mock.HistogramFunc = s.histogram

	return s
}

// Metrics returns the sink as a go-utils Metrics, for SetMetrics and
// app.WithMetrics.
func (s *Sink) Metrics() gometrics.Metrics {
	return s.mock
}

// Key builds the key a series is stored under.
func Key(name string, labels map[string]string) string {
	pairs := make([]string, 0, len(labels))
	for k, v := range labels {
		pairs = append(pairs, k+"="+v)
	}

	slices.Sort(pairs)

	return name + "{" + strings.Join(pairs, ",") + "}"
}

func optionsOf(opts []gometrics.MetricOption) *gometrics.MetricOptions {
	o := &gometrics.MetricOptions{}
	for _, opt := range opts {
		opt(o)
	}

	return o
}

func (s *Sink) counter(name string, opts ...gometrics.MetricOption) gometrics.Counter {
	s.mu.Lock()
	defer s.mu.Unlock()

	k := Key(name, optionsOf(opts).Labels)
	if c, ok := s.counters[k]; ok {
		return c
	}

	c := gometrics.NewCounter(name, opts...)
	s.counters[k] = c

	return c
}

func (s *Sink) gauge(name string, opts ...gometrics.MetricOption) gometrics.Gauge {
	s.mu.Lock()
	defer s.mu.Unlock()

	k := Key(name, optionsOf(opts).Labels)
	if g, ok := s.gauges[k]; ok {
		return g
	}

	g := gometrics.NewGauge(name, opts...)
	s.gauges[k] = g

	return g
}

func (s *Sink) histogram(name string, opts ...gometrics.MetricOption) gometrics.Histogram {
	s.mu.Lock()
	defer s.mu.Unlock()

	k := Key(name, optionsOf(opts).Labels)
	if h, ok := s.histograms[k]; ok {
		return h
	}

	h := gometrics.NewHistogram(name, opts...)
	s.histograms[k] = h

	return h
}

// Counter returns the value of the counter stored under key, failing the
// test when there is none.
func (s *Sink) Counter(t *testing.T, key string) float64 {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.counters[key]
	if !ok {
		t.Fatalf("no counter %s; have %v", key, sortedKeys(s.counters))
	}

	return c.Value()
}

// Gauge returns the value of the gauge stored under key, failing the test
// when there is none.
func (s *Sink) Gauge(t *testing.T, key string) float64 {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	g, ok := s.gauges[key]
	if !ok {
		t.Fatalf("no gauge %s; have %v", key, sortedKeys(s.gauges))
	}

	return g.Value()
}

// HistogramCount returns how many observations the histogram stored under
// key holds, failing the test when there is none.
func (s *Sink) HistogramCount(t *testing.T, key string) uint64 {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	h, ok := s.histograms[key]
	if !ok {
		t.Fatalf("no histogram %s; have %v", key, sortedKeys(s.histograms))
	}

	return h.Count()
}

// Series returns every key the sink has handed out, sorted.
func (s *Sink) Series() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := sortedKeys(s.counters)
	out = append(out, sortedKeys(s.gauges)...)
	out = append(out, sortedKeys(s.histograms)...)

	slices.Sort(out)

	return out
}

func sortedKeys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	slices.Sort(out)

	return out
}
