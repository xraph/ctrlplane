package worker

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// WorkerInfo holds runtime information about a registered worker.
type WorkerInfo struct {
	Name     string        `db:"name"       json:"name"`
	Interval time.Duration `db:"interval"   json:"interval"`
	Running  bool          `db:"running"    json:"running"`
	LastRun  *time.Time    `db:"last_run"   json:"last_run,omitempty"`
	LastErr  string        `db:"last_error" json:"last_error,omitempty"`
	RunCount int64         `db:"run_count"  json:"run_count"`
}

// Scheduler manages a set of workers, running each on its configured interval.
type Scheduler struct {
	workers []Worker
	cancel  context.CancelFunc
	done    chan struct{}

	mu       sync.RWMutex
	statuses map[string]*workerStatus
	active   atomic.Bool
}

// workerStatus tracks runtime state for a single worker.
type workerStatus struct {
	running  bool
	lastRun  *time.Time
	lastErr  string
	runCount int64
}

// NewScheduler creates a new scheduler with an empty worker list.
func NewScheduler() *Scheduler {
	return &Scheduler{
		statuses: make(map[string]*workerStatus),
	}
}

// Register adds a worker to the scheduler.
func (s *Scheduler) Register(w Worker) {
	s.workers = append(s.workers, w)

	s.mu.Lock()
	s.statuses[w.Name()] = &workerStatus{}
	s.mu.Unlock()
}

// Workers returns a snapshot of all registered workers with their runtime status.
func (s *Scheduler) Workers() []WorkerInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	infos := make([]WorkerInfo, 0, len(s.workers))

	for _, w := range s.workers {
		info := WorkerInfo{
			Name:     w.Name(),
			Interval: w.Interval(),
		}

		if st, ok := s.statuses[w.Name()]; ok {
			info.Running = st.running
			info.LastRun = st.lastRun
			info.LastErr = st.lastErr
			info.RunCount = st.runCount
		}

		infos = append(infos, info)
	}

	return infos
}

// WorkerByName returns runtime info for a specific worker.
func (s *Scheduler) WorkerByName(name string) (WorkerInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, w := range s.workers {
		if w.Name() != name {
			continue
		}

		info := WorkerInfo{
			Name:     w.Name(),
			Interval: w.Interval(),
		}

		if st, ok := s.statuses[name]; ok {
			info.Running = st.running
			info.LastRun = st.lastRun
			info.LastErr = st.lastErr
			info.RunCount = st.runCount
		}

		return info, true
	}

	return WorkerInfo{}, false
}

// Start launches a goroutine for each periodic worker. It returns immediately.
func (s *Scheduler) Start(ctx context.Context) error {
	ctx, s.cancel = context.WithCancel(ctx)
	s.done = make(chan struct{})
	s.active.Store(true)

	var wg sync.WaitGroup

	for _, w := range s.workers {
		if w.Interval() <= 0 {
			continue
		}

		wg.Add(1)

		go func(w Worker) {
			defer wg.Done()

			s.mu.Lock()
			if st, ok := s.statuses[w.Name()]; ok {
				st.running = true
			}
			s.mu.Unlock()

			ticker := time.NewTicker(w.Interval())
			defer ticker.Stop()

			for {
				select {
				case <-ticker.C:
					err := w.Run(ctx)

					now := time.Now().UTC()

					s.mu.Lock()
					if st, ok := s.statuses[w.Name()]; ok {
						st.lastRun = &now
						st.runCount++

						if err != nil {
							st.lastErr = err.Error()
						} else {
							st.lastErr = ""
						}
					}
					s.mu.Unlock()

					if err != nil {
						fmt.Fprintf(os.Stderr, "worker %s: %v\n", w.Name(), err)
					}

				case <-ctx.Done():
					s.mu.Lock()
					if st, ok := s.statuses[w.Name()]; ok {
						st.running = false
					}
					s.mu.Unlock()

					return
				}
			}
		}(w)
	}

	go func() {
		wg.Wait()
		s.active.Store(false)
		close(s.done)
	}()

	return nil
}

// Stop cancels all running workers and waits for them to finish.
func (s *Scheduler) Stop(_ context.Context) error {
	s.cancel()

	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
	}

	return nil
}
