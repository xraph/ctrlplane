package managedstate

import (
	"testing"
	"time"

	"github.com/xraph/ctrlplane/managed"
)

func TestAttemptFenceFactDigestLegacyParity(t *testing.T) {
	for _, receipt := range [][]byte{nil, {}, []byte("original native receipt")} {
		fence := managed.AttemptFenceRecord{
			SchemaVersion: "1", DescriptorDigest: "descriptor", CommandDigest: "command",
			AttemptNativeAcceptance: managed.AttemptNativeAcceptance{
				NativeAcceptance:  managed.AttemptNativeFacts{RequestID: "native-request", RequestDigest: "request-digest", AcceptedAt: time.Date(2026, time.October, 14, 1, 2, 3, 456, time.FixedZone("offset", -5*60*60))},
				NativeFenceDigest: "native-fence", ReceiptID: "receipt", Receipt: receipt, ReceiptDigest: "receipt-digest", OriginalBindingProof: []byte("original proof"), ProofDigest: "proof-digest",
			},
		}
		// This is the pre-helper encoding, independent of attemptFact's delegation.
		want, err := attemptHash("ctrlplane.managed.attempt-fact.v1", struct {
			Kind  string `json:"kind"`
			Value any    `json:"value"`
		}{"removal_fence", fence})
		if err != nil {
			t.Fatal(err)
		}

		got, err := managed.AttemptFenceFactDigest(fence)
		if err != nil || got != want {
			t.Fatalf("public digest=%q want=%q err=%v", got, want, err)
		}

		internal, err := attemptFact("removal_fence", fence)
		if err != nil || internal != want {
			t.Fatalf("internal digest=%q want=%q err=%v", internal, want, err)
		}

		otherKind, err := attemptFact("other", fence)
		if err != nil || otherKind == got {
			t.Fatalf("fact kind was lost: %v", err)
		}

		fence.NativeFenceDigest = "changed"

		changed, err := managed.AttemptFenceFactDigest(fence)
		if err != nil || changed == got {
			t.Fatalf("changed fact was not bound: %v", err)
		}
	}
}

func TestAttemptFenceFactDigestPreservesMarshalFailure(t *testing.T) {
	fence := managed.AttemptFenceRecord{AttemptNativeAcceptance: managed.AttemptNativeAcceptance{
		NativeAcceptance: managed.AttemptNativeFacts{AcceptedAt: time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)},
	}}
	if digest, err := managed.AttemptFenceFactDigest(fence); err == nil || digest != "" {
		t.Fatalf("invalid time digest=%q err=%v", digest, err)
	}

	if digest, err := attemptFact("removal_fence", fence); err == nil || digest != "" {
		t.Fatalf("invalid internal time digest=%q err=%v", digest, err)
	}
}
