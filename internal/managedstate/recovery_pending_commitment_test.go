package managedstate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xraph/ctrlplane/managed"
)

func TestRecoveryPendingCommitmentsFitMaximumState(t *testing.T) {
	_, _, _, plan := activationMaterializationFixture(t, managed.MaxRecoveryBindings, managed.MaxRecoverySteps-1, false) //nolint:dogsled // Only the activation plan is needed to check its encoded state bound.
	state := plan.State

	state.PendingOriginals = make([]string, managed.MaxRecoverySteps)
	for i := range state.PendingOriginals {
		state.PendingOriginals[i] = strings.Repeat("a", 64)
	}

	state.Revision = "9223372036854775807"
	state.AdmissionDigest = strings.Repeat("b", 64)
	state.RevocationDigest = strings.Repeat("c", 64)
	state.SettlementDigest = strings.Repeat("d", 64)
	state.TerminalDigest = strings.Repeat("e", 64)

	raw, err := json.Marshal(state)
	if err != nil || len(raw) > managed.MaxAttemptFact {
		t.Fatal("maximum pending commitment state exceeds existing cap", len(raw), err)
	}

	var roundtrip recoveryAttemptState
	if err = json.Unmarshal(raw, &roundtrip); err != nil || len(roundtrip.PendingOriginals) != managed.MaxRecoverySteps {
		t.Fatal("commitment codec", err)
	}

	for i := range state.PendingOriginals {
		if roundtrip.PendingOriginals[i] != state.PendingOriginals[i] {
			t.Fatal("commitment changed", i)
		}
	}
}
