package managedstate

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

func TestNativeCommitReceiptIsNonadvancingAtSignedBoundary(t *testing.T) {
	_, key, now := recoveryFixture()

	command := managed.Command{Key: key.Key, ID: id.New(managed.PrefixOperation), Revision: math.MaxInt64, LeaseEpoch: 1, Owner: "owner"}
	for _, remaining := range []uint64{0, 1, 684} {
		counter := uint64(math.MaxInt64) - remaining
		target := &managed.Target{Revision: counter, LeaseEpoch: 1}
		state := managed.AttemptState{Revision: managed.Decimal(strconv.FormatUint(counter, 10))}

		receipt, attemptRevision, err := invocationNativeCommitReceipt(target, state, command, strings.Repeat("a", 64), now)
		if err != nil || receipt.Revision != counter || attemptRevision != counter || state.Revision != managed.Decimal(strconv.FormatUint(counter, 10)) {
			t.Fatal("native retention consumed a revision", remaining, err)
		}

		reserve := recoveryReserveAfter{existing: remaining}
		if err = invocationNativeCommitHeadroom(target.Revision, attemptRevision, reserve); err != nil {
			t.Fatal("reserved boundary refused nonadvancing retention", remaining, err)
		}

		if err = recoveryCheckHeadroom(target.Revision, attemptRevision, 1, reserve); !errors.Is(err, managed.ErrCapacity) {
			t.Fatal("fixture did not distinguish advancing issuance", remaining, err)
		}

		if remaining != 0 {
			if err = invocationNativeCommitHeadroom(target.Revision+1, attemptRevision, reserve); !errors.Is(err, managed.ErrCapacity) {
				t.Fatal("parent reserve exceeded", err)
			}

			if err = invocationNativeCommitHeadroom(target.Revision, attemptRevision+1, reserve); !errors.Is(err, managed.ErrCapacity) {
				t.Fatal("attempt reserve exceeded", err)
			}
		}
	}

	for _, invalid := range []struct {
		parent  uint64
		attempt managed.Decimal
	}{
		{uint64(math.MaxInt64) + 1, "1"},
		{1, "9223372036854775808"},
		{1, "invalid"},
	} {
		if _, _, err := invocationNativeCommitReceipt(&managed.Target{Revision: invalid.parent}, managed.AttemptState{Revision: invalid.attempt}, command, strings.Repeat("a", 64), now); !errors.Is(err, managed.ErrCapacity) {
			t.Fatal("invalid signed counter accepted", err)
		}
	}
}

type nativeCommitAcceptingVerifier struct {
	managed.RemovalRecoveryVerifier

	calls int
}

func (verifier *nativeCommitAcceptingVerifier) VerifyRemovalCommit(context.Context, managed.RecoveryRecord[managed.RecoveryNativeIntent], managed.ProviderNativeCommitFacts) error {
	verifier.calls++

	return nil
}

