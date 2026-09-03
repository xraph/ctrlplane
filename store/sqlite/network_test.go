package sqlite

import (
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/network"
)

// TestRouteModel_RoutingTargetsRoundTrip mirrors the postgres guard.
// ServiceName picks the service inside a multi-service instance;
// Hostname scopes the route to a single host so per-workspace path
// routes don't collide on the shared wildcard listener.
//
// The mapping is hand-written per store, so a field added to
// network.Route silently vanishes here until someone adds it to
// routeModel by hand. Nothing fails to compile; the route just reloads
// with the field blank.
func TestRouteModel_RoutingTargetsRoundTrip(t *testing.T) {
	t.Parallel()

	in := &network.Route{
		Entity:      ctrlplane.NewEntity(id.PrefixRoute),
		TenantID:    "tenant-x",
		InstanceID:  id.New(id.PrefixInstance),
		ServiceName: "worker",
		Hostname:    "acme.api.example.com",
		Path:        "/twinos",
		Port:        7900,
		Protocol:    "http",
		Weight:      1,
	}

	m := toRouteModel(in)
	if m.ServiceName != in.ServiceName {
		t.Fatalf("toRouteModel dropped ServiceName: got %q want %q", m.ServiceName, in.ServiceName)
	}

	if m.Hostname != in.Hostname {
		t.Fatalf("toRouteModel dropped Hostname: got %q want %q", m.Hostname, in.Hostname)
	}

	out := fromRouteModel(m)
	if out.ServiceName != in.ServiceName {
		t.Fatalf("ServiceName round-trip: got %q want %q", out.ServiceName, in.ServiceName)
	}

	if out.Hostname != in.Hostname {
		t.Fatalf("Hostname round-trip: got %q want %q", out.Hostname, in.Hostname)
	}
}
