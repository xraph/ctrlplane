package managedstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

// These tests start with a checked view and exercise staging and exact leaf replay.
// They do not qualify the full public reader or concrete backend transactions.
func recoveryCutoffFixture(t *testing.T, bindings, services int, issued bool) (*recoveryLeafFixture, *managed.Target, attemptView, managed.RecoveryAcceptance) {
	t.Helper()
	fixture, target, view, plan := activationMaterializationFixture(t, bindings, services, issued)

	staged, err := stageRecoveryActivationMaterialization(t.Context(), fixture, target, view, plan)
	if err != nil {
		t.Fatal(err)
	}

	if err = applyRecoveryActivationRows(t.Context(), fixture, staged); err != nil {
		t.Fatal(err)
	}

	fixture.receipts[attemptReceiptKey{target.Key, staged.receipt.ID, staged.receipt.Digest}] = staged.receipt
	target.Revision = staged.receipt.Revision
	view.state, view.recoveryActivation = plan.State.AttemptState, &plan.State.Activation
	operation := id.New(managed.PrefixOperation)
	digest := managed.Digest([]byte(operation.String()))
	receipt := managed.Receipt{ID: operation, Digest: digest, Revision: target.Revision + 1, LeaseEpoch: target.LeaseEpoch, AcceptedAt: staged.receipt.AcceptedAt.Add(time.Second), LeaseUntil: target.LeaseUntil}

	return fixture, target, view, managed.RecoveryAcceptance{OperationID: operation, RequestDigest: digest, Receipt: managed.ReceiptWire(receipt)}
}

func TestRecoveryCutoffPreservesOriginalSlotsAndUnresolvedEffects(t *testing.T) {
	for _, size := range [][2]int{{1, 1}, {64, 32}} {
		for _, issued := range []bool{false, true} {
			fixture, target, view, acceptance := recoveryCutoffFixture(t, size[0], size[1], issued)
			before := cloneActivationFixture(fixture)

			staged, updated, cutoff, err := stageRecoveryCutoff(t.Context(), fixture, target, view, acceptance)
			if err != nil {
				t.Fatal(size, issued, err)
			}

			if len(staged.rows) != 5 || !reflect.DeepEqual(fixture.rows, before.rows) || !reflect.DeepEqual(fixture.receipts, before.receipts) {
				t.Fatal("cutoff preparation changed persistence")
			}

			if target.Revision != 11 || view.state.Revision != "8" || updated.state.Revision != "9" || acceptance.Receipt.Revision != "12" {
				t.Fatal("cutoff counter cost")
			}

			if !bytes.Equal(view.state.ProviderSlots, updated.state.ProviderSlots) || !bytes.Equal(view.state.BindingSlots, updated.state.BindingSlots) || updated.state.SettlementDigest != "" {
				t.Fatal("cutoff changed effects or settlement")
			}

			rev, err := attemptRevocationRead(t.Context(), staged, updated)
			if err != nil || rev.Disposition != managed.Fenced || rev.CoverageDigest != "" || rev.SettlementDigest != "" {
				t.Fatal("cutoff fabricated coverage", rev, err)
			}

			if cutoff.Facts.RevocationDigest != updated.state.RevocationDigest || cutoff.Facts.RevocationReceipt != acceptance.Receipt {
				t.Fatal("cutoff lost original receipt")
			}

			if !recoveryInvocationAfterCutoff(updated.state, "result") || !recoveryInvocationAfterCutoff(updated.state, "closure") || recoveryInvocationAfterCutoff(updated.state, "retry") || recoveryInvocationAfterCutoff(updated.state, "marker") {
				t.Fatal("cutoff confused evidence retention with issuing")
			}

			if _, _, _, err = stageRecoveryCutoff(t.Context(), staged, target, updated, acceptance); !errors.Is(err, managed.ErrBlocked) {
				t.Fatal("second cutoff staged", err)
			}
		}
	}
}

