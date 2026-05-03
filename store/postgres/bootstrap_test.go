package postgres

import (
	"encoding/json"
	"reflect"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/bootstrap"
	"github.com/xraph/ctrlplane/datacenter"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// TestBootstrapModel_RoundTrip asserts that every BootstrapWorkload
// field survives the model conversion intact. JSONB-backed fields
// (Services, ServiceRefs, Labels) are the high-risk ones — a missing
// marshal call here would silently drop them on the way to the
// database, which is exactly the failure mode the matching instance
// test was written to regress.
func TestBootstrapModel_RoundTrip(t *testing.T) {
	t.Parallel()

	bw := &bootstrap.BootstrapWorkload{
		Entity:       ctrlplane.NewEntity(id.PrefixBootstrap),
		DatacenterID: id.New(id.PrefixDatacenter),
		Name:         "fluent-bit",
		Kind:         provider.KindDeployment,
		Services: []provider.ServiceSpec{
			{
				Name:  "main",
				Image: "fluent/fluent-bit:2.0",
				Role:  provider.RoleMain,
				Env:   map[string]string{"LOG_LEVEL": "info"},
				Resources: provider.ResourceSpec{
					CPUMillis: 100,
					MemoryMB:  128,
					Replicas:  1,
				},
			},
		},
		State:       bootstrap.StateRunning,
		ProviderRef: "ref-abc",
		ServiceRefs: map[string]string{"main": "container-xyz"},
		LastError:   "",
		Attempts:    2,
		Labels:      map[string]string{"ctrlplane.system": "true"},
	}

	m := toBootstrapModel(bw)

	if len(m.Services) == 0 {
		t.Fatal("toBootstrapModel: Services not encoded — column would land NULL")
	}

	if len(m.ServiceRefs) == 0 {
		t.Fatal("toBootstrapModel: ServiceRefs not encoded")
	}

	if len(m.Labels) == 0 {
		t.Fatal("toBootstrapModel: Labels not encoded")
	}

	// Verify the encoded JSON is valid and matches the input.
	var raw []provider.ServiceSpec
	if err := json.Unmarshal(m.Services, &raw); err != nil {
		t.Fatalf("stored Services are not valid JSON: %v", err)
	}

	if !reflect.DeepEqual(raw, bw.Services) {
		t.Fatalf("encoded Services diverged: got %+v, want %+v", raw, bw.Services)
	}

	out := fromBootstrapModel(m)
	if out.ID != bw.ID {
		t.Fatalf("ID round-trip: got %s, want %s", out.ID, bw.ID)
	}

	if out.DatacenterID != bw.DatacenterID {
		t.Fatalf("DatacenterID round-trip: got %s, want %s", out.DatacenterID, bw.DatacenterID)
	}

	if out.Name != bw.Name || out.Kind != bw.Kind || out.State != bw.State {
		t.Fatalf("scalar fields drifted: %+v vs %+v", out, bw)
	}

	if !reflect.DeepEqual(out.Services, bw.Services) {
		t.Fatalf("Services round-trip mismatch")
	}

	if !reflect.DeepEqual(out.ServiceRefs, bw.ServiceRefs) {
		t.Fatalf("ServiceRefs round-trip mismatch")
	}

	if !reflect.DeepEqual(out.Labels, bw.Labels) {
		t.Fatalf("Labels round-trip mismatch")
	}

	if out.Attempts != bw.Attempts || out.ProviderRef != bw.ProviderRef {
		t.Fatalf("attempts/providerRef drift: %+v vs %+v", out, bw)
	}
}

// TestBootstrapModel_EmptyJSONBStaysNil mirrors the instance-test
// invariant: a row with no Services / ServiceRefs / Labels stores
// nil bytes (not the string "null") so columns land NULL on insert
// and existing rows that pre-date the migration continue to read
// back cleanly with empty maps and slices.
func TestBootstrapModel_EmptyJSONBStaysNil(t *testing.T) {
	t.Parallel()

	bw := &bootstrap.BootstrapWorkload{
		Entity:       ctrlplane.NewEntity(id.PrefixBootstrap),
		DatacenterID: id.New(id.PrefixDatacenter),
		Name:         "minimal",
		Kind:         provider.KindDeployment,
		State:        bootstrap.StatePending,
	}

	m := toBootstrapModel(bw)

	if m.Services != nil {
		t.Fatalf("empty Services should encode as nil, got %q", string(m.Services))
	}

	if m.ServiceRefs != nil {
		t.Fatalf("empty ServiceRefs should encode as nil, got %q", string(m.ServiceRefs))
	}

	if m.Labels != nil {
		t.Fatalf("empty Labels should encode as nil, got %q", string(m.Labels))
	}

	out := fromBootstrapModel(m)
	if out.Services != nil {
		t.Fatalf("decoded Services from nil should remain nil, got %+v", out.Services)
	}
}

// TestDatacenterModel_BootstrapServicesRoundTrip asserts the new
// bootstrap_services column on cp_datacenters round-trips operator-
// declared specs intact. Without the dedicated marshalJSONB call in
// toDatacenterModel, the field would silently drop on insert.
func TestDatacenterModel_BootstrapServicesRoundTrip(t *testing.T) {
	t.Parallel()

	specs := []bootstrap.BootstrapServiceSpec{
		{
			Name: "fluent-bit",
			Kind: provider.KindDeployment,
			Services: []provider.ServiceSpec{
				{Name: "main", Image: "fluent/fluent-bit:2.0", Role: provider.RoleMain},
			},
		},
		{
			Name: "node-exporter",
			Services: []provider.ServiceSpec{
				{Name: "main", Image: "prom/node-exporter:1.6", Role: provider.RoleMain},
			},
		},
	}

	dc := &datacenter.Datacenter{
		Entity:            ctrlplane.NewEntity(id.PrefixDatacenter),
		TenantID:          "tenant-x",
		Name:              "us-east-1",
		Slug:              "us-east-1",
		ProviderName:      "kubernetes",
		Region:            "us-east-1",
		Status:            datacenter.StatusActive,
		BootstrapServices: specs,
	}

	m := toDatacenterModel(dc)

	if len(m.BootstrapServices) == 0 {
		t.Fatal("toDatacenterModel: BootstrapServices not encoded")
	}

	out := fromDatacenterModel(m)
	if !reflect.DeepEqual(out.BootstrapServices, specs) {
		t.Fatalf("BootstrapServices round-trip mismatch:\n  got:  %+v\n  want: %+v", out.BootstrapServices, specs)
	}
}
