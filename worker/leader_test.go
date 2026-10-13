package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type countingWorker struct{ runs atomic.Int64 }

func (w *countingWorker) Name() string            { return "counter" }
func (w *countingWorker) Interval() time.Duration { return time.Millisecond }
func (w *countingWorker) Run(context.Context) error {
	w.runs.Add(1)

	return nil
}

type fakeElector struct {
	leader   atomic.Bool
	failing  atomic.Bool
	resigned atomic.Bool
}

func (e *fakeElector) IsLeader(context.Context) (bool, error) {
	if e.failing.Load() {
		return true, errors.New("lock backend unreachable")
	}

	return e.leader.Load(), nil
}

func (e *fakeElector) Resign(context.Context) error {
	e.resigned.Store(true)

	return nil
}

// waitFor polls cond for up to a second.
func waitFor(t *testing.T, cond func() bool) bool {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}

		time.Sleep(time.Millisecond)
	}

	return false
}

func TestScheduler_RunsWorkersOnlyWhileLeader(t *testing.T) {
	t.Parallel()

	w := &countingWorker{}
	e := &fakeElector{}

	s := NewScheduler()
	s.Register(w)
	s.SetLeaderElector(e)

	if err := s.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// A follower ticks but never runs the worker.
	time.Sleep(30 * time.Millisecond)

	if got := w.runs.Load(); got != 0 {
		t.Fatalf("worker ran %d times as follower, want 0", got)
	}

	if s.IsLeader() {
		t.Fatal("IsLeader() = true for a follower")
	}

	e.leader.Store(true)

	if !waitFor(t, func() bool { return w.runs.Load() > 0 }) {
		t.Fatal("worker never ran after this replica became leader")
	}

	// An elector error counts as not leading, even if it also said yes.
	e.failing.Store(true)

	if !waitFor(t, func() bool { return !s.IsLeader() }) {
		t.Fatal("scheduler kept leading through an elector error")
	}

	before := w.runs.Load()

	time.Sleep(30 * time.Millisecond)

	// At most one run already past the check may land after the switch.
	if got := w.runs.Load(); got > before+1 {
		t.Fatalf("worker ran %d more times while the elector failed", got-before)
	}

	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if !e.resigned.Load() {
		t.Fatal("Stop did not resign leadership")
	}
}

func TestScheduler_NoElectorAlwaysLeads(t *testing.T) {
	t.Parallel()

	w := &countingWorker{}

	s := NewScheduler()
	s.Register(w)

	if err := s.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	if !s.IsLeader() {
		t.Fatal("IsLeader() = false without an elector")
	}

	if !waitFor(t, func() bool { return w.runs.Load() > 0 }) {
		t.Fatal("worker never ran without an elector")
	}
}
