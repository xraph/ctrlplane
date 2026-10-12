package postgres

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/xraph/grove/driver"

	"github.com/xraph/ctrlplane/id"
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

// ReadReceipt requires the caller's validated target under the existing parent lock.
// It reads current transaction data and never creates or repairs a receipt.
func (t managedControllerTransaction) ReadReceipt(ctx context.Context, key managed.Key, operation id.ID, digest string) (managed.Receipt, error) {
	if err := ctx.Err(); err != nil {
		return managed.Receipt{}, err
	}

	var row struct {
		TenantID string `grove:"tenant_id"`
		Receipt  []byte `grove:"receipt"`
	}

	err := t.tx.NewRaw(`SELECT p.tenant_id,c.receipt FROM cp_managed_targets p LEFT JOIN cp_managed_commands c ON c.target_id=p.id AND c.operation_id=$2 WHERE p.id=$1`, key.InstanceID.String(), operation.String()).Scan(ctx, &row)
	if isNoRows(err) {
		return managed.Receipt{}, managed.ErrNotFound
	}

	if err != nil {
		return managed.Receipt{}, err
	}

	if row.TenantID != key.TenantID {
		return managed.Receipt{}, managed.ErrAuthority
	}

	if len(row.Receipt) == 0 {
		return managed.Receipt{}, managed.ErrNotFound
	}

	var receipt managed.Receipt
	if json.Unmarshal(row.Receipt, &receipt) != nil || receipt.ID != operation {
		return managed.Receipt{}, managed.ErrUnavailable
	}

	if receipt.Digest != digest {
		return managed.Receipt{}, managed.ErrConflict
	}

	return receipt, nil
}

// ReadImmutableRecords selects only the exact bounded tuples in the held transaction.
func (t managedControllerTransaction) ReadImmutableRecords(ctx context.Context, keys []managedstate.ControllerRecordKey) ([]managedstate.ControllerRecordResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := managedstate.ValidateImmutableRecordKeys(keys); err != nil {
		return nil, err
	}

	return t.readSelectedRecords(ctx, keys, managedstate.ImmutableRecordLimit)
}

// ReadHeldRecords selects provider rows only for the caller's no-write interval.
func (t managedControllerTransaction) ReadHeldRecords(ctx context.Context, keys []managedstate.ControllerRecordKey) ([]managedstate.ControllerRecordResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := managedstate.ValidateHeldRecordKeys(keys); err != nil {
		return nil, err
	}

	return t.readSelectedRecords(ctx, keys, managedstate.HeldRecordLimit)
}

func (t managedControllerTransaction) readSelectedRecords(ctx context.Context, keys []managedstate.ControllerRecordKey, limit func(string) int) ([]managedstate.ControllerRecordResult, error) {
	values := make([]string, 0, len(keys))

	args := make([]any, 0, len(keys)*5)
	for n, selected := range keys {
		refs := make([]string, 5)
		for i := range refs {
			refs[i] = "$" + strconv.Itoa(len(args)+i+1)
		}

		values = append(values, "("+refs[0]+"::int,"+refs[1]+"::text,"+refs[2]+"::text,"+refs[3]+"::text,"+refs[4]+"::int)")
		args = append(args, n, selected.Kind, selected.Scope, selected.Key, limit(selected.Kind))
	}

	var rows []struct {
		Position int    `grove:"position"`
		Kind     string `grove:"kind"`
		Scope    string `grove:"scope"`
		Key      string `grove:"key"`
		Found    bool   `grove:"found"`
		Size     int    `grove:"size"`
		Data     []byte `grove:"data"`
	}

	query := "WITH requested(position,kind,scope,key,cap) AS (VALUES " + strings.Join(values, ",") + ") SELECT q.position,q.kind,q.scope,q.key,r.key IS NOT NULL AS found,COALESCE(octet_length(r.data),0) AS size,CASE WHEN octet_length(r.data)<=q.cap THEN r.data END AS data FROM requested q LEFT JOIN cp_managed_controller_records r ON r.kind=q.kind AND r.scope=q.scope AND r.key=q.key ORDER BY q.position"
	if err := t.tx.NewRaw(query, args...).Scan(ctx, &rows); err != nil {
		return nil, err
	}

	if len(rows) != len(keys) {
		return nil, managed.ErrUnavailable
	}

	out := make([]managedstate.ControllerRecordResult, 0, len(rows))
	for n, row := range rows {
		selected := managedstate.ControllerRecordKey{Kind: row.Kind, Scope: row.Scope, Key: row.Key}
		if row.Position != n || selected != keys[n] {
			return nil, managed.ErrUnavailable
		}

		out = append(out, managedstate.ControllerRecordResult{ControllerRecordKey: selected, Found: row.Found, Size: row.Size, Data: row.Data})
	}

	return out, ctx.Err()
}

