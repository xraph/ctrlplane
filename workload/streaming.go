package workload

import (
	"bufio"
	"context"
	"errors"
	"sync"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/instance"
)

// replicaResyncInterval is how often WatchHealth + StreamLogs
// re-list a workload's replicas. A Scale that adds new replicas
// mid-stream picks them up within this window; a removed replica's
// ctx-cancel cleans its goroutine up immediately.
const replicaResyncInterval = 30 * time.Second

// fanInBuffer is the size of the outbound channel buffer. Big
// enough that one slow consumer doesn't backpressure every fan-in
// goroutine; small enough that a stuck consumer eventually drops
// rather than buffering forever.
const fanInBuffer = 64

// nonFollowDrainTimeout caps how long a non-follow log stream
// waits for per-replica scanners to finish after the initial
// replica list has been kicked off. Long enough for typical tail
// reads to drain; short enough not to wedge the caller.
const nonFollowDrainTimeout = 5 * time.Second

// WatchHealth fans in HealthResults from every replica's
// health.Service.Watch into one outbound channel. Each event is
// tagged with the source instance ID + replica index so the
// consumer can route per-replica state to a UI without an extra
// lookup.
func (s *service) WatchHealth(ctx context.Context, workloadID id.ID) (<-chan *HealthEvent, error) {
	if s.health == nil {
		return nil, errors.New("workload watch health: health service not configured")
	}

	out := make(chan *HealthEvent, fanInBuffer)
	go s.fanInHealth(ctx, workloadID, out)

	return out, nil
}

// StreamLogs fans in log lines from every replica's instance.Logs
// stream. Each line gets tagged with replica metadata. Same
// fan-in / re-list semantics as WatchHealth.
func (s *service) StreamLogs(ctx context.Context, workloadID id.ID, opts LogsOptions) (<-chan *LogEvent, error) {
	out := make(chan *LogEvent, fanInBuffer)
	go s.fanInLogs(ctx, workloadID, opts, out)

	return out, nil
}

// sub holds per-replica subscription state. The cancel func tears
// down the per-replica reader and unblocks the per-replica sender
// goroutine so the outer fan-in can wait on it cleanly.
type sub struct {
	cancel context.CancelFunc
}

// fanInHealth maintains one Watch per replica and merges into out.
// Re-lists replicas every replicaResyncInterval to absorb scale
// events without manual restart.
//
// Teardown ordering matters: when ctx cancels, every per-replica
// goroutine MUST exit before close(out) runs — otherwise a child
// trying to send a final event into the select races against the
// close and panics. We track children with a WaitGroup, cancel
// every sub explicitly to unblock blocking I/O, then wg.Wait()
// before close(out).
func (s *service) fanInHealth(ctx context.Context, workloadID id.ID, out chan<- *HealthEvent) {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		subs = make(map[string]*sub)
	)

	defer func() {
		// Snapshot + cancel under lock so addReplica can't sneak a
		// fresh sub past the wait barrier.
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

		ch, err := s.health.Watch(subCtx, rep.ID)
		if err != nil {
			cancel()

			return
		}

		subs[key] = &sub{cancel: cancel}
		idx := readReplicaIndex(rep)
		repID := rep.ID

		wg.Go(func() {
			for r := range ch {
				select {
				case out <- &HealthEvent{
					WorkloadID:   workloadID,
					InstanceID:   repID,
					ReplicaIndex: idx,
					Result:       r,
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

		// Snapshot stale keys under the lock, then drop the lock
		// before calling removeReplica — removeReplica re-acquires
		// the same mutex, so calling it under the lock would
		// deadlock.
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

	ticker := time.NewTicker(replicaResyncInterval)
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

// fanInLogs is the equivalent for log streams. The per-replica
// goroutine reads bufio.Scanner over instance.Logs (one line per
// scan) and forwards each line as a LogEvent. Same WaitGroup-
// guarded teardown as fanInHealth.
func (s *service) fanInLogs(ctx context.Context, workloadID id.ID, opts LogsOptions, out chan<- *LogEvent) {
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

		rc, err := s.instances.Logs(subCtx, rep.ID, instance.LogsOptions{
			Follow: opts.Follow,
			Since:  opts.Since,
			Tail:   opts.Tail,
		})
		if err != nil {
			cancel()

			return
		}

		subs[key] = &sub{cancel: cancel}
		idx := readReplicaIndex(rep)
		repID := rep.ID

		wg.Go(func() {
			defer rc.Close()

			scanner := bufio.NewScanner(rc)
			// Allow long single lines (default 64KB is fine for
			// most; docker can emit single multi-KB lines for
			// stack traces).
			scanner.Buffer(make([]byte, 0, 4096), 1<<20)

			for scanner.Scan() {
				line := append([]byte(nil), scanner.Bytes()...)
				select {
				case out <- &LogEvent{
					WorkloadID:   workloadID,
					InstanceID:   repID,
					ReplicaIndex: idx,
					Line:         line,
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

	if !opts.Follow {
		// Non-follow mode is "tail then exit" — wait for replica
		// goroutines to drain (they finish when the docker stream
		// returns EOF) by giving the scanners a fixed window.
		select {
		case <-ctx.Done():
		case <-time.After(nonFollowDrainTimeout):
		}

		return
	}

	ticker := time.NewTicker(replicaResyncInterval)
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
