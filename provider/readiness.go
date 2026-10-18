package provider

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/xraph/ctrlplane/internal/obs"
)

// Defaults used when a ReadinessPolicy leaves a field at zero.
const (
	DefaultReadinessTimeout  = 5 * time.Minute
	DefaultReadinessInterval = 5 * time.Second
)

// ErrNotReady is returned when a workload fails, or does not become ready
// before its readiness timeout, after the provider call that created or
// updated it returned.
var ErrNotReady = errors.New("ctrlplane: workload did not become ready")

// ReadinessWaiter is an optional interface for providers whose Provision
// and Deploy return before the workload is actually serving, which is how
// most managed container platforms (Cloud Run, ECS, Fly) behave. When a
// provider implements it, Ctrl Plane polls the provider's status after
// those calls and only records success once the status reports Ready.
// A status in StateFailed, or no readiness before the timeout, fails the
// instance or the deployment.
//
// Providers that finish their work inside Provision and Deploy, like the
// built-in docker provider, need not implement it.
//
// A status read that fails is retried. If the platform reports "not found"
// for a moment after accepting a request, wrap ctrlplane.ErrNotFound in the
// error: telemetry then records that read as not_found instead of a failed
// provider call.
type ReadinessWaiter interface {
	ReadinessPolicy() ReadinessPolicy
}

// ReadinessPolicy says how long to wait for readiness and how often to
// poll. Zero values fall back to DefaultReadinessTimeout and
// DefaultReadinessInterval.
type ReadinessPolicy struct {
	Timeout  time.Duration `json:"timeout"`
	Interval time.Duration `json:"interval"`
}

// StatusFunc reads the current status of the workload being waited on.
type StatusFunc func(ctx context.Context) (*InstanceStatus, error)

// Readiness outcomes, as ReadinessOutcome reports them.
const (
	ReadinessReady    = "ready"
	ReadinessFailed   = "failed"
	ReadinessTimeout  = "timeout"
	ReadinessCanceled = "canceled"
)

// ReadinessOutcome says how a WaitReady call that returned st and err
// ended: ready, failed (the provider reported StateFailed), canceled (the
// caller's context was cancelled) or timeout.
func ReadinessOutcome(st *InstanceStatus, err error) string {
	switch {
	case err == nil:
		return ReadinessReady
	case errors.Is(err, context.Canceled):
		return ReadinessCanceled
	case st != nil && st.State == StateFailed:
		return ReadinessFailed
	default:
		return ReadinessTimeout
	}
}

// WaitReady polls status until it reports Ready, reports StateFailed, or
// the policy's timeout passes. It polls once straight away. A status read
// that errors is retried on the next tick, since an asynchronous platform
// often reports "not found" for a moment after accepting a request; the
// last error is included if the wait times out. The returned status is the
// last one read, which may be nil.
//
// Under a traced context the wait gets a "wait for instance readiness" span
// recording how many times it polled and how it ended.
func WaitReady(ctx context.Context, policy ReadinessPolicy, status StatusFunc) (*InstanceStatus, error) {
	ctx, span := obs.StartChild(ctx, "wait for instance readiness")
	defer span.End()

	polls := 0

	st, err := waitReady(ctx, policy, func(ctx context.Context) (*InstanceStatus, error) {
		polls++

		return status(ctx)
	})

	outcome := ReadinessOutcome(st, err)
	span.SetAttributes(obs.AttrReadinessPolls.Int(polls), obs.AttrReadinessOutcome.String(outcome))

	if outcome == ReadinessFailed || outcome == ReadinessTimeout {
		obs.Fail(span, err, obs.Sentinel{Err: ErrNotReady, Name: "provider.ErrNotReady"})
	}

	return st, err
}

func waitReady(ctx context.Context, policy ReadinessPolicy, status StatusFunc) (*InstanceStatus, error) {
	timeout := policy.Timeout
	if timeout <= 0 {
		timeout = DefaultReadinessTimeout
	}

	interval := policy.Interval
	if interval <= 0 {
		interval = DefaultReadinessInterval
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var (
		last    *InstanceStatus
		lastErr error
	)

	for {
		st, err := status(ctx)

		switch {
		case err != nil:
			lastErr = err
		case st != nil:
			last, lastErr = st, nil

			if st.State == StateFailed {
				return st, fmt.Errorf("%w: %s", ErrNotReady, describe(st))
			}

			if st.Ready {
				return st, nil
			}
		}

		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return last, fmt.Errorf("wait for readiness: %w", ctx.Err())
			}

			if lastErr != nil {
				return last, fmt.Errorf("%w within %s: last status read failed: %w", ErrNotReady, timeout, lastErr)
			}

			return last, fmt.Errorf("%w within %s: %s", ErrNotReady, timeout, describe(last))
		case <-ticker.C:
		}
	}
}

// describe renders a status for an error message.
func describe(st *InstanceStatus) string {
	if st == nil {
		return "no status reported"
	}

	if st.Message != "" {
		return fmt.Sprintf("state %s: %s", st.State, st.Message)
	}

	return "state " + string(st.State)
}
