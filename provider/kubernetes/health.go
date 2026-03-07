package kubernetes

import (
	"context"
	"fmt"
	"time"

	"github.com/xraph/ctrlplane/provider"
)

// HealthCheck tests connectivity to the Kubernetes API server.
func (p *Provider) HealthCheck(ctx context.Context) (*provider.HealthStatus, error) {
	_ = ctx

	start := time.Now()

	_, err := p.client.Discovery().ServerVersion()

	latency := time.Since(start)
	now := time.Now().UTC()

	if err != nil {
		return &provider.HealthStatus{
			Healthy:   false,
			Message:   fmt.Sprintf("API server unreachable: %v", err),
			Latency:   latency,
			CheckedAt: now,
		}, nil
	}

	return &provider.HealthStatus{
		Healthy:   true,
		Message:   "API server reachable",
		Latency:   latency,
		CheckedAt: now,
	}, nil
}
