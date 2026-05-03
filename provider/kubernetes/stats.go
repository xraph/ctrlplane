package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// fetchPodMetrics queries metrics.k8s.io/v1beta1 for every pod
// matching the instance's label selector and sums per-container
// CPU + memory usage. Network usage isn't covered by the metrics
// API — that would need Prometheus / kubelet's /stats/summary.
//
// Returns a zero-valued ResourceUsage when:
//   - no pods match (the workload hasn't started yet, or just got
//     deleted)
//   - metrics-server isn't installed (the API endpoint 404s)
//   - any per-pod metric request errors
//
// The metrics package's poller treats those zero samples as gaps,
// not failures, so a cluster without metrics-server installed shows
// "—" in the dashboard rather than perpetual errors.
func fetchPodMetrics(ctx context.Context, client kubernetes.Interface, namespace string, instanceID id.ID) (*provider.ResourceUsage, error) {
	selector := instanceSelector(instanceID)
	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return &provider.ResourceUsage{}, nil //nolint:nilerr // no pods = no sample, not a failure
	}
	if len(pods.Items) == 0 {
		return &provider.ResourceUsage{}, nil
	}

	usage := &provider.ResourceUsage{}
	for i := range pods.Items {
		pod := &pods.Items[i]
		pm, err := getPodMetrics(ctx, client, namespace, pod.Name)
		if err != nil || pm == nil {
			continue
		}
		// Resource quotas are reported per-container; sum across
		// containers in the pod.
		var cpuMillis int64
		var memBytes int64
		for _, c := range pm.Containers {
			cpuMillis += parseCPUQuantity(c.Usage.CPU)
			memBytes += parseMemoryQuantity(c.Usage.Memory)
		}

		// Limits come from the pod spec, not metrics — sum container
		// memory limits across the pod for a "MemoryLimitMB" value
		// the dashboard can compare against.
		var memLimitBytes int64
		for _, c := range pod.Spec.Containers {
			if q, ok := c.Resources.Limits["memory"]; ok {
				memLimitBytes += q.Value()
			}
		}

		// CPUPercent is a tricky shape on k8s — there's no direct
		// "% of host" the way docker reports it. We surface
		// millicpus-as-percent (1000m = 100% of one core) so the
		// metric is comparable across pods when CPU limits are set.
		usage.CPUPercent += float64(cpuMillis) / 10.0 // 1000m → 100%
		usage.MemoryUsedMB += int(memBytes / (1024 * 1024))
		usage.MemoryLimitMB += int(memLimitBytes / (1024 * 1024))
	}

	return usage, nil
}

// podMetrics is a minimal decoded shape for the
// metrics.k8s.io/v1beta1 PodMetrics resource. Hand-rolled so we
// don't pull in the k8s.io/metrics module just for one endpoint.
type podMetrics struct {
	Containers []containerMetrics `json:"containers"`
}

type containerMetrics struct {
	Name  string        `json:"name"`
	Usage usageQuantity `json:"usage"`
}

type usageQuantity struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

// getPodMetrics issues the raw REST call. Using the discovery
// client's REST primitives avoids the k8s.io/metrics dep.
func getPodMetrics(ctx context.Context, client kubernetes.Interface, namespace, podName string) (*podMetrics, error) {
	rest := client.Discovery().RESTClient()
	path := fmt.Sprintf("/apis/metrics.k8s.io/v1beta1/namespaces/%s/pods/%s", namespace, podName)
	raw, err := rest.Get().AbsPath(path).DoRaw(ctx)
	if err != nil {
		return nil, err
	}
	var pm podMetrics
	if err := json.Unmarshal(raw, &pm); err != nil {
		return nil, err
	}
	return &pm, nil
}

// parseCPUQuantity returns CPU usage in millicpus (1000 = 1 core).
// Handles both "100m" and "0.5" forms via the upstream resource
// package — keeps us honest about edge cases (nanoseconds suffix,
// scientific notation, etc.).
func parseCPUQuantity(s string) int64 {
	if s == "" {
		return 0
	}
	q, err := resource.ParseQuantity(s)
	if err != nil {
		// Fallback: strip a trailing 'n' (nanocpus, the most common
		// raw shape from metrics-server) and parse as integer.
		if strings.HasSuffix(s, "n") {
			if n, perr := strconv.ParseInt(strings.TrimSuffix(s, "n"), 10, 64); perr == nil {
				return n / 1_000_000 // n → m
			}
		}
		return 0
	}
	return q.MilliValue()
}

// parseMemoryQuantity returns memory usage in bytes.
func parseMemoryQuantity(s string) int64 {
	if s == "" {
		return 0
	}
	q, err := resource.ParseQuantity(s)
	if err != nil {
		return 0
	}
	return q.Value()
}
