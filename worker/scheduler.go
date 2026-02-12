package worker

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"
)

// Scheduler manages a set of workers, running each on its configured interval.
type Scheduler struct {
	workers []Worker
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewScheduler creates a new scheduler with an empty worker list.
func NewScheduler() *Scheduler {
	return &Scheduler{}
}

// Register adds a worker to the scheduler.
func (s *Scheduler) Register(w Worker) {
	s.workers = append(s.workers, w)
}

// Start launches a goroutine for each periodic worker. It returns immediately.
func (s *Scheduler) Start(ctx context.Context) error {
	ctx, s.cancel = context.WithCancel(ctx)
	s.done = make(chan struct{})

	var wg sync.WaitGroup

	for _, w := range s.workers {
		if w.Interval() <= 0 {
			continue
		}

		wg.Add(1)

		go func(w Worker) {
			defer wg.Done()

			ticker := time.NewTicker(w.Interval())
			defer ticker.Stop()

			for {
				select {
				case <-ticker.C:
					if err := w.Run(ctx); err != nil {
						fmt.Fprintf(os.Stderr, "worker %s: %v\n", w.Name(), err)
					}
				case <-ctx.Done():
					return
				}
			}
		}(w)
	}

	go func() {
		wg.Wait()
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
