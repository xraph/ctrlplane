package metrics

import (
	"testing"
	"time"
)

// TestRingBuffer_PushOverwritesOldest covers the wraparound case:
// a 3-slot ring fed 5 samples drops the first 2 and snapshots 3.
func TestRingBuffer_PushOverwritesOldest(t *testing.T) {
	r := newRingBuffer(3)

	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 5 {
		r.push(Sample{At: base.Add(time.Duration(i) * time.Second), CPUPercent: float64(i)})
	}

	got := r.snapshot()
	if len(got) != 3 {
		t.Fatalf("snapshot length: want 3, got %d", len(got))
	}

	if got[0].CPUPercent != 2 || got[1].CPUPercent != 3 || got[2].CPUPercent != 4 {
		t.Fatalf("snapshot values: want [2 3 4], got %v", []float64{got[0].CPUPercent, got[1].CPUPercent, got[2].CPUPercent})
	}
}

// TestRingBuffer_LastReturnsLatest sanity-checks the Latest hot path.
func TestRingBuffer_LastReturnsLatest(t *testing.T) {
	r := newRingBuffer(4)
	if _, ok := r.last(); ok {
		t.Fatal("last on empty ring should return ok=false")
	}

	r.push(Sample{CPUPercent: 1})
	r.push(Sample{CPUPercent: 2})

	last, ok := r.last()
	if !ok || last.CPUPercent != 2 {
		t.Fatalf("last: want CPU=2 ok=true, got %v ok=%v", last.CPUPercent, ok)
	}
}

// TestRingBuffer_DownsampleAveragesPerBucket asserts bucket means
// across 10 samples downsampled to 5s buckets within a 50s window.
func TestRingBuffer_DownsampleAveragesPerBucket(t *testing.T) {
	r := newRingBuffer(20)
	since := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	// 10 samples, one per second.
	for i := range 10 {
		r.push(Sample{
			At:           since.Add(time.Duration(i) * time.Second),
			CPUPercent:   float64(i),
			MemoryUsedMB: i * 10,
		})
	}

	until := since.Add(10 * time.Second)
	buckets := r.downsample(since, until, 5*time.Second)

	if len(buckets) != 2 {
		t.Fatalf("buckets: want 2, got %d", len(buckets))
	}
	// Bucket 0: samples 0..4 → mean CPU = 2.0, mean Mem = 20
	if buckets[0].CPUPercent != 2.0 || buckets[0].MemoryUsedMB != 20 {
		t.Fatalf("bucket 0: want cpu=2 mem=20, got cpu=%v mem=%v", buckets[0].CPUPercent, buckets[0].MemoryUsedMB)
	}
	// Bucket 1: samples 5..9 → mean CPU = 7.0, mean Mem = 70
	if buckets[1].CPUPercent != 7.0 || buckets[1].MemoryUsedMB != 70 {
		t.Fatalf("bucket 1: want cpu=7 mem=70, got cpu=%v mem=%v", buckets[1].CPUPercent, buckets[1].MemoryUsedMB)
	}
}

// TestAutoResolution_NeverBelowSourceFrequency asserts the floor
// guard so a "1m range" doesn't ask for sub-10s buckets that
// wouldn't have multiple samples each.
func TestAutoResolution_NeverBelowSourceFrequency(t *testing.T) {
	got := autoResolution(time.Minute, 120) // 60s / 120 = 0.5s, but floor is 10s
	if got != 10*time.Second {
		t.Fatalf("want 10s floor, got %v", got)
	}

	got = autoResolution(24*time.Hour, 120) // 24h / 120 = 12m
	if got != 12*time.Minute {
		t.Fatalf("want 12m, got %v", got)
	}
}
