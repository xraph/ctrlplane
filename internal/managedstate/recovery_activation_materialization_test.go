package managedstate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

// These fixtures begin with a checked original view. Full public entry, native binding
// loading and concrete store rollback still require the application campaign.
func activationMaterializationFixture(t *testing.T, bindings, services int, issued bool) (*recoveryLeafFixture, *managed.Target, attemptView, recoveryActivationPlan) {
	t.Helper()
	fixture, target, view, request, now := recoveryBelowMaximumFixture(t)

	provision, err := managed.ParseProvisionSpec(target.Manifest)
	if err != nil {
		t.Fatal(err)
	}

	template := provision.Services[0]
	provision.Services = nil
	target.Manifest.Members = nil
	target.Resources = map[string]string{"network": "network-resource"}

	for i := range services {
		service := template

		service.Name = "service" + strconv.Itoa(i)
		if i > 0 {
			service.Role = "sidecar"
		}

		provision.Services = append(provision.Services, service)
		target.Manifest.Members = append(target.Manifest.Members, managed.Member{Name: "member" + strconv.Itoa(i), Service: service.Name})
		target.Resources[service.Name] = "resource" + strconv.Itoa(i)
	}

	raw, err := json.Marshal(provision)
	if err != nil {
		t.Fatal(err)
	}

	roles := make([]managed.RuntimeRole, services)
	for i := range roles {
		roles[i] = managed.Polling
	}

	target.Manifest.Spec, err = json.Marshal(managed.ManagedSpec{Profile: "ctrlplane.managed.v1", RuntimeRoles: roles, Provision: raw})
	if err != nil {
		t.Fatal(err)
	}

	target.Manifest.SpecDigest = managed.Digest(target.Manifest.Spec)
	target.Physical = managed.Physical{Provider: "fixture", Placement: "local", Project: "project", Generation: "reserved-name"}
	target.Seal = 1
	target.Deletion = &managed.Deletion{OperationID: view.descriptor.ProviderOperationID, FenceDigest: strings.Repeat("c", 64)}
	target.Reservations = nil
	view.descriptor.ReservationIDs = nil
	view.state.BindingSlots = make([]byte, managed.MaxRecoveryBindings)

	for i := range bindings {
		reservationID := id.New(managed.PrefixReservation)
		target.Reservations = append(target.Reservations, managed.Reservation{Registration: managed.Registration{ID: reservationID}, Phase: managed.Accepted})
		view.descriptor.ReservationIDs = append(view.descriptor.ReservationIDs, reservationID)
		view.state.BindingSlots[i] = attemptBeginBit | attemptFenceBit
	}

	steps, err := attemptSteps(target, view)
	if err != nil {
		t.Fatal(err)
	}

	if issued {
		for index, step := range steps {
			activationOriginalSlot(t, fixture, target, &view, step, index, now)
		}
	}

	state, err := json.Marshal(view.state)
	if err != nil {
		t.Fatal(err)
	}

	fixture.rows[[3]string{"removal_attempt_state", view.scope, view.ref.AttemptID.String()}] = state

	plan, err := prepareRecoveryActivationPlanHeld(t.Context(), fixture, target, view, request, now, bindings)
	if err != nil {
		t.Fatal(err)
	}

	return fixture, target, view, plan
}

