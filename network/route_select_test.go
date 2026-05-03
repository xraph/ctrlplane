package network

import (
	"testing"

	"github.com/xraph/ctrlplane/provider"
)

func TestSelectEndpoint_NilWhenNoEndpoints(t *testing.T) {
	t.Parallel()

	got := SelectEndpoint(&Route{ServiceName: "any", Port: 80}, nil)
	if got != nil {
		t.Fatalf("want nil, got %+v", got)
	}
}

func TestSelectEndpoint_ExactServiceAndPort(t *testing.T) {
	t.Parallel()

	endpoints := []provider.Endpoint{
		{ServiceName: "main", Port: 8080},
		{ServiceName: "main", Port: 9090},
		{ServiceName: "metrics", Port: 9100},
	}

	got := SelectEndpoint(&Route{ServiceName: "main", Port: 9090}, endpoints)
	if got == nil || got.ServiceName != "main" || got.Port != 9090 {
		t.Fatalf("want main:9090, got %+v", got)
	}
}

func TestSelectEndpoint_ServiceMatchPortFallback(t *testing.T) {
	t.Parallel()

	endpoints := []provider.Endpoint{
		{ServiceName: "main", Port: 8080},
		{ServiceName: "metrics", Port: 9100},
	}

	// Route names "metrics" but at the wrong port — fall through to
	// any endpoint owned by metrics.
	got := SelectEndpoint(&Route{ServiceName: "metrics", Port: 8888}, endpoints)
	if got == nil || got.ServiceName != "metrics" {
		t.Fatalf("want metrics service, got %+v", got)
	}
}

func TestSelectEndpoint_LegacyRouteByPort(t *testing.T) {
	t.Parallel()

	endpoints := []provider.Endpoint{
		{ServiceName: "main", Port: 8080},
		{ServiceName: "metrics", Port: 9100},
	}

	// Route doesn't name a service — pick the first endpoint with the
	// matching port (back-compat with single-service workloads where
	// Route.ServiceName has always been empty).
	got := SelectEndpoint(&Route{Port: 9100}, endpoints)
	if got == nil || got.Port != 9100 {
		t.Fatalf("want port 9100 endpoint, got %+v", got)
	}
}

func TestSelectEndpoint_FirstEndpointFallback(t *testing.T) {
	t.Parallel()

	endpoints := []provider.Endpoint{
		{ServiceName: "main", Port: 8080},
	}

	// Route specifies neither a service nor a port — pick the first
	// endpoint as a last resort.
	got := SelectEndpoint(&Route{}, endpoints)
	if got == nil || got.Port != 8080 {
		t.Fatalf("want first endpoint, got %+v", got)
	}
}

func TestSelectEndpoint_NilRoute(t *testing.T) {
	t.Parallel()

	endpoints := []provider.Endpoint{
		{ServiceName: "main", Port: 8080},
	}

	got := SelectEndpoint(nil, endpoints)
	if got == nil || got.Port != 8080 {
		t.Fatalf("want first endpoint when route nil, got %+v", got)
	}
}
