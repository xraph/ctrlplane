package metrics

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
)

// fakeSampler returns scripted samples; the test drives Sample()
// counter for assertions.
type fakeSampler struct {
	mu      sync.Mutex
	calls   atomic.Int32
	samples []Sample
	err     error
}

func (f *fakeSampler) Sample(_ context.Context, _ id.ID) (*Sample, error) {
	idx := int(f.calls.Add(1)) - 1
	if f.err != nil {
		return nil, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if idx >= len(f.samples) {
		// repeat the last sample so the poller has something to push
		s := f.samples[len(f.samples)-1]
		s.At = time.Now()
		return &s, nil
	}
	s := f.samples[idx]
	if s.At.IsZero() {
		s.At = time.Now()
	}
	return &s, nil
}

// TestService_TrackPushesSamples drives Track and asserts the
// poller produces samples that land in Latest + Watch.
func TestService_TrackPushesSamples(t *testing.T) {
	t.Parallel()

	sampler := &fakeSampler{
		samples: []Sample{
			{CPUPercent: 5, MemoryUsedMB: 100, NetworkInBytesPerSec: 1024},
			{CPUPercent: 10, MemoryUsedMB: 110, NetworkInBytesPerSec: 2048},
		},
	}
	svc := NewService(sampler, Config{
		PollInterval:      30 * time.Millisecond,
		RetentionCapacity: 16,
		WatchBuffer:       8,
	})

	instID := id.New(id.PrefixInstance)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := svc.Watch(ctx, instID)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	svc.Track(instID)
	defer svc.Untrack(instID)

	// Wait for at least 2 samples to land.
	deadline := time.After(500 * time.Millisecond)
	got := 0
	for got < 2 {
		select {
		case <-ch:
			got++
		case <-deadline:
			t.Fatalf("only received %d samples in 500ms", got)
		}
	}

	if last, ok := svc.Latest(instID); !ok {
		t.Fatal("Latest returned ok=false after samples landed")
	} else if last.CPUPercent == 0 {
		t.Fatal("Latest sample has zero CPU — pipeline didn't carry through")
	}
}

// TestService_UntrackClosesWatcherAndStopsPoller asserts cleanup
// semantics: Untrack closes any active watcher and the sampler
// stops being called.
func TestService_UntrackClosesWatcherAndStopsPoller(t *testing.T) {
	t.Parallel()

	sampler := &fakeSampler{samples: []Sample{{CPUPercent: 1}}}
	svc := NewService(sampler, Config{
		PollInterval:      20 * time.Millisecond,
		RetentionCapacity: 16,
		WatchBuffer:       8,
	})

	instID := id.New(id.PrefixInstance)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := svc.Watch(ctx, instID)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	svc.Track(instID)
	time.Sleep(60 * time.Millisecond)

	beforeCalls := sampler.calls.Load()
	svc.Untrack(instID)

	// Watcher should close.
	select {
	case _, ok := <-ch:
		// drain anything that snuck in before close
		for ok {
			_, ok = <-ch
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("watch channel did not close within 200ms of Untrack")
	}

	// Sampler should stop being called.
	time.Sleep(80 * time.Millisecond)
	afterCalls := sampler.calls.Load()
	if afterCalls > beforeCalls+1 {
		// allow one in-flight tick
		t.Fatalf("sampler kept ticking after Untrack: before=%d after=%d", beforeCalls, afterCalls)
	}
}

// TestService_NetworkRateDerivedFromCumulative asserts the bytes-
// per-second derivation: feed two cumulative samples 100ms apart
// with a 102400-byte delta; expect ~1024000 bytes/sec.
func TestService_NetworkRateDerivedFromCumulative(t *testing.T) {
	t.Parallel()

	now := time.Now()
	sampler := &fakeSampler{
		samples: []Sample{
			{At: now, NetworkInBytesPerSec: 0, NetworkOutBytesPerSec: 0},
			{At: now.Add(100 * time.Millisecond), NetworkInBytesPerSec: 102400, NetworkOutBytesPerSec: 51200},
		},
	}
	svc := NewService(sampler, Config{
		PollInterval:      30 * time.Millisecond,
		RetentionCapacity: 16,
		WatchBuffer:       8,
	})

	instID := id.New(id.PrefixInstance)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := svc.Watch(ctx, instID)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	svc.Track(instID)
	defer svc.Untrack(instID)

	// Drain first sample (prevAt set; rates zero).
	select {
	case s := <-ch:
		if s.NetworkInBytesPerSec != 0 {
			t.Fatalf("first sample rate must be 0 (no prev), got %v", s.NetworkInBytesPerSec)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("no first sample within 500ms")
	}

	// Second sample carries the cumulative delta — service should
	// have derived a positive bytes/sec rate.
	select {
	case s := <-ch:
		if s.NetworkInBytesPerSec <= 0 {
			t.Fatalf("expected positive in-rate, got %v", s.NetworkInBytesPerSec)
		}
		if s.NetworkOutBytesPerSec <= 0 {
			t.Fatalf("expected positive out-rate, got %v", s.NetworkOutBytesPerSec)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("no second sample within 500ms")
	}
}
