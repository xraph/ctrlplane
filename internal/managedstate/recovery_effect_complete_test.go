package managedstate

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

func effectCompleteShapeFixture(t *testing.T) managed.RemovalRevalidationFacts {
	t.Helper()
	_, facts := recoveryHostProof(t)
	facts.Binding.OriginalProcessDigest = strings.Repeat("a", 64)
	facts.ProcessDisposition = "effect_complete"
	facts.EffectComplete = &managed.RecoveryEffectComplete{
		Invocation:       managed.ProviderInvocationKey{AttemptKey: facts.Binding.Key.AttemptKey, CommandID: id.New(managed.PrefixOperation)},
		PermissionDigest: strings.Repeat("b", 64), Result: managed.RecoveryAssociation{Kind: managed.RecoveryResultRecord, Key: strings.Repeat("c", 64), Digest: strings.Repeat("d", 64), OperationID: id.New(managed.PrefixOperation), ReceiptDigest: strings.Repeat("e", 64)},
		ResourceID: "exact-resource", Incarnation: "provider-incarnation", OriginalProcessDigest: facts.Binding.OriginalProcessDigest, PreEffectProofDigest: strings.Repeat("f", 64),
		OriginalPreEffect: &managed.RecoveryOriginalPreEffect{Key: facts.Binding.Key, OperationID: id.New(managed.PrefixOperation), FactDigest: strings.Repeat("a", 64)},
	}

	return facts
}

func TestEffectCompleteRequiresExplicitProofSourceAndPositiveAssociation(t *testing.T) {
	facts := effectCompleteShapeFixture(t)
	if err := recoveryProofDispositionShape(facts); err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func(*managed.RemovalRevalidationFacts){
		"missing":          func(f *managed.RemovalRevalidationFacts) { f.EffectComplete = nil },
		"live with effect": func(f *managed.RemovalRevalidationFacts) { f.ProcessDisposition = "live" },
		"unknown":          func(f *managed.RemovalRevalidationFacts) { f.ProcessDisposition = "process_missing" },
		"resource":         func(f *managed.RemovalRevalidationFacts) { f.EffectComplete.ResourceID = "" },
		"incarnation":      func(f *managed.RemovalRevalidationFacts) { f.EffectComplete.Incarnation = "" },
		"process": func(f *managed.RemovalRevalidationFacts) {
			f.EffectComplete.OriginalProcessDigest = strings.Repeat("9", 64)
		},
		"result": func(f *managed.RemovalRevalidationFacts) {
			f.EffectComplete.Result.Kind = managed.RecoveryFailureRecord
		},
		"no source": func(f *managed.RemovalRevalidationFacts) { f.EffectComplete.OriginalPreEffect = nil },
		"two sources": func(f *managed.RemovalRevalidationFacts) {
			x := f.EffectComplete.Result
			x.Kind = managed.RecoveryPermissionProofRecord
			f.EffectComplete.RetryPreEffect = &x
		},
		"wrong binding": func(f *managed.RemovalRevalidationFacts) {
			f.EffectComplete.OriginalPreEffect.Key.ReservationID = id.New(managed.PrefixReservation)
		},
	} {
		t.Run(name, func(t *testing.T) {
			cloned, err := recoverySnapshot(facts)
			if err != nil {
				t.Fatal(err)
			}

			mutate(&cloned)

			if err = recoveryProofDispositionShape(cloned); err == nil {
				t.Fatal("unproven effect accepted")
			}
		})
	}
}

