package managedstate

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

func continuationDecimal(v uint64) managed.Decimal { return managed.Decimal(strconv.FormatUint(v, 10)) }

func continuationAccess(q managed.ActionSubmission) managed.AccessAuditRequest {
	return managed.AccessAuditRequest{AttemptID: q.ID, Installation: q.Installation, Authority: q.Authority, ApplicationID: q.ApplicationID, OrganizationID: q.OrganizationID, Actor: q.Actor, Scope: managed.AuditScope{Kind: "tenant", TenantID: q.TenantID}, Action: "managed.continue", ResourceKind: "instance", ResourceID: q.InstanceID.String(), Outcome: "allowed", Reason: "authorized", OccurredAt: q.OccurredAt}
}

func continuationEligibility(ctx context.Context, tx ControllerTransaction, t *managed.Target, v attemptView) (managed.Disposition, string, error) {
	if !v.validated || v.state.RevocationDigest == "" || v.state.TerminalDigest != "" {
		return "", "", managed.ErrBlocked
	}

	rev, err := attemptRevocationRead(ctx, tx, v)
	if err != nil {
		return "", "", err
	}

	slots, _, coverage, err := attemptCoverage(ctx, tx, t, v)
	if err != nil {
		return "", "", err
	}

	if rev.Disposition != managed.NotIssued && rev.Disposition != managed.SettledWithoutDeletion {
		return "", "", managed.ErrBlocked
	}

	if rev.Disposition == managed.NotIssued {
		if v.state.SettlementDigest != "" || rev.SettlementDigest != "" {
			return "", "", managed.ErrUnavailable
		}

		for _, slot := range slots {
			if slot.State != "unissued" {
				return "", "", managed.ErrBlocked
			}
		}
	} else {
		settled, e := attemptSettlementRead(ctx, tx, v)
		if e != nil {
			return "", "", e
		}

		if settled.Disposition != managed.SettledWithoutDeletion || settled.CoverageDigest != coverage || settled.ProducerProfile != managed.LocalNoDispatchProfile {
			return "", "", managed.ErrBlocked
		}

		for _, slot := range slots {
			if slot.State != "unissued" && slot.State != "local_no_dispatch" {
				return "", "", managed.ErrBlocked
			}
		}
	}

	if rev.CoverageDigest != coverage {
		return "", "", managed.ErrUnavailable
	}

	for _, rid := range v.descriptor.ReservationIDs {
		binding, e := attemptBindingRead(ctx, tx, t, v, attemptBinding(v, rid))
		if e != nil {
			return "", "", e
		}

		if len(binding.Commands) == 0 {
			continue
		}

		if binding.Fence == nil || binding.Abort == nil || binding.Witness == nil || binding.Finish != nil {
			return "", "", managed.ErrBlocked
		}

		if binding.Witness.Disposition == managed.Removed || binding.Witness.WitnessedAt.Before(binding.Fence.NativeAcceptance.AcceptedAt) {
			return "", "", managed.ErrBlocked
		}
	}

	return rev.Disposition, coverage, nil
}