func activationOriginalSlot(t *testing.T, fixture *recoveryLeafFixture, target *managed.Target, view *attemptView, step managed.ProviderStepKey, index int, now time.Time) {
	t.Helper()

	resource := step.Service
	if resource == "" {
		resource = "network"
	}

	permission := managed.ProviderStepPermission{Step: step, CommandID: id.New(managed.PrefixOperation), Digest: strings.Repeat("a", 64), SpecDigest: target.Manifest.SpecDigest, CreateProfile: "ctrlplane.managed.create.v1", Physical: target.Physical, Seal: target.Seal, FenceDigest: target.Deletion.FenceDigest, ResourceID: target.Resources[resource], IssuedAt: now, Revision: target.Revision}
	receipt := managed.Receipt{ID: permission.CommandID, Digest: permission.Digest, Revision: permission.Revision, LeaseEpoch: target.LeaseEpoch, AcceptedAt: now, LeaseUntil: target.LeaseUntil}
	fixture.receipts[attemptReceiptKey{target.Key, receipt.ID, receipt.Digest}] = receipt

	rawPermission, err := json.Marshal(permission)
	if err != nil {
		t.Fatal(err)
	}

	association := attemptProviderSlot{SchemaVersion: "1", AttemptRef: view.ref, ProviderOperationID: step.ProviderOperationID, Index: managed.Decimal(strconv.Itoa(index)), PermissionDigest: managed.Digest(rawPermission), Receipt: managed.ReceiptWire(receipt)}

	stepKey, err := providerStepID(step)
	if err != nil {
		t.Fatal(err)
	}

	if err = controllerWrite(t.Context(), fixture, "step", target.TenantID, stepKey, managed.ProviderStepRecord{Permission: permission}, true, 262144); err != nil {
		t.Fatal(err)
	}

	if err = attemptWrite(t.Context(), fixture, "removal_provider_slot", view.scope, stepKey, association, true, managed.MaxAttemptAssociation); err != nil {
		t.Fatal(err)
	}

	view.state.ProviderSlots[index] = attemptPermissionBit
	_, effect, _, _ := effectBindingFixture(t) //nolint:dogsled // Only the retained record is needed from the shared fixture.
	effect.Facts.Original = managed.ProviderEffectOriginal{Key: managed.ProviderEffectBindingKey{AttemptKey: attemptKey(*view), Step: step, OriginalCommandID: permission.CommandID}, AttemptRef: view.ref, Permission: managed.ProviderPermissionWire(permission), Receipt: managed.ReceiptWire(receipt)}

	command, err := managed.DecodeProviderEffectCommand(effect.Facts.Command)
	if err != nil {
		t.Fatal(err)
	}

	command.ResourceID = permission.ResourceID
	if step.Kind == managed.StepRemoveNetwork {
		command.ResourceKind = "network"
	}

	effect.Facts.Command, err = managed.EncodeProviderEffectCommand(command)
	if err != nil {
		t.Fatal(err)
	}

	effect.Facts.ProviderCommandDigest, err = managed.ProviderEffectCommandDigest(effect.Facts.Command)
	if err != nil {
		t.Fatal(err)
	}

	effect.Digest, err = managed.ProviderEffectBindingDigest(effect.Facts)
	if err != nil {
		t.Fatal(err)
	}

	effect.Acceptance.RequestDigest, err = managed.ProviderEffectBindingAcceptanceDigest(effect.RequestDigest, effect.Digest)
	if err != nil {
		t.Fatal(err)
	}

	effect.Acceptance.Receipt.Digest = effect.Acceptance.RequestDigest
	effect.Acceptance.Receipt.Revision = managed.Decimal(strconv.FormatUint(target.Revision, 10))
	retainEffectFixture(t, fixture, effect)
}

func TestActivationStagesSmallAndMaximumFixedSets(t *testing.T) {
	for _, size := range [][2]int{{1, 1}, {64, 32}} {
		for _, issued := range []bool{false, true} {
			fixture, target, view, plan := activationMaterializationFixture(t, size[0], size[1], issued)
			beforeWrites := fixture.writes
			beforeTarget := target.Revision

			staged, err := stageRecoveryActivationMaterialization(t.Context(), fixture, target, view, plan)
			if err != nil {
				t.Fatal(size, issued, err)
			}

			if fixture.writes != beforeWrites || target.Revision != beforeTarget {
				t.Fatal("preparation persisted changes")
			}

			inspection := view
			inspection.state = plan.State.AttemptState

			inspection.recoveryActivation = &plan.State.Activation
			for index, selection := range plan.Selections {
				if err = inspectRecoveryFixedSelection(t.Context(), staged, target, inspection, plan.Profile, selection, index); err != nil {
					t.Fatal(size, issued, err)
				}
			}

			want := 3 + len(plan.Selections)
			if issued {
				want += 6 * len(plan.Selections)
			}

			if len(staged.rows) != want {
				t.Fatal("fixed row count", len(staged.rows), want)
			}
		}
	}
}

