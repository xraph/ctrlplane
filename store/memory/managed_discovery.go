package memory

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"strconv"

	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

// ListDiscoveryWork reads at most limit contiguous authority ordinals under the mutex.
// Accepted references are append-only in this slice, so no retained-history scan is needed.
func (t managedControllerTransaction) ListDiscoveryWork(ctx context.Context, scope, after string, limit int) ([]managedstate.DiscoveryWorkRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	ordinal, err := strconv.ParseUint(after, 10, 64)
	if err != nil || ordinal > math.MaxInt64 || after != fmt.Sprintf("%020d", ordinal) || limit < 1 || limit > managed.MaxWorkPageItems+1 {
		return nil, managed.ErrInvalid
	}

	rows := t.store.managedControllerRecords["work_index:"+scope]

	out := make([]managedstate.DiscoveryWorkRow, 0, limit)
	for range limit {
		if err = ctx.Err(); err != nil {
			return nil, err
		}

		if ordinal == math.MaxInt64 {
			break
		}

		ordinal++
		key := fmt.Sprintf("%020d", ordinal)

		raw, exists := rows[key]
		if !exists {
			break
		}

		row := managedstate.DiscoveryWorkRow{Key: key, Size: len(raw)}
		if len(raw) <= managed.MaxWorkReference {
			row.Data = bytes.Clone(raw)
		}

		out = append(out, row)
	}

	return out, ctx.Err()
}

var _ managedstate.DiscoveryPageTransaction = managedControllerTransaction{}
var _ managed.WorkDiscoveryStore = (*Store)(nil)
var _ managed.PhaseCommandStore = (*Store)(nil)

var _ managed.PhaseOriginalResultStore = (*Store)(nil)
