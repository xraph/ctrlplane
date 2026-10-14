package managedstate

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

func TestGuardedOriginalFingerprintFixesPermissionTimeProofs(t *testing.T) {
	_, key, _ := recoveryFixture()
	request := managed.GuardedOriginalRequest{Command: managed.RecoveryCommand{Key: key.Key, ID: id.New(managed.PrefixOperation), Revision: "8", LeaseEpoch: "2", Owner: "owner", Profile: managed.RemovalRecoveryProfile}}

	for _, count := range []int{1, managed.MaxRecoveryBindings} {
		proofs := make([]managed.RecoveryAssociation, count)
		for i := range proofs {
			proofs[i] = managed.RecoveryAssociation{Kind: managed.RecoveryProofRecord, Key: strings.Repeat("a", 64), Digest: strings.Repeat("b", 64), OperationID: id.New(managed.PrefixOperation), ReceiptDigest: strings.Repeat("c", 64)}
		}

		before, err := guardedOriginalRequestDigest(request, proofs)
		if err != nil {
			t.Fatal(err)
		}

		changed := append([]managed.RecoveryAssociation(nil), proofs...)
		changed[count-1].Digest = strings.Repeat("d", 64)

		after, err := guardedOriginalRequestDigest(request, changed)
		if err != nil || after == before {
			t.Fatal("proof substitution preserved pending acceptance", err)
		}

		record := guardedOriginal{SchemaVersion: "1", Request: request, Proofs: proofs}

		encoded, err := json.Marshal(record)
		if err != nil || len(encoded) > maxGuardedOriginal {
			t.Fatal("bounded proof set exceeded row cap", len(encoded), err)
		}
	}
}

func TestGuardedOriginalActualRowsCapAndCapPlusOne(t *testing.T) {
	for _, kind := range []string{guardedOriginalKind, guardedOriginalSelectionKind, guardedOriginalMaterializationKind} {
		limit := ImmutableRecordLimit(kind)
		if limit == 0 {
			t.Fatal("missing exact row cap", kind)
		}

		fixture, key, _ := recoveryFixture()

		scope, err := attemptScope(key.Key)
		if err != nil {
			t.Fatal(err)
		}
		// Padding tests complete JSON storage boundaries, not valid authority input.
		raw := map[string]string{"padding": ""}

		encoded, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}

		raw["padding"] = strings.Repeat("x", limit-len(encoded))
		if err = attemptWrite(t.Context(), fixture, kind, scope, "cap", raw, true, limit); err != nil {
			t.Fatal(kind, err)
		}

		raw["padding"] += "x"
		if err = attemptWrite(t.Context(), fixture, kind, scope, "over", raw, true, limit); !errors.Is(err, managed.ErrCapacity) {
			t.Fatal(kind, err)
		}

		if _, err = fixture.ReadRecord(t.Context(), kind, scope, "over"); !errors.Is(err, managed.ErrNotFound) {
			t.Fatal("oversized row persisted", kind, err)
		}
	}
}

func TestGuardedOrdinalZeroRequiresItsFixedProofSet(t *testing.T) {
	selected := retrySelected{current: managed.ProviderStepRetryCurrent{Ordinal: "0"}, permission: managed.RecoveryRecord[managed.ProviderInvocationPermission]{Facts: managed.ProviderInvocationPermission{PermissionProofCount: "0"}}}
	if invocationGuardedOriginal(selected) {
		t.Fatal("legacy original became guarded")
	}

	selected.permission.Facts.PermissionProofCount = "1"
	if invocationGuardedOriginal(selected) {
		t.Fatal("missing proof commitment became guarded")
	}

	selected.permission.Facts.PermissionProofSetDigest = strings.Repeat("a", 64)
	if !invocationGuardedOriginal(selected) {
		t.Fatal("guarded original lost proof requirement")
	}

	selected.current.Ordinal = "1"
	if invocationGuardedOriginal(selected) {
		t.Fatal("retry misclassified as original")
	}
}