func TestActivationMissingEffectRefusesIssuedOriginalBeforeWrites(t *testing.T) {
	fixture, target, view, plan := activationMaterializationFixture(t, 1, 1, true)

	permission, _, err := attemptProviderRead(t.Context(), fixture, target, view, plan.Selections[0].Step, 0)
	if err != nil {
		t.Fatal(err)
	}

	scope, key, err := providerEffectKey(managed.ProviderEffectBindingKey{AttemptKey: attemptKey(view), Step: permission.Permission.Step, OriginalCommandID: permission.Permission.CommandID})
	if err != nil {
		t.Fatal(err)
	}

	delete(fixture.rows, [3]string{managed.ProviderEffectBindingKind, scope, key})

	before := fixture.writes
	if _, err = stageRecoveryActivationMaterialization(t.Context(), fixture, target, view, plan); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("missing effect became unissued", err)
	}

	if fixture.writes != before || plan.Selections[0].Disposition != "original" {
		t.Fatal("refusal changed original")
	}
}

type activationFailingWrites struct {
	*recoveryLeafFixture

	failAt, calls int
	failReceipt   bool
	target        *managed.Target
}

func (tx *activationFailingWrites) WriteRecord(ctx context.Context, kind, scope, key string, raw []byte, insert bool) error {
	tx.calls++
	if tx.calls == tx.failAt {
		return managed.ErrUnavailable
	}

	return tx.recoveryLeafFixture.WriteRecord(ctx, kind, scope, key, raw, insert)
}

func cloneActivationFixture(fixture *recoveryLeafFixture) *recoveryLeafFixture {
	cloned := &recoveryLeafFixture{rows: map[[3]string][]byte{}, receipts: map[attemptReceiptKey]managed.Receipt{}}
	for key, raw := range fixture.rows {
		cloned.rows[key] = bytes.Clone(raw)
	}

	maps.Copy(cloned.receipts, fixture.receipts)

	return cloned
}

type activationTransactionFixture struct {
	Backend

	state       *recoveryLeafFixture
	target      *managed.Target
	failAt      int
	failReceipt bool
}

func (backend *activationTransactionFixture) ControllerAtomic(ctx context.Context, _ string, _ []managed.Key, change func(ControllerTransaction) error) error {
	target := *backend.target

	isolated := &activationFailingWrites{recoveryLeafFixture: cloneActivationFixture(backend.state), failAt: backend.failAt, failReceipt: backend.failReceipt, target: &target}
	if err := change(isolated); err != nil {
		return err
	}

	backend.state.rows = isolated.rows
	backend.state.receipts = isolated.receipts
	*backend.target = *isolated.target

	return nil
}

func (tx *activationFailingWrites) Atomic(_ context.Context, key managed.Key, operation id.ID, digest string, change func(*managed.Target, time.Time) (managed.Receipt, error)) (managed.Receipt, error) {
	current := *tx.target

	receipt, err := change(&current, tx.target.LeaseUntil.Add(-time.Minute))
	if err != nil {
		return managed.Receipt{}, err
	}

	if tx.failReceipt {
		return managed.Receipt{}, managed.ErrUnavailable
	}

	if receipt.ID != operation || receipt.Digest != digest {
		return managed.Receipt{}, managed.ErrConflict
	}

	tx.receipts[attemptReceiptKey{key, operation, digest}] = receipt
	tx.target = &current

	return receipt, nil
}

