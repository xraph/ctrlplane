package api

import (
	"reflect"
	"testing"

	"github.com/xraph/ctrlplane/id"
)

// assertNoZeroFields fails for every zero-valued field on v. Callers
// hand it a request built with every field set, so a zero value means
// the mapper skipped an assignment.
func assertNoZeroFields(t *testing.T, v any) {
	t.Helper()

	rv := reflect.ValueOf(v)
	for i := range rv.NumField() {
		if rv.Field(i).IsZero() {
			t.Errorf("%s.%s was not carried across", rv.Type(), rv.Type().Field(i).Name)
		}
	}
}

// TestToAddRouteRequest_CarriesEveryField guards the hand-written
// mapping from the HTTP DTO onto the service request. The two structs
// are maintained separately, so a field can exist on both sides and
// still never make the trip — which is a silently ignored request body,
// not an error the caller sees.
func TestToAddRouteRequest_CarriesEveryField(t *testing.T) {
	t.Parallel()

	got := toAddRouteRequest(&AddRouteAPIRequest{
		InstanceID:        id.New(id.PrefixInstance),
		ServiceName:       "worker",
		Hostname:          "acme.api.example.com",
		Path:              "/api",
		Port:              8080,
		Protocol:          "http",
		Weight:            100,
		StripPrefix:       true,
		RewriteRedirects:  true,
		RewriteCookiePath: true,
		UpstreamOrigin:    "https://staging.internal:8443",
		TLSVerify:         new(false),
	})

	assertNoZeroFields(t, got)

	if got.ServiceName != "worker" {
		t.Errorf("ServiceName = %q, want %q", got.ServiceName, "worker")
	}

	if got.Hostname != "acme.api.example.com" {
		t.Errorf("Hostname = %q, want %q", got.Hostname, "acme.api.example.com")
	}
}

// TestToUpdateRouteRequest_CarriesEveryField is the PATCH counterpart.
// Every field is a pointer, so a dropped assignment reads as "the caller
// omitted the key" and the update silently leaves the stored value
// alone.
func TestToUpdateRouteRequest_CarriesEveryField(t *testing.T) {
	t.Parallel()

	got := toUpdateRouteRequest(&UpdateRouteAPIRequest{
		RouteID:           id.New(id.PrefixRoute),
		ServiceName:       new("worker"),
		Hostname:          new("acme.api.example.com"),
		Path:              new("/api"),
		Weight:            new(100),
		StripPrefix:       new(true),
		RewriteRedirects:  new(true),
		RewriteCookiePath: new(true),
		UpstreamOrigin:    new("https://staging.internal:8443"),
		TLSVerify:         new(false),
	})

	assertNoZeroFields(t, got)

	if *got.ServiceName != "worker" {
		t.Errorf("ServiceName = %q, want %q", *got.ServiceName, "worker")
	}

	if *got.Hostname != "acme.api.example.com" {
		t.Errorf("Hostname = %q, want %q", *got.Hostname, "acme.api.example.com")
	}
}
