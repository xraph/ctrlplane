package managedstate

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

func TestRecoveryActivationStateRefusesLegacyDecoder(t *testing.T) {
	_, key, now := recoveryFixture()
	ref := managed.AttemptRef{AttemptID: key.AttemptID, Generation: key.Generation, DescriptorDigest: managed.Digest([]byte("descriptor"))}
	facts := managed.RemovalRecoveryActivation{Key: key, AttemptRef: ref, Profile: managed.RemovalRecoveryProfile, RetryProfile: managed.ProviderStepRetryProfile, ProviderOperationID: id.New(managed.PrefixOperation), AdmissionDigest: managed.Digest([]byte("admission")), BindingCount: "1", StepCount: "2", OriginalSelectionDigest: managed.Digest([]byte("selections")), ActivatedAt: now}
	record := recoveryTestRecord(t, managed.RecoveryActivationRecord, facts, now)

	selected, err := recoveryFactKey(key, record.Kind, record.Acceptance.OperationID, facts)
	if err != nil {
		t.Fatal(err)
	}

	association, err := managed.RecoveryAssociationFor(selected, record)
	if err != nil {
		t.Fatal(err)
	}

	state := recoveryAttemptState{AttemptState: managed.AttemptState{SchemaVersion: "2", AttemptRef: ref, Revision: "2", BindingSlots: []byte{attemptBeginBit | attemptFenceBit}, ProviderSlots: []byte{attemptPermissionBit | attemptUncertaintyBit, 0}}, Activation: association}

	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = attemptDecode[managed.AttemptState](raw, managed.MaxAttemptFact); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatalf("legacy exact decoder accepted recovery state: %v", err)
	}

	decoded, err := attemptDecode[recoveryAttemptState](raw, managed.MaxAttemptFact)
	if err != nil || decoded.SchemaVersion != "2" || decoded.Activation != association || decoded.ProviderSlots[0] != attemptPermissionBit|attemptUncertaintyBit || decoded.ProviderSlots[1] != 0 {
		t.Fatalf("state lost original uncertainty or explicit empty slot: %v", err)
	}
}

func TestRecoveryActivationRefusesBeforeWrites(t *testing.T) {
	repository, _, _ := configuredRecoveryFixture(t)
	fixture, key, now := recoveryFixture()
	command := managed.RecoveryCommand{Key: key.Key, ID: id.New(managed.PrefixOperation), Revision: "4", LeaseEpoch: "1", Owner: "owner", AttemptRef: managed.AttemptRef{AttemptID: key.AttemptID, Generation: key.Generation, DescriptorDigest: managed.Digest([]byte("descriptor"))}, Profile: managed.RemovalRecoveryProfile}

	target := &managed.Target{Identity: managed.Identity{Key: key.Key}, Revision: 4, LeaseEpoch: 1, LeaseOwner: "owner", LeaseUntil: now}
	if _, err := repository.prepareRecoveryActivationHeld(t.Context(), fixture, target, managed.RemovalRecoveryActivationRequest{Command: command}, now); !errors.Is(err, managed.ErrConflict) {
		t.Fatalf("expired callback lease: %v", err)
	}

	if fixture.writes != 0 || fixture.reads != 0 || target.Revision != 4 {
		t.Fatal("expired activation touched authority")
	}

	var absent *Repository
	if _, err := absent.prepareRecoveryActivationHeld(t.Context(), fixture, target, managed.RemovalRecoveryActivationRequest{Command: command}, now); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatalf("unconfigured activation: %v", err)
	}

	if _, err := recoveryOriginalSelectionsHeld(t.Context(), fixture, target, attemptView{}); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatalf("unvalidated selections accepted: %v", err)
	}

	if _, err := recoveryActivationReserve(target, attemptView{}); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatalf("unvalidated reserve accepted: %v", err)
	}
}

func TestRecoveryOriginalSelectionDigestPreservesPossibleEffects(t *testing.T) {
	_, key, _ := recoveryFixture()
	step := managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: key.Key, ActionID: id.New(managed.PrefixOperation)}, ProviderOperationID: id.New(managed.PrefixOperation), Kind: managed.StepRemoveContainer, Service: "api"}
	association := attemptProviderSlot{SchemaVersion: "1", PermissionDigest: managed.Digest([]byte("permission")), LastUncertaintyDigest: managed.Digest([]byte("uncertainty"))}
	selected := []recoveryOriginalSelection{{Step: step, Disposition: "original", Association: &association}}

	original, err := attemptHash("ctrlplane.managed.recovery-original-selections.v1", selected)
	if err != nil {
		t.Fatal(err)
	}

	association.LastUncertaintyDigest = ""

	changed, err := attemptHash("ctrlplane.managed.recovery-original-selections.v1", selected)
	if err != nil || changed == original {
		t.Fatal("selection digest erased uncertainty", err)
	}

	selected[0] = recoveryOriginalSelection{Step: step, Disposition: "unissued"}

	unissued, err := attemptHash("ctrlplane.managed.recovery-original-selections.v1", selected)
	if err != nil || unissued == original || unissued == changed {
		t.Fatal("accepted permission adopted as unissued", err)
	}
}