func TestActivationDraftRollbackAtEveryStagedWrite(t *testing.T) {
	fixture, target, view, plan := activationMaterializationFixture(t, 1, 1, true)

	staged, err := stageRecoveryActivationMaterialization(t.Context(), fixture, target, view, plan)
	if err != nil {
		t.Fatal(err)
	}

	before := cloneActivationFixture(fixture)
	backend := &activationTransactionFixture{state: fixture, target: target}
	apply := func() error {
		return New(backend).controller(t.Context(), "activation-fixture", []managed.Key{target.Key}, func(tx ControllerTransaction) error {
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
		backend.failAt = failAt
		backend.failReceipt = failAt == len(staged.rows)+1

		if err = apply(); !errors.Is(err, managed.ErrUnavailable) {
			t.Fatal("injected failure was ignored", err)
		}

		if !reflect.DeepEqual(fixture.rows, before.rows) || !reflect.DeepEqual(fixture.receipts, before.receipts) || target.Revision != 10 {
			t.Fatal("failed transaction published partial rows")
		}
	}

	backend.failAt = 0
	backend.failReceipt = false

	if err = apply(); err != nil {
		t.Fatal(err)
	}

	if target.Revision != staged.receipt.Revision {
		t.Fatal("successful model transaction did not advance parent")
	}

	if _, err = fixture.ReadReceipt(t.Context(), target.Key, staged.receipt.ID, staged.receipt.Digest); err != nil {
		t.Fatal("successful model transaction omitted receipt", err)
	}

	for _, row := range staged.rows {
		if !bytes.Equal(fixture.rows[[3]string{row.kind, row.scope, row.key}], row.raw) {
			t.Fatal("successful model transaction omitted row")
		}
	}
}

func TestRecoveryInspectionSchemaFloorAndMutationFence(t *testing.T) {
	fixture, target, view, plan := activationMaterializationFixture(t, 1, 1, false)

	staged, err := stageRecoveryActivationMaterialization(t.Context(), fixture, target, view, plan)
	if err != nil {
		t.Fatal(err)
	}

	legacy := newAttemptCheckedReads(staged)
	if _, _, _, err = readRecoveryCompatibleAttemptState(t.Context(), legacy, view.scope, view.ref.AttemptID.String()); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal("legacy state reader accepted schema 2", err)
	}

	inspection := newAttemptCheckedReads(staged)
	inspection.recoveryInspection = true

	state, activation, _, err := readRecoveryCompatibleAttemptState(t.Context(), inspection, view.scope, view.ref.AttemptID.String())
	if err != nil || state.SchemaVersion != "2" || activation == nil || *activation != plan.State.Activation {
		t.Fatal(err)
	}

	view.state = state

	view.recoveryActivation = activation
	if err = attemptUpdate(t.Context(), fixture, &view); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal("legacy mutation stripped activation", err)
	}

	if err = New(&recoveryBackendWitness{}).requireConfiguredRecovery(); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal("partial activation enabled full capability", err)
	}
}

