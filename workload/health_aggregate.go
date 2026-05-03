package workload

import (
	"context"
	"errors"

	"github.com/xraph/ctrlplane/health"
	"github.com/xraph/ctrlplane/id"
)

// GetHealth returns the worst-of-replicas health for the workload.
// Status precedence (most severe first): unhealthy → degraded →
// unknown → healthy. "Unknown" sits above healthy so a workload
// with one replica reporting unknown checks doesn't appear "healthy"
// when we don't actually know.
//
// When the workload has zero replicas, status is unknown.
func (s *service) GetHealth(ctx context.Context, workloadID id.ID) (*WorkloadHealth, error) {
	if s.health == nil {
		return nil, errors.New("workload health: health service not configured")
	}

	replicas, err := s.ListInstances(ctx, workloadID)
	if err != nil {
		return nil, err
	}

	out := &WorkloadHealth{WorkloadID: workloadID, ReplicaCount: len(replicas)}
	if len(replicas) == 0 {
		out.Status = string(health.StatusUnknown)

		return out, nil
	}

	for _, rep := range replicas {
		ih, err := s.health.GetHealth(ctx, rep.ID)
		if err != nil || ih == nil {
			out.UnknownCount++

			continue
		}

		switch ih.Status {
		case health.StatusHealthy:
			out.HealthyCount++
		case health.StatusDegraded:
			out.DegradedCount++
		case health.StatusUnhealthy:
			out.UnhealthyCnt++
		default:
			out.UnknownCount++
		}
	}

	out.Status = string(reduceStatus(out))

	return out, nil
}

// reduceStatus applies the worst-of-replicas precedence.
func reduceStatus(h *WorkloadHealth) health.Status {
	switch {
	case h.UnhealthyCnt > 0:
		return health.StatusUnhealthy
	case h.DegradedCount > 0:
		return health.StatusDegraded
	case h.UnknownCount > 0 && h.HealthyCount == 0:
		return health.StatusUnknown
	case h.UnknownCount > 0:
		// Mixed unknown + healthy → degraded so the badge isn't
		// "all good" when some replicas have no signal.
		return health.StatusDegraded
	default:
		return health.StatusHealthy
	}
}
