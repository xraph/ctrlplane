//go:build linux || darwin

package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

// This exact fixture stands for independent accepted store and native custody.
// Self-consistent submitted records alone cannot pass its trust comparison.
type retryCustodyAuthority struct {
	*custodyAuthorityFixture

	fresh     bool
	denyRetry bool
	admits    int
}

func (a *retryCustodyAuthority) VerifyRetry(ctx context.Context, r ManagedDockerRequest) error {
	if r.Retry == nil || a.denyRetry {
		return managed.ErrAuthority
	}

	return a.VerifyRequest(ctx, r)
}
func (a *retryCustodyAuthority) Admit(context.Context, ManagedDockerRequest) error {
	return managed.ErrUnsupported
}
func (a *retryCustodyAuthority) AdmitRetry(ctx context.Context, r ManagedDockerRequest) error {
	if err := a.VerifyRetry(ctx, r); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.admits++
	if !a.fresh {
		return managed.ErrAuthority
	}

	a.fresh = false

	return nil
}

func retryCustodyRecord[T any](t *testing.T, kind managed.RecoveryRecordKind, facts T, revision uint64, at time.Time) managed.RecoveryRecord[T] {
	t.Helper()

	digest, err := managed.RecoveryFactDigest(facts)
	if err != nil {
		t.Fatal(err)
	}

	receipt := managed.Receipt{ID: id.New(managed.PrefixOperation), Digest: strings.Repeat("d", 64), Revision: revision, LeaseEpoch: 1, AcceptedAt: at, LeaseUntil: at.Add(time.Minute)}

	record := managed.RecoveryRecord[T]{SchemaVersion: "1", Kind: kind, Facts: facts, Digest: digest, Acceptance: managed.RecoveryAcceptance{OperationID: receipt.ID, RequestDigest: receipt.Digest, Receipt: managed.ReceiptWire(receipt)}}
	if _, err = managed.EncodeRecoveryRecord(record); err != nil {
		t.Fatal(err)
	}

	return record
}
func retryCustodyRedigest[T any](t *testing.T, record *managed.RecoveryRecord[T]) {
	t.Helper()

	var err error

	record.Digest, err = managed.RecoveryFactDigest(record.Facts)
	if err != nil {
		t.Fatal(err)
	}
}
func retryCustodyFixture(t *testing.T, kind managed.ProviderStepKind, ordinal uint64) ManagedDockerRequest {
	t.Helper()

	request := custodyRequestFixture(kind)
	original := request.Binding.Original
	now := original.Permission.IssuedAt

	command, err := managed.DecodeProviderEffectCommand(request.Binding.Command)
	if err != nil {
		t.Fatal(err)
	}

	root := retryCustodyRecord(t, managed.RecoveryRootRecord, managed.ProviderStepRetryRoot{AttemptRef: original.AttemptRef, Step: original.Key.Step, Profile: managed.ProviderStepRetryProfile, SpecDigest: original.Permission.SpecDigest, ResourceID: original.Permission.ResourceID, Incarnation: command.Incarnation, ProviderCommandDigest: request.Binding.ProviderCommandDigest, OriginalPermission: original.Permission, OriginalPermissionReceipt: original.Receipt}, 2, now)
	permission := original.Permission
	permission.CommandID = id.New(managed.PrefixOperation)
	permission.Digest = strings.Repeat("d", 64)
	permission.Revision = "4"
	permission.IssuedAt = now
	key := managed.ProviderInvocationKey{AttemptKey: original.Key.AttemptKey, Step: original.Key.Step, CommandID: permission.CommandID}
	accepted := retryCustodyRecord(t, managed.RecoveryPermissionRecord, managed.ProviderInvocationPermission{Key: key, RootDigest: root.Digest, Ordinal: managed.Decimal(strconv.FormatUint(ordinal, 10)), Permission: permission, ProviderCommandDigest: request.Binding.ProviderCommandDigest, PermissionProofSetDigest: strings.Repeat("1", 64), PermissionProofCount: "64"}, 4, now)

	ref, err := managed.RecoveryAssociationFor(strings.Repeat("2", 64), accepted)
	if err != nil {
		t.Fatal(err)
	}

	rootRef, err := managed.RecoveryAssociationFor(strings.Repeat("3", 64), root)
	if err != nil {
		t.Fatal(err)
	}

	previousCommand := original.Permission.CommandID
	previousAuthority := rootRef

	if ordinal > 1 {
		previousCommand = id.New(managed.PrefixOperation)
		previousAuthority = managed.RecoveryAssociation{Kind: managed.RecoveryAuthorizationRecord, Key: strings.Repeat("4", 64), Digest: strings.Repeat("5", 64), OperationID: id.New(managed.PrefixOperation), ReceiptDigest: strings.Repeat("6", 64)}
	}

	closureID := id.New(managed.PrefixOperation)
	previous := managed.ProviderStepRetryPrevious{SchemaVersion: "1", RootDigest: root.Digest, CommandID: previousCommand, Ordinal: managed.Decimal(strconv.FormatUint(ordinal-1, 10)), PermissionDigest: strings.Repeat("7", 64), Failure: managed.RecoveryAssociation{Kind: managed.RecoveryFailureRecord, Key: strings.Repeat("8", 64), Digest: strings.Repeat("9", 64), OperationID: closureID, ReceiptDigest: strings.Repeat("a", 64)}, Closure: managed.RecoveryAssociation{Kind: managed.RecoveryClosureRecord, Key: strings.Repeat("b", 64), Digest: strings.Repeat("c", 64), OperationID: closureID, ReceiptDigest: strings.Repeat("a", 64)}, Authority: previousAuthority}

	previous.Digest, err = managed.RetryPreviousDigest(previous)
	if err != nil {
		t.Fatal(err)
	}

	authorization := retryCustodyRecord(t, managed.RecoveryAuthorizationRecord, managed.ProviderStepRetryAuthorization{Key: key, RootDigest: root.Digest, Ordinal: accepted.Facts.Ordinal, Permission: ref, Previous: previous, ExpectedCurrentDigest: strings.Repeat("e", 64), ParentRevision: "4", AttemptRevision: "3", AuthorizedAt: now}, 4, now)
	authorization.Acceptance = accepted.Acceptance

	concrete, err := permission.Value()
	if err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(concrete)
	if err != nil {
		t.Fatal(err)
	}

	marker := retryCustodyRecord(t, managed.RecoveryMarkerRecord, managed.ProviderInvocationMarker{Key: key, RootDigest: root.Digest, PermissionDigest: managed.Digest(raw), SelectionDigest: strings.Repeat("f", 64), NativeCommitSetDigest: strings.Repeat("1", 64), CurrentProofSetDigest: strings.Repeat("2", 64), BindingCount: "64", AcceptedAt: now}, 5, now)
	request.Permission = permission
	request.Receipt = accepted.Acceptance.Receipt
	request.Invocation = &accepted.Facts

	request.Retry = &ManagedDockerRetryEvidence{Root: root, Permission: accepted, Authorization: authorization, Marker: marker}
	if request.Receipt.ID == request.Permission.CommandID {
		t.Fatal("fixture collapsed independent acceptance and invocation IDs")
	}

	if err = validateManagedDockerRetry(request); err != nil {
		t.Fatal("retry fixture", err)
	}

	return request
}
func retryCustodyExecutor(t *testing.T, j ManagedDockerJournal, transport ManagedDockerTransport, a *retryCustodyAuthority) *ManagedDockerExecutor {
	t.Helper()

	executor, err := NewManagedDockerExecutor(j, transport, a, a, "engine-A")
	if err != nil {
		t.Fatal(err)
	}

	return executor
}
func retryCustodyTrust(request ManagedDockerRequest) *retryCustodyAuthority {
	authority := &retryCustodyAuthority{custodyAuthorityFixture: &custodyAuthorityFixture{requests: map[string]ManagedDockerRequest{}}, fresh: true}
	authority.trust(request)

	return authority
}

