// Package metrics is ctrlplane's in-process time-series store for
// per-instance resource samples (CPU / memory / network) and
// optional application-layer metrics (request rate, latency P95
// scraped from /metrics endpoints).
//
// The package is intentionally lightweight: an in-memory ring
// buffer per instance, a 10s polling loop, and SSE-friendly fan-out
// for live consumers. Historical buckets are downsampled at query
// time. Samples are lost on restart — operators who need durable
// metrics push samples into Prometheus / VictoriaMetrics / etc. via
// the observability extension.
package metrics

import (
	"time"

	"github.com/xraph/ctrlplane/id"
)

// Sample is one point-in-time measurement for a single instance.
// Resource fields (CPU/Memory/Network) come from the provider's
// Resources() call. Application fields (RequestsPerSec, LatencyP95Ms)
// come from optional /metrics scraping — they're zero-valued when
// no scraper is configured for the workload.
type Sample struct {
	At time.Time `json:"at"`

	// Resource — always populated when poll succeeds.
	CPUPercent            float64 `json:"cpu_percent"`
	MemoryUsedMB          int     `json:"memory_used_mb"`
	MemoryLimitMB         int     `json:"memory_limit_mb"`
	NetworkInBytesPerSec  float64 `json:"network_in_bytes_per_sec"`
	NetworkOutBytesPerSec float64 `json:"network_out_bytes_per_sec"`

	// Application — populated only when the workload exposes a
	// /metrics endpoint AND ctrlplane is configured to scrape it.
	// Zero values mean "no signal", not "zero rate".
	RequestsPerSec float64 `json:"requests_per_sec,omitempty"`
	LatencyP95Ms   float64 `json:"latency_p95_ms,omitempty"`
}

// Series is a sorted slice of Samples. Callers should treat the
// slice as immutable — buckets returned from Range are copies.
type Series []Sample

// RangeQuery describes a query window. Resolution=0 means "auto" — the
// store picks a bucket size that targets ~120 points across the
// window so sparkline rendering stays fluid regardless of range.
type RangeQuery struct {
	Since      time.Time
	Until      time.Time
	Resolution time.Duration
}

// instanceKey isolates ring-buffer ownership in a small wrapper so
// callers don't accidentally key on a string elsewhere.
type instanceKey struct{ id string }

func keyFor(i id.ID) instanceKey { return instanceKey{id: i.String()} }
