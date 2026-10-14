package managedstate

import (
	"errors"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

func TestRecoveryRecordAssociationCanonicalParity(t *testing.T) {
	key := managed.AttemptKey{Key: managed.Key{TenantID: "association", InstanceID: id.New(id.PrefixInstance)}, AttemptID: id.New(managed.PrefixOperation), Generation: "1"}
	ref := managed.AttemptRef{AttemptID: key.AttemptID, Generation: key.Generation}
	binding := managed.RemovalRecoveryBinding{Key: managed.AttemptBindingKey{AttemptKey: key, ReservationID: id.New(managed.PrefixOperation)}}
	step := managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: key.Key, ActionID: id.New(managed.PrefixOperation)}, ProviderOperationID: id.New(managed.PrefixOperation), Kind: managed.StepRemoveContainer, Service: "api"}
	invocation := managed.ProviderInvocationKey{AttemptKey: key, Step: step, CommandID: id.New(managed.PrefixOperation)}
	bindingDigest := managed.Digest([]byte("binding"))

	cases := []struct {
		kind    managed.RecoveryRecordKind
		facts   any
		subject any
	}{
		{managed.RecoveryActivationRecord, managed.RemovalRecoveryActivation{Key: key, AttemptRef: ref}, key},
		{managed.RecoveryProofRecord, managed.RemovalRevalidationFacts{Binding: binding}, binding.Key},
		{managed.RecoveryIntentRecord, managed.RecoveryNativeIntent{Binding: binding, Operation: "prepare"}, struct {
			Binding   managed.AttemptBindingKey `json:"binding"`
			Operation string                    `json:"operation"`
		}{binding.Key, "prepare"}},
		{managed.RecoveryCommitRecord, managed.ProviderNativeCommitFacts{Binding: binding, Invocation: invocation}, struct {
			Binding    managed.AttemptBindingKey     `json:"binding"`
			Invocation managed.ProviderInvocationKey `json:"invocation"`
		}{binding.Key, invocation}},
		{managed.RecoveryCloseRecord, managed.RecoveryNativeCloseFacts{Binding: binding}, binding.Key},
		{managed.RecoveryRootRecord, managed.ProviderStepRetryRoot{AttemptRef: ref, Step: step}, step},
		{managed.RecoveryPermissionProofRecord, managed.ProviderPermissionProof{Invocation: invocation, BindingDigest: bindingDigest}, struct {
			Invocation    managed.ProviderInvocationKey `json:"invocation"`
			BindingDigest string                        `json:"binding_digest"`
		}{invocation, bindingDigest}},
		{managed.RecoveryPermissionRecord, managed.ProviderInvocationPermission{Key: invocation}, invocation},
		{managed.RecoveryAuthorizationRecord, managed.ProviderStepRetryAuthorization{Key: invocation}, invocation},
		{managed.RecoveryMarkerRecord, managed.ProviderInvocationMarker{Key: invocation}, invocation},
		{managed.RecoveryClosureRecord, managed.ProviderInvocationClosure{Key: invocation}, invocation},
		{managed.RecoveryFailureRecord, managed.ProviderInvocationFailure{Key: invocation}, invocation},
		{managed.RecoveryResultRecord, managed.ProviderInvocationResult{Key: invocation}, invocation},
		{managed.RecoveryCutoffRecord, managed.RemovalCutoffFacts{Key: key, AttemptRef: ref}, key},
		{managed.RecoveryCoverageRecord, managed.EffectiveRemovalCoverage{Key: key, AttemptRef: ref}, key},
		{managed.RecoveryCoverageBindingRecord, managed.EffectiveRemovalBinding{Binding: binding}, binding.Key},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			operation := id.New(managed.PrefixOperation)
			request := managed.Digest([]byte("request"))

			factDigest, err := managed.RecoveryFactDigest(tc.facts)
			if err != nil {
				t.Fatal(err)
			}

			record := managed.RecoveryRecord[any]{SchemaVersion: "1", Kind: tc.kind, Facts: tc.facts, Digest: factDigest, Acceptance: managed.RecoveryAcceptance{OperationID: operation, RequestDigest: request, Receipt: managed.WireReceipt{ID: operation, Digest: request, Revision: "2", LeaseEpoch: "1", AcceptedAt: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)}}}
			// Freeze the original envelope, field order and each typed subject independently
			// of the production switch. The managed API and store must address identical rows.
			expected, err := attemptHash("ctrlplane.managed.recovery-fact-key.v1", struct {
				Key         managed.AttemptKey         `json:"key"`
				Kind        managed.RecoveryRecordKind `json:"kind"`
				OperationID id.ID                      `json:"operation_id"`
				Subject     any                        `json:"subject"`
			}{key, tc.kind, operation, tc.subject})
			if err != nil {
				t.Fatal(err)
			}

			internal, err := recoveryFactKey(key, tc.kind, operation, tc.facts)
			if err != nil || internal != expected {
				t.Fatal("internal canonical key changed", internal, expected, err)
			}

			actual, err := managed.RecoveryRecordAssociation(key, record)
			if err != nil {
				t.Fatal(err)
			}

			old, err := managed.RecoveryAssociationFor(expected, record)
			if err != nil || actual != old {
				t.Fatal("public association differs from retained address", err)
			}

			for _, mutate := range []func(*managed.AttemptKey){
				func(k *managed.AttemptKey) { k.TenantID += "-other" },
				func(k *managed.AttemptKey) { k.InstanceID = id.New(id.PrefixInstance) },
				func(k *managed.AttemptKey) { k.AttemptID = id.New(managed.PrefixOperation) },
				func(k *managed.AttemptKey) { k.Generation = "2" },
			} {
				other := key
				mutate(&other)

				if _, err = managed.RecoveryRecordAssociation(other, record); !errors.Is(err, managed.ErrAuthority) {
					t.Fatal("foreign attempt accepted", err)
				}
			}

			bad := record

			bad.Digest = managed.Digest([]byte("tampered"))
			if _, err = managed.RecoveryRecordAssociation(key, bad); !errors.Is(err, managed.ErrInvalid) {
				t.Fatal("tampered fact digest accepted", err)
			}

			bad = record

			bad.Acceptance.Receipt.Digest = managed.Digest([]byte("tampered"))
			if _, err = managed.RecoveryRecordAssociation(key, bad); !errors.Is(err, managed.ErrInvalid) {
				t.Fatal("tampered receipt accepted", err)
			}
		})
	}
}