// readContinuationBaseHeld is a nonrecursive exact association leaf.
func readContinuationBaseHeld(ctx context.Context, tx ControllerTransaction, t *managed.Target, action id.ID) (managed.ContinuationRecord, error) {
	var out managed.ContinuationRecord

	scope, err := attemptScope(t.Key)
	if err != nil {
		return out, err
	}

	m, err := attemptRead[managed.ContinuationMaterial](ctx, tx, "removal_continuation", scope, action.String(), managed.MaxRemovalContinuationMaterial)
	if err != nil {
		return out, err
	}

	q := m.Request
	digest, err := managed.RemovalContinuationDigest(q)

	generation, e := q.Predecessor.Generation.Uint64()
	if err != nil || e != nil || m.SchemaVersion != "1" || q.Action.Key != t.Key || q.Action.ID != action || m.Digest != digest || q.Action.CanonicalDigest != digest || m.SuccessorGeneration != continuationDecimal(generation+1) {
		return out, managed.ErrUnavailable
	}

	c, err := attemptRead[managed.ContinuationClosure](ctx, tx, "removal_attempt_closure", scope, q.Predecessor.AttemptID.String(), managed.MaxRemovalContinuationClosure)
	if err != nil {
		return out, err
	}

	if c.SchemaVersion != "1" || c.Key != t.Key || c.ActionID != action || c.MaterialDigest != digest || c.Predecessor != q.Predecessor || c.Successor.AttemptID != q.SuccessorAttemptID || c.Successor.Generation != m.SuccessorGeneration || c.ProviderOperationID != q.ProviderOperationID || c.Lineage.SchemaVersion != "1" || c.PointerRevision != m.SuccessorGeneration || !attemptTime(c.AcceptedAt) {
		return out, managed.ErrUnavailable
	}

	op, err := acceptedOperationBase(ctx, tx, managed.OperationKey{Key: t.Key, ActionID: action})
	if err != nil {
		return out, err
	}

	association := managed.ContinuationAssociation{MaterialID: action, MaterialDigest: digest, Predecessor: c.Predecessor, Successor: c.Successor, ProviderOperationID: c.ProviderOperationID}
	if op.RemovalAttemptRoot != nil || op.ContinuationAssociation == nil || *op.ContinuationAssociation != association || !reflect.DeepEqual(op.Submission, q.Action) || op.Revision == 0 || op.Revision > math.MaxInt64 {
		return out, managed.ErrUnavailable
	}

	rev, err := c.ParentRevision.Uint64()
	if err != nil || rev == 0 || rev > math.MaxInt64 {
		return out, managed.ErrUnavailable
	}

	expected := managed.ActionReceipt{ID: action, Key: t.Key, Target: t.Key, CanonicalDigest: digest, OriginalDigest: q.Action.OriginalDigest, Revision: rev, DesiredRevision: q.Action.ExpectedDesiredRevision, AuditID: action, AcceptedAt: c.AcceptedAt}
	if !reflect.DeepEqual(op.Receipt, expected) {
		return out, managed.ErrUnavailable
	}

	auditScope, err := controllerScope(q.Action.Installation, q.Action.Authority, managed.AuditScope{Kind: "tenant", TenantID: t.TenantID})
	if err != nil {
		return out, err
	}

	audit, err := controllerRead[managed.AuditIntent](ctx, tx, "audit", auditScope, action.String())
	if err != nil {
		return out, err
	}

	if !reflect.DeepEqual(audit, intentFromAccess(continuationAccess(q.Action), digest, c.AcceptedAt)) {
		return out, managed.ErrUnavailable
	}

	event, err := attemptEventRead(ctx, tx, attemptView{scope: scope, ref: c.Successor, descriptor: managed.AttemptDescriptor{Key: t.Key}}, action)
	if err != nil {
		return out, err
	}

	if event.Kind != "continue_attempt" || event.FactKind != "removal_attempt_closure" || event.FactKey != c.Predecessor.AttemptID.String() || event.RequestDigest != digest || event.Receipt.Revision != rev || event.Receipt.LeaseEpoch != q.Action.LeaseEpoch || !event.Receipt.AcceptedAt.Equal(c.AcceptedAt) {
		return out, managed.ErrUnavailable
	}

	out = managed.ContinuationRecord{Material: m, Closure: c, Receipt: managed.ContinuationReceipt{Receipt: event.Receipt, ActionReceipt: op.Receipt, Successor: c.Successor, ProviderOperationID: c.ProviderOperationID}}

	return out, nil
}

