package obs

import (
	"net/http"
	"strconv"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Traced reports whether req was made under a span. Clients that bring
// their own otelhttp transport, like the Docker SDK, use it as the
// transport's filter so an untraced poll doesn't start a root trace.
func Traced(req *http.Request) bool {
	return trace.SpanContextFromContext(req.Context()).IsValid()
}

// MethodSpanName names a client span after the request method alone. The
// Docker SDK's default adds the URL path, which carries container IDs.
func MethodSpanName(_ string, req *http.Request) string {
	return req.Method
}

// Transport wraps rt so a request made under a span gets a client span and
// carries the trace context, without any inbound baggage. A request whose context has no span goes out
// untouched, so a poller with no trace of its own adds nothing. A nil rt
// means http.DefaultTransport.
//
// The span is named after the method and records the method, the server
// address and port and the response status. It never records the URL path
// or query: on a cluster API those name tenant namespaces and workloads.
func Transport(rt http.RoundTripper) http.RoundTripper {
	if rt == nil {
		rt = http.DefaultTransport
	}

	if _, ok := rt.(*transport); ok {
		return rt
	}

	return &transport{base: rt}
}

type transport struct {
	base http.RoundTripper
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return t.base.RoundTrip(req)
	}

	attrs := []attribute.KeyValue{attribute.String("http.request.method", req.Method)}

	host, port := hostPort(req)
	if host != "" {
		attrs = append(attrs, attribute.String("server.address", host))
	}

	if port > 0 {
		attrs = append(attrs, attribute.Int("server.port", port))
	}

	ctx, span := Tracer().Start(ctx, req.Method,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...))
	defer span.End()

	// A RoundTripper must not modify the request it was given. The
	// upstream is a cluster or scheduler API the operator doesn't
	// necessarily run, so it gets the trace context but never the
	// baggage a client sent in: that often names users or tenants.
	out := req.Clone(ctx)
	otel.GetTextMapPropagator().Inject(baggage.ContextWithoutBaggage(ctx), propagation.HeaderCarrier(out.Header))

	resp, err := t.base.RoundTrip(out)
	if err != nil {
		class := ErrorType(err)
		span.SetAttributes(attribute.String("error.type", class))
		FailClass(span, class)

		return resp, err
	}

	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))

	if resp.StatusCode >= http.StatusInternalServerError {
		class := strconv.Itoa(resp.StatusCode)
		span.SetAttributes(attribute.String("error.type", class))
		FailClass(span, class)
	}

	return resp, nil
}

// CloseIdleConnections passes through to the wrapped transport, so a client
// built on this one can still drop its idle connections.
func (t *transport) CloseIdleConnections() {
	if c, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

// hostPort reads the server host and port off the request URL, filling in
// the scheme's default port.
func hostPort(req *http.Request) (string, int) {
	if req.URL == nil {
		return "", 0
	}

	host := req.URL.Hostname()

	if p := req.URL.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err == nil {
			return host, n
		}
	}

	switch req.URL.Scheme {
	case "https":
		return host, 443
	case "http":
		return host, 80
	default:
		return host, 0
	}
}