func TestOriginalPreEffectMatchesExactOriginalAndPredatesPermission(t *testing.T) {
	facts := effectCompleteShapeFixture(t)
	source := facts.EffectComplete.OriginalPreEffect
	now := facts.NativeAcceptedAt
	selected := retrySelected{current: managed.ProviderStepRetryCurrent{Ordinal: "0"}, root: managed.RecoveryRecord[managed.ProviderStepRetryRoot]{Facts: managed.ProviderStepRetryRoot{OriginalPermission: managed.WireProviderStepPermission{IssuedAt: now, Revision: "2"}}}}
	facts.Binding.NativeFenceDigest = strings.Repeat("1", 64)
	fence := managed.AttemptFenceRecord{LocalOperationID: source.OperationID, Key: source.Key, AttemptNativeAcceptance: managed.AttemptNativeAcceptance{NativeFenceDigest: facts.Binding.NativeFenceDigest, ProofDigest: facts.EffectComplete.PreEffectProofDigest, NativeAcceptance: managed.AttemptNativeFacts{AcceptedAt: now.Add(-time.Second)}}, Fence: managed.WireFence{ValidUntil: now.Add(time.Second)}}

	receipt := managed.Receipt{ID: source.OperationID, Revision: 1, AcceptedAt: now.Add(-time.Second)}
	if err := recoveryOriginalPreEffectMatches(facts, selected, fence, source.FactDigest, receipt); err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func(*managed.AttemptFenceRecord, *managed.Receipt){
		"local operation": func(f *managed.AttemptFenceRecord, _ *managed.Receipt) {
			f.LocalOperationID = id.New(managed.PrefixOperation)
		},
		"native fence":   func(f *managed.AttemptFenceRecord, _ *managed.Receipt) { f.NativeFenceDigest = strings.Repeat("2", 64) },
		"original proof": func(f *managed.AttemptFenceRecord, _ *managed.Receipt) { f.ProofDigest = strings.Repeat("2", 64) },
		"native time": func(f *managed.AttemptFenceRecord, _ *managed.Receipt) {
			f.NativeAcceptance.AcceptedAt = now.Add(time.Nanosecond)
		},
		"receipt time":     func(_ *managed.AttemptFenceRecord, r *managed.Receipt) { r.AcceptedAt = now.Add(time.Nanosecond) },
		"equal revision":   func(_ *managed.AttemptFenceRecord, r *managed.Receipt) { r.AcceptedAt = now; r.Revision = 2 },
		"later revision":   func(_ *managed.AttemptFenceRecord, r *managed.Receipt) { r.AcceptedAt = now; r.Revision = 3 },
		"zero revision":    func(_ *managed.AttemptFenceRecord, r *managed.Receipt) { r.Revision = 0 },
		"receipt identity": func(_ *managed.AttemptFenceRecord, r *managed.Receipt) { r.ID = id.New(managed.PrefixOperation) },
		"expired fence":    func(f *managed.AttemptFenceRecord, _ *managed.Receipt) { f.Fence.ValidUntil = now },
	} {
		t.Run(name, func(t *testing.T) {
			f, r := fence, receipt
			mutate(&f, &r)

			if err := recoveryOriginalPreEffectMatches(facts, selected, f, source.FactDigest, r); err == nil {
				t.Fatal("different or later pre-effect evidence accepted")
			}
		})
	}

	equalClock := receipt

	equalClock.AcceptedAt = now
	if err := recoveryOriginalPreEffectMatches(facts, selected, fence, source.FactDigest, equalClock); err != nil {
		t.Fatal("earlier revision at equal clock refused", err)
	}

	selected.current.Ordinal = "1"
	if err := recoveryOriginalPreEffectMatches(facts, selected, fence, source.FactDigest, receipt); err == nil {
		t.Fatal("retry adopted original pre-effect variant")
	}
}

func TestConfiguredVerifierStillOwnsEffectCompleteAuthenticity(t *testing.T) {
	repository, witness, _ := configuredRecoveryFixture(t)
	intent, facts := recoveryHostProof(t)
	complete := effectCompleteShapeFixture(t).EffectComplete
	complete.Invocation.AttemptKey = facts.Binding.Key.AttemptKey
	complete.OriginalPreEffect.Key = facts.Binding.Key
	facts.Binding.OriginalProcessDigest = complete.OriginalProcessDigest
	intent.Facts.Binding = facts.Binding

	var err error

	intent.Digest, err = managed.RecoveryFactDigest(intent.Facts)
	if err != nil {
		t.Fatal(err)
	}

	key, err := recoveryFactKey(facts.Binding.Key.AttemptKey, managed.RecoveryIntentRecord, intent.Acceptance.OperationID, intent.Facts)
	if err != nil {
		t.Fatal(err)
	}

	facts.Intent, err = managed.RecoveryAssociationFor(key, intent)
	if err != nil {
		t.Fatal(err)
	}

	facts.ProcessDisposition, facts.EffectComplete = "effect_complete", complete

	witness.verify = func(managed.RemovalRevalidationFacts) error { return managed.ErrAuthority }
	if _, err = repository.verifyRecoveryProof(t.Context(), intent, facts); !errors.Is(err, managed.ErrAuthority) || witness.calls != 1 {
		t.Fatal("configured rejection bypassed", err, witness.calls)
	}

	witness.verify = nil
	if _, err = repository.verifyRecoveryProof(t.Context(), intent, facts); err != nil || witness.calls != 2 {
		t.Fatal("valid shape never reached configured verifier", err, witness.calls)
	}

	facts.EffectComplete.OriginalPreEffect = nil
	if _, err = repository.verifyRecoveryProof(t.Context(), intent, facts); err == nil || witness.calls != 2 {
		t.Fatal("missing pre-effect source reached verifier", err, witness.calls)
	}
	// This test covers the host verifier boundary, not full held input validation.
}