// readAttempt validates a bounded root/first/current/immediate slice and exact history.
func readAttempt(ctx context.Context, tx ControllerTransaction, t *managed.Target, k managed.AttemptKey) (attemptView, error) {
	var empty attemptView

	scope, err := attemptScope(t.Key)
	if err != nil {
		return empty, err
	}

	current, err := attemptRead[attemptCurrent](ctx, tx, "removal_attempt_current", scope, "current", managed.MaxAttemptPointer)
	if err != nil {
		return empty, err
	}

	root, err := attemptRead[attemptRoot](ctx, tx, "removal_attempt_root", scope, "root", managed.MaxAttemptPointer)
	if err != nil {
		return empty, err
	}

	if current.SchemaVersion != "1" || current.Key != t.Key || current.PointerRevision != current.AttemptRef.Generation {
		return empty, managed.ErrUnavailable
	}

	cache := make(map[managed.AttemptRef]attemptView)
	load := func(ref managed.AttemptRef) (attemptView, error) {
		if v, ok := cache[ref]; ok {
			return v, nil
		}

		v, e := readAttemptBaseHeld(ctx, tx, t, managed.AttemptKey{Key: t.Key, AttemptID: ref.AttemptID, Generation: ref.Generation})
		if e != nil {
			return empty, e
		}

		if v.ref != ref {
			return empty, managed.ErrUnavailable
		}

		cache[ref] = v

		return v, nil
	}

	selected, err := readAttemptBaseHeld(ctx, tx, t, k)
	if err != nil {
		return empty, err
	}

	cache[selected.ref] = selected

	currentView, err := load(current.AttemptRef)
	if err != nil {
		return empty, err
	}

	rootView, err := load(root.AttemptRef)
	if err != nil {
		return empty, err
	}

	if currentView.state.Successor != nil || rootView.descriptor.Generation != "1" {
		return empty, managed.ErrUnavailable
	}

	links := make(map[id.ID]managed.ContinuationRecord)
	checkedLinks := make(map[id.ID]bool)
	readLink := func(action id.ID) (managed.ContinuationRecord, error) {
		if err := ctx.Err(); err != nil {
			return managed.ContinuationRecord{}, err
		}

		if record, ok := links[action]; ok {
			return record, nil
		}

		record, e := readContinuationBaseHeld(ctx, tx, t, action)
		if e != nil {
			return record, e
		}

		links[action] = record

		return record, nil
	}
	checkLink := func(record managed.ContinuationRecord) error {
		c := record.Closure

		if err := ctx.Err(); err != nil {
			return err
		}

		if checkedLinks[c.ActionID] {
			return nil
		}

		predecessor, e := load(c.Predecessor)
		if e != nil {
			return e
		}

		successor, e := load(c.Successor)
		if e != nil {
			return e
		}

		hash, e := attemptFact("removal_attempt_closure", c)
		if e != nil {
			return e
		}

		if predecessor.state.Successor == nil || *predecessor.state.Successor != c.Successor || predecessor.state.ClosureDigest != hash || predecessor.state.Revision != c.PredecessorRevision || successor.descriptor.Predecessor == nil || *successor.descriptor.Predecessor != c.Predecessor || successor.descriptor.ActionID != c.ActionID || successor.descriptor.ProviderOperationID != c.ProviderOperationID || !successor.descriptor.AcceptedAt.Equal(c.AcceptedAt) {
			return managed.ErrUnavailable
		}

		disposition, coverage, e := continuationEligibility(ctx, tx, t, predecessor)
		if e != nil {
			return e
		}

		if c.Disposition != disposition || c.CoverageDigest != coverage || c.RevocationDigest != predecessor.state.RevocationDigest || c.SettlementDigest != predecessor.state.SettlementDigest || c.Lineage.Root != root.AttemptRef || c.Lineage.RootInitialOperationID != root.InitialOperationID || c.Lineage.OriginalOperationKey != root.OriginalOperationKey {
			return managed.ErrUnavailable
		}

		checkedLinks[c.ActionID] = true

		return nil
	}

	var first *managed.ContinuationRecord

	if rootView.state.Successor != nil {
		successor, e := load(*rootView.state.Successor)
		if e != nil {
			return empty, e
		}

		record, e := readLink(successor.descriptor.ActionID)
		if e != nil {
			return empty, e
		}

		if e = checkLink(record); e != nil {
			return empty, e
		}

		if record.Closure.Predecessor != root.AttemptRef || record.Closure.Lineage.FirstMaterialID != record.Material.Request.Action.ID || record.Closure.Lineage.FirstMaterialDigest != record.Material.Digest || record.Closure.Lineage.FirstSuccessor != record.Closure.Successor {
			return empty, managed.ErrUnavailable
		}

		first = &record
	} else if current.AttemptRef != root.AttemptRef {
		return empty, managed.ErrUnavailable
	}

	for _, v := range []attemptView{currentView, selected} {
		if v.descriptor.Generation != "1" {
			record, e := readLink(v.descriptor.ActionID)
			if e != nil {
				return empty, e
			}

			if first == nil || record.Closure.Lineage != first.Closure.Lineage || record.Closure.Successor != v.ref {
				return empty, managed.ErrUnavailable
			}

			if e = checkLink(record); e != nil {
				return empty, e
			}
		}

		if v.state.Successor != nil {
			successor, e := load(*v.state.Successor)
			if e != nil {
				return empty, e
			}

			record, e := readLink(successor.descriptor.ActionID)
			if e != nil {
				return empty, e
			}

			if first == nil || record.Closure.Lineage != first.Closure.Lineage {
				return empty, managed.ErrUnavailable
			}

			if e = checkLink(record); e != nil {
				return empty, e
			}
		}
	}

	selected.lineageRoot = &rootView

	return selected, nil
}

