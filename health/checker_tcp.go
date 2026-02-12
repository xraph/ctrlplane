package health

import (
	"context"
	"fmt"
	"net"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
)

// TCPChecker performs TCP dial health checks against a target address.
type TCPChecker struct {
	timeout time.Duration
}

// NewTCPChecker creates a new TCP health checker with the given timeout.
func NewTCPChecker(timeout time.Duration) *TCPChecker {
	return &TCPChecker{
		timeout: timeout,
	}
}

// Type returns the check type this checker handles.
func (c *TCPChecker) Type() CheckType {
	return CheckTCP
}

// Check dials the target address and returns the result.
func (c *TCPChecker) Check(ctx context.Context, check *HealthCheck) (*HealthResult, error) {
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
		result.Message = fmt.Sprintf("tcp dial failed: %v", err)

		return result, nil
	}

	conn.Close()

	result.Status = StatusHealthy

	return result, nil
}
