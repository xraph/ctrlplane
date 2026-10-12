// Package continuationtest builds the bounded synthetic rows used by continuation tests.
package continuationtest

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/managedservice"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

// Row is one canonical record in the owned fixture.
type Row struct {
	Kind, Scope, Key string
	Data             []byte
}

// Storage supplies only the owned test fixture's rows and local receipts.
type Storage struct {
	Rows    func() []Row
	Put     func(Row)
	Receipt func(managed.Receipt)
}

func encode(t *testing.T, v any) []byte {
	t.Helper()

	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	return raw
}
func decode[T any](t *testing.T, raw []byte) T {
	t.Helper()

	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}

	return out
}
func hash(t *testing.T, domain string, v any) string {
	t.Helper()

	return managed.Digest(append([]byte(domain+"\n"), encode(t, v)...))
}
func fact(t *testing.T, kind string, v any) string {
	t.Helper()

	return hash(t, "ctrlplane.managed.attempt-fact.v1", struct {
		Kind  string `json:"kind"`
		Value any    `json:"value"`
	}{kind, v})
}
func row(rows []Row, kind, key string) int {
	for i, r := range rows {
		if r.Kind == kind && r.Key == key {
			return i
		}
	}

	return -1
}
func changeAll(rows []Row, from, to []byte) {
	for i := range rows {
		rows[i].Data = bytes.ReplaceAll(rows[i].Data, from, to)
	}
}
func refBody(t *testing.T, r managed.AttemptRef) []byte {
	raw := encode(t, r)

	return raw[1 : len(raw)-1]
}