func continuationIDs(ctx context.Context, tx ControllerTransaction, t *managed.Target, q managed.ContinuationRequest) error {
	scope, err := attemptScope(t.Key)
	if err != nil {
		return err
	}

	original, err := originalScope(t.Key)
	if err != nil {
		return err
	}

	batch, err := managed.QuiescenceBatchScope(t.Key)
	if err != nil {
		return err
	}

	for _, value := range []id.ID{q.Action.ID, q.SuccessorAttemptID, q.ProviderOperationID} {
		if value == t.ProvisionOperationID {
			return managed.ErrConflict
		}

		for _, entry := range [][2]string{{"removal_identity", ""}, {"action", ""}, {"access", ""}, {"operation", t.TenantID}, {"operation_history", t.TenantID}, {"removal_attempt_event", scope}, {"removal_continuation", scope}} {
			_, e := tx.ReadRecord(ctx, entry[0], entry[1], value.String())
			if e == nil {
				return managed.ErrConflict
			}

			if !controllerAbsent(e) {
				return e
			}
		}

		for _, entry := range [][2]string{{managed.OriginalRegistration, original}, {managed.OriginalDrain, original}, {managed.QuiescenceBatchHistory, batch}} {
			_, e := tx.ReadRecord(ctx, entry[0], entry[1], value.String())
			if e == nil {
				return managed.ErrConflict
			}

			if !controllerAbsent(e) {
				return e
			}
		}

		for _, binding := range t.Reservations {
			childScope, e := managed.QuiescenceScope(t.Key, binding.ID)
			if e != nil {
				return e
			}

			_, e = tx.ReadRecord(ctx, managed.QuiescenceHistory, childScope, value.String())
			if e == nil {
				return managed.ErrConflict
			}

			if !controllerAbsent(e) {
				return e
			}
		}

		_, e := tx.ReadReceipt(ctx, t.Key, value, "")
		if e == nil {
			return managed.ErrConflict
		}

		if !controllerAbsent(e) {
			return e
		}
	}

	root, err := attemptRead[attemptRoot](ctx, tx, "removal_attempt_root", scope, "root", managed.MaxAttemptPointer)
	if err != nil {
		return err
	}

	selected, err := selectedAttempt(ctx, tx, t)
	if err != nil {
		return err
	}

	refs := []managed.AttemptRef{root.AttemptRef, selected.ref}
	if selected.descriptor.Predecessor != nil {
		refs = append(refs, *selected.descriptor.Predecessor)
	}

	if selected.descriptor.Generation != "1" {
		birth, e := readContinuationBaseHeld(ctx, tx, t, selected.descriptor.ActionID)
		if e != nil {
			return e
		}

		refs = append(refs, birth.Closure.Lineage.FirstSuccessor)
	}

	seen := map[managed.AttemptRef]bool{}
	for _, ref := range refs {
		if seen[ref] {
			continue
		}

		seen[ref] = true

		v, e := readAttemptBaseHeld(ctx, tx, t, managed.AttemptKey{Key: t.Key, AttemptID: ref.AttemptID, Generation: ref.Generation})
		if e != nil {
			return e
		}

		for _, command := range v.commands {
			for _, value := range []id.ID{q.Action.ID, q.SuccessorAttemptID, q.ProviderOperationID} {
				if value == command.NativeCommand.OperationID || value == command.AuthorizationID {
					return managed.ErrConflict
				}
			}
		}

		steps, e := attemptSteps(t, v)
		if e != nil {
			return e
		}

		for i, step := range steps {
			record, _, e := attemptProviderRead(ctx, tx, t, v, step, i)
			if e != nil {
				return e
			}

			if record.Permission.CommandID.IsNil() {
				continue
			}

			key, e := providerStepID(step)
			if e != nil {
				return e
			}

			failure, e := attemptRead[managed.ProviderStepFailure](ctx, tx, "step_failure", t.TenantID, key, managed.MaxAttemptSettlement)
			if e != nil && !controllerAbsent(e) {
				return e
			}

			for _, value := range []id.ID{q.Action.ID, q.SuccessorAttemptID, q.ProviderOperationID} {
				if value == record.Permission.CommandID || value == failure.InvocationID {
					return managed.ErrConflict
				}
			}
		}
	}

	return nil
}

