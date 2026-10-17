package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/xraph/grove/driver"
	"github.com/xraph/grove/drivers/pgdriver"
	"go.opentelemetry.io/otel/trace"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/internal/obs"
	"github.com/xraph/ctrlplane/managed"
)

type managedBackend struct {
	store          *Store
	tx             *pgdriver.PgTx
	floorAdmission *writerFloorMutationAdmission
}

func (b managedBackend) begin(ctx context.Context) (*pgdriver.PgTx, error) {
	if b.tx != nil {
		return b.tx, nil
	}

	return b.store.pg.BeginTxQuery(ctx, &driver.TxOptions{IsolationLevel: driver.LevelReadCommitted})
}
func (b managedBackend) commit(tx *pgdriver.PgTx) error {
	if b.tx != nil {
		return nil
	}

	return tx.Commit()
}
func (b managedBackend) rollback(tx *pgdriver.PgTx) {
	if b.tx == nil {
		_ = tx.Rollback()
	}
}
func (b managedBackend) raw(query string, args ...any) *pgdriver.RawQuery {
	if b.tx != nil {
		return b.tx.NewRaw(query, args...)
	}

	return b.store.pg.NewRaw(query, args...)
}

// parentReadQuery reuses locks already taken by the outer controller transaction.
func (b managedBackend) parentReadQuery() string {
	if b.tx != nil {
		return "SELECT data FROM cp_managed_targets WHERE id=$1"
	}

	return "SELECT data FROM cp_managed_targets WHERE id=$1 FOR UPDATE"
}

func decodeManaged(body []byte, tenant string) (*managed.Target, error) {
	var t managed.Target
	if err := json.Unmarshal(body, &t); err != nil {
		return nil, fmt.Errorf("%w: invalid managed record", managed.ErrUnavailable)
	}

	if t.TenantID != tenant {
		return nil, managed.ErrAuthority
	}

	return &t, nil
}
func managedConflict(err error) error {
	var state interface{ SQLState() string }
	if errors.As(err, &state) && state.SQLState() == "23505" {
		return fmt.Errorf("%w: physical identity already owned", managed.ErrConflict)
	}

	return err
}
func (b managedBackend) Create(ctx context.Context, t *managed.Target) (*managed.Target, error) {
	tx, err := b.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer b.rollback(tx)

	admission, err := b.writerFloorAdmissionFor(ctx, tx)
	if err != nil {
		return nil, err
	}

	var micros int64
	if err = tx.NewRaw("SELECT (extract(epoch FROM clock_timestamp()) * 1000000)::bigint").Scan(ctx, &micros); err != nil {
		return nil, err
	}

	if err = admission.check(ctx); err != nil {
		return nil, err
	}

	t.CreatedAt = time.UnixMicro(micros).UTC()

	body, err := json.Marshal(t)
	if err != nil {
		return nil, err
	}

	_, err = tx.NewRaw(`INSERT INTO cp_managed_targets (id,tenant_id,provider,placement,project,generation,phase,desired,revision,data)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb) ON CONFLICT (id) DO NOTHING`, t.InstanceID.String(), t.TenantID, t.Physical.Provider, t.Physical.Placement, t.Physical.Project, t.Physical.Generation, string(t.Phase), string(t.Desired), t.Revision, string(body)).Exec(ctx)
	if err != nil {
		return nil, managedConflict(err)
	}

	if err = tx.NewRaw(b.parentReadQuery(), t.InstanceID.String()).Scan(ctx, &body); err != nil {
		return nil, err
	}

	saved, err := decodeManaged(body, t.TenantID)
	if err != nil {
		return nil, err
	}

	if !managedstate.EqualIdentity(saved.Identity, t.Identity) {
		return nil, managed.ErrConflict
	}

	if err = b.commit(tx); err != nil {
		return nil, err
	}

	return saved, nil
}
func (b managedBackend) Read(ctx context.Context, k managed.Key) (*managed.Target, error) {
	var body []byte

	err := b.raw("SELECT data FROM cp_managed_targets WHERE id=$1", k.InstanceID.String()).Scan(ctx, &body)
	if isNoRows(err) {
		return nil, managed.ErrNotFound
	}

	if err != nil {
		return nil, err
	}

	return decodeManaged(body, k.TenantID)
}

type managedSummaryRow struct {
	InstanceID   id.ID               `grove:"id"`
	TenantID     string              `grove:"tenant_id"`
	Phase        managed.Phase       `grove:"phase"`
	Desired      managed.DesiredMode `grove:"desired"`
	Revision     uint64              `grove:"revision"`
	Reservations int                 `grove:"reservations"`
}