// LateGeneration seeds only a root/first/current/immediate proof slice. The
// omitted lifetime ancestry is an inductive fixture premise, not measured history.
// Generation and pointer revision remain coupled. No live counter is reset later.
func LateGeneration(t *testing.T, s Storage, key managed.AttemptKey, target *managed.Target) managed.AttemptKey {
	t.Helper()

	rows := s.Rows()

	ci := row(rows, "removal_attempt", key.AttemptID.String())
	if ci < 0 {
		t.Fatal("missing current")
	}

	current := decode[managed.AttemptDescriptor](t, rows[ci].Data)
	if current.Predecessor == nil {
		t.Fatal("requires later generation")
	}

	pi := row(rows, "removal_attempt", current.Predecessor.AttemptID.String())
	if pi < 0 {
		t.Fatal("missing predecessor")
	}

	previous := decode[managed.AttemptDescriptor](t, rows[pi].Data)
	oldPrevious := *current.Predecessor
	oldCurrent := managed.AttemptRef{AttemptID: current.AttemptID, Generation: current.Generation, DescriptorDigest: hash(t, "ctrlplane.managed.removal-attempt.v1", current)}
	previous.Generation = managed.Decimal(strconv.FormatInt(math.MaxInt64-2, 10))
	newPrevious := managed.AttemptRef{AttemptID: previous.AttemptID, Generation: previous.Generation, DescriptorDigest: hash(t, "ctrlplane.managed.removal-attempt.v1", previous)}
	current.Generation = managed.Decimal(strconv.FormatInt(math.MaxInt64-1, 10))
	current.Predecessor = &newPrevious
	newCurrent := managed.AttemptRef{AttemptID: current.AttemptID, Generation: current.Generation, DescriptorDigest: hash(t, "ctrlplane.managed.removal-attempt.v1", current)}
	changeAll(rows, refBody(t, oldPrevious), refBody(t, newPrevious))
	changeAll(rows, refBody(t, oldCurrent), refBody(t, newCurrent))
	rows[pi].Data = encode(t, previous)

	rows[ci].Data = encode(t, current)
	for _, ref := range []managed.AttemptRef{newPrevious, newCurrent} {
		i := row(rows, "removal_revocation", ref.AttemptID.String())
		if i < 0 {
			t.Fatal("requires revoked attempts")
		}

		original := s.Rows()
		old := decode[managed.AttemptRevocation](t, original[i].Data)
		updated := decode[managed.AttemptRevocation](t, rows[i].Data)

		descriptor := previous
		if ref.AttemptID == current.AttemptID {
			descriptor = current
		}

		spec, e := managed.ParseProvisionSpec(target.Manifest)
		if e != nil {
			t.Fatal(e)
		}

		type stepCoverage struct {
			Step           managed.ProviderStepKey `json:"step"`
			State          string                  `json:"state"`
			MaterialDigest string                  `json:"material_digest"`
		}

		coverage := []stepCoverage{}
		for _, service := range spec.Services {
			coverage = append(coverage, stepCoverage{Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: key.Key, ActionID: descriptor.ActionID}, ProviderOperationID: descriptor.ProviderOperationID, Kind: managed.StepRemoveContainer, Service: service.Name}, State: "unissued"})
		}

		coverage = append(coverage, stepCoverage{Step: managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: key.Key, ActionID: descriptor.ActionID}, ProviderOperationID: descriptor.ProviderOperationID, Kind: managed.StepRemoveNetwork}, State: "unissued"})
		updated.CoverageDigest = hash(t, "ctrlplane.managed.attempt-coverage.v1", struct {
			Attempt      managed.AttemptRef `json:"attempt"`
			Reservations []id.ID            `json:"reservations"`
			Steps        []stepCoverage     `json:"steps"`
		}{ref, descriptor.ReservationIDs, coverage})
		changeAll(rows, encode(t, old.CoverageDigest), encode(t, updated.CoverageDigest))
		rows[i].Data = encode(t, updated)

		changeAll(rows, encode(t, fact(t, "removal_revocation", old)), encode(t, fact(t, "removal_revocation", updated)))
	}

	mi := row(rows, "removal_continuation", current.ActionID.String())
	m := decode[managed.ContinuationMaterial](t, s.Rows()[mi].Data)
	oldDigest := m.Digest
	m.Request.Predecessor = newPrevious
	m.Request.Action.CanonicalDigest = ""

	var err error

	m.Digest, err = managed.RemovalContinuationDigest(m.Request)
	if err != nil {
		t.Fatal(err)
	}

	m.Request.Action.CanonicalDigest = m.Digest
	m.SuccessorGeneration = current.Generation
	changeAll(rows, encode(t, oldDigest), encode(t, m.Digest))
	rows[mi].Data = encode(t, m)
	originalRows := s.Rows()

	for i := range rows {
		if rows[i].Kind == "removal_attempt_closure" {
			oldClosure := decode[managed.ContinuationClosure](t, originalRows[i].Data)
			closure := decode[managed.ContinuationClosure](t, rows[i].Data)
			closure.PointerRevision = closure.Successor.Generation
			rows[i].Data = encode(t, closure)
			changeAll(rows, encode(t, fact(t, "removal_attempt_closure", oldClosure)), encode(t, fact(t, "removal_attempt_closure", closure)))
		}
	}

	pointer := row(rows, "removal_attempt_current", "current")

	ptr := decode[struct {
		SchemaVersion   managed.Decimal    `json:"schema_version"`
		Key             managed.Key        `json:"key"`
		AttemptRef      managed.AttemptRef `json:"attempt_ref"`
		PointerRevision managed.Decimal    `json:"pointer_revision"`
	}](t, rows[pointer].Data)
	ptr.PointerRevision = current.Generation

	rows[pointer].Data = encode(t, ptr)
	for i, r := range rows {
		if !bytes.Equal(r.Data, originalRows[i].Data) {
			t.Logf("late-generation seed kind=%s key=%s before=%s after=%s", r.Kind, r.Key, originalRows[i].Data, r.Data)
		}

		s.Put(r)

		if r.Kind == "removal_attempt_event" {
			var event struct {
				Receipt managed.WireReceipt `json:"receipt"`
			}
			if err = json.Unmarshal(r.Data, &event); err != nil {
				t.Fatal(err)
			}

			receipt, e := event.Receipt.Value()
			if e != nil {
				t.Fatal(e)
			}

			s.Receipt(receipt)
		}
	}

	t.Logf("synthetic bounded generation slice predecessor=%s current=%s pointer=%s; omitted lifetime ancestry not API history", newPrevious.Generation, newCurrent.Generation, current.Generation)

	return managed.AttemptKey{Key: key.Key, AttemptID: key.AttemptID, Generation: newCurrent.Generation}
}