func TestManagedDockerRetryExactReceiptReopen(t *testing.T) {
	for _, kind := range []managed.ProviderStepKind{managed.StepRemoveContainer, managed.StepRemoveNetwork} {
		for _, ordinal := range []uint64{1, 2, 32} {
			t.Run(string(kind)+"/"+strconv.FormatUint(ordinal, 10), func(t *testing.T) {
				request := retryCustodyFixture(t, kind, ordinal)
				authority := retryCustodyTrust(request)
				journal, directory := custodyJournalFixture(t)
				transport := &custodyTransportFixture{}
				executor := retryCustodyExecutor(t, journal, transport, authority)

				first, err := executor.Execute(t.Context(), request)
				if err != nil || first.State != "complete" {
					t.Fatal("retry execution", err)
				}

				reopened, err := OpenFileManagedDockerJournal(directory)
				if err != nil {
					t.Fatal(err)
				}

				again, err := retryCustodyExecutor(t, reopened, transport, authority).Execute(t.Context(), request)
				if err != nil || !reflect.DeepEqual(first, again) || transport.calls.Load() != 1 || authority.admits != 1 {
					t.Fatal("retry replay issued another send", err, transport.calls.Load(), authority.admits)
				}
			})
		}
	}
}

func TestManagedDockerRetryRequiresExplicitAuthorityAndFreshGrant(t *testing.T) {
	request := retryCustodyFixture(t, managed.StepRemoveContainer, 1)
	journal, _ := custodyJournalFixture(t)
	ordinary := &custodyAuthorityFixture{requests: map[string]ManagedDockerRequest{}}
	ordinary.trust(request)

	transport := &custodyTransportFixture{}
	if _, err := custodyExecutorFixture(t, journal, transport, ordinary).Execute(t.Context(), request); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal("legacy authority accepted retry", err)
	}

	authority := retryCustodyTrust(request)

	authority.denyRetry = true
	if _, err := retryCustodyExecutor(t, journal, transport, authority).Execute(t.Context(), request); !errors.Is(err, managed.ErrAuthority) {
		t.Fatal("configured retry verifier bypassed", err)
	}

	authority.denyRetry = false

	authority.fresh = false
	if _, err := retryCustodyExecutor(t, journal, transport, authority).Execute(t.Context(), request); !errors.Is(err, managed.ErrAuthority) {
		t.Fatal("saved marker recreated fresh grant", err)
	}

	if transport.calls.Load() != 0 {
		t.Fatal("ungranted send")
	}
}

