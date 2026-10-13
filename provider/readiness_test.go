package provider

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWaitReady(t *testing.T) {
	t.Parallel()

	errTransient := errors.New("service not found yet")
	fast := ReadinessPolicy{Timeout: 200 * time.Millisecond, Interval: time.Millisecond}

	tests := []struct {
		name    string
		replies []func() (*InstanceStatus, error)
		wantErr bool
		wantMsg string
	}{
		{
			name: "ready after a transient error and a pending status",
			replies: []func() (*InstanceStatus, error){
				func() (*InstanceStatus, error) { return nil, errTransient },
				func() (*InstanceStatus, error) { return &InstanceStatus{State: StateStarting}, nil },
				func() (*InstanceStatus, error) { return &InstanceStatus{State: StateRunning, Ready: true}, nil },
			},
		},
		{
			name: "failed status stops the wait",
			replies: []func() (*InstanceStatus, error){
				func() (*InstanceStatus, error) {
					return &InstanceStatus{State: StateFailed, Message: "image pull failed"}, nil
				},
			},
			wantErr: true,
			wantMsg: "image pull failed",
		},
		{
			name: "never ready times out",
			replies: []func() (*InstanceStatus, error){
				func() (*InstanceStatus, error) { return &InstanceStatus{State: StateStarting}, nil },
			},
			wantErr: true,
			wantMsg: "state starting",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			calls := 0

			st, err := WaitReady(context.Background(), fast, func(context.Context) (*InstanceStatus, error) {
				reply := tt.replies[min(calls, len(tt.replies)-1)]
				calls++

				return reply()
			})

			if !tt.wantErr {
				if err != nil || st == nil || !st.Ready {
					t.Fatalf("WaitReady = (%+v, %v), want a ready status", st, err)
				}

				if calls != len(tt.replies) {
					t.Fatalf("status polled %d times, want %d", calls, len(tt.replies))
				}

				return
			}

			if !errors.Is(err, ErrNotReady) {
				t.Fatalf("WaitReady error = %v, want ErrNotReady", err)
			}

			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("error %q does not mention %q", err, tt.wantMsg)
			}
		})
	}
}

func TestWaitReady_ParentCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := WaitReady(ctx, ReadinessPolicy{Interval: time.Millisecond}, func(context.Context) (*InstanceStatus, error) {
		return &InstanceStatus{State: StateStarting}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitReady error = %v, want context.Canceled", err)
	}
}
