package memory

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/store/postgres"
)

var _ managed.ProviderInvocationCommitsStore = (*Store)(nil)
var _ managed.ProviderInvocationCommitsStore = (*postgres.Store)(nil)

func memoryInvocationCommitsFixture(t *testing.T) (*Store, *managedstate.Repository, *memoryRecoveryHost, managed.ProviderInvocationKey, managed.RecoveryRecord[managed.ProviderNativeCommitFacts], managed.ProviderInvocationCommitRequest) {
	t.Helper()
	s, repository, host, key, _, proof := memoryRecoverySourcesFixture(t)

	before := memoryRecoverySnapshot(t, s)
	if records, err := repository.ReadManagedProviderInvocationCommits(t.Context(), key); err == nil || records != nil || !reflect.DeepEqual(before, memoryRecoverySnapshot(t, s)) {
		t.Fatal("unmarked invocation exposed a commit set", err)
	}

	sources, err := repository.ReadManagedProviderInvocationSources(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}

	ref := sources.Root.Facts.AttemptRef

	accepted, err := repository.ReadManagedProviderInvocation(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}

	permissionRef := memoryRecoveryReference(t, s, key.AttemptKey, accepted.Permission)
	proofRef := memoryRecoveryReference(t, s, key.AttemptKey, proof)
	native := []byte("native commit exact member:" + key.CommandID.String())

	intent, err := repository.RecordManagedRecoveryNativeIntent(t.Context(), managed.RecoveryNativeIntentRequest{Command: memoryRecoveryCommand(t, s, key.AttemptKey.Key, ref), Intent: managed.RecoveryNativeIntent{Binding: proof.Facts.Binding, Profile: managed.RemovalRecoveryProfile, Operation: "commit", RequestID: "bind-" + key.CommandID.String(), RequestDigest: managed.Digest(native), Command: native, CommandDigest: managed.Digest(native), Permission: &permissionRef, Proof: &proofRef}})
	if err != nil {
		t.Fatal(err)
	}

	permissionProof := memoryRecoveryPermissionProof(t, s, key.AttemptKey, key)

	permission, err := accepted.Permission.Facts.Permission.Value()
	if err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(permission)
	if err != nil {
		t.Fatal(err)
	}

	facts := managed.ProviderNativeCommitFacts{Binding: proof.Facts.Binding, Invocation: key, Intent: memoryRecoveryReference(t, s, key.AttemptKey, intent), PermissionDigest: managed.Digest(raw), PermissionProof: memoryRecoveryReference(t, s, key.AttemptKey, permissionProof), CurrentProof: proofRef, PermissionProofDigest: permissionProof.Digest, CurrentProofDigest: proof.Digest, GuardID: proof.Facts.GuardID, GuardRootDigest: proof.Facts.GuardRootDigest, NativeRequestID: intent.Facts.RequestID, NativeRequestDigest: intent.Facts.RequestDigest, NativeReceiptDigest: managed.Digest([]byte("native commit receipt")), NativeAcceptedAt: time.Now().UTC(), Export: memoryRecoveryExport()}

	commit, err := repository.RecordManagedProviderNativeCommit(t.Context(), managed.ProviderNativeCommitRequest{Command: memoryRecoveryCommand(t, s, key.AttemptKey.Key, ref), Facts: facts})
	if err != nil {
		t.Fatal(err)
	}

	current := memoryRecoveryCurrent(t, s, key.AttemptKey, key.CommandID)
	request := managed.ProviderInvocationCommitRequest{Command: memoryRecoveryCommand(t, s, key.AttemptKey.Key, ref), Invocation: key, ExpectedCurrentDigest: current.Digest}

	grant, err := repository.CommitManagedProviderInvocation(t.Context(), request)
	if err != nil || !grant.MayDelegate {
		t.Fatal("first exact marker", err)
	}

	return s, repository, host, key, commit, request
}