// RemovedRevocation retains an integrity-valid cutoff beside a positive Removed
// settlement. No production operation can currently create this combined shape.
// The existing accepted recovery receipt supplies the fixture's local event ID.
func RemovedRevocation(t *testing.T, s Storage, key managed.AttemptKey, recovery id.ID) {
	t.Helper()

	rows := s.Rows()
	si := row(rows, "removal_attempt_state", key.AttemptID.String())
	state := decode[managed.AttemptState](t, rows[si].Data)

	settled := decode[managed.AttemptSettlement](t, rows[row(rows, "removal_settlement", key.AttemptID.String())].Data)
	if settled.Disposition != managed.Removed || state.RevocationDigest != "" {
		t.Fatal("requires unrevoked Removed fixture")
	}

	ei := row(rows, "removal_attempt_event", recovery.String())

	type eventRow struct {
		SchemaVersion managed.Decimal     `json:"schema_version"`
		AttemptRef    managed.AttemptRef  `json:"attempt_ref"`
		Kind          string              `json:"kind"`
		RequestDigest string              `json:"request_digest"`
		FactKind      string              `json:"fact_kind"`
		FactKey       string              `json:"fact_key"`
		FactDigest    string              `json:"fact_digest"`
		Receipt       managed.WireReceipt `json:"receipt"`
	}

	event := decode[eventRow](t, rows[ei].Data)
	rev := managed.AttemptRevocation{LocalOperationID: recovery, SchemaVersion: "1", AttemptRef: state.AttemptRef, ProviderOperationID: settled.ProviderOperationID, CoverageDigest: settled.CoverageDigest, Disposition: managed.Removed, SettlementDigest: state.SettlementDigest, RevokedAt: event.Receipt.AcceptedAt, ParentRevision: event.Receipt.Revision}
	state.RevocationDigest = fact(t, "removal_revocation", rev)

	revision, err := state.Revision.Uint64()
	if err != nil {
		t.Fatal(err)
	}

	state.Revision = managed.Decimal(strconv.FormatUint(revision+1, 10))
	event.Kind = "attempt_revoke"
	event.FactKind = "removal_revocation"
	event.FactKey = key.AttemptID.String()
	event.FactDigest = state.RevocationDigest
	rows[si].Data = encode(t, state)

	rows[ei].Data = encode(t, event)
	for _, r := range rows {
		s.Put(r)
	}

	s.Put(Row{Kind: "removal_revocation", Scope: rows[si].Scope, Key: key.AttemptID.String(), Data: encode(t, rev)})
	t.Log("seeded retained revocation plus Removed; no production mutation path claimed")
}