// ReadImmutableReceipts retains exact parent and operation checks without decoding the target.
func (t managedControllerTransaction) ReadImmutableReceipts(ctx context.Context, key managed.Key, operations []id.ID) ([]managedstate.ControllerReceiptResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := managedstate.ValidateImmutableReceiptKeys(key, operations); err != nil {
		return nil, err
	}

	values := make([]string, 0, len(operations))

	args := []any{key.InstanceID.String()}
	for n, operation := range operations {
		a := "$" + strconv.Itoa(len(args)+1)
		b := "$" + strconv.Itoa(len(args)+2)
		values = append(values, "("+a+"::int,"+b+"::text)")
		args = append(args, n, operation.String())
	}

	var rows []struct {
		Position      int    `grove:"position"`
		Operation     string `grove:"operation"`
		Parent        bool   `grove:"parent"`
		Tenant        string `grove:"tenant"`
		PayloadTenant string `grove:"payload_tenant"`
		PayloadTarget string `grove:"payload_target"`
		Found         bool   `grove:"found"`
		Size          int    `grove:"size"`
		Data          []byte `grove:"data"`
	}

	query := "WITH requested(position,operation) AS (VALUES " + strings.Join(values, ",") + ") SELECT q.position,q.operation,p.id IS NOT NULL AS parent,COALESCE(p.tenant_id,'') AS tenant,COALESCE(p.data->>'tenant_id','') AS payload_tenant,COALESCE(p.data->>'instance_id','') AS payload_target,c.operation_id IS NOT NULL AS found,COALESCE(octet_length(c.receipt::text),0) AS size,CASE WHEN octet_length(c.receipt::text)<=2048 THEN c.receipt END AS data FROM requested q LEFT JOIN cp_managed_targets p ON p.id=$1 LEFT JOIN cp_managed_commands c ON c.target_id=p.id AND c.operation_id=q.operation ORDER BY q.position"
	if err := t.tx.NewRaw(query, args...).Scan(ctx, &rows); err != nil {
		return nil, err
	}

	if len(rows) != len(operations) {
		return nil, managed.ErrUnavailable
	}

	out := make([]managedstate.ControllerReceiptResult, 0, len(rows))
	for n, row := range rows {
		if row.Position != n || row.Operation != operations[n].String() {
			return nil, managed.ErrUnavailable
		}

		if !row.Parent {
			return nil, managed.ErrNotFound
		}

		if row.Tenant != key.TenantID {
			return nil, managed.ErrAuthority
		}

		if row.PayloadTenant != key.TenantID || row.PayloadTarget != key.InstanceID.String() {
			return nil, managed.ErrUnavailable
		}

		var receipt managed.Receipt
		if row.Found && (row.Size > managed.MaxAttemptReceipt || json.Unmarshal(row.Data, &receipt) != nil || receipt.ID != operations[n]) {
			return nil, managed.ErrUnavailable
		}

		out = append(out, managedstate.ControllerReceiptResult{OperationID: operations[n], Found: row.Found, Receipt: receipt})
	}

	return out, ctx.Err()
}