// BeginManagedRemovalContinuation atomically closes one predecessor and allocates its successor.
func (r *Repository) BeginManagedRemovalContinuation(ctx context.Context, q managed.ContinuationRequest) (managed.ContinuationReceipt, error) {
	var out managed.ContinuationReceipt

	q.Action.OccurredAt = q.Action.OccurredAt.UTC()

	digest, err := managed.RemovalContinuationDigest(q)
	if err != nil {
		return out, err
	}

	if digest != q.Action.CanonicalDigest || !validAttemptCommand(q.Action.Command) {
		return out, managed.ErrInvalid
	}

	raw, err := json.Marshal(q)
	if err != nil || len(raw) > managed.MaxRemovalContinuationRequest {
		return out, managed.ErrInvalid
	}

	err = r.controller(ctx, "attempt:"+q.Action.InstanceID.String(), []managed.Key{q.Action.Key}, func(tx ControllerTransaction) error {
		t, e := tx.Read(ctx, q.Action.Key)
		if e != nil {
			return e
		}

		scope, e := attemptScope(t.Key)
		if e != nil {
			return e
		}

		saved, e := readContinuationBaseHeld(ctx, tx, t, q.Action.ID)
		if e == nil {
			if !reflect.DeepEqual(saved.Material.Request, q) {
				return managed.ErrConflict
			}

			if _, e = readAttempt(ctx, tx, t, managed.AttemptKey{Key: t.Key, AttemptID: saved.Closure.Successor.AttemptID, Generation: saved.Closure.Successor.Generation}); e != nil {
				return e
			}

			out = saved.Receipt

			return nil
		}

		if !controllerAbsent(e) {
			return e
		}

		if _, e = tx.ReadRecord(ctx, "removal_continuation", scope, q.Action.ID.String()); !controllerAbsent(e) {
			if e != nil {
				return e
			}

			return managed.ErrUnavailable
		}

		if e = continuationIDs(ctx, tx, t, q); e != nil {
			return e
		}

		var staged managed.ContinuationReceipt

		fresh := false

		_, e = tx.Atomic(ctx, t.Key, q.Action.ID, digest, func(parent *managed.Target, now time.Time) (managed.Receipt, error) {
			a := q.Action
			if parent.Revision != a.Revision || parent.LeaseEpoch != a.LeaseEpoch || parent.LeaseOwner != a.Owner || !now.Before(parent.LeaseUntil) || parent.DesiredRevision != a.ExpectedDesiredRevision {
				return managed.Receipt{}, managed.ErrConflict
			}

			if a.OccurredAt.After(now) || parent.Phase == managed.Deleted || !fixedAttemptProfile(parent) {
				return managed.Receipt{}, managed.ErrBlocked
			}

			predecessor, e := selectedAttempt(ctx, tx, parent)
			if e != nil {
				return managed.Receipt{}, e
			}

			if predecessor.ref != q.Predecessor || predecessor.state.Successor != nil {
				return managed.Receipt{}, managed.ErrConflict
			}

			disposition, coverage, e := continuationEligibility(ctx, tx, parent, predecessor)
			if e != nil {
				return managed.Receipt{}, e
			}

			revision, e := predecessor.state.Revision.Uint64()
			if e != nil || revision >= math.MaxInt64 {
				return managed.Receipt{}, managed.ErrCapacity
			}

			spec, e := managed.ParseProvisionSpec(parent.Manifest)
			if e != nil {
				return managed.Receipt{}, e
			}

			reserve := uint64(7*len(predecessor.descriptor.ReservationIDs) + len(spec.Services) + 1 + 8)
			if parent.Revision > math.MaxInt64-reserve-1 {
				return managed.Receipt{}, managed.ErrCapacity
			}

			generation, e := predecessor.ref.Generation.Uint64()
			if e != nil || generation >= math.MaxInt64 {
				return managed.Receipt{}, managed.ErrCapacity
			}

			d := predecessor.descriptor
			d.AttemptID = q.SuccessorAttemptID
			d.Generation = continuationDecimal(generation + 1)
			d.Predecessor = &predecessor.ref
			d.ActionID = a.ID
			d.ProviderOperationID = q.ProviderOperationID
			d.AcceptedAt = now.UTC()

			dh, e := attemptHash(attemptDescriptorDomain, d)
			if e != nil {
				return managed.Receipt{}, e
			}

			successor := managed.AttemptRef{AttemptID: d.AttemptID, Generation: d.Generation, DescriptorDigest: dh}

			root, e := attemptRead[attemptRoot](ctx, tx, "removal_attempt_root", scope, "root", managed.MaxAttemptPointer)
			if e != nil {
				return managed.Receipt{}, e
			}

			lineage := managed.ContinuationLineage{SchemaVersion: "1", Root: root.AttemptRef, RootInitialOperationID: root.InitialOperationID, OriginalOperationKey: root.OriginalOperationKey, FirstMaterialID: a.ID, FirstMaterialDigest: digest, FirstSuccessor: successor}

			if generation > 1 {
				birth, e := readContinuationBaseHeld(ctx, tx, parent, predecessor.descriptor.ActionID)
				if e != nil {
					return managed.Receipt{}, e
				}

				lineage = birth.Closure.Lineage
			}

			closure := managed.ContinuationClosure{SchemaVersion: "1", Key: parent.Key, ActionID: a.ID, MaterialDigest: digest, Predecessor: predecessor.ref, Successor: successor, ProviderOperationID: q.ProviderOperationID, Lineage: lineage, Disposition: disposition, RevocationDigest: predecessor.state.RevocationDigest, SettlementDigest: predecessor.state.SettlementDigest, CoverageDigest: coverage, PredecessorRevision: continuationDecimal(revision + 1), PointerRevision: d.Generation, ParentRevision: continuationDecimal(parent.Revision + 1), AcceptedAt: now.UTC()}

			closureDigest, e := attemptFact("removal_attempt_closure", closure)
			if e != nil {
				return managed.Receipt{}, e
			}

			predecessor.state.Successor = &successor

			predecessor.state.ClosureDigest = closureDigest
			if e = attemptUpdate(ctx, tx, &predecessor); e != nil {
				return managed.Receipt{}, e
			}

			state := managed.AttemptState{SchemaVersion: "1", AttemptRef: successor, Revision: "1", Phase: "allocated", BindingSlots: make([]byte, 64), ProviderSlots: make([]byte, 33)}

			material := managed.ContinuationMaterial{SchemaVersion: "1", Request: q, SuccessorGeneration: d.Generation, Digest: digest}
			for _, owner := range []attemptIdentity{{"1", d.AttemptID, "attempt", parent.Key, successor}, {"1", d.ProviderOperationID, "provider", parent.Key, successor}} {
				if e = attemptWrite(ctx, tx, "removal_identity", "", owner.ID.String(), owner, true, managed.MaxAttemptPointer); e != nil {
					return managed.Receipt{}, e
				}
			}

			for _, entry := range []struct {
				kind, key string
				value     any
				insert    bool
				maximum   int
			}{
				{"removal_attempt", d.AttemptID.String(), d, true, managed.MaxAttemptDescriptor},
				{"removal_attempt_state", d.AttemptID.String(), state, true, managed.MaxAttemptFact},
				{"removal_attempt_current", "current", attemptCurrent{"1", parent.Key, successor, d.Generation}, false, managed.MaxAttemptPointer},
				{"removal_continuation", a.ID.String(), material, true, managed.MaxRemovalContinuationMaterial},
				{"removal_attempt_closure", predecessor.ref.AttemptID.String(), closure, true, managed.MaxRemovalContinuationClosure},
			} {
				if e = attemptWrite(ctx, tx, entry.kind, scope, entry.key, entry.value, entry.insert, entry.maximum); e != nil {
					return managed.Receipt{}, e
				}
			}

			parent.Revision++
			receipt := managed.Receipt{ID: a.ID, Digest: digest, Revision: parent.Revision, LeaseEpoch: parent.LeaseEpoch, AcceptedAt: now.UTC(), LeaseUntil: parent.LeaseUntil}
			actionReceipt := managed.ActionReceipt{ID: a.ID, Key: parent.Key, Target: parent.Key, CanonicalDigest: digest, OriginalDigest: a.OriginalDigest, Revision: parent.Revision, DesiredRevision: parent.DesiredRevision, AuditID: a.ID, AcceptedAt: now.UTC()}

			op := managed.OperationState{ContinuationAssociation: &managed.ContinuationAssociation{MaterialID: a.ID, MaterialDigest: digest, Predecessor: predecessor.ref, Successor: successor, ProviderOperationID: d.ProviderOperationID}, Key: managed.OperationKey{Key: parent.Key, ActionID: a.ID}, Submission: a, Receipt: actionReceipt, Revision: 1, Observation: managed.OperationObservation{Kind: "pending", ObservedAt: now.UTC()}}
			if e = controllerWrite(ctx, tx, "operation", parent.TenantID, a.ID.String(), op, true, managed.MaxControllerRecord); e != nil {
				return managed.Receipt{}, e
			}

			if e = controllerWrite(ctx, tx, "action", "", a.ID.String(), actionReceipt, true, managed.MaxControllerRecord); e != nil {
				return managed.Receipt{}, e
			}

			access := continuationAccess(a)

			auditScope, e := controllerScope(a.Installation, a.Authority, access.Scope)
			if e != nil {
				return managed.Receipt{}, e
			}

			if e = controllerWrite(ctx, tx, "audit", auditScope, a.ID.String(), intentFromAccess(access, digest, now.UTC()), true, managed.MaxAuditIntent); e != nil {
				return managed.Receipt{}, e
			}

			if e = attemptWrite(ctx, tx, "removal_attempt_event", scope, a.ID.String(), attemptEvent{"1", successor, "continue_attempt", digest, "removal_attempt_closure", predecessor.ref.AttemptID.String(), closureDigest, receipt}, true, managed.MaxAttemptFact); e != nil {
				return managed.Receipt{}, e
			}

			staged = managed.ContinuationReceipt{Receipt: receipt, ActionReceipt: actionReceipt, Successor: successor, ProviderOperationID: d.ProviderOperationID}
			fresh = true

			return receipt, nil
		})
		if e != nil {
			return e
		}

		if !fresh {
			return managed.ErrUnavailable
		}

		post, e := tx.Read(ctx, t.Key)
		if e != nil {
			return e
		}

		if _, e = readAttempt(ctx, tx, post, managed.AttemptKey{Key: t.Key, AttemptID: staged.Successor.AttemptID, Generation: staged.Successor.Generation}); e != nil {
			return e
		}

		out = staged

		return nil
	})
	if err != nil {
		return managed.ContinuationReceipt{}, err
	}

	return out, nil
}