func TestRecoveryCutoffExactDependenciesAndReplay(t *testing.T) {
	fixture, target, view, acceptance := recoveryCutoffFixture(t, 1, 1, true)

	staged, updated, cutoff, err := stageRecoveryCutoff(t.Context(), fixture, target, view, acceptance)
	if err != nil {
		t.Fatal(err)
	}

	if err = applyRecoveryActivationRows(t.Context(), fixture, staged); err != nil {
		t.Fatal(err)
	}

	fixture.receipts[attemptReceiptKey{target.Key, staged.receipt.ID, staged.receipt.Digest}] = staged.receipt

	command := managed.RecoveryCommand{ID: acceptance.OperationID}
	if _, err = recoveryCutoffReplayHeld(t.Context(), fixture, updated, command, acceptance.RequestDigest); err != nil {
		t.Fatal(err)
	}

	if _, err = recoveryCutoffReplayHeld(t.Context(), fixture, updated, command, managed.Digest([]byte("changed"))); !errors.Is(err, managed.ErrConflict) {
		t.Fatal("changed request replay", err)
	}

	factKey, err := recoveryFactKey(attemptKey(view), managed.RecoveryCutoffRecord, acceptance.OperationID, cutoff.Facts)
	if err != nil {
		t.Fatal(err)
	}

	for _, key := range [][3]string{
		{"removal_revocation", view.scope, view.ref.AttemptID.String()},
		{"removal_attempt_event", view.scope, acceptance.OperationID.String()},
		{string(managed.RecoveryCutoffRecord), view.scope, factKey},
		{recoveryAcceptanceKind, view.scope, factKey},
	} {
		broken := cloneActivationFixture(fixture)
		delete(broken.rows, key)

		if _, err = recoveryCutoffHeld(t.Context(), broken, updated); err == nil {
			t.Fatal("lost dependency accepted", key)
		}
	}

	broken := cloneActivationFixture(fixture)
	delete(broken.receipts, attemptReceiptKey{target.Key, staged.receipt.ID, staged.receipt.Digest})

	if _, err = recoveryCutoffHeld(t.Context(), broken, updated); err == nil {
		t.Fatal("missing receipt accepted")
	}
}

func TestRecoveryCutoffRefusesCountersAndAuthorityBeforePersistence(t *testing.T) {
	for _, change := range []func(*managed.Target, *attemptView, *managed.RecoveryAcceptance){
		func(target *managed.Target, _ *attemptView, _ *managed.RecoveryAcceptance) {
			target.Revision = math.MaxInt64
		},
		func(_ *managed.Target, view *attemptView, _ *managed.RecoveryAcceptance) {
			view.state.Revision = managed.Decimal(strconv.FormatUint(math.MaxInt64, 10))
		},
		func(_ *managed.Target, view *attemptView, _ *managed.RecoveryAcceptance) {
			view.state.SettlementDigest = managed.Digest([]byte("settled"))
		},
		func(_ *managed.Target, _ *attemptView, acceptance *managed.RecoveryAcceptance) {
			acceptance.Receipt.Revision = "11"
		},
	} {
		fixture, target, view, acceptance := recoveryCutoffFixture(t, 1, 1, false)
		before := fixture.writes

		change(target, &view, &acceptance)

		if _, _, _, err := stageRecoveryCutoff(t.Context(), fixture, target, view, acceptance); err == nil {
			t.Fatal("invalid authority staged")
		}

		if fixture.writes != before {
			t.Fatal("failed preparation wrote rows")
		}
	}
}

func TestRecoveryCutoffRollbackAtEveryRowAndReceipt(t *testing.T) {
	fixture, target, view, acceptance := recoveryCutoffFixture(t, 1, 1, true)

	staged, _, _, err := stageRecoveryCutoff(t.Context(), fixture, target, view, acceptance)
	if err != nil {
		t.Fatal(err)
	}

	before := cloneActivationFixture(fixture)
	backend := &activationTransactionFixture{state: fixture, target: target}
	apply := func() error {
		return New(backend).controller(t.Context(), "cutoff-model", []managed.Key{target.Key}, func(tx ControllerTransaction) error {
			_, atomicErr := tx.Atomic(t.Context(), target.Key, staged.receipt.ID, staged.receipt.Digest, func(current *managed.Target, _ time.Time) (managed.Receipt, error) {
				if writeErr := applyRecoveryActivationRows(t.Context(), tx, staged); writeErr != nil {
					return managed.Receipt{}, writeErr
				}

				current.Revision = staged.receipt.Revision

				return staged.receipt, nil
			})

			return atomicErr
		})
	}

	for failAt := 1; failAt <= len(staged.rows)+1; failAt++ {
		backend.failAt, backend.failReceipt = failAt, failAt == len(staged.rows)+1

		if err = apply(); !errors.Is(err, managed.ErrUnavailable) {
			t.Fatal("failure ignored", err)
		}

		if !reflect.DeepEqual(fixture.rows, before.rows) || !reflect.DeepEqual(fixture.receipts, before.receipts) || target.Revision != 11 {
			t.Fatal("partial cutoff escaped rollback")
		}
	}

	backend.failAt, backend.failReceipt = 0, false

	if err = apply(); err != nil {
		t.Fatal(err)
	}

	var saved recoveryAttemptState
	if err = json.Unmarshal(fixture.rows[[3]string{"removal_attempt_state", view.scope, view.ref.AttemptID.String()}], &saved); err != nil {
		t.Fatal(err)
	}

	if saved.RevocationDigest == "" || target.Revision != 12 {
		t.Fatal("complete cutoff missing")
	}
}
