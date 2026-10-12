package managed

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
)

func continuationDigestFixture() ContinuationRequest {
	q := ContinuationRequest{
		Action:      ActionSubmission{Command: Command{Key: Key{TenantID: "tenant", InstanceID: id.New(id.PrefixInstance)}, ID: id.New(PrefixOperation), Revision: 19, LeaseEpoch: 3, Owner: "controller"}, Kind: ActionContinue, Installation: "installation", Authority: "authority", ApplicationID: "application", OrganizationID: "organization", Actor: AuditActor{Subject: "operator"}, OriginalCommand: []byte(`{"continue":true}`), ExpectedDesiredRevision: 2, OccurredAt: time.Date(2026, 10, 12, 3, 0, 0, 1, time.UTC)},
		Predecessor: AttemptRef{AttemptID: id.New(PrefixOperation), Generation: "1", DescriptorDigest: strings.Repeat("a", 64)}, SuccessorAttemptID: id.New(PrefixOperation), ProviderOperationID: id.New(PrefixOperation),
	}
	q.Action.OriginalDigest = Digest(q.Action.OriginalCommand)

	return q
}

func TestRemovalContinuationDigest(t *testing.T) {
	q := continuationDigestFixture()

	digest, err := RemovalContinuationDigest(q)
	if err != nil || len(digest) != 64 {
		t.Fatalf("empty advertised slot: %q %v", digest, err)
	}

	q.Action.CanonicalDigest = digest

	again, err := RemovalContinuationDigest(q)
	if err != nil || again != digest {
		t.Fatalf("self-reference: %q %v", again, err)
	}

	ordinary, err := ActionDigest(q.Action)
	if err != nil || ordinary == digest {
		t.Fatalf("ordinary action digest granted typed authority: %v", err)
	}

	for name, change := range map[string]func(*ContinuationRequest){
		"actor":      func(v *ContinuationRequest) { v.Action.Actor.Subject = "other" },
		"generation": func(v *ContinuationRequest) { v.Predecessor.Generation = "2" },
		"provider":   func(v *ContinuationRequest) { v.ProviderOperationID = id.New(PrefixOperation) },
		"command": func(v *ContinuationRequest) {
			v.Action.OriginalCommand = []byte("other")
			v.Action.OriginalDigest = Digest(v.Action.OriginalCommand)
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := q
			change(&changed)

			got, e := RemovalContinuationDigest(changed)
			if e != nil || got == digest {
				t.Fatalf("unbound input: %q %v", got, e)
			}
		})
	}

	for name, change := range map[string]func(*ContinuationRequest){
		"alias":           func(v *ContinuationRequest) { v.ProviderOperationID = v.SuccessorAttemptID },
		"overflow":        func(v *ContinuationRequest) { v.Predecessor.Generation = "9223372036854775807" },
		"parent_overflow": func(v *ContinuationRequest) { v.Action.Revision = math.MaxUint64 },
		"new_target":      func(v *ContinuationRequest) { v.Action.NewTarget = &Identity{} },
		"release":         func(v *ContinuationRequest) { v.Action.ReleaseID = id.New(id.PrefixRelease) },
		"raw_hash":        func(v *ContinuationRequest) { v.Action.OriginalDigest = strings.Repeat("0", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := q
			change(&changed)

			if _, e := RemovalContinuationDigest(changed); !errors.Is(e, ErrInvalid) {
				t.Fatalf("invalid accepted: %v", e)
			}
		})
	}
}

func TestRemovalContinuationCodecBounds(t *testing.T) {
	q := continuationDigestFixture()

	var err error

	q.Action.CanonicalDigest, err = RemovalContinuationDigest(q)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(raw), `"revision":"19"`) || !strings.Contains(string(raw), `"expected_desired_revision":"2"`) {
		t.Fatalf("numeric counters: %s", raw)
	}

	var parsed ContinuationRequest
	if err = json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{
		strings.Replace(string(raw), `"revision":"19"`, `"revision":19`, 1),
		strings.Replace(string(raw), `"revision":"19"`, `"revision":"019"`, 1),
		strings.Replace(string(raw), `"action":{`, `"action":{"unknown":true,`, 1),
		strings.Replace(string(raw), `"action":{`, `"action":{"new_target":null,`, 1),
		string(raw) + " ",
	} {
		if e := parsed.UnmarshalJSON([]byte(bad)); e == nil {
			t.Fatalf("noncanonical accepted: %s", bad)
		}
	}
}