// ReadManagedRemovalContinuation validates exact saved acceptance without current issue authority.
func (r *Repository) ReadManagedRemovalContinuation(ctx context.Context, k managed.ContinuationKey) (managed.ContinuationRecord, error) {
	var out managed.ContinuationRecord
	if !key(k.Key) || k.ActionID.Prefix() != managed.PrefixOperation {
		return out, managed.ErrInvalid
	}

	err := r.controller(ctx, "attempt:"+k.Key.InstanceID.String(), []managed.Key{k.Key}, func(tx ControllerTransaction) error {
		t, e := tx.Read(ctx, k.Key)
		if e != nil {
			return e
		}

		saved, e := readContinuationBaseHeld(ctx, tx, t, k.ActionID)
		if e != nil {
			return e
		}

		if _, e = readAttempt(ctx, tx, t, managed.AttemptKey{Key: t.Key, AttemptID: saved.Closure.Successor.AttemptID, Generation: saved.Closure.Successor.Generation}); e != nil {
			return e
		}

		out = saved

		return nil
	})
	if err != nil {
		return managed.ContinuationRecord{}, err
	}

	return out, nil
}

func currentBindingCheck(ctx context.Context, tx ControllerTransaction, t *managed.Target, v attemptView, c managed.AttemptCommandRecord, o managed.AttemptCurrentBindingObservation, now time.Time) error {
	version, e := o.BindingVersion.Uint64()
	if e != nil || version == 0 || version >= math.MaxInt64 {
		return managed.ErrInvalid
	}

	epoch, e := o.RemovalEpoch.Uint64()
	if e != nil || epoch >= math.MaxInt64 {
		return managed.ErrInvalid
	}

	if o.SchemaVersion != "1" || o.Key != c.Key.AttemptBindingKey || o.DescriptorDigest != v.ref.DescriptorDigest || o.BindingReference != c.BindingReference || o.BindingVersion != c.ExpectedNativeVersion || o.State != "active" || o.VerifierID != v.descriptor.VerifierID || !evidence(o.Evidence) || managed.Digest(o.Evidence) != o.EvidenceDigest || !attemptTime(o.ObservedAt) || !attemptTime(o.ValidUntil) || o.ObservedAt.After(now) || !now.Before(o.ValidUntil) {
		return managed.ErrInvalid
	}

	original, e := readOriginalRecordForBinding(ctx, tx, t, o.BindingReference)
	if e != nil {
		return e
	}

	duration, e := original.Material.Registration.Process.MaximumProofValidityNS.Uint64()
	if e != nil || duration > math.MaxInt64 || o.ValidUntil.Sub(o.ObservedAt) > time.Duration(duration) {
		return managed.ErrInvalid
	}

	raw, e := json.Marshal(o)
	if e != nil || len(raw) > managed.MaxAttemptCurrentBinding {
		return managed.ErrCapacity
	}

	return nil
}

