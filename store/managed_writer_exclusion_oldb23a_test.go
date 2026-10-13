package store_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/managed"
)

func exclusionOldB23aIdentity(t *testing.T) {
	t.Helper()

	binary := os.Getenv("CTRLPLANE_OLD_B23A_BINARY")
	if binary == "" {
		if os.Getenv("CTRLPLANE_WRITER_EXCLUSION_REQUIRED") == "1" || os.Getenv("CTRLPLANE_OLD_B23A_REQUIRED") == "1" {
			t.Fatal("required unchanged oldb23a binary missing")
		}

		t.Skip("unchanged oldb23a binary not configured")
	}

	raw, err := exec.CommandContext(t.Context(), binary, "identity").Output()
	if err != nil {
		t.Fatal(err)
	}

	var identity struct {
		SchemaVersion string `json:"schema_version"`
		Module        string `json:"module"`
		Version       string `json:"version"`
		Sum           string `json:"sum"`
		Replaced      bool   `json:"replaced"`
		GoVersion     string `json:"go_version"`
	}
	if err = json.Unmarshal(raw, &identity); err != nil || identity.SchemaVersion != "1" || identity.Module != "github.com/xraph/ctrlplane" || identity.Version != "v1.7.1-0.20261011231032-8827a4f36fad" || identity.Sum != "h1:KKPnswqMXoylKncOYcW/PBx5xzi8i39xa4ql0wZU198=" || identity.Replaced {
		t.Fatal("wrong oldb23a identity", identity, err)
	}
}

func exclusionSuccessor(t *testing.T, f *exclusionFixture) (*removalFixture, managed.OperationAdvance, managed.OperationKey) {
	t.Helper()

	fixture, _ := removalActionFixture(t, f.admin, controllerAction(t), 1)
	if _, err := f.admin.RevokeManagedRemovalAttempt(t.Context(), fixture.request()); err != nil {
		t.Fatal(err)
	}

	continueRemovalFixture(t, fixture)
	key := managed.OperationKey{Key: fixture.key.Key, ActionID: fixture.s.action.ID}
	q := managed.OperationAdvance{Command: quiescenceCommand(t, f.admin, fixture.key.Key), ActionID: key.ActionID, ExpectedOperationRevision: 1, Observation: managed.OperationObservation{Kind: "retry", ObservedAt: quiescenceClock(t, f.admin), RetryAt: quiescenceClock(t, f.admin).Add(time.Minute), Diagnostic: "isolated-old-journal"}}

	return fixture, q, key
}

func TestWriterExclusionActualOldB23aJournal(t *testing.T) {
	exclusionOldB23aIdentity(t)
	// Sequential subtests require the hazardous old-library mutation to succeed
	// in a separate unfenced database before any denial earns a passing result.
	positivePassed := false

	t.Run("unfenced_actual_positive_control", func(t *testing.T) {
		f := newExclusionFixture(t)
		_, q, key := exclusionSuccessor(t, f)
		child := startOldB23a(t, f.dsn(f.legacy))
		before := f.snapshot()
		out := child.call(t, "advance_operation", q)
		child.close(t)

		if out.Status != "ok" || out.ErrorClass != "" || reflect.DeepEqual(before, f.snapshot()) {
			t.Fatal("actual old mutation positive control failed", out)
		}

		if _, err := f.admin.ReadManagedOperation(t.Context(), key); !errors.Is(err, managed.ErrUnavailable) {
			t.Fatal("expected recorded successor-association loss not reproduced", err)
		}

		positivePassed = true
	})

	if !positivePassed {
		t.Fatal("no passing actual old-library positive control; refusal evidence invalid")
	}

	t.Run("fenced_same_old_operation", func(t *testing.T) {
		f := newExclusionFixture(t)
		fixture, q, key := exclusionSuccessor(t, f)

		intact, err := f.admin.ReadManagedOperation(t.Context(), key)
		if err != nil {
			t.Fatal(err)
		}
		// Keep a real old process/pool open long enough to prove disabling LOGIN
		// alone is insufficient. Closing it is an explicit cutover prerequisite.
		child := startOldB23a(t, f.dsn(f.legacy))

		probe := child.call(t, "read_target", fixture.key.Key)
		if probe.Status != "ok" {
			t.Fatal("old process could not reach positive-control target", probe)
		}

		f.drain()
		f.requireRefusal(f.activation())
		child.close(t)

		if _, err = f.activate(t.Context(), f.activation()); err != nil {
			t.Fatal(err)
		}

		readOnly := f.readOnly(f.legacy)
		if err = f.closeStore(readOnly); err != nil {
			t.Fatal(err)
		}
		// Regenerate the original operation's lease/CAS envelope using the owner,
		// so the denied call is not rejected merely by stale fixture authority.
		q.Command = quiescenceCommand(t, f.admin, fixture.key.Key)
		q.Observation.ObservedAt = quiescenceClock(t, f.admin)
		q.Observation.RetryAt = q.Observation.ObservedAt.Add(time.Minute)
		before := f.snapshot()
		fenced := startOldB23a(t, f.dsn(f.legacy))

		reached := fenced.call(t, "read_target", fixture.key.Key)
		if reached.Status != "ok" {
			t.Fatal("fenced old child did not reach intended database", reached)
		}

		out := fenced.call(t, "advance_operation", q)
		fenced.close(t)

		if out.Status != "error" || out.ErrorClass != "other" || out.NewlyIssued || out.NewlyAuthorized {
			t.Fatal("expected old database error without issuance", out)
		}
		// The unchanged child intentionally emits only its coarse error class.
		// The same principal's concrete SQL denial is checked independently in
		// TestWriterExclusionAuditAndEveryTableDML with SQLSTATE 42501.
		f.unchanged(before)

		after, err := f.admin.ReadManagedOperation(t.Context(), key)
		if err != nil || !reflect.DeepEqual(after, intact) {
			t.Fatal("old denial damaged successor association", err)
		}
	})
}
