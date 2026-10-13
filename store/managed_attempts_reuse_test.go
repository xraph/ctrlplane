package store_test

import (
	"runtime"
	"testing"
	"time"
)

func TestManagedRemovalContinuationReadProbe(t *testing.T) {
	f := newRemovalFixture(t, "memory")
	for range 2 {
		if _, err := f.s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
			t.Fatal(err)
		}

		continueRemovalFixture(t, f)
	}

	for _, rid := range f.reservations {
		f.command(rid, "begin")
	}

	runtime.GC()

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	started := time.Now()

	for range 3 {
		if _, err := f.s.ReadManagedRemovalAttempt(t.Context(), f.key); err != nil {
			t.Fatal(err)
		}
	}

	runtime.ReadMemStats(&after)
	t.Logf("read probe: bindings=2 generation=3 iterations=3 elapsed=%s allocations=%d allocated_bytes=%d", time.Since(started), after.Mallocs-before.Mallocs, after.TotalAlloc-before.TotalAlloc)
}