func readOriginalRecordForBinding(ctx context.Context, tx ControllerTransaction, t *managed.Target, ref managed.AttemptBindingReference) (managed.OriginalLifecycleRecord, error) {
	record, _, err := readOriginal(ctx, tx, managed.OriginalLifecycleKey{Key: t.Key, Kind: managed.OriginalRegistration, OperationID: ref.OriginalRegistrationOperationID}, t)

	return record, err
}

func attemptCurrentBindingRead(ctx context.Context, tx ControllerTransaction, t *managed.Target, v attemptView, c managed.AttemptCommandRecord) (managed.AttemptCurrentBindingObservation, error) {
	var out managed.AttemptCurrentBindingObservation

	slot, err := attemptSlot(c.Key.AttemptBindingKey, managed.AttemptBegin)
	if err != nil {
		return out, err
	}

	out, err = attemptRead[managed.AttemptCurrentBindingObservation](ctx, tx, "removal_current_binding", v.scope, slot, managed.MaxAttemptCurrentBinding)
	if err != nil {
		return out, err
	}

	digest, err := attemptHash("ctrlplane.managed.current-binding.v1", out)
	if err != nil {
		return out, err
	}

	if digest != c.CurrentBindingDigest {
		return out, managed.ErrUnavailable
	}

	if err = currentBindingCheck(ctx, tx, t, v, c, out, c.IssuedAt); err != nil {
		return out, err
	}

	return out, nil
}