// ExpiredFinish qualifies first terminal authorization on the seeded combined
// cutoff/Removed shape, after the original short proofs have actually expired.
func ExpiredFinish(t *testing.T, s managedservice.Authority, rows Storage, key managed.AttemptKey, observations []managed.QuiescenceObservation, clock func() time.Time) {
	t.Helper()

	current, err := s.ReadManagedRemovalAttempt(t.Context(), key)
	if err != nil {
		t.Fatal("terminal fixture setup", err)
	}

	ordered := make([]managed.QuiescenceObservation, 0, len(current.Descriptor.ReservationIDs))
	for _, rid := range current.Descriptor.ReservationIDs {
		found := false

		for _, observation := range observations {
			if observation.Reference.ReservationID == rid {
				ordered = append(ordered, observation)
				found = true

				break
			}
		}

		if !found {
			t.Fatal("missing original observation", rid)
		}
	}

	observations = ordered

	command := func() managed.Command {
		target, e := s.ReadManagedTarget(t.Context(), key.Key)
		if e != nil {
			t.Fatal("terminal fixture native/provider setup", e)
		}

		return managed.Command{Key: key.Key, ID: id.New(managed.PrefixOperation), Revision: target.Revision, LeaseEpoch: target.LeaseEpoch, Owner: target.LeaseOwner}
	}
	request := func() managed.AttemptRequest {
		return managed.AttemptRequest{Command: command(), AttemptID: key.AttemptID, Generation: key.Generation}
	}
	now := clock().UTC()
	deadline := now.Add(3 * time.Second)
	q := managed.QuiescenceBatchAcceptanceRequest{Schema: managed.QuiescenceBatchRequestSchema, Attempt: current.State.AttemptRef}
	cmd := command()
	q.Command = managed.QuiescenceBatchCommand{Key: key.Key, ID: cmd.ID, Revision: managed.Decimal(strconv.FormatUint(cmd.Revision, 10)), LeaseEpoch: managed.Decimal(strconv.FormatUint(cmd.LeaseEpoch, 10)), Owner: cmd.Owner}

	for _, o := range observations {
		o.ObservedAt = now
		o.ValidUntil = deadline

		o.ChallengeDigest, err = managed.QuiescenceChallengeDigest(o.QuiescenceLeaf)
		if err != nil {
			t.Fatal("terminal fixture setup", err)
		}

		q.Entries = append(q.Entries, managed.QuiescenceBatchEntry{OperationID: id.New(managed.PrefixOperation), Observation: o})
	}

	if _, err = s.(managed.QuiescenceBatchStore).AcceptManagedQuiescenceBatch(t.Context(), q); err != nil {
		t.Fatal("terminal fixture batch acceptance", err)
	}

	native := func(rid id.ID, kind string) managed.AttemptCommandRequest {
		raw := []byte("terminal-fixture-" + kind)

		return managed.AttemptCommandRequest{AttemptRequest: request(), ReservationID: rid, Kind: kind, ExpectedNativeVersion: "11", NativeCommand: managed.PortCommand{SchemaVersion: "1", OperationID: id.New(managed.PrefixOperation), RequestID: id.New(managed.PrefixOperation).String(), Original: raw, OriginalDigest: managed.Digest(raw), Native: raw, NativeDigest: managed.Digest(raw)}}
	}
	acceptance := func(c managed.AttemptCommandRecord) managed.AttemptNativeAcceptance {
		raw := []byte("accepted")

		return managed.AttemptNativeAcceptance{NativeAcceptance: managed.AttemptNativeFacts{RequestID: c.NativeCommand.RequestID, RequestDigest: c.NativeCommand.NativeDigest, CommandDigest: managed.Digest(raw), ResponseVersion: "1", BindingVersion: "11", RemovalEpoch: "5", AcceptedAt: clock().UTC()}, NativeFenceDigest: managed.Digest([]byte("fence")), ReceiptID: "receipt", Receipt: raw, ReceiptDigest: managed.Digest(raw), OriginalBindingProof: raw, ProofDigest: managed.Digest(raw)}
	}

	var first managed.AttemptCommandRecord

	begins := map[id.ID]managed.AttemptCommandRequest{}

	for i, rid := range current.Descriptor.ReservationIDs {
		ref := observations[i].Reference

		reg, e := s.ReadManagedOriginalLifecycle(t.Context(), managed.OriginalLifecycleKey{Key: key.Key, Kind: managed.OriginalRegistration, OperationID: ref.RegistrationOperationID})
		if e != nil {
			t.Fatal("terminal fixture native/provider setup", e)
		}

		validity, validityErr := strconv.ParseInt(string(reg.Material.Registration.Process.MaximumProofValidityNS), 10, 64)
		if validityErr != nil || validity <= 0 {
			t.Fatal("invalid original validity", validityErr)
		}

		bound := time.Duration(validity)

		sourceDeadline := now.Add(bound)
		if sourceDeadline.After(deadline) {
			sourceDeadline = deadline
		}

		proof := []byte("current binding")
		n := native(rid, managed.AttemptBegin)
		n.ExpectedNativeVersion = "10"
		n.CurrentBinding = &managed.AttemptCurrentBindingObservation{SchemaVersion: "1", Key: managed.AttemptBindingKey{AttemptKey: key, ReservationID: rid}, DescriptorDigest: current.State.AttemptRef.DescriptorDigest, BindingReference: managed.AttemptBindingReference{ReservationID: rid, ReservationVersion: reg.Material.Registration.BindingVersion, OriginalRegistrationOperationID: ref.RegistrationOperationID, OriginalRegistrationDigest: ref.RegistrationMaterialDigest, ProcessDigest: fact(t, "original_process", reg.Material.Registration.Process)}, BindingVersion: "10", RemovalEpoch: "4", State: "active", ObservedAt: now, ValidUntil: sourceDeadline, VerifierID: current.Descriptor.VerifierID, Evidence: proof, EvidenceDigest: managed.Digest(proof)}
		begins[rid] = n

		grant, e := s.AuthorizeManagedAttemptCommand(t.Context(), n)
		if e != nil {
			t.Fatal("terminal fixture Begin binding", rid, reg.Material.Command.ReservationID, e)
		}

		first = grant.Command
		if _, e = s.RecordManagedAttemptFence(t.Context(), managed.AttemptFenceRequest{AttemptRequest: request(), ReservationID: rid, Acceptance: acceptance(grant.Command), Fence: managed.FenceWire(managed.Fence{CompatibilityEvidence: []byte("retired"), Epoch: 1, Evidence: proof, Digest: managed.Digest(proof), ValidUntil: deadline, Survivors: []string{"off-instance"}})}); e != nil {
			t.Fatal("terminal fixture native/provider setup", e)
		}
	}

	if _, err = s.AdmitManagedRemovalAttempt(t.Context(), managed.AttemptAdmissionRequest{AttemptRequest: request(), VerifierID: current.Descriptor.VerifierID}); err != nil {
		t.Fatal("terminal fixture setup", err)
	}

	target, err := s.ReadManagedTarget(t.Context(), key.Key)
	if err != nil {
		t.Fatal("terminal fixture setup", err)
	}

	spec, err := managed.ParseProvisionSpec(target.Manifest)
	if err != nil {
		t.Fatal("terminal fixture setup", err)
	}

	steps := make([]managed.ProviderStepKey, 0, len(spec.Services)+1)
	for _, service := range spec.Services {
		steps = append(steps, managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: key.Key, ActionID: current.Descriptor.ActionID}, ProviderOperationID: current.Descriptor.ProviderOperationID, Kind: managed.StepRemoveContainer, Service: service.Name})
	}

	steps = append(steps, managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: key.Key, ActionID: current.Descriptor.ActionID}, ProviderOperationID: current.Descriptor.ProviderOperationID, Kind: managed.StepRemoveNetwork})
	for _, step := range steps {
		grant, e := s.AuthorizeManagedProviderStep(t.Context(), managed.ProviderStepAuthorization{Command: command(), Step: step})
		if e != nil {
			t.Fatal("terminal fixture native/provider setup", e)
		}

		if e = s.RecordManagedProviderStepResult(t.Context(), managed.ProviderStepResult{Permission: grant.Permission, Outcome: managed.PortTerminal, ResourceID: grant.Permission.ResourceID, Evidence: []byte("positive"), ObservedAt: clock().UTC()}); e != nil {
			t.Fatal("terminal fixture native/provider setup", e)
		}
	}

	if _, err = s.SettleManagedRemovalAttempt(t.Context(), managed.AttemptSettlementRequest{AttemptRequest: request(), Disposition: managed.Removed, ProducerProfile: "ctrlplane.managed.remove.v1", VerifierID: current.Descriptor.VerifierID, Evidence: []byte("complete"), SettledAt: clock().UTC()}); err != nil {
		t.Fatal("terminal fixture setup", err)
	}

	recovery := managed.AttemptRecoveryRequest{AttemptRequest: request(), ReservationID: first.Key.ReservationID, Kind: first.Key.Kind, CommandDigest: hash(t, "ctrlplane.managed.attempt-command.v1", first)}
	if _, err = s.AuthorizeManagedAttemptRecovery(t.Context(), recovery); err != nil {
		t.Fatal("terminal fixture setup", err)
	}

	RemovedRevocation(t, rows, key, recovery.ID)

	if _, err = s.ReadManagedRemovalAttempt(t.Context(), key); err != nil {
		t.Fatal("combined seeded integrity", err)
	}

	timer := time.NewTimer(deadline.Add(20 * time.Millisecond).Sub(clock()))
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}

	if !clock().After(deadline) {
		t.Fatal("proof not expired")
	}

	for _, rid := range current.Descriptor.ReservationIDs {
		deny := begins[rid]
		deny.AttemptRequest = request()

		deny.NativeCommand.OperationID = id.New(managed.PrefixOperation)
		if _, err = s.AuthorizeManagedAttemptCommand(t.Context(), deny); !errors.Is(err, managed.ErrBlocked) {
			t.Fatal("fresh Begin after cutoff")
		}
	}

	for _, step := range steps {
		if _, err = s.AuthorizeManagedProviderStep(t.Context(), managed.ProviderStepAuthorization{Command: command(), Step: step}); err == nil {
			t.Fatal("fresh provider after cutoff")
		}
	}

	for _, rid := range current.Descriptor.ReservationIDs {
		grant, e := s.AuthorizeManagedAttemptCommand(t.Context(), native(rid, managed.AttemptFinish))
		if e != nil || !grant.NewlyIssued {
			t.Fatal("first Finish after expiry", e)
		}

		if _, e = s.RecordManagedAttemptFinish(t.Context(), managed.AttemptFinishRequest{AttemptRequest: request(), ReservationID: rid, Acceptance: acceptance(grant.Command)}); e != nil {
			t.Fatal("terminal fixture native/provider setup", e)
		}
	}

	if _, err = s.TombstoneManagedRemovalAttempt(t.Context(), request()); err != nil {
		t.Fatal("tombstone after expiry", err)
	}

	target, err = s.ReadManagedTarget(t.Context(), key.Key)
	if err != nil || target.Phase != managed.Deleted {
		t.Fatal("terminal phase", err)
	}

	if _, err = s.AdmitManagedRemovalAttempt(t.Context(), managed.AttemptAdmissionRequest{AttemptRequest: request(), VerifierID: current.Descriptor.VerifierID}); !errors.Is(err, managed.ErrBlocked) && !errors.Is(err, managed.ErrAuthority) {
		t.Fatal("terminal admission", err)
	}

	t.Logf("first Finish and tombstone passed after actual native/quiescence/fence expiry %s with retained revocation and Removed", deadline.Format(time.RFC3339Nano))
}
