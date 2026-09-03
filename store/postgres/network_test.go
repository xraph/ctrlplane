package postgres

import (
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/network"
)

// TestRouteModel_ProxyFieldsRoundTrip guards the VirtualGateway
// proxy-mode fields through the model⇄domain mapping. Before they were
// wired into toRouteModel/fromRouteModel, octopus would read zero values
// (no redirect rewrite, tls_verify implicitly off) after a store reload.
//
// StripPrefix is asserted alongside them because it is the field the
// annotation emitter reads to pick octopus's path mode; a route that
// loses it reloads as passthrough.
func TestRouteModel_ProxyFieldsRoundTrip(t *testing.T) {
	t.Parallel()

	in := &network.Route{
		Entity:      ctrlplane.NewEntity(id.PrefixRoute),
		TenantID:    "tenant-x",
		InstanceID:  id.New(id.PrefixInstance),
		Path:        "/twinos",
		Port:        7900,
		Protocol:    "http",
		Weight:      1,
		StripPrefix: true,

		RewriteRedirects:  true,
		RewriteCookiePath: true,
		UpstreamOrigin:    "https://x:443",
		TLSVerify:         false,
	}

	m := toRouteModel(in)
	if !m.StripPrefix || !m.RewriteRedirects || !m.RewriteCookiePath ||
		m.UpstreamOrigin != "https://x:443" || m.TLSVerify {
		t.Fatalf("toRouteModel dropped a proxy field: %+v", m)
	}

	out := fromRouteModel(m)
	if out.StripPrefix != in.StripPrefix {
		t.Fatalf("StripPrefix round-trip: got %v want %v", out.StripPrefix, in.StripPrefix)
	}

	if out.RewriteRedirects != in.RewriteRedirects {
		t.Fatalf("RewriteRedirects round-trip: got %v want %v", out.RewriteRedirects, in.RewriteRedirects)
	}

	if out.RewriteCookiePath != in.RewriteCookiePath {
		t.Fatalf("RewriteCookiePath round-trip: got %v want %v", out.RewriteCookiePath, in.RewriteCookiePath)
	}

	if out.UpstreamOrigin != in.UpstreamOrigin {
		t.Fatalf("UpstreamOrigin round-trip: got %q want %q", out.UpstreamOrigin, in.UpstreamOrigin)
	}

	if out.TLSVerify != in.TLSVerify {
		t.Fatalf("TLSVerify round-trip: got %v want %v", out.TLSVerify, in.TLSVerify)
	}
}

// TestRouteModel_RoutingTargetsRoundTrip guards the two fields that
// decide where a route points. ServiceName picks the service inside a
// multi-service instance; Hostname scopes the route to a single host so
// per-workspace path routes don't collide on the shared wildcard
// listener.
//
// Both live on network.Route but were absent from routeModel, so a
// route created with either one reloaded from postgres with the field
// blank: traffic fell back to the instance's Main service and the route
// widened onto every host on the listener. Nothing errored, which is
// what makes the round-trip assertion worth keeping.
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
