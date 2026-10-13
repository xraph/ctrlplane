package provider

import (
	"context"
	"errors"
	"fmt"
	"time"
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

// WaitReady polls status until it reports Ready, reports StateFailed, or
// the policy's timeout passes. It polls once straight away. A status read
// that errors is retried on the next tick, since an asynchronous platform
// often reports "not found" for a moment after accepting a request; the
// last error is included if the wait times out. The returned status is the
// last one read, which may be nil.
func WaitReady(ctx context.Context, policy ReadinessPolicy, status StatusFunc) (*InstanceStatus, error) {
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
