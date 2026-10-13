package managedstate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/xraph/ctrlplane/managed"
)

// These producers are interface witnesses only; no transport is exercised here.
type recoveryHostWitness struct {
	managed.RemovalRevalidationProducer
	managed.RemovalCommitProducer
	managed.RemovalGuardCloseProducer
	managed.ProviderInvocationClosureProducer
	managed.RemovalRecoveryVerifier

	verify       func(managed.RemovalRevalidationFacts) error
	calls        int
	closureErr   error
	closureCalls int
	closeCalls   int
}

func (w *recoveryHostWitness) VerifyRemovalRevalidation(_ context.Context, _ managed.RecoveryRecord[managed.RecoveryNativeIntent], facts managed.RemovalRevalidationFacts) error {
	w.calls++
	if w.verify != nil {
		return w.verify(facts)
	}

	return nil
}

type recoveryBackendWitness struct{ Backend }

func configuredRecoveryFixture(t *testing.T) (*Repository, *recoveryHostWitness, managed.RemovalRecoveryHost) {
	t.Helper()

	witness := &recoveryHostWitness{}
	identity := managed.RecoveryProducerIdentity{ProducerID: "producer", Profile: "fixture.v1", Version: "1", VerifierID: "verifier"}
	host := managed.RemovalRecoveryHost{Revalidation: witness, Commit: witness, Close: witness, Closure: witness, Verifier: witness, RevalidationIdentity: identity, CommitIdentity: identity, CloseIdentity: identity, ClosureVerifierID: "closure-verifier"}

	repository, err := New(&recoveryBackendWitness{}).WithRemovalRecovery(host)
	if err != nil {
		t.Fatal(err)
	}

	return repository, witness, host
}

func TestRecoveryHostRefusesMissingAndTypedNilCapabilities(t *testing.T) {
	repository, _, host := configuredRecoveryFixture(t)

	var absent *recoveryHostWitness

	for name, change := range map[string]func(*managed.RemovalRecoveryHost){
		"revalidation": func(h *managed.RemovalRecoveryHost) { h.Revalidation = nil },
		"commit":       func(h *managed.RemovalRecoveryHost) { h.Commit = absent },
		"close":        func(h *managed.RemovalRecoveryHost) { h.Close = absent },
		"closure":      func(h *managed.RemovalRecoveryHost) { h.Closure = nil },
		"verifier":     func(h *managed.RemovalRecoveryHost) { h.Verifier = absent },
	} {
		t.Run(name, func(t *testing.T) {
			changed := host
			change(&changed)

			if _, err := repository.WithRemovalRecovery(changed); !errors.Is(err, managed.ErrUnsupported) {
				t.Fatalf("partial configuration: %v", err)
			}
		})
	}

	if _, _, _, err := managed.RequireRemovalRecovery(repository); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatalf("unfinished optional capability advertised: %v", err)
	}

	host.RevalidationIdentity.Version = "9223372036854775808"
	if _, err := repository.WithRemovalRecovery(host); !errors.Is(err, managed.ErrInvalid) {
		t.Fatalf("out-of-range producer version: %v", err)
	}
}

func recoveryHostProof(t *testing.T) (managed.RecoveryRecord[managed.RecoveryNativeIntent], managed.RemovalRevalidationFacts) {
	t.Helper()

	_, key, now := recoveryFixture()
	proof := recoveryTestProof(t, key, now).Facts
	hash := strings.Repeat("a", 64)
	intent := recoveryTestRecord(t, managed.RecoveryIntentRecord, managed.RecoveryNativeIntent{Binding: proof.Binding, Profile: managed.RemovalRecoveryProfile, Operation: "revalidate", RequestID: "request", RequestDigest: hash, Command: []byte("native command"), CommandDigest: managed.Digest([]byte("native command"))}, now)

	selected, err := recoveryFactKey(key, intent.Kind, intent.Acceptance.OperationID, intent.Facts)
	if err != nil {
		t.Fatal(err)
	}

	proof.Intent, err = managed.RecoveryAssociationFor(selected, intent)
	if err != nil {
		t.Fatal(err)
	}

	proof.NativeRequestID = "request"
	proof.NativeRequestDigest = hash
	proof.NativeMaterialDigest = hash
	proof.NativeReceiptDigest = hash
	proof.NativeCurrentDigest = hash
	proof.NativeScopeDigest = hash
	proof.CandidateDigest = hash
	proof.BuildDigest = hash
	proof.SurvivorDigest = hash
	proof.ExclusionDigest = hash
	proof.CoverageDigest = hash
	proof.CandidateVersion = "1"
	proof.RemovalEpoch = "1"
	proof.ProcessDisposition = "live"

	return intent, proof
}

