package postgres

import (
	"context"

	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

// ListDiscoveryWork bounds both indexed row count and payload allocation in SQL.
func (t managedControllerTransaction) ListDiscoveryWork(ctx context.Context, scope, after string, limit int) ([]managedstate.DiscoveryWorkRow, error) {
	if t.tx == nil {
		return nil, managed.ErrUnavailable
	}

	if limit < 1 || limit > managed.MaxWorkPageItems+1 {
		return nil, managed.ErrInvalid
	}

	var rows []struct {
		Key  string `grove:"key"`
		Size int    `grove:"size"`
		Data []byte `grove:"data"`
	}
	if err := t.tx.NewRaw("SELECT key,octet_length(data) AS size,CASE WHEN octet_length(data)<=2048 THEN data END AS data FROM cp_managed_controller_records WHERE kind='work_index' AND scope=$1 AND key>$2 ORDER BY key LIMIT $3", scope, after, limit).Scan(ctx, &rows); err != nil {
		return nil, err
	}

	out := make([]managedstate.DiscoveryWorkRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, managedstate.DiscoveryWorkRow{Key: row.Key, Size: row.Size, Data: row.Data})
	}

	return out, ctx.Err()
}

var _ managedstate.DiscoveryPageTransaction = managedControllerTransaction{}
var _ managed.WorkDiscoveryStore = (*Store)(nil)

var _ managed.PhaseCommandStore = (*Store)(nil)

var _ managed.PhaseOriginalResultStore = (*Store)(nil)
