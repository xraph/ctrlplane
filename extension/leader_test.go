package extension

import (
	"strings"
	"testing"

	"github.com/xraph/ctrlplane/app"
	"github.com/xraph/ctrlplane/store/memory"
)

// TestLeaderElection_RequiresPostgres makes sure asking for leader election
// on a store that cannot provide it fails Register instead of silently
// running every worker on every replica.
func TestLeaderElection_RequiresPostgres(t *testing.T) {
	t.Parallel()

	ext := New(WithLeaderElection(""))

	if ext.leaderLock == "" {
		t.Fatal("WithLeaderElection(\"\") did not fall back to the default lock name")
	}

	cp, err := app.New(app.WithStore(memory.New()))
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}

	ext.cp = cp

	err = ext.applyLeaderElection()
	if err == nil || !strings.Contains(err.Error(), "postgres") {
		t.Fatalf("applyLeaderElection() = %v, want an error naming the postgres requirement", err)
	}

	// Without the option nothing is checked.
	plain := New()
	plain.cp = cp

	if err := plain.applyLeaderElection(); err != nil {
		t.Fatalf("applyLeaderElection() without the option = %v, want nil", err)
	}
}
