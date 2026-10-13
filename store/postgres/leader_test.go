package postgres

import (
	"os"
	"testing"

	"github.com/xraph/ctrlplane/id"
)

// TestLeaderElector_OneLeaderAtATime opens two stores on the same database,
// as two replicas would, and checks the advisory lock lets only one lead
// and hands over on Resign. It needs CTRLPLANE_TEST_POSTGRES_DSN.
func TestLeaderElector_OneLeaderAtATime(t *testing.T) {
	dsn := os.Getenv("CTRLPLANE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("CTRLPLANE_TEST_POSTGRES_DSN unset")
	}

	ctx := t.Context()
	lock := "leader-test-" + id.New(id.PrefixTenant).String()

	a := openProjectionPG(t, dsn).LeaderElector(lock)
	b := openProjectionPG(t, dsn).LeaderElector(lock)

	mustLead := func(name string, e *LeaderElector, want bool) {
		t.Helper()

		got, err := e.IsLeader(ctx)
		if err != nil {
			t.Fatalf("%s IsLeader: %v", name, err)
		}

		if got != want {
			t.Fatalf("%s IsLeader = %v, want %v", name, got, want)
		}
	}

	mustLead("a", a, true)
	mustLead("b", b, false)
	mustLead("a again", a, true)

	if err := a.Resign(ctx); err != nil {
		t.Fatalf("a Resign: %v", err)
	}

	mustLead("b after a resigned", b, true)
	mustLead("a after resigning", a, false)

	if err := b.Resign(ctx); err != nil {
		t.Fatalf("b Resign: %v", err)
	}

	// Resigning when not leading is a no-op.
	if err := b.Resign(ctx); err != nil {
		t.Fatalf("second Resign: %v", err)
	}
}

// TestLeaderElector_FailoverWhenSessionDies ends the leader's session, as
// a crashed replica or a dropped connection would, and checks the other
// replica takes over and the old leader notices it lost.
func TestLeaderElector_FailoverWhenSessionDies(t *testing.T) {
	dsn := os.Getenv("CTRLPLANE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("CTRLPLANE_TEST_POSTGRES_DSN unset")
	}

	ctx := t.Context()
	lock := "leader-test-" + id.New(id.PrefixTenant).String()

	storeB := openProjectionPG(t, dsn)
	a := openProjectionPG(t, dsn).LeaderElector(lock)
	b := storeB.LeaderElector(lock)

	if ok, err := a.IsLeader(ctx); err != nil || !ok {
		t.Fatalf("a IsLeader = (%v, %v), want leader", ok, err)
	}

	var pid int
	if err := a.conn.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatalf("backend pid: %v", err)
	}

	if _, err := storeB.pg.Exec(ctx, "SELECT pg_terminate_backend($1, 5000)", pid); err != nil {
		t.Fatalf("terminate leader session: %v", err)
	}

	if ok, err := b.IsLeader(ctx); err != nil || !ok {
		t.Fatalf("b IsLeader after the leader's session died = (%v, %v), want leader", ok, err)
	}

	if ok, err := a.IsLeader(ctx); err != nil || ok {
		t.Fatalf("old leader IsLeader = (%v, %v), want (false, nil)", ok, err)
	}

	if err := b.Resign(ctx); err != nil {
		t.Fatalf("b Resign: %v", err)
	}
}
