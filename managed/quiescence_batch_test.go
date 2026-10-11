package managed

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
)

func maximalBatchRequest(t *testing.T) QuiescenceBatchAcceptanceRequest {
	t.Helper()

	token := strings.Repeat("x", 256)
	hash := strings.Repeat("a", 64)
	maximum := Decimal(strconv.FormatInt(math.MaxInt64, 10))
	stamp := time.Date(9999, 12, 31, 23, 58, 59, 999999999, time.UTC)
	p := OriginalProcessFacts{Installation: token, Namespace: token, Build: token, Queue: token, RuntimeID: token, InstanceID: token, HostID: token, IdentityVersion: "1", ArtifactDigest: hash, ConfigurationDigest: hash, ConfigurationVersion: token, EnrollmentEvidenceDigest: hash, ProbePolicyID: token, ProbePolicyVersion: maximum, MaximumProofValidityNS: maximum, VerifierID: token, Descriptor: StartupBindingRef{BindingID: token, BindingGeneration: maximum, BindingDigest: hash}, ProducerVerifier: token}

	q := QuiescenceBatchAcceptanceRequest{Schema: QuiescenceBatchRequestSchema, Command: QuiescenceBatchCommand{Key: Key{TenantID: token, InstanceID: id.New(id.PrefixInstance)}, ID: id.New(PrefixOperation), Revision: Decimal(strconv.FormatInt(math.MaxInt64-65, 10)), LeaseEpoch: maximum, Owner: token}, Attempt: AttemptRef{AttemptID: id.New(PrefixOperation), Generation: "1", DescriptorDigest: hash}}
	for range 64 {
		ref := QuiescenceReference{Schema: "ctrlplane.quiescence-reference.v1", TargetIdentityDigest: hash, ReservationID: id.New(PrefixReservation), Member: token, Role: RetainedQuery, RegistrationOperationID: id.New(PrefixOperation), RegistrationReferenceDigest: hash, RegistrationMaterialDigest: hash, DrainOperationID: id.New(PrefixOperation), DrainReferenceDigest: hash, DrainMaterialDigest: hash, Process: p, OperationID: token, Deadline: stamp}
		ref.HandleDigest, _ = OriginalDrainHandleDigest(hash, OriginalDrainFacts{Process: p, OperationID: token, Deadline: stamp})
		o := QuiescenceObservation{QuiescenceLeaf: QuiescenceLeaf{Reference: ref, ObservedAt: stamp, ValidUntil: stamp.Add(time.Minute), AdmissionClosed: true, Quiescent: true, Complete: false, DeadlineExpired: true, ErrorClass: "incomplete", ActiveClaims: "0", UnknownClaims: "0", ObservationID: token, ChallengeID: token, VerifierID: token}, Attestation: bytes.Repeat([]byte{255}, MaxQuiescenceAttestation)}
		o.ChallengeDigest, _ = QuiescenceChallengeDigest(o.QuiescenceLeaf)
		q.Entries = append(q.Entries, QuiescenceBatchEntry{OperationID: id.New(PrefixOperation), Observation: o})
	}

	return q
}
func TestQuiescenceBatchCodecBounds(t *testing.T) {
	q := maximalBatchRequest(t)

	raw, err := q.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := ParseQuiescenceBatchAcceptanceRequest(raw)
	if err != nil || !reflect.DeepEqual(q, parsed) {
		t.Fatal("maximum canonical request", err)
	}

	framed, err := json.Marshal(struct {
		Kind  string                           `json:"kind"`
		Input QuiescenceBatchAcceptanceRequest `json:"input"`
	}{QuiescenceBatchHistory, q})
	if err != nil {
		t.Fatal(err)
	}

	entry, err := json.Marshal(q.Entries[0])
	if err != nil {
		t.Fatal(err)
	}

	if len(framed) > MaxQuiescenceBatchInput || len(entry) > MaxQuiescenceBatchEntry {
		t.Fatal("actual containing cap mismatch")
	}

	receipt := QuiescenceBatchReceipt{Schema: QuiescenceBatchReceiptSchema, Key: q.Command.Key, BatchID: q.Command.ID, Attempt: q.Attempt, Count: "64", Receipt: OriginalLocalReceipt{ID: q.Command.ID, Digest: strings.Repeat("a", 64), Revision: Decimal(strconv.FormatInt(math.MaxInt64, 10)), LeaseEpoch: q.Command.LeaseEpoch, AcceptedAt: q.Entries[0].Observation.ObservedAt.Add(time.Nanosecond), LeaseUntil: q.Entries[0].Observation.ValidUntil}}

	encoded, err := receipt.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	if round, e := ParseQuiescenceBatchReceipt(encoded); e != nil || round != receipt {
		t.Fatal("maximum result", e)
	}

	t.Logf("actual joint maximum64 request=%d framed=%d entry=%d public_receipt=%d raw_attestation_total=%d", len(raw), len(framed), len(entry), len(encoded), 64*MaxQuiescenceAttestation)

	owned, err := q.Normalize()
	if err != nil {
		t.Fatal(err)
	}

	owned.Entries[0].Observation.Attestation[0] = 0
	if q.Entries[0].Observation.Attestation[0] != 255 {
		t.Fatal("aliased request bytes")
	}

	for name, mutate := range map[string]func(*QuiescenceBatchAcceptanceRequest){"65": func(q *QuiescenceBatchAcceptanceRequest) { q.Entries = append(q.Entries, q.Entries[0]) }, "empty": func(q *QuiescenceBatchAcceptanceRequest) { q.Entries = nil }, "oversized-attestation": func(q *QuiescenceBatchAcceptanceRequest) { q.Entries[0].Observation.Attestation = make([]byte, 4097) }, "counter-overflow": func(q *QuiescenceBatchAcceptanceRequest) {
		q.Command.Revision = Decimal(strconv.FormatInt(math.MaxInt64-64, 10))
	}, "duplicate": func(q *QuiescenceBatchAcceptanceRequest) { q.Entries[1].OperationID = q.Entries[0].OperationID }, "root-child": func(q *QuiescenceBatchAcceptanceRequest) { q.Entries[0].OperationID = q.Command.ID }} {
		t.Run(name, func(t *testing.T) {
			changed := maximalBatchRequest(t)
			mutate(&changed)

			if _, e := changed.Bytes(); e == nil {
				t.Fatal("invalid batch accepted")
			}
		})
	}

	for _, bad := range [][]byte{bytes.Replace(raw, []byte(`"revision":"`+string(q.Command.Revision)+`"`), []byte(`"revision":`+string(q.Command.Revision)), 1), bytes.Replace(raw, []byte(`"generation":"1"`), []byte(`"generation":"01"`), 1), bytes.Replace(raw, []byte(`"active_claims":"0"`), []byte(`"active_claims":0`), 1), bytes.Replace(raw, []byte(`"entries":[`), []byte(`"entries":null,"entries":[`), 1), append(append([]byte{}, raw...), []byte("{}")...)} {
		if _, e := ParseQuiescenceBatchAcceptanceRequest(bad); e == nil {
			t.Fatal("invalid recursive codec accepted")
		}
	}
}
