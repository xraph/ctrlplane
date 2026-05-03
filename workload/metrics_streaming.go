package workload

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
	"github.com/xraph/ctrlplane/metrics"
)

// metricsResyncInterval mirrors the health/log fan-in cadence so a
// Scale that adds replicas surfaces in the metrics stream within
// the same window.
const metricsResyncInterval = 30 * time.Second

// RangeMetrics queries every replica's per-instance series for the
// window, aligns by bucket timestamp, and sums (CPU/Memory/Network)
// or averages (request rate, P95 latency) across the replicas that
// had a sample in each bucket. Returns an empty series when no
// replica has any samples in the window.
func (s *service) RangeMetrics(ctx context.Context, workloadID id.ID, q MetricsRange) (MetricsSeries, error) {
	if s.metrics == nil {
		return nil, errors.New("workload range metrics: metrics service not configured")
	}

	replicas, err := s.ListInstances(ctx, workloadID)
	if err != nil {
		return nil, err
	}

	if len(replicas) == 0 {
		return nil, nil
	}

	rq := metrics.RangeQuery{Since: q.Since, Until: q.Until, Resolution: q.Resolution}

	type acc struct {
		count                  int
		cpu, memUsed, memLimit float64
		netIn, netOut          float64
		reqRate, latP95Sum     float64
		latP95Count            int
	}

	buckets := map[time.Time]*acc{}

	for _, rep := range replicas {
		series, qerr := s.metrics.Range(ctx, rep.ID, rq)
		if qerr != nil {
			continue
		}

		for _, sample := range series {
			a, ok := buckets[sample.At]
			if !ok {
				a = &acc{}
				buckets[sample.At] = a
			}

			a.count++
			a.cpu += sample.CPUPercent
			a.memUsed += float64(sample.MemoryUsedMB)
			a.memLimit += float64(sample.MemoryLimitMB)
			a.netIn += sample.NetworkInBytesPerSec
			a.netOut += sample.NetworkOutBytesPerSec

			a.reqRate += sample.RequestsPerSec
			if sample.LatencyP95Ms > 0 {
				a.latP95Sum += sample.LatencyP95Ms
				a.latP95Count++
			}
		}
	}

	out := make(MetricsSeries, 0, len(buckets))
	for ts, a := range buckets {
		var p95 float64
		if a.latP95Count > 0 {
			p95 = a.latP95Sum / float64(a.latP95Count)
		}

		out = append(out, AggregatedSample{
			At:                    ts,
			ReplicaCount:          a.count,
			CPUPercent:            a.cpu, // sum: workload total CPU%
			MemoryUsedMB:          int(a.memUsed),
			MemoryLimitMB:         int(a.memLimit),
			NetworkInBytesPerSec:  a.netIn,
			NetworkOutBytesPerSec: a.netOut,
			RequestsPerSec:        a.reqRate,
			LatencyP95Ms:          p95,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })

	return out, nil
}

// WatchMetrics fans in per-replica metric samples and emits one
// MetricsEvent per sample tagged with the source replica. Same
// teardown semantics as WatchHealth / StreamLogs — WaitGroup-guarded
// so close-on-send can't race.
func (s *service) WatchMetrics(ctx context.Context, workloadID id.ID) (<-chan *MetricsEvent, error) {
	if s.metrics == nil {
		return nil, errors.New("workload watch metrics: metrics service not configured")
	}

	out := make(chan *MetricsEvent, fanInBuffer)
	go s.fanInMetrics(ctx, workloadID, out)

	return out, nil
}

func (s *service) fanInMetrics(ctx context.Context, workloadID id.ID, out chan<- *MetricsEvent) {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		subs = make(map[string]*sub)
	)

	defer func() {
		mu.Lock()
		for _, sb := range subs {
			sb.cancel()
		}

		subs = nil
		mu.Unlock()
		wg.Wait()
		close(out)
	}()

	addReplica := func(rep *instance.Instance) {
		mu.Lock()
		defer mu.Unlock()

		if subs == nil {
			return
		}

		key := rep.ID.String()
		if _, exists := subs[key]; exists {
			return
		}

		subCtx, cancel := context.WithCancel(ctx)

		ch, err := s.metrics.Watch(subCtx, rep.ID)
		if err != nil {
			cancel()

			return
		}

		subs[key] = &sub{cancel: cancel}
		idx := readReplicaIndex(rep)
		repID := rep.ID

		wg.Go(func() {

			for sample := range ch {
				select {
				case out <- &MetricsEvent{
					WorkloadID:   workloadID,
					InstanceID:   repID,
					ReplicaIndex: idx,
					Sample:       sample,
				}:
				case <-subCtx.Done():
					return
				}
			}
		})
	}

	removeReplica := func(instanceID id.ID) {
		mu.Lock()
		defer mu.Unlock()

		if subs == nil {
			return
		}

		key := instanceID.String()
		if sb, ok := subs[key]; ok {
			sb.cancel()
			delete(subs, key)
		}
	}

	resync := func() {
		replicas, err := s.ListInstances(ctx, workloadID)
		if err != nil {
			return
		}

		seen := make(map[string]bool, len(replicas))
		for _, r := range replicas {
			seen[r.ID.String()] = true
			addReplica(r)
		}

		mu.Lock()

		var stale []string

		for key := range subs {
			if !seen[key] {
				stale = append(stale, key)
			}
		}
		mu.Unlock()

		for _, key := range stale {
			rid, err := id.Parse(key)
			if err != nil {
				continue
			}

			removeReplica(rid)
		}
	}

	resync()

	ticker := time.NewTicker(metricsResyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			resync()
		}
	}
}