func TestManagedDockerRetryEvidenceSubstitutionRefuses(t *testing.T) {
	for _, mutation := range []string{"missing marker", "root original", "permission receipt", "current resource", "authorization receipt", "authorization permission", "previous closure", "marker permission", "marker revision", "marker binding count", "current proof custody"} {
		t.Run(mutation, func(t *testing.T) {
			request := retryCustodyFixture(t, managed.StepRemoveContainer, 1)
			authority := retryCustodyTrust(request)
			altered := custodyClone(request)
			e := altered.Retry

			switch mutation {
			case "missing marker":
				e.Marker = managed.RecoveryRecord[managed.ProviderInvocationMarker]{}
			case "root original":
				e.Root.Facts.OriginalPermission.ResourceID = strings.Repeat("9", 64)
				retryCustodyRedigest(t, &e.Root)
			case "permission receipt":
				altered.Receipt.ID = id.New(managed.PrefixOperation)
			case "current resource":
				altered.Permission.ResourceID = strings.Repeat("9", 64)
				e.Permission.Facts.Permission = altered.Permission
				altered.Invocation = &e.Permission.Facts
				retryCustodyRedigest(t, &e.Permission)
			case "authorization receipt":
				e.Authorization.Acceptance.Receipt.Revision = "3"
			case "authorization permission":
				e.Authorization.Facts.Permission.Digest = strings.Repeat("9", 64)
				retryCustodyRedigest(t, &e.Authorization)
			case "previous closure":
				e.Authorization.Facts.Previous.Closure.OperationID = id.New(managed.PrefixOperation)
				e.Authorization.Facts.Previous.Digest, _ = managed.RetryPreviousDigest(e.Authorization.Facts.Previous)
				retryCustodyRedigest(t, &e.Authorization)
			case "marker permission":
				e.Marker.Facts.PermissionDigest = strings.Repeat("9", 64)
				retryCustodyRedigest(t, &e.Marker)
			case "marker revision":
				e.Marker.Acceptance.Receipt.Revision = "4"
			case "marker binding count":
				e.Marker.Facts.BindingCount = "65"
				retryCustodyRedigest(t, &e.Marker)
			case "current proof custody":
				e.Marker.Facts.CurrentProofSetDigest = strings.Repeat("9", 64)
				retryCustodyRedigest(t, &e.Marker)
			}

			journal, _ := custodyJournalFixture(t)

			transport := &custodyTransportFixture{}
			if _, err := retryCustodyExecutor(t, journal, transport, authority).Execute(t.Context(), altered); err == nil {
				t.Fatal("substitution accepted")
			}

			if transport.calls.Load() != 0 || authority.admits != 0 {
				t.Fatal("substitution reached admission or transport")
			}
		})
	}
}

