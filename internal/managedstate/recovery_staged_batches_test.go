package managedstate

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

type recoveryAbsentBatch struct{ ControllerTransaction }

func (*recoveryAbsentBatch) ReadImmutableRecords(_ context.Context, keys []ControllerRecordKey) ([]ControllerRecordResult, error) {
	rows := make([]ControllerRecordResult, len(keys))
	for i, key := range keys {
		rows[i].ControllerRecordKey = key
	}

	return rows, nil
}
func (b *recoveryAbsentBatch) ReadHeldRecords(ctx context.Context, keys []ControllerRecordKey) ([]ControllerRecordResult, error) {
	return b.ReadImmutableRecords(ctx, keys)
}
func (*recoveryAbsentBatch) ReadImmutableReceipts(_ context.Context, _ managed.Key, operations []id.ID) ([]ControllerReceiptResult, error) {
	rows := make([]ControllerReceiptResult, len(operations))
	for i, operation := range operations {
		rows[i].OperationID = operation
	}

	return rows, nil
}

func TestRecoveryStagedBatchesObservePendingTerminalRows(t *testing.T) {
	_, target, _, _ := activationMaterializationFixture(t, 1, 1, false) //nolint:dogsled // Only the target identity is needed for the batch overlay fixture.
	staged := &recoveryActivationRows{ControllerTransaction: &recoveryAbsentBatch{}, key: target.Key, receipt: managed.Receipt{ID: id.New(managed.PrefixOperation), Digest: strings.Repeat("d", 64)}}
	scope, key := strings.Repeat("a", 64), strings.Repeat("b", 64)

	for _, kind := range []string{"removal_command", "removal_binding_settlement", retryCurrentKind} {
		raw := []byte(`{"pending":true}`)
		if err := staged.WriteRecord(t.Context(), kind, scope, key, raw, true); err != nil {
			t.Fatal(err)
		}

		selected := []ControllerRecordKey{{kind, scope, key}}

		read := staged.ReadImmutableRecords
		if kind == retryCurrentKind {
			read = staged.ReadHeldRecords
		}

		rows, err := read(t.Context(), selected)
		if err != nil || len(rows) != 1 || !rows[0].Found || rows[0].Size != len(raw) || !bytes.Equal(rows[0].Data, raw) {
			t.Fatal("pending row hidden by underlying absence", rows, err)
		}

		rows[0].Data[0] = 'x'

		again, err := read(t.Context(), selected)
		if err != nil || !bytes.Equal(again[0].Data, raw) {
			t.Fatal("batch aliases staging", err)
		}
	}

	rows, err := staged.ReadImmutableReceipts(t.Context(), target.Key, []id.ID{staged.receipt.ID})
	if err != nil || len(rows) != 1 || !rows[0].Found || rows[0].Receipt != staged.receipt {
		t.Fatal("pending receipt hidden", rows, err)
	}

	other := target.Key
	other.InstanceID = id.New(id.PrefixInstance)

	rows, err = staged.ReadImmutableReceipts(t.Context(), other, []id.ID{staged.receipt.ID})
	if err != nil || rows[0].Found {
		t.Fatal("receipt escaped parent scope", err)
	}
}

func TestRecoveryStagedBatchCapsAndMalformedMembership(t *testing.T) {
	key := ControllerRecordKey{"removal_binding_settlement", strings.Repeat("a", 64), strings.Repeat("b", 64)}

	for _, size := range []int{managed.MaxAttemptFact, managed.MaxAttemptFact + 1} {
		staged := &recoveryActivationRows{ControllerTransaction: &recoveryAbsentBatch{}}
		if err := staged.WriteRecord(t.Context(), key.Kind, key.Scope, key.Key, bytes.Repeat([]byte("x"), size), true); err != nil {
			t.Fatal(err)
		}

		rows, err := staged.ReadImmutableRecords(t.Context(), []ControllerRecordKey{key})
		if err != nil || !rows[0].Found || rows[0].Size != size || size == managed.MaxAttemptFact && len(rows[0].Data) != size || size > managed.MaxAttemptFact && len(rows[0].Data) != 0 {
			t.Fatal("cap changed", rows, err)
		}

		if _, err = staged.ReadImmutableRecords(t.Context(), []ControllerRecordKey{key, key}); err == nil {
			t.Fatal("duplicate key accepted")
		}

		if _, err = staged.overlayRecordBatch(t.Context(), []ControllerRecordKey{key}, nil, ImmutableRecordLimit); err == nil {
			t.Fatal("missing batch member accepted")
		}

		if _, err = staged.overlayRecordBatch(t.Context(), []ControllerRecordKey{key}, []ControllerRecordResult{{ControllerRecordKey: key, Found: false, Size: 1}}, ImmutableRecordLimit); err == nil {
			t.Fatal("malformed absence accepted")
		}
	}
}
