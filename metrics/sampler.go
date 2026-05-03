package metrics

import (
	"context"
	"time"

	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
)

// instanceSampler bridges metrics.Service to instance.Service so
// the metrics package doesn't have to know about provider
// registries directly. The poller calls Sample(); this resolves
// through instance.Service.Resources(), which routes via the
// instance's provider.
//
// Authentication: the poller runs in a background context with no
// claims, but instance.Service.Resources requires auth. We elevate
// to system-admin per call — the metrics layer is a privileged
// internal consumer, not a per-tenant API surface, so cross-tenant
// elevation is the right posture (the tenant filter would fail
// on a no-op poller context anyway). Callers that want per-tenant
// scoping should use the read APIs (Range, Watch, Latest), which
// happily filter at the handler layer.
type instanceSampler struct {
	instances instance.Service
}

// NewInstanceSampler wires a Sampler that drives off instance.Service.
func NewInstanceSampler(instances instance.Service) Sampler {
	return &instanceSampler{instances: instances}
}

// Sample returns a one-shot Sample. NetworkInBytesPerSec /
// NetworkOutBytesPerSec carry the *cumulative* byte counters here
// — service.runPoller diffs them to derive a true per-second rate.
func (s *instanceSampler) Sample(ctx context.Context, instanceID id.ID) (*Sample, error) {
	elevated := auth.WithClaims(ctx, &auth.Claims{
		SubjectID: "ctrlplane:metrics-poller",
		Roles:     []string{"system:admin"},
	})

	usage, err := s.instances.Resources(elevated, instanceID)
	if err != nil {
		return nil, err
	}
	if usage == nil {
		return nil, nil //nolint:nilnil // empty usage == no sample for this tick
	}

	return &Sample{
		At:                    time.Now(),
		CPUPercent:            usage.CPUPercent,
		MemoryUsedMB:          usage.MemoryUsedMB,
		MemoryLimitMB:         usage.MemoryLimitMB,
		NetworkInBytesPerSec:  usage.NetworkInMB * (1024 * 1024), // cumulative bytes; service derives rate
		NetworkOutBytesPerSec: usage.NetworkOutMB * (1024 * 1024),
	}, nil
}
