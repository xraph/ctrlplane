package metrics

import (
	"sort"
	"sync"
	"time"
)

// ringBuffer is a fixed-capacity circular buffer of Samples. New
// samples overwrite the oldest when capacity is reached. Reads
// snapshot the underlying slice into a chronologically sorted copy
// so callers can iterate without worrying about wraparound.
//
// Size budget at 10s/sample, 7d retention: 60480 samples. Each
// Sample is ~64 bytes → ~4 MB per instance. Operators with large
// workload counts can tighten capacity; the default is set at
// construction time.
type ringBuffer struct {
	mu    sync.RWMutex
	cap   int
	items []Sample // length up to cap; oldest at items[start]
	start int
	size  int
}

func newRingBuffer(capacity int) *ringBuffer {
	if capacity <= 0 {
		capacity = 1
	}

	return &ringBuffer{
		cap:   capacity,
		items: make([]Sample, 0, capacity),
	}
}

// push appends a sample, overwriting the oldest when full. O(1).
func (r *ringBuffer) push(s Sample) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.size < r.cap {
		r.items = append(r.items, s)
		r.size++

		return
	}

	r.items[r.start] = s
	r.start = (r.start + 1) % r.cap
}

// snapshot returns a chronologically sorted copy of every sample
// currently held. Cost is O(n) — safe to call from query paths
// since n is bounded by capacity (default 60k).
func (r *ringBuffer) snapshot() []Sample {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.size == 0 {
		return nil
	}

	out := make([]Sample, r.size)
	for i := range r.size {
		out[i] = r.items[(r.start+i)%r.cap]
	}
	// Defensive: items may briefly be non-monotonic if a sample's
	// At was generated out of order (clock skew on concurrent
	// pollers — shouldn't happen with one poller per instance, but
	// snapshot is meant to be sortable for downstream consumers).
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })

	return out
}

// last returns the most recently pushed sample, ok=false when empty.
func (r *ringBuffer) last() (Sample, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.size == 0 {
		return Sample{}, false
	}

	idx := (r.start + r.size - 1) % r.cap

	return r.items[idx], true
}

// downsample buckets samples by resolution, returning one
// bucket-mean per (since + N*resolution) window inside [since, until).
// Empty buckets are skipped — sparkline renderers handle gaps.
func (r *ringBuffer) downsample(since, until time.Time, resolution time.Duration) []Sample {
	if resolution <= 0 {
		return nil
	}

	all := r.snapshot()
	if len(all) == 0 {
		return nil
	}

	bucket := func(t time.Time) time.Time {
		offset := t.Sub(since)
		if offset < 0 {
			return since
		}

		n := int64(offset / resolution)

		return since.Add(time.Duration(n) * resolution)
	}

	type acc struct {
		count                  int
		cpu, memUsed, memLimit float64
		netIn, netOut          float64
		reqRate, latP95        float64
	}

	buckets := map[time.Time]*acc{}

	for _, s := range all {
		if s.At.Before(since) || !s.At.Before(until) {
			continue
		}

		b := bucket(s.At)

		a, ok := buckets[b]
		if !ok {
			a = &acc{}
			buckets[b] = a
		}

		a.count++
		a.cpu += s.CPUPercent
		a.memUsed += float64(s.MemoryUsedMB)
		a.memLimit += float64(s.MemoryLimitMB)
		a.netIn += s.NetworkInBytesPerSec
		a.netOut += s.NetworkOutBytesPerSec
		a.reqRate += s.RequestsPerSec
		a.latP95 += s.LatencyP95Ms
	}

	out := make([]Sample, 0, len(buckets))
	for ts, a := range buckets {
		c := float64(a.count)
		out = append(out, Sample{
			At:                    ts,
			CPUPercent:            a.cpu / c,
			MemoryUsedMB:          int(a.memUsed / c),
			MemoryLimitMB:         int(a.memLimit / c),
			NetworkInBytesPerSec:  a.netIn / c,
			NetworkOutBytesPerSec: a.netOut / c,
			RequestsPerSec:        a.reqRate / c,
			LatencyP95Ms:          a.latP95 / c,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })

	return out
}

// autoResolution picks a bucket size targeting ~targetPoints buckets
// across the window. Floor at 10s (the poll interval) so we never
// downsample below source resolution.
func autoResolution(window time.Duration, targetPoints int) time.Duration {
	if targetPoints <= 0 {
		targetPoints = 120
	}

	res := max(window/time.Duration(targetPoints), 10*time.Second)

	return res
}
