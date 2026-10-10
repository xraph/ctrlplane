package postgres

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/xraph/grove/driver"

	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

type managedControllerTransaction struct {
	managedBackend

	clock time.Time
}

func (b managedBackend) ControllerAtomic(ctx context.Context, serial string, keys []managed.Key, change func(managedstate.ControllerTransaction) error) error {
	tx, err := b.store.pg.BeginTxQuery(ctx, &driver.TxOptions{IsolationLevel: driver.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// The global controller lock serializes absent receipt, owner and cursor keys.
	// A lifecycle mutations still serialize on the physical parent row below.
	if _, err = tx.NewRaw("SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "ctrlplane-managed-controller").Exec(ctx); err != nil {
		return err
	}

	keys = slices.Clone(keys)
	slices.SortFunc(keys, func(a, b managed.Key) int { return strings.Compare(a.InstanceID.String(), b.InstanceID.String()) })

	previous := ""
	for _, key := range keys {
		if key.InstanceID.String() == previous {
			continue
		}

		previous = key.InstanceID.String()

		var body []byte

		err = tx.NewRaw("SELECT data FROM cp_managed_targets WHERE id=$1 FOR UPDATE", key.InstanceID.String()).Scan(ctx, &body)
		if err != nil && !isNoRows(err) {
			return err
		}

		if err == nil {
			if _, err = decodeManaged(body, key.TenantID); err != nil {
				return err
			}
		}
	}

	var micros int64
	if err = tx.NewRaw("SELECT (extract(epoch FROM clock_timestamp())*1000000)::bigint").Scan(ctx, &micros); err != nil {
		return err
	}

	_ = serial

	scoped := managedControllerTransaction{managedBackend: managedBackend{store: b.store, tx: tx}, clock: time.UnixMicro(micros).UTC()}
	if err = change(scoped); err != nil {
		return err
	}

	return tx.Commit()
}

func (t managedControllerTransaction) Now() time.Time { return t.clock }
func (t managedControllerTransaction) ReadRecord(ctx context.Context, kind, scope, key string) ([]byte, error) {
	var raw []byte

	err := t.tx.NewRaw("SELECT data FROM cp_managed_controller_records WHERE kind=$1 AND scope=$2 AND key=$3", kind, scope, key).Scan(ctx, &raw)
	if isNoRows(err) {
		return nil, managed.ErrNotFound
	}

	return raw, err
}
func (t managedControllerTransaction) WriteRecord(ctx context.Context, kind, scope, key string, raw []byte, insert bool) error {
	if insert {
		_, err := t.tx.NewRaw("INSERT INTO cp_managed_controller_records(kind,scope,key,data) VALUES($1,$2,$3,$4)", kind, scope, key, raw).Exec(ctx)

		return managedConflict(err)
	}

	result, err := t.tx.NewRaw("UPDATE cp_managed_controller_records SET data=$4 WHERE kind=$1 AND scope=$2 AND key=$3", kind, scope, key, raw).Exec(ctx)
	if err != nil {
		return err
	}

	count, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if count != 1 {
		return managed.ErrNotFound
	}

	return nil
}
func (t managedControllerTransaction) ListRecords(ctx context.Context, kind, scope, after string, limit int) ([]managedstate.ControllerRecord, error) {
	var rows []struct {
		Key  string `grove:"key"`
		Data []byte `grove:"data"`
	}

	err := t.tx.NewRaw("SELECT key,data FROM cp_managed_controller_records WHERE kind=$1 AND scope=$2 AND key>$3 ORDER BY key LIMIT $4", kind, scope, after, limit).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	out := make([]managedstate.ControllerRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, managedstate.ControllerRecord{Key: row.Key, Data: row.Data})
	}

	return out, nil
}