func TestRecoveryConfiguredProofVerification(t *testing.T) {
	repository, witness, _ := configuredRecoveryFixture(t)

	intent, proof := recoveryHostProof(t)
	for name, change := range map[string]func(*managed.RemovalRevalidationFacts){
		"producer":           func(p *managed.RemovalRevalidationFacts) { p.Export.ProducerID = "caller" },
		"binding":            func(p *managed.RemovalRevalidationFacts) { p.Binding.DescriptorDigest = strings.Repeat("f", 64) },
		"native domain":      func(p *managed.RemovalRevalidationFacts) { p.NativeRequestDigest = p.Export.RawDigest },
		"receipt":            func(p *managed.RemovalRevalidationFacts) { p.Intent.ReceiptDigest = strings.Repeat("b", 64) },
		"overflow":           func(p *managed.RemovalRevalidationFacts) { p.CandidateVersion = "9223372036854775808" },
		"future observation": func(p *managed.RemovalRevalidationFacts) { p.SourceObservedAt = p.ValidUntil },
		"missing process":    func(p *managed.RemovalRevalidationFacts) { p.ProcessDisposition = "effect_complete" },
	} {
		t.Run(name, func(t *testing.T) {
			changed, err := recoverySnapshot(proof)
			if err != nil {
				t.Fatal(err)
			}

			change(&changed)

			before := witness.calls
			if _, err = repository.verifyRecoveryProof(t.Context(), intent, changed); err == nil || witness.calls != before {
				t.Fatalf("invalid proof reached verifier: %v", err)
			}
		})
	}

	witness.verify = func(managed.RemovalRevalidationFacts) error { return managed.ErrAuthority }

	if _, err := repository.verifyRecoveryProof(t.Context(), intent, proof); !errors.Is(err, managed.ErrAuthority) {
		t.Fatalf("verifier refusal lost: %v", err)
	}

	witness.verify = func(input managed.RemovalRevalidationFacts) error {
		input.Export.Raw[0] = 'X'

		return nil
	}

	accepted, err := repository.verifyRecoveryProof(t.Context(), intent, proof)
	if err != nil || string(accepted.Export.Raw) != "compact" || string(proof.Export.Raw) != "compact" {
		t.Fatalf("verifier mutated retained snapshot: %v", err)
	}
}

func (w *recoveryHostWitness) VerifyProviderNoDispatch(_ context.Context, _ managed.RecoveryRecord[managed.ProviderInvocationPermission], _ *managed.RecoveryRecord[managed.ProviderInvocationMarker], _ managed.ProviderInvocationFailure) error {
	w.closureCalls++

	return w.closureErr
}

func TestRecoveryNoDispatchNeedsAffirmativeConfiguredWitness(t *testing.T) {
	repository, witness, _ := configuredRecoveryFixture(t)
	fixture, key, root, current, now := retryOriginalFixture(t)

	selected, err := retryReadSelected(t.Context(), fixture, key, root)
	if err != nil {
		t.Fatal(err)
	}

	permission, err := selected.permission.Facts.Permission.Value()
	if err != nil {
		t.Fatal(err)
	}

	hash, err := recoveryPermissionDigest(selected.permission.Facts.Permission)
	if err != nil {
		t.Fatal(err)
	}

	failure := managed.ProviderStepFailure{SchemaVersion: "1", Permission: permission, AttemptRef: selected.root.Facts.AttemptRef, InvocationID: permission.CommandID, ProducerProfile: managed.LocalNoDispatchProfile, VerifierID: "closure-verifier", Class: "local_no_dispatch", Phase: "before_transport_delegate", Reason: "invocation_closed", ObservedAt: now, Witness: []byte("affirmative"), WitnessDigest: managed.Digest([]byte("affirmative"))}
	facts := managed.ProviderInvocationFailure{Key: selected.permission.Facts.Key, RootDigest: selected.root.Digest, PermissionDigest: hash, Failure: failure}
	marker := recoveryTestRecord(t, managed.RecoveryMarkerRecord, managed.ProviderInvocationMarker{Key: facts.Key, RootDigest: facts.RootDigest, PermissionDigest: hash, SelectionDigest: current.Digest, AcceptedAt: now}, now)
	before := fixture.writes

	for _, retained := range []*managed.RecoveryRecord[managed.ProviderInvocationMarker]{nil, &marker} {
		witness.closureErr = managed.ErrAuthority
		if _, err = repository.verifyRecoveryNoDispatch(t.Context(), selected.permission, retained, facts); !errors.Is(err, managed.ErrAuthority) {
			t.Fatalf("configured custody refusal lost: %v", err)
		}

		witness.closureErr = nil

		if _, err = repository.verifyRecoveryNoDispatch(t.Context(), selected.permission, retained, facts); err != nil {
			t.Fatalf("affirmative witness refused before closure staging: %v", err)
		}

		absent := facts
		absent.Failure.Witness = nil
		absent.Failure.WitnessDigest = managed.Digest(nil)

		calls := witness.closureCalls
		if _, err = repository.verifyRecoveryNoDispatch(t.Context(), selected.permission, retained, absent); err == nil || witness.closureCalls != calls {
			t.Fatalf("marker presence or absence substituted for evidence: %v", err)
		}
	}

	if fixture.writes != before {
		t.Fatal("verification wrote a closure or granted a retry")
	}
}