func TestManagedDockerRetryUncertaintyAndClosureNeverResend(t *testing.T) {
	for _, closeFirst := range []bool{false, true} {
		t.Run(strconv.FormatBool(closeFirst), func(t *testing.T) {
			request := retryCustodyFixture(t, managed.StepRemoveContainer, 1)
			authority := retryCustodyTrust(request)
			journal, directory := custodyJournalFixture(t)
			transport := &custodyTransportFixture{fail: !closeFirst}

			executor := retryCustodyExecutor(t, journal, transport, authority)
			if _, err := executor.Prepare(t.Context(), request); err != nil {
				t.Fatal(err)
			}

			if closeFirst {
				if _, err := executor.WitnessManagedProviderNoDispatch(t.Context(), request.Invocation.Key); err != nil {
					t.Fatal(err)
				}
			}

			if _, err := executor.Execute(t.Context(), request); err == nil {
				t.Fatal("closed or ambiguous retry succeeded")
			}

			reopened, err := OpenFileManagedDockerJournal(directory)
			if err != nil {
				t.Fatal(err)
			}

			if _, err = retryCustodyExecutor(t, reopened, transport, authority).Execute(t.Context(), request); !errors.Is(err, managed.ErrUnavailable) {
				t.Fatal("reopened retry sent", err)
			}

			want := int32(1)
			if closeFirst {
				want = 0
			}

			if transport.calls.Load() != want {
				t.Fatal("send count", transport.calls.Load(), want)
			}

			if !closeFirst {
				if _, err = executor.WitnessManagedProviderNoDispatch(t.Context(), request.Invocation.Key); !errors.Is(err, managed.ErrUnavailable) {
					t.Fatal("uncertainty became no-dispatch", err)
				}
			}
		})
	}
}

func TestManagedDockerOrdinalZeroEncodingUnchanged(t *testing.T) {
	request := custodyRequestFixture(managed.StepRemoveContainer)
	legacy := struct {
		BackendAuthority string                                `json:"backend_authority"`
		Permission       managed.WireProviderStepPermission    `json:"permission"`
		Receipt          managed.WireReceipt                   `json:"receipt"`
		Command          ManagedDockerCommand                  `json:"command"`
		Binding          *managed.ProviderEffectBindingFacts   `json:"binding"`
		Invocation       *managed.ProviderInvocationPermission `json:"invocation"`
	}{request.BackendAuthority, request.Permission, request.Receipt, request.Command, request.Binding, request.Invocation}

	old, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}

	current, err := json.Marshal(request)
	if err != nil || !bytes.Equal(old, current) {
		t.Fatal("ordinal-zero bytes changed", err)
	}

	altered := custodyClone(request)

	altered.Retry = &ManagedDockerRetryEvidence{}
	if custodyKey(altered.Permission) != custodyKey(request.Permission) {
		t.Fatal("redundant evidence created another journal address")
	}
}

func TestManagedDockerRetryLostClaimNeverSends(t *testing.T) {
	request := retryCustodyFixture(t, managed.StepRemoveContainer, 1)
	authority := retryCustodyTrust(request)
	journal, directory := custodyJournalFixture(t)
	transport := &custodyTransportFixture{}

	executor := retryCustodyExecutor(t, &lostClaimJournal{ManagedDockerJournal: journal}, transport, authority)
	if _, err := executor.Execute(t.Context(), request); err == nil {
		t.Fatal("lost claim acknowledged")
	}

	reopened, err := OpenFileManagedDockerJournal(directory)
	if err != nil {
		t.Fatal(err)
	}

	executor = retryCustodyExecutor(t, reopened, transport, authority)
	if _, err = executor.Execute(t.Context(), request); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatal("claim replay", err)
	}

	retained, err := executor.Read(t.Context(), request)
	if err != nil || retained.State != "delegated" || !reflect.DeepEqual(retained.Request.Retry.Marker, request.Retry.Marker) {
		t.Fatal("lost durable marker custody", err)
	}

	if transport.calls.Load() != 0 || authority.admits != 1 {
		t.Fatal("ambiguous claim sent or readmitted", transport.calls.Load(), authority.admits)
	}
}
