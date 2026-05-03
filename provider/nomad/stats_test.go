package nomad

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xraph/ctrlplane/id"
)

// TestFetchAllocResources_HappyPath drives the full HTTP flow
// against a httptest stand-in: list job allocs → fetch per-alloc
// stats → assert sums.
func TestFetchAllocResources_HappyPath(t *testing.T) {
	t.Parallel()

	instID := id.New(id.PrefixInstance)
	jobName := nomadJobName(instID)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/job/" + jobName + "/allocations":
			_ = json.NewEncoder(w).Encode([]nomadAlloc{
				{ID: "alloc-1", ClientStatus: "running"},
				{ID: "alloc-2", ClientStatus: "running"},
				{ID: "alloc-3", ClientStatus: "complete"}, // should be skipped
			})
		case "/v1/client/allocation/alloc-1/stats":
			_ = json.NewEncoder(w).Encode(nomadAllocStats{
				ResourceUsage: &nomadResourceUsage{
					CPUStats:    &nomadCPUStats{Percent: 12.5},
					MemoryStats: &nomadMemoryStats{RSS: 64 * 1024 * 1024},
				},
			})
		case "/v1/client/allocation/alloc-2/stats":
			_ = json.NewEncoder(w).Encode(nomadAllocStats{
				ResourceUsage: &nomadResourceUsage{
					CPUStats:    &nomadCPUStats{Percent: 7.5},
					MemoryStats: &nomadMemoryStats{RSS: 32 * 1024 * 1024},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	usage, err := fetchAllocResources(context.Background(), srv.URL, "default", instID)
	if err != nil {
		t.Fatalf("fetchAllocResources: %v", err)
	}
	if usage.CPUPercent != 20.0 {
		t.Fatalf("CPUPercent: want 20.0 (12.5+7.5), got %v", usage.CPUPercent)
	}
	if usage.MemoryUsedMB != 96 {
		t.Fatalf("MemoryUsedMB: want 96 (64+32), got %d", usage.MemoryUsedMB)
	}
}

// TestFetchAllocResources_JobMissingReturnsZero asserts the "no
// sample" semantic when the job hasn't been created yet — the
// metrics poller treats a zero usage as a gap, not an error.
func TestFetchAllocResources_JobMissingReturnsZero(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer srv.Close()

	usage, err := fetchAllocResources(context.Background(), srv.URL, "default", id.New(id.PrefixInstance))
	if err != nil {
		t.Fatalf("fetchAllocResources should not error on missing job: %v", err)
	}
	if usage.CPUPercent != 0 || usage.MemoryUsedMB != 0 {
		t.Fatalf("missing job should yield zero usage, got %+v", usage)
	}
}

// TestHealthCheck_AgentReachable asserts a 200 from /v1/agent/health
// flips Healthy=true and the message describes reachability.
func TestHealthCheck_AgentReachable(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agent/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p, err := New(WithAddress(srv.URL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	hs, err := p.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if !hs.Healthy {
		t.Fatalf("expected healthy, got %+v", hs)
	}
}

// TestHealthCheck_AgentDown — connection refused → unhealthy.
// Use a port we know nothing listens on.
func TestHealthCheck_AgentDown(t *testing.T) {
	t.Parallel()
	p, err := New(WithAddress("http://127.0.0.1:1")) // nothing on port 1
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	hs, err := p.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("HealthCheck should not error on unreachable: %v", err)
	}
	if hs.Healthy {
		t.Fatal("expected unhealthy when agent unreachable")
	}
	if hs.Message == "" {
		t.Fatal("unhealthy message must describe the failure")
	}
}

// TestFetchAllocResources_PerTaskFallback covers the edge case
// where ResourceUsage is nil and only per-task stats are populated
// (older Nomad versions or some driver shapes).
func TestFetchAllocResources_PerTaskFallback(t *testing.T) {
	t.Parallel()

	instID := id.New(id.PrefixInstance)
	jobName := nomadJobName(instID)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/job/" + jobName + "/allocations":
			_ = json.NewEncoder(w).Encode([]nomadAlloc{{ID: "alloc-x", ClientStatus: "running"}})
		case "/v1/client/allocation/alloc-x/stats":
			_ = json.NewEncoder(w).Encode(nomadAllocStats{
				Tasks: map[string]*nomadTaskStats{
					"app": {
						ResourceUsage: &nomadResourceUsage{
							CPUStats:    &nomadCPUStats{Percent: 33.3},
							MemoryStats: &nomadMemoryStats{RSS: 16 * 1024 * 1024},
						},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	usage, err := fetchAllocResources(context.Background(), srv.URL, "default", instID)
	if err != nil {
		t.Fatalf("fetchAllocResources: %v", err)
	}
	if usage.CPUPercent != 33.3 {
		t.Fatalf("CPUPercent: want 33.3 (per-task), got %v", usage.CPUPercent)
	}
	if usage.MemoryUsedMB != 16 {
		t.Fatalf("MemoryUsedMB: want 16 (per-task), got %d", usage.MemoryUsedMB)
	}
}
