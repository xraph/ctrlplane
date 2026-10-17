package docker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/xraph/ctrlplane/internal/obs/obstest"
)

// TestDaemonCallsCarryTraceContextOnly points a Docker SDK client built
// with traceOptions at a stand-in daemon. An untraced call starts no span
// and sends no trace headers; a traced call gets one client span and the
// W3C trace context, and never the baggage the caller's request brought in.
func TestDaemonCallsCarryTraceContextOnly(t *testing.T) {
	rec := obstest.Record(t)

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	var (
		mu          sync.Mutex
		traceparent []string
		bag         []string
	)

	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()

		traceparent = append(traceparent, r.Header.Get("Traceparent"))
		bag = append(bag, r.Header.Get("Baggage"))
		mu.Unlock()

		w.Header().Set("Api-Version", "1.47")
		_, _ = w.Write([]byte("OK"))
	}))
	t.Cleanup(daemon.Close)

	cli, err := client.New(client.WithHost("tcp://"+strings.TrimPrefix(daemon.URL, "http://")), client.WithAPIVersion("1.47"), traceOptions())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = cli.Close() })

	if _, err := cli.Ping(context.Background(), client.PingOptions{}); err != nil {
		t.Fatalf("untraced ping: %v", err)
	}

	member, err := baggage.NewMember("user_id", "alice")
	if err != nil {
		t.Fatal(err)
	}

	inbound, err := baggage.New(member)
	if err != nil {
		t.Fatal(err)
	}

	ctx, span := otel.Tracer("test").Start(baggage.ContextWithBaggage(context.Background(), inbound), "check provider health")
	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		t.Fatalf("traced ping: %v", err)
	}

	span.End()

	mu.Lock()
	defer mu.Unlock()

	if len(traceparent) < 2 || traceparent[0] != "" || traceparent[len(traceparent)-1] == "" {
		t.Fatalf("traceparent per request = %q", traceparent)
	}

	for _, b := range bag {
		if b != "" {
			t.Fatalf("inbound baggage reached the daemon: %q", bag)
		}
	}

	var clientSpans int

	for _, s := range rec.Ended() {
		if s.SpanKind() != trace.SpanKindClient {
			continue
		}

		clientSpans++

		if s.Name() != http.MethodGet && s.Name() != http.MethodHead {
			t.Fatalf("daemon span named %q, want the method", s.Name())
		}

		if s.Parent().SpanID() != span.SpanContext().SpanID() {
			t.Fatal("the daemon span is not under the caller's span")
		}
	}

	if clientSpans == 0 {
		t.Fatal("the traced ping produced no client span")
	}
}

// TestDaemonSpansCarryNoPathQueryOrErrorText drives requests whose path,
// query and error text name an image reference and a container, plus one
// that fails in the transport, and checks none of that reaches a span.
func TestDaemonSpansCarryNoPathQueryOrErrorText(t *testing.T) {
	rec := obstest.Record(t)

	const (
		imageRef      = "registry.example.com/alice/app@sha256:deadbeefcafe"
		containerName = "alice-private-container"
	)

	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", "1.47")

		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			_, _ = w.Write([]byte("OK"))
		case strings.Contains(r.URL.Path, "/containers/create"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"Conflict. The container name \"/` + containerName + `\" is already in use"}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"No such image: ` + imageRef + `"}`))
		}
	}))
	t.Cleanup(daemon.Close)

	newClient := func(host string) *client.Client {
		t.Helper()

		cli, err := client.New(client.WithHost("tcp://"+host), client.WithAPIVersion("1.47"), traceOptions())
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = cli.Close() })

		return cli
	}

	cli := newClient(strings.TrimPrefix(daemon.URL, "http://"))

	ctx, span := otel.Tracer("test").Start(context.Background(), "execute managed docker step")

	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		t.Fatalf("ping: %v", err)
	}

	if _, err := cli.ImageInspect(ctx, imageRef); err == nil {
		t.Fatal("image inspect should fail")
	}

	if _, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name:   containerName,
		Config: &container.Config{Image: imageRef},
	}); err == nil {
		t.Fatal("container create should fail")
	}

	// A daemon that is gone: the transport error is a *url.Error, whose
	// text quotes the full URL.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadHost := strings.TrimPrefix(dead.URL, "http://")
	dead.Close()

	if _, err := newClient(deadHost).ImageInspect(ctx, imageRef); err == nil {
		t.Fatal("inspect against a closed daemon should fail")
	}

	span.End()

	var clientSpans []string

	for _, s := range rec.Ended() {
		if s.SpanKind() == trace.SpanKindClient {
			clientSpans = append(clientSpans, s.Name())
		}
	}

	if len(clientSpans) < 4 {
		t.Fatalf("want a client span per daemon request, got %v", clientSpans)
	}

	obstest.AssertAbsent(t, rec.Ended(), "_ping", "/v1.47", "registry.example.com", "deadbeef", "alice", "No such image", "Conflict.")
}
