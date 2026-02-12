package health

import (
	"context"
	"fmt"
	"net"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
)

// GRPCChecker performs gRPC health checks against a target address.
// This is a simplified checker that verifies TCP connectivity to the gRPC
// endpoint. A full implementation would use the gRPC health checking protocol
// (grpc.health.v1.Health).
type GRPCChecker struct {
	timeout time.Duration
}

// NewGRPCChecker creates a new gRPC health checker with the given timeout.
func NewGRPCChecker(timeout time.Duration) *GRPCChecker {
	return &GRPCChecker{
		timeout: timeout,
	}
}

// Type returns the check type this checker handles.
func (c *GRPCChecker) Type() CheckType {
	return CheckGRPC
}

// Check verifies connectivity to the gRPC endpoint and returns the result.
func (c *GRPCChecker) Check(ctx context.Context, check *HealthCheck) (*HealthResult, error) {
	start := time.Now()

	timeout := c.timeout
	if check.Timeout > 0 {
		timeout = check.Timeout
	}

	dialer := &net.Dialer{
		Timeout: timeout,
	}

	conn, err := dialer.DialContext(ctx, "tcp", check.Target)

	result := &HealthResult{
		Entity:     ctrlplane.NewEntity(id.PrefixHealthResult),
		CheckID:    check.ID,
		InstanceID: check.InstanceID,
		TenantID:   check.TenantID,
		Latency:    time.Since(start),
		CheckedAt:  time.Now().UTC(),
	}

	if err != nil {
		result.Status = StatusUnhealthy
		result.Message = fmt.Sprintf("grpc dial failed: %v", err)

		return result, nil
	}

	conn.Close()

	result.Status = StatusHealthy

	return result, nil
}
