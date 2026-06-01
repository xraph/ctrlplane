package metrics

import (
	"context"
	"sync"
	"time"

	"github.com/xraph/ctrlplane/id"
)

// service is the concrete Service. Holds a per-instance ring
// buffer + a per-instance poller goroutine. Watch subscribers fan
// out from push().
type service struct {
	cfg     Config
	sampler Sampler

	mu      sync.RWMutex
	rings   map[instanceKey]*ringBuffer
	pollers map[instanceKey]context.CancelFunc

	subsMu sync.RWMutex
	subs   map[instanceKey][]chan Sample
}

// NewService wires the metrics service. The Sampler is invoked
// once per PollInterval per tracked instance; failures are
// silently dropped (transient container restarts shouldn't show up
// as poller errors in the request path).
func NewService(sampler Sampler, cfg Config) Service {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultConfig().PollInterval
	}

	if cfg.RetentionCapacity <= 0 {
		cfg.RetentionCapacity = DefaultConfig().RetentionCapacity
	}

	if cfg.WatchBuffer <= 0 {
		cfg.WatchBuffer = DefaultConfig().WatchBuffer
	}

	return &service{
		cfg:     cfg,
		sampler: sampler,
		rings:   make(map[instanceKey]*ringBuffer),
		pollers: make(map[instanceKey]context.CancelFunc),
		subs:    make(map[instanceKey][]chan Sample),
	}
}

// Track is idempotent. First call spawns the poller; subsequent
// calls return immediately.
func (s *service) Track(instanceID id.ID) {
	key := keyFor(instanceID)

	s.mu.Lock()
	if _, exists := s.pollers[key]; exists {
		s.mu.Unlock()

		return
	}

	if _, exists := s.rings[key]; !exists {
		s.rings[key] = newRingBuffer(s.cfg.RetentionCapacity)
	}

	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // cancel is retained in s.pollers and invoked by Untrack
	s.pollers[key] = cancel
	s.mu.Unlock()

	go s.runPoller(ctx, instanceID)
}

// Untrack stops the poller and drops the ring + subscribers. A
// later Track restarts from empty.
func (s *service) Untrack(instanceID id.ID) {
	key := keyFor(instanceID)

	s.mu.Lock()
	if cancel, ok := s.pollers[key]; ok {
		cancel()
		delete(s.pollers, key)
	}

	delete(s.rings, key)
	s.mu.Unlock()

	// Drain + close all subs for this instance — Watch consumers
	// see the channel close and exit.
	s.subsMu.Lock()
	for _, ch := range s.subs[key] {
		close(ch)
	}

	delete(s.subs, key)
	s.subsMu.Unlock()
}

func (s *service) Range(_ context.Context, instanceID id.ID, q RangeQuery) (Series, error) {
	key := keyFor(instanceID)

	s.mu.RLock()
	ring, ok := s.rings[key]
	s.mu.RUnlock()

	if !ok {
		return nil, nil
	}

	if q.Until.IsZero() {
		q.Until = time.Now()
	}

	if q.Since.IsZero() {
		q.Since = q.Until.Add(-1 * time.Hour)
	}

	resolution := q.Resolution
	if resolution <= 0 {
		resolution = autoResolution(q.Until.Sub(q.Since), 120)
	}

	return ring.downsample(q.Since, q.Until, resolution), nil
}

func (s *service) Latest(instanceID id.ID) (Sample, bool) {
	key := keyFor(instanceID)

	s.mu.RLock()
	ring, ok := s.rings[key]
	s.mu.RUnlock()

	if !ok {
		return Sample{}, false
	}

	return ring.last()
}

// Watch registers a subscriber. Send is non-blocking — slow
// consumers drop.
func (s *service) Watch(ctx context.Context, instanceID id.ID) (<-chan Sample, error) {
	key := keyFor(instanceID)
	ch := make(chan Sample, s.cfg.WatchBuffer)

	s.subsMu.Lock()
	s.subs[key] = append(s.subs[key], ch)
	s.subsMu.Unlock()

	go func() {
		<-ctx.Done()
		s.subsMu.Lock()
		defer s.subsMu.Unlock()

		list := s.subs[key]
		for i, c := range list {
			if c == ch {
				s.subs[key] = append(list[:i], list[i+1:]...)

				close(ch)

				return
			}
		}
	}()

	return ch, nil
}

// runPoller is the per-instance polling loop. Network rates are
// derived from cumulative byte counters by diffing against the
// previous sample's totals.
func (s *service) runPoller(ctx context.Context, instanceID id.ID) {
	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()

	var (
		prevAt          time.Time
		prevNetInBytes  float64
		prevNetOutBytes float64
	)

	tick := func() {
		sampleCtx, cancel := context.WithTimeout(ctx, s.cfg.PollInterval)
		defer cancel()

		raw, err := s.sampler.Sample(sampleCtx, instanceID)
		if err != nil || raw == nil {
			return
		}

		now := raw.At
		if now.IsZero() {
			now = time.Now()
			raw.At = now
		}

		// Derive bytes/sec from cumulative counters. The Sampler
		// hands us raw cumulative totals stuffed into the rate
		// fields — that's the contract: the sampler reports
		// cumulative, the service derives rates.
		if !prevAt.IsZero() {
			dt := now.Sub(prevAt).Seconds()
			if dt > 0 {
				inDelta := raw.NetworkInBytesPerSec - prevNetInBytes
				outDelta := raw.NetworkOutBytesPerSec - prevNetOutBytes

				if inDelta < 0 {
					inDelta = 0 // counter reset (container restart)
				}

				if outDelta < 0 {
					outDelta = 0
				}
				// Stash cumulative for next iteration before we
				// rewrite into per-second rates.
				prevNetInBytes = raw.NetworkInBytesPerSec
				prevNetOutBytes = raw.NetworkOutBytesPerSec
				raw.NetworkInBytesPerSec = inDelta / dt
				raw.NetworkOutBytesPerSec = outDelta / dt
			}
		} else {
			prevNetInBytes = raw.NetworkInBytesPerSec
			prevNetOutBytes = raw.NetworkOutBytesPerSec
			raw.NetworkInBytesPerSec = 0
			raw.NetworkOutBytesPerSec = 0
		}

		prevAt = now

		s.push(instanceID, *raw)
	}

	// First tick immediately so users don't wait an interval to
	// see the first sample.
	tick()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick()
		}
	}
}

// push stores the sample and fans out to subscribers. Send is
// non-blocking — slow consumers drop.
func (s *service) push(instanceID id.ID, sample Sample) {
	key := keyFor(instanceID)

	s.mu.RLock()
	ring := s.rings[key]
	s.mu.RUnlock()

	if ring == nil {
		return // Untrack raced with the poller; drop.
	}

	ring.push(sample)

	s.subsMu.RLock()
	defer s.subsMu.RUnlock()

	for _, ch := range s.subs[key] {
		select {
		case ch <- sample:
		default:
		}
	}
}
