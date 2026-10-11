package managedstate

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

func TestQuiescenceBatchInternalBounds(t *testing.T) {
	hash := strings.Repeat("a", 64)
	key := managed.Key{TenantID: strings.Repeat("x", 256), InstanceID: id.New(id.PrefixInstance)}
	operation := id.New(managed.PrefixOperation)
	stamp := time.Date(9999, 12, 31, 23, 59, 58, 999999999, time.UTC)

	record := quiescenceBatchRecord{Schema: managed.QuiescenceBatchRecordSchema, Command: managed.QuiescenceBatchCommand{Key: key, ID: operation, Revision: managed.Decimal(strconv.FormatInt(math.MaxInt64-65, 10)), LeaseEpoch: managed.Decimal(strconv.FormatInt(math.MaxInt64, 10)), Owner: strings.Repeat("x", 256)}, Attempt: managed.AttemptRef{AttemptID: id.New(managed.PrefixOperation), Generation: "1", DescriptorDigest: hash}, Receipt: managed.OriginalReceipt(managed.Receipt{ID: operation, Digest: hash, Revision: math.MaxInt64, LeaseEpoch: math.MaxInt64, AcceptedAt: stamp, LeaseUntil: stamp.Add(time.Second)})}
	for range 64 {
		record.Entries = append(record.Entries, quiescenceBatchMember{ReservationID: id.New(managed.PrefixReservation), OperationID: id.New(managed.PrefixOperation), RequestDigest: hash, RecordDigest: hash})
	}

	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}

	if len(raw) > managed.MaxQuiescenceBatchRecord {
		t.Fatal("manifest cap mismatch", len(raw))
	}

	if _, err = attemptDecode[quiescenceBatchRecord](raw, managed.MaxQuiescenceBatchRecord); err != nil {
		t.Fatal(err)
	}

	receipt, err := json.Marshal(managed.Receipt{ID: operation, Digest: hash, Revision: math.MaxInt64, LeaseEpoch: math.MaxInt64, AcceptedAt: stamp, LeaseUntil: stamp.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}

	if len(receipt) > managed.MaxAttemptReceipt {
		t.Fatal("A receipt cap")
	}

	t.Logf("actual aggregate64 manifest=%d six-field A receipt=%d", len(raw), len(receipt))
}
func TestQuiescenceBatchReadOnlyAndCandidate(t *testing.T) {
	v := &attemptCheckedReads{}
	if _, err := v.ApplyQuiescenceObservationBatch(t.Context(), managed.QuiescenceBatchAcceptanceRequest{}); !errors.Is(err, managed.ErrBlocked) {
		t.Fatal("readonly batch mutation", err)
	}

	for _, c := range []*QuiescenceBatchCandidate{nil, {}} {
		if _, err := c.StageTarget(&managed.Target{}); err == nil {
			t.Fatal("zero candidate")
		}

		if _, err := c.Take(managed.Key{}, id.ID{}, 1); err == nil {
			t.Fatal("zero take")
		}
	}
}

func TestQuiescenceBatchCandidateBinding(t *testing.T) {
	stamp := time.Now().UTC()
	key := managed.Key{TenantID: "tenant", InstanceID: id.New(id.PrefixInstance)}
	batch := id.New(managed.PrefixOperation)
	child := id.New(managed.PrefixOperation)
	receipt := managed.Receipt{ID: batch, Digest: strings.Repeat("a", 64), Revision: 12, LeaseEpoch: 2, AcceptedAt: stamp, LeaseUntil: stamp.Add(time.Minute)}
	makeCandidate := func() *QuiescenceBatchCandidate {
		cr := receipt
		cr.ID = child
		cr.Revision = 11

		return &QuiescenceBatchCandidate{ready: true, key: key, batchID: batch, baseRevision: 10, finalRevision: 12, owner: "owner", epoch: 2, leaseUntil: receipt.LeaseUntil, stage: QuiescenceBatchStage{Scope: "scope", Manifest: []byte("manifest"), Receipt: receipt, Children: []QuiescenceBatchChildStage{{Scope: "child", OperationID: child, History: []byte("history"), Current: []byte("current"), Receipt: cr}}}}
	}

	for _, kind := range []string{"tenant", "target", "batch", "revision"} {
		t.Run(kind, func(t *testing.T) {
			c := makeCandidate()
			k, b, r := key, batch, uint64(10)

			switch kind {
			case "tenant":
				k.TenantID = "other"
			case "target":
				k.InstanceID = id.New(id.PrefixInstance)
			case "batch":
				b = id.New(managed.PrefixOperation)
			case "revision":
				r++
			}

			if _, err := c.Take(k, b, r); !errors.Is(err, managed.ErrAuthority) {
				t.Fatal("foreign candidate", err)
			}

			if c.used {
				t.Fatal("failed take consumed candidate")
			}
		})
	}

	for _, kind := range []string{"revision", "owner", "epoch", "deadline"} {
		t.Run(kind, func(t *testing.T) {
			c := makeCandidate()
			target := &managed.Target{Identity: managed.Identity{Key: key}, Revision: 10, LeaseOwner: "owner", LeaseEpoch: 2, LeaseUntil: receipt.LeaseUntil}

			switch kind {
			case "revision":
				target.Revision++
			case "owner":
				target.LeaseOwner = "other"
			case "epoch":
				target.LeaseEpoch++
			case "deadline":
				target.LeaseUntil = target.LeaseUntil.Add(time.Second)
			}

			if _, err := c.StageTarget(target); !errors.Is(err, managed.ErrConflict) {
				t.Fatal("stale candidate", err)
			}
		})
	}

	c := makeCandidate()
	target := &managed.Target{Identity: managed.Identity{Key: key}, Revision: 10, LeaseOwner: "owner", LeaseEpoch: 2, LeaseUntil: receipt.LeaseUntil}

	next, err := c.StageTarget(target)
	if err != nil || next.Revision != 12 || target.Revision != 10 {
		t.Fatal("candidate target ownership", err)
	}

	stage, err := c.Take(key, batch, 10)
	if err != nil {
		t.Fatal(err)
	}

	stage.Manifest[0] = 'X'
	stage.Children[0].History[0] = 'X'

	stage.Children[0].Current[0] = 'X'
	if string(c.stage.Manifest) != "manifest" || string(c.stage.Children[0].History) != "history" || string(c.stage.Children[0].Current) != "current" {
		t.Fatal("candidate bytes aliased")
	}

	if _, err = c.Take(key, batch, 10); !errors.Is(err, managed.ErrAuthority) {
		t.Fatal("candidate reused", err)
	}

	if _, err = c.StageTarget(target); !errors.Is(err, managed.ErrConflict) {
		t.Fatal("used candidate retargeted", err)
	}
}
