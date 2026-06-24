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

		PathMode:          "strip",
		RewriteRedirects:  true,
		RewriteCookiePath: true,
		UpstreamOrigin:    "https://x:443",
		TLSVerify:         false,
	}

	m := toRouteModel(in)
	if m.PathMode != "strip" || !m.RewriteRedirects || !m.RewriteCookiePath ||
		m.UpstreamOrigin != "https://x:443" || m.TLSVerify {
		t.Fatalf("toRouteModel dropped a proxy field: %+v", m)
	}

	out := fromRouteModel(m)
	if out.PathMode != in.PathMode {
		t.Fatalf("PathMode round-trip: got %q want %q", out.PathMode, in.PathMode)
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
