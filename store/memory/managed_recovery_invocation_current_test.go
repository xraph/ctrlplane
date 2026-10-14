package memory

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/store/postgres"
)

var _ managed.ProviderInvocationCurrentStore = (*Store)(nil)
var _ managed.ProviderInvocationCurrentStore = (*postgres.Store)(nil)

func TestManagedProviderInvocationCurrentExactRead(t *testing.T) {
	s, repository, host, key, _, _ := memoryRecoverySourcesFixture(t)
	expected := memoryRecoveryCurrent(t, s, key.AttemptKey, key.CommandID)
	memoryRecoverySourcesAdvanceLease(s, key.AttemptKey.Key)
	before := memoryRecoverySnapshot(t, s)
	calls := [3]int{host.proofs, host.commits, host.closes}

	actual, err := repository.ReadManagedProviderInvocationCurrent(t.Context(), key)
	if err != nil || actual != expected {
		t.Fatal("exact current selection", err)
	}

	actual.Digest = "changed"

	again, err := repository.ReadManagedProviderInvocationCurrent(t.Context(), key)
	if err != nil || again != expected {
		t.Fatal("current reader mutated pointer", err)
	}

	wrong := key

	wrong.CommandID = id.New(managed.PrefixOperation)
	if refused, readErr := repository.ReadManagedProviderInvocationCurrent(t.Context(), wrong); readErr == nil || refused != (managed.ProviderStepRetryCurrent{}) {
		t.Fatal("other invocation returned current", readErr)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if refused, readErr := repository.ReadManagedProviderInvocationCurrent(ctx, key); !errors.Is(readErr, context.Canceled) || refused != (managed.ProviderStepRetryCurrent{}) {
		t.Fatal("canceled read", readErr)
	}

	if !reflect.DeepEqual(before, memoryRecoverySnapshot(t, s)) || calls != [3]int{host.proofs, host.commits, host.closes} {
		t.Fatal("current read wrote or called producer")
	}
}

func TestManagedProviderInvocationCurrentRefusesMissingAuthority(t *testing.T) {
	for _, kind := range []string{"pointer", "status", "permission", "receipt", "marker-members", "rewritten-pointer"} {
		t.Run(kind, func(t *testing.T) {
			s, repository, host, key, _, _ := memoryInvocationCommitsFixture(t)
			current := memoryRecoveryCurrent(t, s, key.AttemptKey, key.CommandID)
			scope := memoryAttemptScope(t, key.AttemptKey.Key)

			s.mu.Lock()
			switch kind {
			case "pointer":
				delete(s.managedControllerRecords, "step_retry_current:"+scope)
			case "status":
				delete(s.managedControllerRecords, "recovery_invocation_status:"+scope)
			case "permission":
				delete(s.managedControllerRecords[string(managed.RecoveryPermissionRecord)+":"+scope], current.Permission.Key)
			case "receipt":
				delete(s.managedReceipts, key.AttemptKey.InstanceID.String()+":"+current.Permission.OperationID.String())
			case "marker-members":
				delete(s.managedControllerRecords, "recovery_invocation_marker_members:"+scope)
			case "rewritten-pointer":
				for address, raw := range s.managedControllerRecords["step_retry_current:"+scope] {
					var pointer managed.ProviderStepRetryCurrent
					if err := json.Unmarshal(raw, &pointer); err != nil {
						t.Fatal(err)
					}

					pointer.MarkerKey = ""

					var err error

					pointer.Digest, err = managed.RetryCurrentDigest(pointer)
					if err != nil {
						t.Fatal(err)
					}

					raw, err = json.Marshal(pointer)
					if err != nil {
						t.Fatal(err)
					}

					s.managedControllerRecords["step_retry_current:"+scope][address] = raw
				}
			}
			s.mu.Unlock()
			before := memoryRecoverySnapshot(t, s)
			calls := [3]int{host.proofs, host.commits, host.closes}

			actual, err := repository.ReadManagedProviderInvocationCurrent(t.Context(), key)
			if err == nil || actual != (managed.ProviderStepRetryCurrent{}) {
				t.Fatal("incomplete authority returned current", err)
			}

			if !reflect.DeepEqual(before, memoryRecoverySnapshot(t, s)) || calls != [3]int{host.proofs, host.commits, host.closes} {
				t.Fatal("failed read wrote or called producer")
			}
		})
	}
}