func TestRecoveryFactKeyValidationParity(t *testing.T) {
	valid := managed.AttemptKey{Key: managed.Key{TenantID: "association", InstanceID: id.New(id.PrefixInstance)}, AttemptID: id.New(managed.PrefixOperation), Generation: "1"}
	operation := id.New(managed.PrefixOperation)

	for _, mutate := range []func(*managed.AttemptKey){
		func(k *managed.AttemptKey) { k.Generation = "0" },
		func(k *managed.AttemptKey) { k.Generation = "01" },
		func(k *managed.AttemptKey) { k.Generation = "9223372036854775808" },
		func(k *managed.AttemptKey) { k.TenantID = "invalid space" },
		func(k *managed.AttemptKey) { k.InstanceID = operation },
		func(k *managed.AttemptKey) { k.AttemptID = valid.InstanceID },
	} {
		key := valid
		mutate(&key)

		_, err := managed.RecoveryFactKey(key, managed.RecoveryRootRecord, operation, managed.ProviderStepRetryRoot{})
		if !errors.Is(err, attemptVersion(key)) {
			t.Fatal("key validation changed", err, attemptVersion(key))
		}
	}

	if _, err := managed.RecoveryFactKey(valid, managed.RecoveryRootRecord, operation, struct{}{}); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatal("unknown fact supported", err)
	}

	if _, err := managed.RecoveryFactKey(valid, managed.RecoveryRootRecord, valid.InstanceID, managed.ProviderStepRetryRoot{}); !errors.Is(err, managed.ErrInvalid) {
		t.Fatal("invalid operation accepted", err)
	}

	if _, err := managed.RecoveryFactKey(valid, "unknown", operation, managed.ProviderStepRetryRoot{}); !errors.Is(err, managed.ErrInvalid) {
		t.Fatal("unknown kind accepted", err)
	}
}

func TestRecoveryRecordAssociationNestedAttemptRefusal(t *testing.T) {
	key := managed.AttemptKey{Key: managed.Key{TenantID: "association", InstanceID: id.New(id.PrefixInstance)}, AttemptID: id.New(managed.PrefixOperation), Generation: "1"}
	other := key
	other.AttemptID = id.New(managed.PrefixOperation)
	wrongRef := managed.AttemptRef{AttemptID: other.AttemptID, Generation: key.Generation}
	binding := managed.RemovalRecoveryBinding{Key: managed.AttemptBindingKey{AttemptKey: key}}
	wrongStep := managed.ProviderStepKey{OperationKey: managed.OperationKey{Key: managed.Key{TenantID: "other", InstanceID: key.InstanceID}}}

	cases := []struct {
		kind  managed.RecoveryRecordKind
		facts any
	}{
		{managed.RecoveryActivationRecord, managed.RemovalRecoveryActivation{Key: key, AttemptRef: wrongRef}},
		{managed.RecoveryCommitRecord, managed.ProviderNativeCommitFacts{Binding: binding, Invocation: managed.ProviderInvocationKey{AttemptKey: other}}},
		{managed.RecoveryRootRecord, managed.ProviderStepRetryRoot{Step: wrongStep, AttemptRef: managed.AttemptRef{AttemptID: key.AttemptID, Generation: key.Generation}}},
		{managed.RecoveryPermissionRecord, managed.ProviderInvocationPermission{Key: managed.ProviderInvocationKey{AttemptKey: key, Step: wrongStep}}},
		{managed.RecoveryCutoffRecord, managed.RemovalCutoffFacts{Key: key, AttemptRef: wrongRef}},
		{managed.RecoveryCoverageRecord, managed.EffectiveRemovalCoverage{Key: key, AttemptRef: wrongRef}},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			operation := id.New(managed.PrefixOperation)
			request := managed.Digest([]byte("request"))

			factDigest, err := managed.RecoveryFactDigest(tc.facts)
			if err != nil {
				t.Fatal(err)
			}

			record := managed.RecoveryRecord[any]{SchemaVersion: "1", Kind: tc.kind, Facts: tc.facts, Digest: factDigest, Acceptance: managed.RecoveryAcceptance{OperationID: operation, RequestDigest: request, Receipt: managed.WireReceipt{ID: operation, Digest: request, Revision: "2", LeaseEpoch: "1", AcceptedAt: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)}}}
			if _, err = managed.RecoveryRecordAssociation(key, record); !errors.Is(err, managed.ErrAuthority) {
				t.Fatal("contradictory nested attempt accepted", err)
			}
		})
	}
}
