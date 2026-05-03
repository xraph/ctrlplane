package kubernetes

import (
	"context"
	"fmt"
	"time"

	"github.com/xraph/ctrlplane/provider"
)

// HealthCheck tests connectivity to the Kubernetes API server.
//
// Uses the discovery REST client directly (instead of
// Discovery().ServerVersion(), which doesn't accept a context)
// so the caller's ctx deadline is honored — without this, an
// unreachable cluster blocked the providerhealth cache sweep
// indefinitely and stalled the studio process at startup.
func (p *Provider) HealthCheck(ctx context.Context) (*provider.HealthStatus, error) {
	start := time.Now()

	rest := p.client.Discovery().RESTClient()
	_, err := rest.Get().AbsPath("/version").DoRaw(ctx)

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