func (b managedBackend) List(ctx context.Context, q managed.PageRequest, after string) ([]managed.Summary, error) {
	var rows []managedSummaryRow

	err := b.raw(`SELECT id,tenant_id,phase,desired,revision,jsonb_array_length(data->'reservations') AS reservations
 FROM cp_managed_targets WHERE ($1='' OR tenant_id=$1) AND id>$2 ORDER BY id LIMIT $3`, q.TenantID, after, q.Limit+1).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	out := make([]managed.Summary, 0, len(rows))
	for _, v := range rows {
		out = append(out, managed.Summary{Key: managed.Key{TenantID: v.TenantID, InstanceID: v.InstanceID}, Phase: v.Phase, Desired: v.Desired, Revision: v.Revision, ReservationCount: v.Reservations})
	}

	return out, nil
}
func (b managedBackend) Obligations(ctx context.Context, tenant string) (bool, error) {
	var exists bool

	err := b.raw("SELECT EXISTS(SELECT 1 FROM cp_managed_targets WHERE tenant_id=$1 AND phase<>$2)", tenant, string(managed.Deleted)).Scan(ctx, &exists)

	return exists, err
}

// Atomic runs one managed target transition in a transaction. Under a
// traced call it is a "transact managed target" span; the statements grove
// traces nest inside it, the row lock wait included.
func (b managedBackend) Atomic(ctx context.Context, k managed.Key, operation id.ID, digest string, fn func(*managed.Target, time.Time) (managed.Receipt, error)) (managed.Receipt, error) {
	ctx, span := obs.StartChild(ctx, "transact managed target",
		trace.WithAttributes(obs.AttrInstanceID.String(k.InstanceID.String())))
	defer span.End()

	receipt, err := b.atomic(ctx, k, operation, digest, fn)
	endManagedSpan(span, err)

	return receipt, err
}

// endManagedSpan records how a managed transaction ended. A refusal (the
// target is missing, the command conflicts, or an obligation blocks it) is
// the protocol working, so it is an attribute rather than a failure.
func endManagedSpan(span trace.Span, err error) {
	if err == nil {
		return
	}

	if errors.Is(err, managed.ErrNotFound) || errors.Is(err, managed.ErrConflict) || errors.Is(err, managed.ErrBlocked) {
		span.SetAttributes(obs.AttrManagedRefusal.String(obs.ErrorType(err)))

		return
	}

	obs.Fail(span, err)
}

func (b managedBackend) atomic(ctx context.Context, k managed.Key, operation id.ID, digest string, fn func(*managed.Target, time.Time) (managed.Receipt, error)) (managed.Receipt, error) {
	tx, err := b.begin(ctx)
	if err != nil {
		return managed.Receipt{}, err
	}
	defer b.rollback(tx)

	admission, err := b.writerFloorAdmissionFor(ctx, tx)
	if err != nil {
		return managed.Receipt{}, err
	}

	var body []byte
	if err = tx.NewRaw(b.parentReadQuery(), k.InstanceID.String()).Scan(ctx, &body); err != nil {
		if isNoRows(err) {
			return managed.Receipt{}, managed.ErrNotFound
		}

		return managed.Receipt{}, err
	}

	t, err := decodeManaged(body, k.TenantID)
	if err != nil {
		return managed.Receipt{}, err
	}

	var saved []byte

	err = tx.NewRaw("SELECT receipt FROM cp_managed_commands WHERE target_id=$1 AND operation_id=$2", k.InstanceID.String(), operation.String()).Scan(ctx, &saved)
	if err == nil {
		var receipt managed.Receipt
		if json.Unmarshal(saved, &receipt) != nil || receipt.ID != operation {
			return managed.Receipt{}, managed.ErrUnavailable
		}

		if receipt.Digest != digest {
			return managed.Receipt{}, managed.ErrConflict
		}

		return receipt, nil
	}

	if !isNoRows(err) {
		return managed.Receipt{}, err
	}

	if err = admission.check(ctx); err != nil {
		return managed.Receipt{}, err
	}

	var micros int64
	if err = tx.NewRaw("SELECT (extract(epoch FROM clock_timestamp()) * 1000000)::bigint").Scan(ctx, &micros); err != nil {
		return managed.Receipt{}, err
	}

	receipt, err := fn(t, time.UnixMicro(micros).UTC())
	if err != nil {
		return managed.Receipt{}, err
	}

	if _, err = managedstate.Clone(t); err != nil {
		return managed.Receipt{}, err
	}

	body, err = json.Marshal(t)
	if err != nil {
		return managed.Receipt{}, err
	}

	_, err = tx.NewRaw(`UPDATE cp_managed_targets SET phase=$2,desired=$3,revision=$4,data=$5::jsonb WHERE id=$1`, k.InstanceID.String(), string(t.Phase), string(t.Desired), t.Revision, string(body)).Exec(ctx)
	if err != nil {
		return managed.Receipt{}, err
	}

	data, err := json.Marshal(receipt)
	if err != nil {
		return managed.Receipt{}, err
	}

	_, err = tx.NewRaw("INSERT INTO cp_managed_commands(target_id,operation_id,receipt) VALUES($1,$2,$3::jsonb)", k.InstanceID.String(), operation.String(), string(data)).Exec(ctx)
	if err != nil {
		return managed.Receipt{}, err
	}

	if err = b.commit(tx); err != nil {
		return managed.Receipt{}, err
	}

	return receipt, nil
}