func TestManagedProviderInvocationCommitsHistoricalReplay(t *testing.T) {
	s, repository, host, key, commit, request := memoryInvocationCommitsFixture(t)
	memoryRecoverySourcesAdvanceLease(s, key.AttemptKey.Key)
	before := memoryRecoverySnapshot(t, s)
	calls := [3]int{host.proofs, host.commits, host.closes}

	records, err := repository.ReadManagedProviderInvocationCommits(t.Context(), key)
	if err != nil || len(records) != 1 || !reflect.DeepEqual(records[0], commit) {
		t.Fatal("exact marker commit inspection", err)
	}

	records[0].Facts.Export.Raw[0] ^= 0xff

	again, err := repository.ReadManagedProviderInvocationCommits(t.Context(), key)
	if err != nil || len(again) != 1 || !reflect.DeepEqual(again[0], commit) {
		t.Fatal("caller mutated retained native commit", err)
	}

	replay, err := repository.CommitManagedProviderInvocation(t.Context(), request)
	if err != nil || replay.MayDelegate {
		t.Fatal("historical marker replay granted delegation", err)
	}

	if !reflect.DeepEqual(before, memoryRecoverySnapshot(t, s)) || calls != [3]int{host.proofs, host.commits, host.closes} {
		t.Fatal("commit reader wrote or invoked producer")
	}
}

func TestManagedProviderInvocationCommitsMissingCustodyRefuses(t *testing.T) {
	for _, kind := range []string{"marker-member", "commit", "commit-event", "commit-receipt", "intent-request", "native-proof", "rewritten-commit"} {
		t.Run(kind, func(t *testing.T) {
			s, repository, host, key, commit, _ := memoryInvocationCommitsFixture(t)
			reference := memoryRecoveryReference(t, s, key.AttemptKey, commit)
			scope := memoryAttemptScope(t, key.AttemptKey.Key)

			s.mu.Lock()
			switch kind {
			case "marker-member":
				rows := s.managedControllerRecords["recovery_invocation_marker_members:"+scope]
				for row := range rows {
					delete(rows, row)

					break
				}
			case "commit":
				delete(s.managedControllerRecords[string(managed.RecoveryCommitRecord)+":"+scope], reference.Key)
			case "commit-event":
				delete(s.managedControllerRecords["removal_attempt_event:"+scope], commit.Acceptance.OperationID.String())
			case "commit-receipt":
				delete(s.managedReceipts, key.AttemptKey.InstanceID.String()+":"+commit.Acceptance.OperationID.String())
			case "intent-request":
				delete(s.managedControllerRecords["removal_recovery_request:"+scope], commit.Facts.Intent.Key)
			case "native-proof":
				delete(s.managedControllerRecords[string(managed.RecoveryProofRecord)+":"+scope], commit.Facts.CurrentProof.Key)
			case "rewritten-commit":
				commit.Facts.NativeReceiptDigest = managed.Digest([]byte("substitute receipt"))

				var hashErr error

				commit.Digest, hashErr = managed.RecoveryFactDigest(commit.Facts)
				if hashErr != nil {
					t.Fatal(hashErr)
				}

				raw, encodeErr := managed.EncodeRecoveryRecord(commit)
				if encodeErr != nil {
					t.Fatal(encodeErr)
				}

				s.managedControllerRecords[string(managed.RecoveryCommitRecord)+":"+scope][reference.Key] = raw
			}
			s.mu.Unlock()
			before := memoryRecoverySnapshot(t, s)
			calls := [3]int{host.proofs, host.commits, host.closes}

			records, err := repository.ReadManagedProviderInvocationCommits(t.Context(), key)
			if err == nil || records != nil || !reflect.DeepEqual(before, memoryRecoverySnapshot(t, s)) || calls != [3]int{host.proofs, host.commits, host.closes} {
				t.Fatal("partial or unauthenticated native commit set accepted", err)
			}
		})
	}
}
