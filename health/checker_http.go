package health

import (
	"context"
	"fmt"
	"net/http"
	"time"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/id"
)

// HTTPChecker performs HTTP GET health checks against a target URL.
type HTTPChecker struct {
	client *http.Client
}

// NewHTTPChecker creates a new HTTP health checker with the given timeout.
func NewHTTPChecker(timeout time.Duration) *HTTPChecker {
	return &HTTPChecker{
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// Type returns the check type this checker handles.
func (c *HTTPChecker) Type() CheckType {
	return CheckHTTP
}

// Check executes an HTTP GET against the health check target and returns the result.
func (c *HTTPChecker) Check(ctx context.Context, check *HealthCheck) (*HealthResult, error) {
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, check.Target, nil)
	if err != nil {
		return nil, fmt.Errorf("http checker: build request: %w", err)
	}

	resp, err := c.client.Do(req)

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
		result.Message = err.Error()

		return result, nil
	}

	defer resp.Body.Close()

	result.StatusCode = resp.StatusCode

	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusBadRequest {
		result.Status = StatusHealthy
	} else {
		result.Status = StatusUnhealthy
		result.Message = fmt.Sprintf("unexpected status code: %d", resp.StatusCode)
	}

	return result, nil
}