func TestRemovalContinuationMaximumCodecs(t *testing.T) {
	q := continuationDigestFixture()
	token := strings.Repeat("x", 256)
	q.Action.TenantID = token
	q.Action.Owner = token
	q.Action.Installation = token
	q.Action.Authority = token
	q.Action.ApplicationID = token
	q.Action.OrganizationID = token
	q.Action.Actor.Subject = token
	q.Action.OriginalCommand = bytes.Repeat([]byte{0xff}, 4096)
	q.Action.OriginalDigest = Digest(q.Action.OriginalCommand)
	q.Action.Revision = math.MaxInt64 - 1000
	q.Action.LeaseEpoch = math.MaxInt64
	q.Action.ExpectedDesiredRevision = math.MaxInt64
	q.Predecessor.Generation = "9223372036854775806"
	q.Action.OccurredAt = time.Date(9999, 12, 31, 23, 59, 56, 999999999, time.UTC)

	var err error

	q.Action.CanonicalDigest, err = RemovalContinuationDigest(q)
	if err != nil {
		t.Fatal(err)
	}

	successor := AttemptRef{AttemptID: q.SuccessorAttemptID, Generation: "9223372036854775807", DescriptorDigest: strings.Repeat("b", 64)}
	root := AttemptRef{AttemptID: id.New(PrefixOperation), Generation: "1", DescriptorDigest: strings.Repeat("c", 64)}
	first := AttemptRef{AttemptID: id.New(PrefixOperation), Generation: "2", DescriptorDigest: strings.Repeat("d", 64)}
	material := ContinuationMaterial{"1", q, successor.Generation, q.Action.CanonicalDigest}
	lineage := ContinuationLineage{"1", root, id.New(PrefixOperation), OperationKey{Key: q.Action.Key, ActionID: id.New(PrefixOperation)}, id.New(PrefixOperation), strings.Repeat("e", 64), first}
	closure := ContinuationClosure{SchemaVersion: "1", Key: q.Action.Key, ActionID: q.Action.ID, MaterialDigest: material.Digest, Predecessor: q.Predecessor, Successor: successor, ProviderOperationID: q.ProviderOperationID, Lineage: lineage, Disposition: SettledWithoutDeletion, RevocationDigest: strings.Repeat("f", 64), SettlementDigest: strings.Repeat("a", 64), CoverageDigest: strings.Repeat("b", 64), PredecessorRevision: "9223372036854775807", PointerRevision: successor.Generation, ParentRevision: Decimal(strconv.FormatUint(q.Action.Revision+1, 10)), AcceptedAt: q.Action.OccurredAt.Add(time.Second)}
	receipt := ContinuationReceipt{Receipt: Receipt{ID: q.Action.ID, Digest: material.Digest, Revision: q.Action.Revision + 1, LeaseEpoch: q.Action.LeaseEpoch, AcceptedAt: closure.AcceptedAt, LeaseUntil: closure.AcceptedAt.Add(time.Second)}, ActionReceipt: ActionReceipt{ID: q.Action.ID, Key: q.Action.Key, Target: q.Action.Key, CanonicalDigest: material.Digest, OriginalDigest: q.Action.OriginalDigest, Revision: q.Action.Revision + 1, DesiredRevision: q.Action.ExpectedDesiredRevision, AuditID: q.Action.ID, AcceptedAt: closure.AcceptedAt}, Successor: successor, ProviderOperationID: q.ProviderOperationID}
	association := ContinuationAssociation{q.Action.ID, material.Digest, q.Predecessor, successor, q.ProviderOperationID}
	binding := AttemptBindingKey{AttemptKey: AttemptKey{Key: q.Action.Key, AttemptID: successor.AttemptID, Generation: successor.Generation}, ReservationID: id.New(PrefixReservation)}
	reference := AttemptBindingReference{ReservationID: binding.ReservationID, ReservationVersion: "9223372036854775807", OriginalRegistrationOperationID: id.New(PrefixOperation), OriginalRegistrationDigest: strings.Repeat("a", 64), ProcessDigest: strings.Repeat("b", 64)}
	observation := AttemptCurrentBindingObservation{SchemaVersion: "1", Key: binding, DescriptorDigest: successor.DescriptorDigest, BindingReference: reference, BindingVersion: "9223372036854775806", RemovalEpoch: "9223372036854775806", State: "active", ObservedAt: q.Action.OccurredAt, ValidUntil: q.Action.OccurredAt.Add(time.Second), VerifierID: token, Evidence: bytes.Repeat([]byte{0xff}, 4096)}
	observation.EvidenceDigest = Digest(observation.Evidence)
	native := PortCommand{SchemaVersion: "1", OperationID: id.New(PrefixOperation), RequestID: token, Original: bytes.Repeat([]byte{0xff}, 4096), Native: bytes.Repeat([]byte{0xff}, 4096)}
	native.OriginalDigest = Digest(native.Original)
	native.NativeDigest = Digest(native.Native)
	command := AttemptCommandRequest{AttemptRequest: AttemptRequest{Command: q.Action.Command, AttemptID: successor.AttemptID, Generation: successor.Generation}, ReservationID: binding.ReservationID, Kind: AttemptBegin, ExpectedNativeVersion: observation.BindingVersion, NativeCommand: native, CurrentBinding: &observation}

	for _, entry := range []struct {
		name  string
		value any
		cap   int
	}{{"request", q, MaxRemovalContinuationRequest}, {"material", material, MaxRemovalContinuationMaterial}, {"lineage", lineage, MaxRemovalContinuationClosure}, {"closure", closure, MaxRemovalContinuationClosure}, {"association", association, MaxRemovalContinuationAssociation}, {"receipt", receipt, MaxRemovalContinuationReceipt}, {"reader", ContinuationRecord{material, closure, receipt}, MaxRemovalContinuationRecord}, {"current_binding", observation, MaxAttemptCurrentBinding}, {"successor_command", command, MaxSuccessorCommandRequest}} {
		raw, e := json.Marshal(entry.value)
		if e != nil || len(raw) > entry.cap {
			t.Fatalf("%s encoded %d cap %d: %v", entry.name, len(raw), entry.cap, e)
		}

		t.Logf("schema=%s encoded_bytes=%d cap=%d", entry.name, len(raw), entry.cap)
	}
}

func TestRemovalContinuationDigestGolden(t *testing.T) {
	q := continuationDigestFixture()
	q.Action.InstanceID = id.MustParse("inst_00000000000000000000000001")
	q.Action.ID = id.MustParse("mop_00000000000000000000000001")
	q.Predecessor.AttemptID = id.MustParse("mop_00000000000000000000000002")
	q.SuccessorAttemptID = id.MustParse("mop_00000000000000000000000003")
	q.ProviderOperationID = id.MustParse("mop_00000000000000000000000004")

	digest, err := RemovalContinuationDigest(q)
	if err != nil || digest != "14de44beba092c97591fc146efc9c7626aa85c9ac20d577b62a328f0aaa7e87e" {
		t.Fatalf("golden digest %s: %v", digest, err)
	}
}
