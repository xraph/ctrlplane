package worker

import "context"

// LeaderElector decides which replica runs the scheduler's workers. When
// you run more than one replica of Ctrl Plane against the same store,
// give each one an elector over a shared lock so only the leader
// reconciles, collects garbage and renews certificates; without one every
// replica runs every worker.
//
// The postgres store ships one built on a session-level advisory lock; see
// postgres.Store.LeaderElector.
type LeaderElector interface {
	// IsLeader reports whether this replica holds leadership, trying to
	// take it when it does not. The scheduler calls it before every worker
	// run, so it should be cheap when leadership is already held.
	IsLeader(ctx context.Context) (bool, error)

	// Resign gives leadership up so another replica can take it. It is
	// safe to call when this replica is not the leader.
	Resign(ctx context.Context) error
}