func successorObservationEligibilityHeld(ctx context.Context, tx ControllerTransaction, t *managed.Target, v attemptView, rid id.ID) error {
	if v.descriptor.Generation == "1" {
		return originalDrainFreshEligibility(t, rid)
	}

	selected, err := reservation(t, rid)
	if err != nil {
		return err
	}

	if v.lineageRoot == nil {
		return managed.ErrUnavailable
	}

	rootView := *v.lineageRoot
	if rootView.state.Successor == nil {
		return managed.ErrUnavailable
	}

	for _, sibling := range t.Reservations {
		if sibling.RuntimeID != selected.RuntimeID {
			continue
		}

		if sibling.Phase != managed.Accepted || sibling.Drain == nil {
			return managed.ErrBlocked
		}

		if _, err = attemptOriginal(ctx, tx, t, sibling.ID); err != nil {
			return err
		}

		drain, err := managed.ParseOriginalReference(sibling.Drain.Evidence)
		if err != nil {
			return err
		}

		if _, _, err = readOriginal(ctx, tx, managed.OriginalLifecycleKey{Key: t.Key, Kind: managed.OriginalDrain, OperationID: drain.OperationID}, t); err != nil {
			return err
		}

		if sibling.Abort == nil {
			continue
		}

		binding, err := attemptBindingRead(ctx, tx, t, rootView, attemptBinding(rootView, sibling.ID))
		if err != nil {
			return err
		}

		if binding.Abort == nil || binding.Fence == nil || binding.Witness == nil || sibling.Fence == nil {
			return managed.ErrBlocked
		}

		native, err := binding.Fence.Fence.Value()
		if err != nil {
			return err
		}

		expected := managed.AbortProof{FenceDigest: native.Digest, VerifiedAt: binding.Abort.ProofVerifiedAt, ValidUntil: binding.Abort.ProofValidUntil, Evidence: binding.Abort.OriginalBindingProof, Receipt: binding.Abort.Receipt}
		if !reflect.DeepEqual(sibling.Abort, &expected) || !reflect.DeepEqual(sibling.Fence, &native) {
			return managed.ErrBlocked
		}
	}

	return nil
}

func attemptLiveClosureHeld(ctx context.Context, tx ControllerTransaction, t *managed.Target, v attemptView, now time.Time) (string, error) {
	if v.descriptor.Generation == "1" {
		return closure(ctx, tx, t, now)
	}

	if !v.validated || v.state.RevocationDigest != "" || v.state.SettlementDigest != "" || (!t.Provision.Complete && !t.Provision.Revoked) {
		return "", managed.ErrBlocked
	}

	type bindingFence struct {
		ID    id.ID         `json:"id"`
		Fence managed.Fence `json:"fence"`
	}

	fences := []bindingFence{}

	for _, rid := range v.descriptor.ReservationIDs {
		binding, err := reservation(t, rid)
		if err != nil {
			return "", err
		}

		if binding.Phase == managed.Revoked {
			continue
		}

		if err = successorObservationEligibilityHeld(ctx, tx, t, v, rid); err != nil {
			return "", err
		}

		f, err := attemptFenceRead(ctx, tx, t, v, attemptBinding(v, rid))
		if err != nil {
			return "", err
		}

		native, err := f.Fence.Value()
		if err != nil || !now.Before(native.ValidUntil) {
			return "", managed.ErrBlocked
		}

		c, err := attemptCommandRead(ctx, tx, t, v, managed.AttemptCommandKey{AttemptBindingKey: attemptBinding(v, rid), Kind: managed.AttemptBegin})
		if err != nil {
			return "", err
		}

		observation, err := attemptCurrentBindingRead(ctx, tx, t, v, c)
		if err != nil {
			return "", err
		}

		if !now.Before(observation.ValidUntil) {
			return "", managed.ErrBlocked
		}

		if err = laterQuiescence(ctx, tx, t, *binding, now); err != nil {
			return "", err
		}

		fences = append(fences, bindingFence{rid, native})
	}

	raw, err := json.Marshal(fences)
	if err != nil {
		return "", err
	}

	return managed.Digest(raw), nil
}

func providerAttemptHeld(ctx context.Context, tx ControllerTransaction, t *managed.Target, step managed.ProviderStepKey) (attemptView, error) {
	owner, err := attemptRead[attemptIdentity](ctx, tx, "removal_identity", "", step.ProviderOperationID.String(), managed.MaxAttemptPointer)
	if err != nil {
		return attemptView{}, err
	}

	if owner.SchemaVersion != "1" || owner.Role != "provider" || owner.ID != step.ProviderOperationID || owner.Key != t.Key {
		return attemptView{}, managed.ErrUnavailable
	}

	v, err := readAttempt(ctx, tx, t, managed.AttemptKey{Key: t.Key, AttemptID: owner.AttemptRef.AttemptID, Generation: owner.AttemptRef.Generation})
	if err != nil {
		return v, err
	}

	if v.ref != owner.AttemptRef || v.descriptor.ActionID != step.ActionID || v.descriptor.ProviderOperationID != step.ProviderOperationID {
		return attemptView{}, managed.ErrAuthority
	}

	return v, nil
}

func removalLocalIDAvailable(ctx context.Context, tx ControllerTransaction, operation id.ID) error {
	if tx == nil {
		return nil
	}

	_, err := tx.ReadRecord(ctx, "removal_identity", "", operation.String())
	if controllerAbsent(err) {
		return nil
	}

	if err != nil {
		return err
	}

	return managed.ErrConflict
}
