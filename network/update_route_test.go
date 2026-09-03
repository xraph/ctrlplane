package network

import (
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
)

// seedProxyRoute builds a route already in proxy mode against an
// external origin with verification deliberately turned off — the state
// an operator lands in for a self-signed staging upstream.
func seedProxyRoute(t *testing.T) *Route {
	t.Helper()

	return &Route{
		Entity:         ctrlplane.NewEntity(id.PrefixRoute),
		TenantID:       "tenant-x",
		InstanceID:     id.New(id.PrefixInstance),
		Path:           "/api",
		Port:           8080,
		Protocol:       "http",
		Weight:         100,
		StripPrefix:    true,
		UpstreamOrigin: "https://staging.internal:8443",
		TLSVerify:      false,
	}
}

// TestUpdateRoute_ClearingOriginResetsTLSVerify pins the invariant that
// makes tls_verify safe to store: a route with no upstream origin always
// has verification on.
//
// Octopus only consults tls_verify when an origin is set, so a route
// left at tls_verify=false with no origin is dead state — invisible
// until someone points the route at a new origin months later and
// silently inherits unverified TLS. Clearing the origin resets the flag
// so that can't happen. The reset wins over an explicit tls_verify=false
// in the same request, because honouring it would recreate exactly the
// state the invariant exists to prevent.
func TestUpdateRoute_ClearingOriginResetsTLSVerify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		origin        *string
		tlsVerify     *bool
		wantOrigin    string
		wantTLSVerify bool
	}{
		{
			name:          "clearing the origin resets verification",
			origin:        new(""),
			wantOrigin:    "",
			wantTLSVerify: true,
		},
		{
			name:          "reset wins over an explicit false in the same request",
			origin:        new(""),
			tlsVerify:     new(false),
			wantOrigin:    "",
			wantTLSVerify: true,
		},
		{
			name:          "moving to a new origin keeps verification off",
			origin:        new("https://other.internal:8443"),
			wantOrigin:    "https://other.internal:8443",
			wantTLSVerify: false,
		},
		{
			name:          "leaving the origin alone changes nothing",
			wantOrigin:    "https://staging.internal:8443",
			wantTLSVerify: false,
		},
		{
			name:          "verification can still be turned on in place",
			tlsVerify:     new(true),
			wantOrigin:    "https://staging.internal:8443",
			wantTLSVerify: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			seeded := seedProxyRoute(t)
			st := &captureStore{seeded: seeded}
			svc := &service{store: st, events: event.NewInMemoryBus()}

			route, err := svc.UpdateRoute(testContext(t), seeded.ID, UpdateRouteRequest{
				UpstreamOrigin: tt.origin,
				TLSVerify:      tt.tlsVerify,
			})
			if err != nil {
				t.Fatalf("UpdateRoute: %v", err)
			}

			if route.UpstreamOrigin != tt.wantOrigin {
				t.Errorf("UpstreamOrigin = %q, want %q", route.UpstreamOrigin, tt.wantOrigin)
			}

			if route.TLSVerify != tt.wantTLSVerify {
				t.Errorf("TLSVerify = %v, want %v", route.TLSVerify, tt.wantTLSVerify)
			}

			if len(st.updated) != 1 {
				t.Fatalf("persisted %d routes, want 1", len(st.updated))
			}

			if st.updated[0].TLSVerify != tt.wantTLSVerify {
				t.Errorf("persisted TLSVerify = %v, want %v", st.updated[0].TLSVerify, tt.wantTLSVerify)
			}
		})
	}
}

// TestUpdateRoute_RewriteFlagsCarryThrough covers the two plain
// carry-over fields: nil leaves the stored value alone, non-nil assigns.
func TestUpdateRoute_RewriteFlagsCarryThrough(t *testing.T) {
	t.Parallel()

	seeded := seedProxyRoute(t)
	seeded.RewriteRedirects = true
	seeded.RewriteCookiePath = false

	st := &captureStore{seeded: seeded}
	svc := &service{store: st, events: event.NewInMemoryBus()}

	route, err := svc.UpdateRoute(testContext(t), seeded.ID, UpdateRouteRequest{
		RewriteCookiePath: new(true),
	})
	if err != nil {
		t.Fatalf("UpdateRoute: %v", err)
	}

	if !route.RewriteRedirects {
		t.Error("RewriteRedirects was clobbered by an omitted key")
	}

	if !route.RewriteCookiePath {
		t.Error("RewriteCookiePath = false, want true")
	}
}

// TestUpdateRoute_RoutingTargetsCarryThrough covers the two fields that
// decide where a route points: ServiceName picks the service inside a
// multi-service instance, Hostname scopes the route to a single host on
// the shared wildcard listener. Both follow the same pointer contract as
// the proxy fields — nil leaves the stored value alone, a pointer
// assigns, and a pointer to "" clears.
//
// Hostname matters most here. An update that dropped it would widen a
// per-workspace route back onto every host sharing the listener, which
// is a routing collision rather than a visible failure.
func TestUpdateRoute_RoutingTargetsCarryThrough(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		serviceName     *string
		hostname        *string
		wantServiceName string
		wantHostname    string
	}{
		{
			name:            "omitted keys leave both alone",
			wantServiceName: "api",
			wantHostname:    "acme.api.example.com",
		},
		{
			name:            "service name can be retargeted",
			serviceName:     new("worker"),
			wantServiceName: "worker",
			wantHostname:    "acme.api.example.com",
		},
		{
			name:            "hostname can be retargeted",
			hostname:        new("beta.api.example.com"),
			wantServiceName: "api",
			wantHostname:    "beta.api.example.com",
		},
		{
			name:            "empty strings clear both",
			serviceName:     new(""),
			hostname:        new(""),
			wantServiceName: "",
			wantHostname:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			seeded := seedProxyRoute(t)
			seeded.ServiceName = "api"
			seeded.Hostname = "acme.api.example.com"

			st := &captureStore{seeded: seeded}
			svc := &service{store: st, events: event.NewInMemoryBus()}

			route, err := svc.UpdateRoute(testContext(t), seeded.ID, UpdateRouteRequest{
				ServiceName: tt.serviceName,
				Hostname:    tt.hostname,
			})
			if err != nil {
				t.Fatalf("UpdateRoute: %v", err)
			}

			if route.ServiceName != tt.wantServiceName {
				t.Errorf("ServiceName = %q, want %q", route.ServiceName, tt.wantServiceName)
			}

			if route.Hostname != tt.wantHostname {
				t.Errorf("Hostname = %q, want %q", route.Hostname, tt.wantHostname)
			}

			if len(st.updated) != 1 {
				t.Fatalf("persisted %d routes, want 1", len(st.updated))
			}

			if st.updated[0].Hostname != tt.wantHostname {
				t.Errorf("persisted Hostname = %q, want %q", st.updated[0].Hostname, tt.wantHostname)
			}
		})
	}
}
