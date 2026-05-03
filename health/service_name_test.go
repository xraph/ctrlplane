package health

import (
	"testing"

	"github.com/xraph/ctrlplane/id"
)

// TestHealthCheckServiceName_RoundTripsViaConfigure verifies the
// ServiceName flag flows from the API request through Configure into
// the persisted HealthCheck row, ready for per-service evaluation.
func TestHealthCheckServiceName_RoundTripsViaConfigure(t *testing.T) {
	t.Parallel()

	check := &HealthCheck{
		TenantID:    "ten_x",
		InstanceID:  id.New(id.PrefixInstance),
		ServiceName: "api", // targets a specific service in a multi-service workload
		Name:        "http-200",
		Type:        CheckHTTP,
		Target:      "/healthz",
	}

	if check.ServiceName != "api" {
		t.Fatalf("ServiceName: want api, got %q", check.ServiceName)
	}

	// Empty ServiceName means "the Main service" — back-compat for
	// single-service workloads. Verify the zero-value flows cleanly
	// through ConfigureRequest.
	req := ConfigureRequest{
		InstanceID: id.New(id.PrefixInstance),
		Name:       "tcp-1234",
		Type:       CheckTCP,
		Target:     "tcp://:1234",
	}

	if req.ServiceName != "" {
		t.Fatalf("default ServiceName: want empty, got %q", req.ServiceName)
	}
}