func TestActivationPreservesOriginalPositiveAndUncertainResults(t *testing.T) {
	for _, outcome := range []managed.PortOutcome{managed.PortTerminal, managed.PortUnavailable} {
		fixture, target, view, plan := activationMaterializationFixture(t, 1, 1, true)
		step := plan.Selections[0].Step

		record, association, err := attemptProviderRead(t.Context(), fixture, target, view, step, 0)
		if err != nil {
			t.Fatal(err)
		}

		result := managed.ProviderStepResult{Permission: record.Permission, Outcome: outcome, ResourceID: record.Permission.ResourceID, Evidence: []byte("original evidence"), ObservedAt: record.Permission.IssuedAt}
		record.Result = &result

		resultRaw, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}

		hash := managed.Digest(resultRaw)
		if outcome == managed.PortTerminal {
			association.PositiveDigest = hash
			view.state.ProviderSlots[0] |= attemptPositiveBit
		} else {
			hash, err = controllerFingerprint(result)
			if err != nil {
				t.Fatal(err)
			}

			association.LastUncertaintyDigest = hash
			view.state.ProviderSlots[0] |= attemptUncertaintyBit
		}

		fixture.rows[[3]string{"step_history", target.TenantID, hash}] = resultRaw

		stepKey, err := providerStepID(step)
		if err != nil {
			t.Fatal(err)
		}

		recordRaw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}

		fixture.rows[[3]string{"step", target.TenantID, stepKey}] = recordRaw

		associationRaw, err := json.Marshal(association)
		if err != nil {
			t.Fatal(err)
		}

		fixture.rows[[3]string{"removal_provider_slot", view.scope, stepKey}] = associationRaw
		request := managed.RemovalRecoveryActivationRequest{Command: managed.RecoveryCommand{Key: target.Key, ID: plan.Profile.Acceptance.OperationID, Revision: managed.Decimal(strconv.FormatUint(target.Revision, 10)), LeaseEpoch: managed.Decimal(strconv.FormatUint(target.LeaseEpoch, 10)), Owner: target.LeaseOwner, AttemptRef: view.ref, Profile: managed.RemovalRecoveryProfile}}

		plan, err = prepareRecoveryActivationPlanHeld(t.Context(), fixture, target, view, request, plan.Profile.Facts.ActivatedAt, 1)
		if err != nil {
			t.Fatal(err)
		}

		staged, err := stageRecoveryActivationMaterialization(t.Context(), fixture, target, view, plan)
		if err != nil {
			t.Fatal(err)
		}

		inspection := view
		inspection.state = plan.State.AttemptState

		inspection.recoveryActivation = &plan.State.Activation
		if err = inspectRecoveryFixedSelection(t.Context(), staged, target, inspection, plan.Profile, plan.Selections[0], 0); err != nil {
			t.Fatal(err)
		}

		for _, row := range staged.rows {
			if row.kind == string(managed.RecoveryMarkerRecord) || row.kind == string(managed.RecoveryClosureRecord) {
				t.Fatal("adoption invented delegation or closure")
			}
		}

		if !bytes.Equal(recordRaw, fixture.rows[[3]string{"step", target.TenantID, stepKey}]) {
			t.Fatal("adoption rewrote original result")
		}
	}
}

func TestRecoveryFixedSelectionRefusesChangedRows(t *testing.T) {
	fixture, target, view, plan := activationMaterializationFixture(t, 1, 1, true)

	staged, err := stageRecoveryActivationMaterialization(t.Context(), fixture, target, view, plan)
	if err != nil {
		t.Fatal(err)
	}

	view.state = plan.State.AttemptState
	view.recoveryActivation = &plan.State.Activation

	for _, mutate := range []func(*recoveryFixedSelection){
		func(f *recoveryFixedSelection) { f.Root = nil },
		func(f *recoveryFixedSelection) { f.Disposition = "unissued"; f.Root = nil },
		func(f *recoveryFixedSelection) { f.Activation.Digest = strings.Repeat("f", 64) },
	} {
		cloned := *staged
		cloned.rows = append([]recoveryActivationRow(nil), staged.rows...)
		changed := false

		for index, row := range cloned.rows {
			if row.kind != recoveryFixedSelectionKind {
				continue
			}

			fixed, decodeErr := attemptDecode[recoveryFixedSelection](row.raw, managed.MaxStepRetryPointer)
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}

			if fixed.Step != plan.Selections[0].Step {
				continue
			}

			mutate(&fixed)

			cloned.rows[index].raw, err = json.Marshal(fixed)
			if err != nil {
				t.Fatal(err)
			}

			changed = true
		}

		if !changed {
			t.Fatal("fixed selection fixture missing")
		}

		if err = inspectRecoveryFixedSelection(t.Context(), &cloned, target, view, plan.Profile, plan.Selections[0], 0); err == nil {
			t.Fatal("changed selection accepted")
		}
	}
}
