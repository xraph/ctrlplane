package managedstate

import (
	"context"
	"encoding/json"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

// Batch inspection observes the same pending rows as point reads. The backing
// transaction still validates transport membership and all unstaged results.
func (staged *recoveryActivationRows) ReadImmutableRecords(ctx context.Context, keys []ControllerRecordKey) ([]ControllerRecordResult, error) {
	if err := ValidateImmutableRecordKeys(keys); err != nil {
		return nil, err
	}

	rows, err := staged.ControllerTransaction.ReadImmutableRecords(ctx, keys)
	if err != nil {
		return nil, err
	}

	return staged.overlayRecordBatch(ctx, keys, rows, ImmutableRecordLimit)
}

func (staged *recoveryActivationRows) ReadHeldRecords(ctx context.Context, keys []ControllerRecordKey) ([]ControllerRecordResult, error) {
	if err := ValidateHeldRecordKeys(keys); err != nil {
		return nil, err
	}

	rows, err := staged.ControllerTransaction.ReadHeldRecords(ctx, keys)
	if err != nil {
		return nil, err
	}

	return staged.overlayRecordBatch(ctx, keys, rows, HeldRecordLimit)
}

func (staged *recoveryActivationRows) overlayRecordBatch(ctx context.Context, keys []ControllerRecordKey, rows []ControllerRecordResult, limit func(string) int) ([]ControllerRecordResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if len(rows) != len(keys) {
		return nil, managed.ErrUnavailable
	}

	expected := make(map[ControllerRecordKey]bool, len(keys))
	for _, key := range keys {
		expected[key] = true
	}

	seen := make(map[ControllerRecordKey]bool, len(rows))

	result := make([]ControllerRecordResult, 0, len(rows))
	for _, row := range rows {
		key := row.ControllerRecordKey
		if !expected[key] || seen[key] || row.Size < 0 || !row.Found && (row.Size != 0 || len(row.Data) != 0) || row.Found && row.Size <= limit(row.Kind) && len(row.Data) != row.Size || row.Size > limit(row.Kind) && len(row.Data) != 0 {
			return nil, managed.ErrUnavailable
		}

		seen[key] = true
		for _, pending := range staged.rows {
			if pending.kind != key.Kind || pending.scope != key.Scope || pending.key != key.Key {
				continue
			}

			row = ControllerRecordResult{ControllerRecordKey: key, Found: true, Size: len(pending.raw)}
			if len(pending.raw) <= limit(key.Kind) {
				row.Data = append([]byte(nil), pending.raw...)
			}

			break
		}

		result = append(result, row)
	}

	return result, nil
}

func (staged *recoveryActivationRows) ReadImmutableReceipts(ctx context.Context, key managed.Key, operations []id.ID) ([]ControllerReceiptResult, error) {
	if err := ValidateImmutableReceiptKeys(key, operations); err != nil {
		return nil, err
	}

	rows, err := staged.ControllerTransaction.ReadImmutableReceipts(ctx, key, operations)
	if err != nil {
		return nil, err
	}

	if err = ctx.Err(); err != nil {
		return nil, err
	}

	if len(rows) != len(operations) {
		return nil, managed.ErrUnavailable
	}

	expected := make(map[id.ID]bool, len(operations))
	for _, operation := range operations {
		expected[operation] = true
	}

	seen := make(map[id.ID]bool, len(rows))
	for index, row := range rows {
		if !expected[row.OperationID] || seen[row.OperationID] || row.Found && row.Receipt.ID != row.OperationID {
			return nil, managed.ErrUnavailable
		}

		seen[row.OperationID] = true
		if key == staged.key && row.OperationID == staged.receipt.ID {
			raw, encodeErr := json.Marshal(staged.receipt)
			if encodeErr != nil || len(raw) > managed.MaxAttemptReceipt {
				return nil, managed.ErrUnavailable
			}

			rows[index] = ControllerReceiptResult{OperationID: row.OperationID, Found: true, Receipt: staged.receipt}
		}
	}

	return rows, nil
}
