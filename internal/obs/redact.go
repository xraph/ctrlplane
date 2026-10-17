package obs

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"
)

// redactedKeys are the only attributes a redacted span keeps. Everything
// else otelhttp records (url.full, url.path, url.query, user_agent.original
// and friends) is dropped: on the Docker API the path and query name
// containers, images and label filters.
var redactedKeys = map[attribute.Key]bool{
	"http.request.method":       true,
	"http.response.status_code": true,
	"server.address":            true,
	"server.port":               true,
	"error.type":                true,
}

// RedactedTracerProvider returns a tracer provider for HTTP instrumentation
// that lives inside a third-party SDK, such as the otelhttp transport the
// Docker client always installs. Spans it starts go to the global tracer
// provider, looked up on every span, but keep only the method, status code,
// server address and port and error.type. A failed span's status
// description is replaced by its error.type, or left empty, because otelhttp
// writes err.Error() there and a *url.Error quotes the full URL. Recorded
// errors become an exception event carrying only exception.type.
func RedactedTracerProvider() trace.TracerProvider {
	return redactedProvider{}
}

type redactedProvider struct{ embedded.TracerProvider }

func (redactedProvider) Tracer(name string, opts ...trace.TracerOption) trace.Tracer {
	return redactedTracer{name: name, opts: opts}
}

type redactedTracer struct {
	embedded.Tracer

	name string
	opts []trace.TracerOption
}

func (t redactedTracer) Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	cfg := trace.NewSpanStartConfig(opts...)

	clean := []trace.SpanStartOption{
		trace.WithSpanKind(cfg.SpanKind()),
		trace.WithAttributes(redactAttrs(cfg.Attributes())...),
		trace.WithLinks(cfg.Links()...),
	}

	if !cfg.Timestamp().IsZero() {
		clean = append(clean, trace.WithTimestamp(cfg.Timestamp()))
	}

	if cfg.NewRoot() {
		clean = append(clean, trace.WithNewRoot())
	}

	ctx, inner := otel.GetTracerProvider().Tracer(t.name, t.opts...).Start(ctx, name, clean...) //nolint:spancheck // the instrumentation ends it
	span := &redactedSpan{Span: inner}

	return trace.ContextWithSpan(ctx, span), span
}

func redactAttrs(attrs []attribute.KeyValue) []attribute.KeyValue {
	out := make([]attribute.KeyValue, 0, len(attrs))

	for _, kv := range attrs {
		if redactedKeys[kv.Key] {
			out = append(out, kv)
		}
	}

	return out
}

// redactedSpan filters what the instrumentation writes onto its span.
type redactedSpan struct {
	trace.Span

	mu      sync.Mutex
	errType string
}

func (s *redactedSpan) SetAttributes(kv ...attribute.KeyValue) {
	kept := redactAttrs(kv)

	for _, a := range kept {
		if a.Key == "error.type" {
			s.mu.Lock()
			s.errType = a.Value.AsString()
			s.mu.Unlock()
		}
	}

	s.Span.SetAttributes(kept...)
}

func (s *redactedSpan) SetStatus(code codes.Code, _ string) {
	if code != codes.Error {
		s.Span.SetStatus(code, "")

		return
	}

	s.mu.Lock()
	class := s.errType
	s.mu.Unlock()

	s.Span.SetStatus(codes.Error, class)

	if class != "" {
		s.Span.AddEvent("exception", trace.WithAttributes(attribute.String("exception.type", class)))
	}
}

func (s *redactedSpan) RecordError(err error, _ ...trace.EventOption) {
	if err == nil {
		return
	}

	s.Span.AddEvent("exception", trace.WithAttributes(attribute.String("exception.type", ErrorType(err))))
}

func (s *redactedSpan) AddEvent(name string, opts ...trace.EventOption) {
	cfg := trace.NewEventConfig(opts...)
	s.Span.AddEvent(name, trace.WithAttributes(redactAttrs(cfg.Attributes())...), trace.WithTimestamp(cfg.Timestamp()))
}

func (s *redactedSpan) TracerProvider() trace.TracerProvider {
	return redactedProvider{}
}
