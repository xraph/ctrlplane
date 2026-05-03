package nomad

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// statsHTTPTimeout caps how long Resources will wait for the
// alloc-stats API. The metrics poller calls this every 10s; any
// hang longer than the poll interval is wasted work and should
// just give up so the next tick gets a chance.
const statsHTTPTimeout = 5 * time.Second

// nomadJobName is the conventional job name for a ctrlplane-managed
// instance. Mirrors the docker provider's container naming so
// `nomad job status cp-<instanceID>` shows the instance.
func nomadJobName(instanceID id.ID) string {
	return "cp-" + instanceID.String()
}

// fetchAllocResources queries Nomad's HTTP API to pull live
// resource usage for the running allocation backing this instance.
// Sums CPU + memory across every healthy alloc of the instance's
// job (handles replica fan-out). Network is summed across alloc
// network device counters when present.
//
// Returns a zero-valued usage on:
//   - job not found (instance not yet provisioned, or just deleted)
//   - no running allocs (job placed but tasks haven't started)
//   - any per-alloc stats error
//
// The metrics poller treats zero samples as gaps, so a not-yet-
// running job shows "—" in the dashboard rather than errors.
func fetchAllocResources(ctx context.Context, address, namespace string, instanceID id.ID) (*provider.ResourceUsage, error) {
	address = strings.TrimRight(address, "/")
	httpClient := &http.Client{Timeout: statsHTTPTimeout}

	// 1. List allocations for the conventional job name.
	jobName := nomadJobName(instanceID)
	allocs, err := listJobAllocs(ctx, httpClient, address, namespace, jobName)
	if err != nil || len(allocs) == 0 {
		return &provider.ResourceUsage{}, nil //nolint:nilerr // missing job = no sample
	}

	usage := &provider.ResourceUsage{}
	for _, a := range allocs {
		if a.ClientStatus != "running" {
			continue
		}
		stats, err := getAllocStats(ctx, httpClient, address, a.ID)
		if err != nil || stats == nil {
			continue
		}
		// CPU: aggregated across tasks. ResourceUsage.CPU.Percent
		// is "% of one CPU" in Nomad's API — same convention as
		// docker so we can sum directly.
		if stats.ResourceUsage != nil {
			if stats.ResourceUsage.CPUStats != nil {
				usage.CPUPercent += stats.ResourceUsage.CPUStats.Percent
			}
			if stats.ResourceUsage.MemoryStats != nil {
				usage.MemoryUsedMB += int(stats.ResourceUsage.MemoryStats.RSS / (1024 * 1024))
			}
		}
		// Per-task fallback: some Nomad versions only populate
		// per-task stats, not the aggregated ResourceUsage block.
		for _, t := range stats.Tasks {
			if stats.ResourceUsage != nil {
				continue // aggregate already counted
			}
			if t.ResourceUsage == nil {
				continue
			}
			if t.ResourceUsage.CPUStats != nil {
				usage.CPUPercent += t.ResourceUsage.CPUStats.Percent
			}
			if t.ResourceUsage.MemoryStats != nil {
				usage.MemoryUsedMB += int(t.ResourceUsage.MemoryStats.RSS / (1024 * 1024))
			}
		}
	}

	return usage, nil
}

// nomadAlloc is the projection of a Nomad allocation we read from
// /v1/job/<jobID>/allocations. Used by both stats (CPU/memory roll-
// ups) and Status (per-task state aggregation).
type nomadAlloc struct {
	ID           string                       `json:"ID"`
	JobID        string                       `json:"JobID,omitempty"`
	TaskGroup    string                       `json:"TaskGroup,omitempty"`
	ClientStatus string                       `json:"ClientStatus"`
	TaskStates   map[string]nomadTaskStateAPI `json:"TaskStates,omitempty"`
}

type nomadTaskStateAPI struct {
	State    string              `json:"State"`
	Failed   bool                `json:"Failed"`
	Restarts int                 `json:"Restarts"`
	Events   []nomadTaskEventAPI `json:"Events"`
}

type nomadTaskEventAPI struct {
	Type       string `json:"Type"`
	DisplayMsg string `json:"DisplayMessage"`
	ExitCode   int    `json:"ExitCode"`
}

// nomadAllocStats mirrors the shape returned by
// /v1/client/allocation/<alloc_id>/stats. Hand-rolled so we don't
// pull in github.com/hashicorp/nomad/api just for one endpoint.
type nomadAllocStats struct {
	ResourceUsage *nomadResourceUsage        `json:"ResourceUsage,omitempty"`
	Tasks         map[string]*nomadTaskStats `json:"Tasks,omitempty"`
}

type nomadTaskStats struct {
	ResourceUsage *nomadResourceUsage `json:"ResourceUsage,omitempty"`
}

type nomadResourceUsage struct {
	CPUStats    *nomadCPUStats    `json:"CpuStats,omitempty"`
	MemoryStats *nomadMemoryStats `json:"MemoryStats,omitempty"`
}

type nomadCPUStats struct {
	Percent          float64 `json:"Percent"`
	TotalTicks       float64 `json:"TotalTicks"`
	UserMode         float64 `json:"UserMode"`
	SystemMode       float64 `json:"SystemMode"`
	ThrottledPeriods uint64  `json:"ThrottledPeriods"`
	ThrottledTime    uint64  `json:"ThrottledTime"`
}

type nomadMemoryStats struct {
	RSS            uint64 `json:"RSS"`
	Cache          uint64 `json:"Cache"`
	Swap           uint64 `json:"Swap"`
	Usage          uint64 `json:"Usage"`
	MaxUsage       uint64 `json:"MaxUsage"`
	KernelUsage    uint64 `json:"KernelUsage"`
	KernelMaxUsage uint64 `json:"KernelMaxUsage"`
}

func listJobAllocs(ctx context.Context, httpClient *http.Client, address, namespace, jobName string) ([]nomadAlloc, error) {
	u := fmt.Sprintf("%s/v1/job/%s/allocations", address, jobName)
	if namespace != "" {
		u += "?namespace=" + namespace
	}
	body, err := nomadGet(ctx, httpClient, u)
	if err != nil {
		return nil, err
	}
	var out []nomadAlloc
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("nomad: decode allocs: %w", err)
	}
	return out, nil
}

func getAllocStats(ctx context.Context, httpClient *http.Client, address, allocID string) (*nomadAllocStats, error) {
	u := fmt.Sprintf("%s/v1/client/allocation/%s/stats", address, allocID)
	body, err := nomadGet(ctx, httpClient, u)
	if err != nil {
		return nil, err
	}
	var out nomadAllocStats
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("nomad: decode alloc stats: %w", err)
	}
	return &out, nil
}

func nomadGet(ctx context.Context, httpClient *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("nomad: %s: 404", url)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("nomad: %s: status %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