func (w *recoveryHostWitness) VerifyRemovalClose(_ context.Context, _ managed.RecoveryRecord[managed.RecoveryNativeIntent], _ managed.RecoveryNativeCloseFacts) error {
	w.closeCalls++

	return nil
}

func TestRecoveryCloseRequiresExactIntentCoverage(t *testing.T) {
	repository, witness, _ := configuredRecoveryFixture(t)
	intent, proof := recoveryHostProof(t)
	cutoff := proof.Intent
	cutoff.Kind = managed.RecoveryCutoffRecord
	coverage := proof.Intent
	coverage.Kind = managed.RecoveryCoverageRecord
	intent.Facts.Operation = "close"
	intent.Facts.Cutoff = &cutoff
	intent.Facts.Coverage = &coverage
	facts := managed.RecoveryNativeCloseFacts{Binding: proof.Binding, Cutoff: cutoff, Coverage: coverage, GuardID: proof.GuardID, GuardRootDigest: proof.GuardRootDigest, NativeRequestID: proof.NativeRequestID, NativeRequestDigest: proof.NativeRequestDigest, NativeReceiptDigest: proof.NativeReceiptDigest, NativeAcceptedAt: proof.NativeAcceptedAt, Export: proof.Export}

	for name, change := range map[string]func(*managed.RecoveryNativeIntent){
		"missing cutoff":     func(i *managed.RecoveryNativeIntent) { i.Cutoff = nil },
		"missing coverage":   func(i *managed.RecoveryNativeIntent) { i.Coverage = nil },
		"different cutoff":   func(i *managed.RecoveryNativeIntent) { i.Cutoff.ReceiptDigest = strings.Repeat("f", 64) },
		"different coverage": func(i *managed.RecoveryNativeIntent) { i.Coverage.Digest = strings.Repeat("e", 64) },
		"matching":           func(*managed.RecoveryNativeIntent) {},
	} {
		t.Run(name, func(t *testing.T) {
			saved, err := recoverySnapshot(intent)
			if err != nil {
				t.Fatal(err)
			}

			change(&saved.Facts)
			// Keep the intent envelope and its reference valid so only the close
			// cutoff/coverage binding can reject these negative cases.
			saved.Digest, err = managed.RecoveryFactDigest(saved.Facts)
			if err != nil {
				t.Fatal(err)
			}

			key, err := recoveryFactKey(saved.Facts.Binding.Key.AttemptKey, saved.Kind, saved.Acceptance.OperationID, saved.Facts)
			if err != nil {
				t.Fatal(err)
			}

			input := facts

			input.Intent, err = managed.RecoveryAssociationFor(key, saved)
			if err != nil {
				t.Fatal(err)
			}

			before := witness.closeCalls

			_, err = repository.verifyRecoveryClose(t.Context(), saved, input)
			if name == "matching" {
				if err != nil || witness.closeCalls != before+1 {
					t.Fatalf("matching intent did not reach accepting verifier: %v", err)
				}
			} else if !errors.Is(err, managed.ErrAuthority) || witness.closeCalls != before {
				t.Fatalf("mismatched intent reached accepting verifier: %v", err)
			}
		})
	}
}

func TestRecoveryHostRefusesTransactionBackendsBeforeCallbacks(t *testing.T) {
	_, witness, host := configuredRecoveryFixture(t)
	fixture, _, _ := recoveryFixture()

	var absent *recoveryLeafFixture
	for _, backend := range []Backend{fixture, absent} {
		if _, err := New(backend).WithRemovalRecovery(host); !errors.Is(err, managed.ErrUnsupported) {
			t.Fatal("transaction-backed recovery host accepted", err)
		}
	}

	if witness.calls != 0 || witness.closureCalls != 0 || witness.closeCalls != 0 {
		t.Fatal("configuration invoked host callbacks")
	}
}