// This exercises the same preflight validator and configured verifier boundary used by
// the public method. The full held original-binding/public transaction remains unrun.
func TestNativeCommitPreflightRefusesDependenciesBeforeVerifier(t *testing.T) {
	fixture, selected, now := invocationFixture(t)
	member, _, commit := invocationCommitMemberFixture(t, fixture, selected, now)
	proofFacts := member.proof.Facts
	proofFacts.Binding.ProviderOperationID = selected.permission.Facts.Key.Step.ProviderOperationID
	proof := recoveryTestRecord(t, managed.RecoveryProofRecord, proofFacts, now)
	proofRef := recoveryTestStore(t, fixture, selected.permission.Facts.Key.AttemptKey, proof)

	bindingDigest, err := recoveryBindingKey(proofFacts.Binding)
	if err != nil {
		t.Fatal(err)
	}

	permissionProof, err := activationRecord(managed.RecoveryPermissionProofRecord, managed.ProviderPermissionProof{Invocation: selected.permission.Facts.Key, BindingDigest: bindingDigest, PermissionDigest: commit.Facts.PermissionDigest, Proof: proofRef, NativeMaterialDigest: proofFacts.NativeMaterialDigest}, selected.permission.Acceptance)
	if err != nil {
		t.Fatal(err)
	}

	permissionProofRef := recoveryTestStore(t, fixture, selected.permission.Facts.Key.AttemptKey, permissionProof)
	intent := recoveryTestRecord(t, managed.RecoveryIntentRecord, managed.RecoveryNativeIntent{Binding: proofFacts.Binding, Profile: managed.RemovalRecoveryProfile, Operation: "commit", RequestID: "native-request", RequestDigest: strings.Repeat("c", 64), Command: []byte("bounded-command"), CommandDigest: managed.Digest([]byte("bounded-command")), Permission: &selected.current.Permission, Proof: &proofRef}, now)
	intentRef := recoveryTestStore(t, fixture, selected.permission.Facts.Key.AttemptKey, intent)
	facts := commit.Facts
	facts.Binding, facts.Intent = proofFacts.Binding, intentRef
	facts.PermissionProof, facts.CurrentProof = permissionProofRef, proofRef
	facts.PermissionProofDigest, facts.CurrentProofDigest = permissionProof.Digest, proof.Digest
	member = invocationProofMember{binding: proofFacts.Binding, bindingDigest: bindingDigest, current: managed.RemovalRevalidationCurrent{Proof: proofRef}, proof: proof}
	repository, _, _ := configuredRecoveryFixture(t)
	verifier := &nativeCommitAcceptingVerifier{}
	repository.recoveryHost.Verifier = verifier

	checkThenVerify := func(input managed.ProviderNativeCommitFacts) error {
		if _, preflightErr := invocationCommitFactsHeld(t.Context(), fixture, selected, member, input, now, now); preflightErr != nil {
			return preflightErr
		}

		_, verifyErr := repository.verifyRecoveryCommit(t.Context(), intent, input)

		return verifyErr
	}
	if err = checkThenVerify(facts); err != nil || verifier.calls != 1 {
		t.Fatal("valid preflight failed to reach accepting verifier", err, verifier.calls)
	}

	scope, err := attemptScope(selected.permission.Facts.Key.AttemptKey.Key)
	if err != nil {
		t.Fatal(err)
	}

	for _, missing := range [][3]string{
		{string(managed.RecoveryPermissionProofRecord), scope, permissionProofRef.Key},
		{recoveryAcceptanceKind, scope, permissionProofRef.Key},
		{string(managed.RecoveryProofRecord), scope, proofRef.Key},
		{recoveryAcceptanceKind, scope, proofRef.Key},
	} {
		saved := fixture.rows[missing]
		delete(fixture.rows, missing)

		verifier.calls = 0
		if err = checkThenVerify(facts); err == nil || verifier.calls != 0 {
			t.Fatal("missing proof reached verifier", missing, err, verifier.calls)
		}

		fixture.rows[missing] = saved
	}

	for _, mutate := range []func(*managed.ProviderNativeCommitFacts){
		func(value *managed.ProviderNativeCommitFacts) { value.PermissionProof.Digest = strings.Repeat("e", 64) },
		func(value *managed.ProviderNativeCommitFacts) { value.PermissionProofDigest = strings.Repeat("e", 64) },
		func(value *managed.ProviderNativeCommitFacts) { value.CurrentProof = selected.current.Permission },
		func(value *managed.ProviderNativeCommitFacts) { value.CurrentProofDigest = strings.Repeat("e", 64) },
		func(value *managed.ProviderNativeCommitFacts) { value.GuardRootDigest = strings.Repeat("e", 64) },
	} {
		changed := facts
		mutate(&changed)

		verifier.calls = 0
		if err = checkThenVerify(changed); err == nil || verifier.calls != 0 {
			t.Fatal("mismatched accepted proof reached verifier", err, verifier.calls)
		}
	}
}