// This fixture starts with a checked view. It exercises plan preparation, selections
// and reserve predicates, but does not qualify the full authority-loading entry point.
func recoveryBelowMaximumFixture(t *testing.T) (*recoveryLeafFixture, *managed.Target, attemptView, managed.RemovalRecoveryActivationRequest, time.Time) {
	t.Helper()

	fixture, key, now := recoveryFixture()
	startup := managed.StartupBindingRef{BindingID: "binding", BindingGeneration: "1", BindingDigest: strings.Repeat("a", 64)}
	provision := managed.ProvisionSpec{Version: "1", Services: []managed.ProvisionService{{Name: "api", Image: "x@sha256:" + strings.Repeat("b", 64), Role: "main", CPUMillis: "1", MemoryMiB: "1", PIDLimit: "1", Startup: startup}}}

	raw, err := json.Marshal(provision)
	if err != nil {
		t.Fatal(err)
	}

	encoded, err := json.Marshal(managed.ManagedSpec{Profile: "ctrlplane.managed.v1", RuntimeRoles: []managed.RuntimeRole{managed.Polling}, Provision: raw})
	if err != nil {
		t.Fatal(err)
	}

	reservationID := id.New(managed.PrefixReservation)
	target := &managed.Target{Identity: managed.Identity{Key: key.Key, Manifest: managed.Manifest{Version: 1, Members: []managed.Member{{Name: "worker", Service: "api"}}, Spec: encoded, SpecDigest: managed.Digest(encoded)}}, Revision: 10, LeaseEpoch: 1, LeaseOwner: "owner", LeaseUntil: now.Add(time.Minute), Resources: map[string]string{"api": "container", "network": "network"}, Reservations: []managed.Reservation{{Registration: managed.Registration{ID: reservationID}, Phase: managed.Accepted}}}
	ref := managed.AttemptRef{AttemptID: key.AttemptID, Generation: key.Generation, DescriptorDigest: managed.Digest([]byte("descriptor"))}

	scope, err := attemptScope(key.Key)
	if err != nil {
		t.Fatal(err)
	}

	view := attemptView{validated: true, scope: scope, ref: ref, descriptor: managed.AttemptDescriptor{Key: key.Key, AttemptID: key.AttemptID, Generation: key.Generation, ActionID: id.New(managed.PrefixOperation), ProviderOperationID: id.New(managed.PrefixOperation), ReservationIDs: []id.ID{reservationID}}, state: managed.AttemptState{SchemaVersion: "1", AttemptRef: ref, Revision: "7", AdmissionDigest: managed.Digest([]byte("admission")), BindingSlots: make([]byte, managed.MaxRecoveryBindings), ProviderSlots: make([]byte, managed.MaxRecoverySteps)}}
	view.state.BindingSlots[0] = attemptBeginBit | attemptFenceBit
	request := managed.RemovalRecoveryActivationRequest{Command: managed.RecoveryCommand{Key: key.Key, ID: id.New(managed.PrefixOperation), Revision: "10", LeaseEpoch: "1", Owner: "owner", AttemptRef: ref, Profile: managed.RemovalRecoveryProfile}}

	return fixture, target, view, request, now
}

func TestRecoveryBelowMaximumPlanPreparation(t *testing.T) {
	fixture, target, view, request, now := recoveryBelowMaximumFixture(t)

	plan, err := prepareRecoveryActivationPlanHeld(t.Context(), fixture, target, view, request, now, 1)
	if err != nil {
		t.Fatal("one binding and two steps must prepare with fixed 64/33 storage", err)
	}

	if plan.Profile.Facts.BindingCount != "1" || plan.Profile.Facts.StepCount != "2" || len(plan.Selections) != 2 {
		t.Fatalf("padding became obligations: %+v", plan.Profile.Facts)
	}

	for _, selected := range plan.Selections {
		if selected.Disposition != "unissued" || selected.Association != nil {
			t.Fatal("unissued original step changed", selected)
		}
	}

	after, err := recoveryActivationReserve(target, view)
	if err != nil {
		t.Fatal(err)
	}

	// 7 aggregate + 5 binding + 2 permission + 2 marker + 2 closure + 2 close.
	reserve, err := recoveryRemainingReserve(after)
	if err != nil || after.existing != 14 || after.markerObligations != 2 || after.closureObligations != 2 || after.closeIntentsMissing != 1 || after.closeResultsMissing != 1 || reserve != 20 {
		t.Fatalf("fixed padding consumed reserve: %+v, %d, %v", after, reserve, err)
	}

	if fixture.writes != 0 || target.Revision != 10 || view.state.Revision != "7" || view.state.SchemaVersion != "1" || plan.State.Revision != "8" || plan.Profile.Acceptance.Receipt.Revision != "11" {
		t.Fatal("preparation changed authority or lost proposed counter cost")
	}
}

func TestRecoveryPreparationRejectsInvalidStorageCapacityAndPadding(t *testing.T) {
	for name, change := range map[string]func(*attemptView){
		"short binding storage":  func(v *attemptView) { v.state.BindingSlots = v.state.BindingSlots[:1] },
		"short provider storage": func(v *attemptView) { v.state.ProviderSlots = v.state.ProviderSlots[:2] },
		"binding padding":        func(v *attemptView) { v.state.BindingSlots[1] = attemptBeginBit },
		"provider padding":       func(v *attemptView) { v.state.ProviderSlots[2] = attemptPositiveBit },
	} {
		t.Run(name, func(t *testing.T) {
			fixture, target, view, request, now := recoveryBelowMaximumFixture(t)
			change(&view)

			if _, err := recoveryOriginalSelectionsHeld(t.Context(), fixture, target, view); !errors.Is(err, managed.ErrUnavailable) {
				t.Fatalf("invalid selection index accepted: %v", err)
			}

			if _, err := recoveryActivationReserve(target, view); !errors.Is(err, managed.ErrUnavailable) {
				t.Fatalf("invalid reserve index accepted: %v", err)
			}

			if _, err := prepareRecoveryActivationPlanHeld(t.Context(), fixture, target, view, request, now, 1); !errors.Is(err, managed.ErrUnavailable) {
				t.Fatalf("invalid preparation index accepted: %v", err)
			}

			if fixture.writes != 0 {
				t.Fatal("invalid preparation wrote authority")
			}
		})
	}
}
