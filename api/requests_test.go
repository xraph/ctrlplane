package api

import (
	"reflect"
	"strings"
	"testing"

	"github.com/xraph/ctrlplane/network"
)

// jsonFields returns the json tag names on t, skipping fields bound
// from the path and fields explicitly suppressed with "-".
func jsonFields(t *testing.T, typ reflect.Type) map[string]struct{} {
	t.Helper()

	out := make(map[string]struct{}, typ.NumField())

	for f := range typ.Fields() {
		tag, ok := f.Tag.Lookup("json")
		if !ok || tag == "-" {
			continue
		}

		out[strings.Split(tag, ",")[0]] = struct{}{}
	}

	return out
}

// TestRouteAPIRequests_MirrorServiceRequests pins the API route DTOs to
// their network counterparts.
//
// The DTOs replicate the body fields by hand because network's requests
// claim a json tag that collides with the path parameter, so nothing
// makes them drift-proof: a field added to network.AddRouteRequest just
// stays unreachable over HTTP until someone copies it across. That is
// how service_name and hostname went missing. This test fails the moment
// the two sides disagree.
func TestRouteAPIRequests_MirrorServiceRequests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		service reflect.Type
		api     reflect.Type
		// ignore lists json names carried by the path instead of the body.
		ignore []string
	}{
		{
			name:    "AddRoute",
			service: reflect.TypeFor[network.AddRouteRequest](),
			api:     reflect.TypeFor[AddRouteAPIRequest](),
			ignore:  []string{"instance_id"},
		},
		{
			name:    "UpdateRoute",
			service: reflect.TypeFor[network.UpdateRouteRequest](),
			api:     reflect.TypeFor[UpdateRouteAPIRequest](),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			want := jsonFields(t, tt.service)
			for _, name := range tt.ignore {
				delete(want, name)
			}

			got := jsonFields(t, tt.api)

			for name := range want {
				if _, ok := got[name]; !ok {
					t.Errorf("%s is missing %q, which %s accepts", tt.api, name, tt.service)
				}
			}

			for name := range got {
				if _, ok := want[name]; !ok {
					t.Errorf("%s exposes %q, which %s does not accept", tt.api, name, tt.service)
				}
			}
		})
	}
}
