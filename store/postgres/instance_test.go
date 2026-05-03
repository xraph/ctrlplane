package postgres

import (
	"encoding/json"
	"reflect"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/provider"
)

// TestInstanceModel_EndpointsRoundTrip regresses the pg counterpart of
// the mongo "endpoints dropped on reload" bug. Before the fix
// toInstanceModel/fromInstanceModel ignored Instance.Endpoints, so
// anything downstream of the store (TraefikRouter, etc.) read an
// empty slice and silently failed.
func TestInstanceModel_EndpointsRoundTrip(t *testing.T) {
	t.Parallel()

	eps := []provider.Endpoint{
		{URL: "http://cp-foo:8080", Port: 8080, Protocol: "http", Public: true},
		{URL: "tcp://cp-foo:5432", Port: 5432, Protocol: "tcp", Public: false},
	}

	in := &instance.Instance{
		Entity:       ctrlplane.NewEntity(id.PrefixInstance),
		TenantID:     "tenant-x",
		Slug:         "foo",
		Name:         "foo",
		ProviderName: "docker",
		State:        provider.StateRunning,
		Endpoints:    eps,
	}

	m := toInstanceModel(in)
	if len(m.Endpoints) == 0 {
		t.Fatal("toInstanceModel: endpoints not encoded — column would land NULL")
	}

	var raw []provider.Endpoint
	if err := json.Unmarshal(m.Endpoints, &raw); err != nil {
		t.Fatalf("stored endpoints are not valid JSON: %v", err)
	}

	if !reflect.DeepEqual(raw, eps) {
		t.Fatalf("encoded endpoints diverged: got %+v, want %+v", raw, eps)
	}

	out := fromInstanceModel(m)
	if !reflect.DeepEqual(out.Endpoints, eps) {
		t.Fatalf("round-trip endpoints mismatch: got %+v, want %+v", out.Endpoints, eps)
	}
}

// TestInstanceModel_NoEndpointsStaysNil confirms that an instance
// without endpoints stores nil (not "[]") so existing rows and
// just-provisioned-not-yet-routed instances continue to read back
// cleanly with len(out.Endpoints) == 0.
func TestInstanceModel_NoEndpointsStaysNil(t *testing.T) {
	t.Parallel()

	in := &instance.Instance{
		Entity:       ctrlplane.NewEntity(id.PrefixInstance),
		TenantID:     "tenant-x",
		Slug:         "bare",
		Name:         "bare",
		ProviderName: "docker",
		State:        provider.StateProvisioning,
	}

	m := toInstanceModel(in)
	if m.Endpoints != nil {
		t.Fatalf("empty endpoints should be nil, got %d bytes", len(m.Endpoints))
	}

	out := fromInstanceModel(m)
	if out.Endpoints != nil {
		t.Fatalf("decoded empty endpoints should be nil, got %+v", out.Endpoints)
	}
}