// ApplyQuiescenceObservationBatch stages fixed typed artifacts under existing held authority.
func (t managedControllerTransaction) ApplyQuiescenceObservationBatch(ctx context.Context, q managed.QuiescenceBatchAcceptanceRequest) (managed.QuiescenceBatchReceipt, error) {
	if t.tx == nil {
		return managed.QuiescenceBatchReceipt{}, managed.ErrUnsupported
	}

	revision, err := q.Command.Revision.Uint64()
	if err != nil {
		return managed.QuiescenceBatchReceipt{}, err
	}

	hook := &quiescenceBatchStager{tx: t, key: q.Command.Key, batch: q.Command.ID, revision: revision}

	return managedstate.RunHeldQuiescenceObservationBatch(ctx, t, q, hook)
}

type quiescenceBatchStager struct {
	tx       managedControllerTransaction
	key      managed.Key
	batch    id.ID
	revision uint64
	used     bool
}

func (h *quiescenceBatchStager) FreshQuiescenceBatchTime(ctx context.Context) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}

	if h.tx.tx == nil || h.used {
		return time.Time{}, managed.ErrBlocked
	}

	var micros int64
	if err := h.tx.tx.NewRaw("SELECT (extract(epoch FROM clock_timestamp())*1000000)::bigint").Scan(ctx, &micros); err != nil {
		return time.Time{}, err
	}

	return time.UnixMicro(micros).UTC(), nil
}
func (h *quiescenceBatchStager) StageQuiescenceBatch(ctx context.Context, c *managedstate.QuiescenceBatchCandidate) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if h.tx.tx == nil || h.used {
		return managed.ErrBlocked
	}

	old, err := h.tx.Read(ctx, h.key)
	if err != nil {
		return err
	}

	target, err := c.StageTarget(old)
	if err != nil {
		return err
	}

	stage, err := c.Take(h.key, h.batch, h.revision)
	if err != nil {
		return err
	}

	h.used = true

	receipts := make([]managed.Receipt, 0, len(stage.Children)+1)
	for _, child := range stage.Children {
		receipts = append(receipts, child.Receipt)
	}

	receipts = append(receipts, stage.Receipt)
	for _, receipt := range receipts {
		raw, e := json.Marshal(receipt)
		if e != nil {
			return e
		}

		_, e = h.tx.tx.NewRaw("INSERT INTO cp_managed_commands(target_id,operation_id,receipt) VALUES($1,$2,$3::jsonb)", h.key.InstanceID.String(), receipt.ID.String(), string(raw)).Exec(ctx)
		if e != nil {
			return managedConflict(e)
		}
	}

	for _, child := range stage.Children {
		if err = h.tx.WriteRecord(ctx, managed.QuiescenceHistory, child.Scope, child.OperationID.String(), child.History, true); err != nil {
			return err
		}

		if err = h.tx.WriteRecord(ctx, managed.QuiescenceCurrent, child.Scope, "current", child.Current, child.First); err != nil {
			return err
		}
	}

	if err = h.tx.WriteRecord(ctx, managed.QuiescenceBatchHistory, stage.Scope, h.batch.String(), stage.Manifest, true); err != nil {
		return err
	}

	body, err := json.Marshal(target)
	if err != nil {
		return err
	}

	result, err := h.tx.tx.NewRaw("UPDATE cp_managed_targets SET revision=$3,data=$4::jsonb WHERE id=$1 AND tenant_id=$2 AND revision=$5", h.key.InstanceID.String(), h.key.TenantID, target.Revision, string(body), h.revision).Exec(ctx)
	if err != nil {
		return err
	}

	count, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if count != 1 {
		return managed.ErrConflict
	}

	return ctx.Err()
}

var _ managed.QuiescenceBatchStore = (*Store)(nil)
