package docker

import (
	"encoding/json"
	"io"

	"github.com/docker/docker/api/types/container"

	"github.com/xraph/ctrlplane/provider"
)

// decodeStats reads a single docker StatsJSON document and converts
// it to provider.ResourceUsage. Mirrors `docker stats` formula:
//
//	cpu_pct = (cpuΔ / systemΔ) * num_cpus * 100
//
// where cpuΔ is the container's CPU time delta and systemΔ is the
// host's CPU time delta over the same window. The non-streaming
// stats endpoint includes a "PreCPUStats" snapshot taken just before
// the current one — the deltas are computed off that pair, so a
// single one-shot read is enough.
//
// Memory: docker reports `usage` and `limit` in bytes; we convert
// to MB to match provider.ResourceUsage's contract.
//
// Network: sum across all interfaces; report total bytes received
// + transmitted as MB. The poller diffs these counters across
// consecutive samples to derive a rate at query time.
func decodeStats(r io.Reader) (*provider.ResourceUsage, error) {
	var stats container.StatsResponse
	if err := json.NewDecoder(r).Decode(&stats); err != nil {
		return &provider.ResourceUsage{}, nil //nolint:nilerr // empty body == no sample
	}

	usage := &provider.ResourceUsage{
		CPUPercent: cpuPercent(&stats),
	}

	if stats.MemoryStats.Limit > 0 {
		usage.MemoryUsedMB = bytesToMB(memoryUsage(&stats.MemoryStats))
		usage.MemoryLimitMB = bytesToMB(stats.MemoryStats.Limit)
	}

	var rxBytes, txBytes uint64
	for _, n := range stats.Networks {
		rxBytes += n.RxBytes
		txBytes += n.TxBytes
	}
	usage.NetworkInMB = bytesToMBFloat(rxBytes)
	usage.NetworkOutMB = bytesToMBFloat(txBytes)

	return usage, nil
}

// cpuPercent computes container CPU% over the delta window between
// the current and previous samples docker hands back. Returns 0
// when the deltas are unavailable (cold start, single-shot first
// sample) — the poller smooths this out across subsequent reads.
func cpuPercent(s *container.StatsResponse) float64 {
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(s.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(s.CPUStats.SystemUsage) - float64(s.PreCPUStats.SystemUsage)
	if systemDelta <= 0 || cpuDelta < 0 {
		return 0
	}
	cpus := float64(s.CPUStats.OnlineCPUs)
	if cpus == 0 {
		cpus = float64(len(s.CPUStats.CPUUsage.PercpuUsage))
	}
	if cpus == 0 {
		cpus = 1
	}
	return (cpuDelta / systemDelta) * cpus * 100.0
}

// memoryUsage subtracts cache from raw usage when cgroup-v1 stats
// are present (matches `docker stats`). cgroup-v2 already excludes
// cache, so the raw usage stands.
func memoryUsage(m *container.MemoryStats) uint64 {
	if cache, ok := m.Stats["cache"]; ok {
		if m.Usage > cache {
			return m.Usage - cache
		}
		return 0
	}
	return m.Usage
}

func bytesToMB(b uint64) int {
	return int(b / (1024 * 1024))
}

func bytesToMBFloat(b uint64) float64 {
	return float64(b) / (1024 * 1024)
}
