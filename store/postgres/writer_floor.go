package postgres

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/xraph/grove"
	"github.com/xraph/grove/driver"
	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

// WriterFloorReader exposes protected inspection without floor administration.
type WriterFloorReader struct {
	*managedstate.WriterFloorReader
}

// WriterFloorAdmin uses an explicitly supplied administrative connection.
// It must use credentials unavailable to runtime hosts, with authority DML already fenced.
// Construction cannot prove credential separation, network custody or deployment admission.
type WriterFloorAdmin struct{ backend *writerFloorBackend }

type writerFloorBackend struct {
	pg        *pgdriver.PgDB
	authority string
}

type writerFloorSnapshot struct {
	tx        *pgdriver.PgTx
	authority string
}

type writerFloorTransaction struct{ writerFloorSnapshot }

// NewWriterFloorReader binds read-only inspection to the host's configured authority.
// The supplied database role needs SELECT on both protected tables, and no floor DML.
func NewWriterFloorReader(db *grove.DB, authority string) (*WriterFloorReader, error) {
	if db == nil {
		return nil, managed.ErrInvalid
	}

	pg, ok := db.Driver().(*pgdriver.PgDB)
	if !ok || pg == nil {
		return nil, managed.ErrInvalid
	}

	reader, err := managedstate.NewWriterFloorReader(&writerFloorBackend{pg: pg, authority: authority}, authority)
	if err != nil {
		return nil, err
	}

	return &WriterFloorReader{WriterFloorReader: reader}, nil
}

// NewWriterFloorAdmin requires a dedicated administrative database handle.
// The caller is responsible for actual PostgreSQL role isolation and the external barrier.
// Ordinary Store and controller transaction values do not implement this capability.
func NewWriterFloorAdmin(adminDB *grove.DB, authority string) (*WriterFloorAdmin, error) {
	if _, err := NewWriterFloorReader(adminDB, authority); err != nil {
		return nil, err
	}

	pg, ok := adminDB.Driver().(*pgdriver.PgDB)
	if !ok || pg == nil {
		return nil, managed.ErrInvalid
	}

	return &WriterFloorAdmin{backend: &writerFloorBackend{pg: pg, authority: authority}}, nil
}

// Activate atomically retains a floor activation. It does not reopen traffic or certify a writer.
func (a *WriterFloorAdmin) Activate(ctx context.Context, request managed.WriterFloorActivation) (managed.WriterFloor, error) {
	if a == nil || a.backend == nil {
		return managed.WriterFloor{}, managed.ErrAuthority
	}

	return managedstate.ActivateWriterFloor(ctx, a.backend, a.backend.authority, request)
}

func writerFloorLock(ctx context.Context, tx *pgdriver.PgTx, authority string, exclusive bool) error {
	query := "SELECT pg_advisory_xact_lock_shared(hashtextextended($1,0))"
	if exclusive {
		query = "SELECT pg_advisory_xact_lock(hashtextextended($1,0))"
	}

	_, err := tx.NewRaw(query, "ctrlplane-writer-floor:"+authority).Exec(ctx)

	return err
}

func (b *writerFloorBackend) WriterFloorSnapshot(ctx context.Context, fn func(managedstate.WriterFloorSnapshot) error) error {
	tx, err := b.pg.BeginTxQuery(ctx, &driver.TxOptions{IsolationLevel: driver.LevelReadCommitted, ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// READ COMMITTED takes its data snapshot after waiting for the lock. All floor
	// writers use the exclusive form; the protected current/history pair stays stable.
	if err = writerFloorLock(ctx, tx, b.authority, false); err != nil {
		return err
	}

	if err = fn(writerFloorSnapshot{tx: tx, authority: b.authority}); err != nil {
		return err
	}

	if err = ctx.Err(); err != nil {
		return err
	}

	return tx.Commit()
}

func (b *writerFloorBackend) WriterFloorAdminAtomic(ctx context.Context, fn func(managedstate.WriterFloorAdminTransaction) error) error {
	tx, err := b.pg.BeginTxQuery(ctx, &driver.TxOptions{IsolationLevel: driver.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err = writerFloorMutationLock(ctx, tx, true); err != nil {
		return err
	}

	if err = writerFloorLock(ctx, tx, b.authority, true); err != nil {
		return err
	}

	if err = fn(&writerFloorTransaction{writerFloorSnapshot{tx: tx, authority: b.authority}}); err != nil {
		return err
	}

	if err = ctx.Err(); err != nil {
		return err
	}

	return tx.Commit()
}

type writerFloorRow struct {
	Authority  string `grove:"authority_id"`
	Activation string `grove:"activation_id"`
	Epoch      string `grove:"epoch"`
	Protocol   string `grove:"minimum_protocol"`
	Digest     string `grove:"digest"`
	Size       int    `grove:"size"`
	Data       []byte `grove:"data"`
}

// Payload is BYTEA, preserving canonical bytes. CASE prevents oversized allocation
// even if a privileged corruption fixture has removed the SQL size constraint.
const writerFloorColumns = "authority_id,activation_id,epoch::text AS epoch,minimum_protocol::text AS minimum_protocol,digest,octet_length(data) AS size,CASE WHEN octet_length(data) BETWEEN 1 AND 4096 THEN data END AS data"

func (s writerFloorSnapshot) read(ctx context.Context, history bool, activation id.ID) ([]byte, error) {
	if s.tx == nil {
		return nil, managed.ErrUnavailable
	}

	query := "SELECT " + writerFloorColumns + " FROM public.cp_writer_floor_current WHERE authority_id=$1"
	args := []any{s.authority}

	if history {
		query = "SELECT " + writerFloorColumns + " FROM public.cp_writer_floor_history WHERE authority_id=$1 AND activation_id=$2"

		args = append(args, activation.String())
	}

	var row writerFloorRow
	if err := s.tx.NewRaw(query, args...).Scan(ctx, &row); err != nil {
		if isNoRows(err) {
			return nil, managed.ErrNotFound
		}

		return nil, err
	}

	if row.Size < 1 || row.Size > managed.MaxWriterFloor || len(row.Data) != row.Size {
		return nil, managed.ErrWriterFloorCorrupt
	}

	floor, err := managed.DecodeWriterFloor(row.Data)
	if err != nil || row.Authority != s.authority || floor.AuthorityID != row.Authority || floor.ActivationID.String() != row.Activation || string(floor.Epoch) != row.Epoch || string(floor.MinimumProtocol) != row.Protocol || floor.Digest != row.Digest {
		return nil, managed.ErrWriterFloorCorrupt
	}

	if history && floor.ActivationID != activation {
		return nil, managed.ErrWriterFloorCorrupt
	}

	return bytes.Clone(row.Data), ctx.Err()
}

func (s writerFloorSnapshot) ReadCurrentWriterFloor(ctx context.Context) ([]byte, error) {
	return s.read(ctx, false, id.ID{})
}
func (s writerFloorSnapshot) ReadWriterFloorActivation(ctx context.Context, activation id.ID) ([]byte, error) {
	return s.read(ctx, true, activation)
}

func (t *writerFloorTransaction) WriterFloorTime(ctx context.Context) (time.Time, error) {
	var micros int64
	if err := t.tx.NewRaw("SELECT (extract(epoch FROM clock_timestamp())*1000000)::bigint").Scan(ctx, &micros); err != nil {
		return time.Time{}, err
	}

	return time.UnixMicro(micros).UTC(), nil
}

func (t *writerFloorTransaction) candidate(raw []byte) (managed.WriterFloor, error) {
	floor, err := managed.DecodeWriterFloor(raw)
	if err != nil {
		return managed.WriterFloor{}, err
	}

	if floor.AuthorityID != t.authority {
		return managed.WriterFloor{}, managed.ErrAuthority
	}

	return floor, nil
}

func (t *writerFloorTransaction) insertHistory(ctx context.Context, floor managed.WriterFloor, raw []byte) error {
	_, err := t.tx.NewRaw("INSERT INTO public.cp_writer_floor_history(authority_id,activation_id,epoch,minimum_protocol,digest,data) VALUES($1,$2,$3::bigint,$4::bigint,$5,$6)", t.authority, floor.ActivationID.String(), string(floor.Epoch), string(floor.MinimumProtocol), floor.Digest, raw).Exec(ctx)

	return managedConflict(err)
}

func (t *writerFloorTransaction) InstallInitialWriterFloor(ctx context.Context, raw []byte) error {
	floor, err := t.candidate(raw)
	if err != nil {
		return err
	}

	if floor.Epoch != "1" || floor.PreviousDigest != "" {
		return managed.ErrConflict
	}

	var occupied bool
	if err = t.tx.NewRaw("SELECT EXISTS(SELECT 1 FROM public.cp_writer_floor_current WHERE authority_id=$1) OR EXISTS(SELECT 1 FROM public.cp_writer_floor_history WHERE authority_id=$1)", t.authority).Scan(ctx, &occupied); err != nil {
		return err
	}

	if occupied {
		return managed.ErrWriterFloorCorrupt
	}

	if err = t.insertHistory(ctx, floor, raw); err != nil {
		return err
	}

	_, err = t.tx.NewRaw("INSERT INTO public.cp_writer_floor_current(authority_id,activation_id,epoch,minimum_protocol,digest,data) VALUES($1,$2,$3::bigint,$4::bigint,$5,$6)", t.authority, floor.ActivationID.String(), string(floor.Epoch), string(floor.MinimumProtocol), floor.Digest, raw).Exec(ctx)

	return managedConflict(err)
}

func (t *writerFloorTransaction) AdvanceWriterFloor(ctx context.Context, expected string, raw []byte) error {
	floor, err := t.candidate(raw)
	if err != nil {
		return err
	}

	oldRaw, err := t.ReadCurrentWriterFloor(ctx)
	if err != nil {
		return err
	}

	old, err := managed.DecodeWriterFloor(oldRaw)
	if err != nil {
		return err
	}

	history, err := t.ReadWriterFloorActivation(ctx, old.ActivationID)
	if err != nil {
		if errors.Is(err, managed.ErrNotFound) {
			return managed.ErrWriterFloorCorrupt
		}

		return err
	}

	if !bytes.Equal(history, oldRaw) {
		return managed.ErrWriterFloorCorrupt
	}

	oldEpoch, _ := old.Epoch.Uint64()
	nextEpoch, _ := floor.Epoch.Uint64()
	oldProtocol, _ := old.MinimumProtocol.Uint64()

	nextProtocol, _ := floor.MinimumProtocol.Uint64()
	if old.Digest != expected || floor.PreviousDigest != expected || nextEpoch != oldEpoch+1 || nextProtocol < oldProtocol || floor.ActivatedAt.Before(old.ActivatedAt) {
		return managed.ErrConflict
	}

	if err = t.insertHistory(ctx, floor, raw); err != nil {
		return err
	}

	result, err := t.tx.NewRaw("UPDATE public.cp_writer_floor_current SET activation_id=$2,epoch=$3::bigint,minimum_protocol=$4::bigint,digest=$5,data=$6 WHERE authority_id=$1 AND digest=$7", t.authority, floor.ActivationID.String(), string(floor.Epoch), string(floor.MinimumProtocol), floor.Digest, raw, expected).Exec(ctx)
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

// checkWriterFloorMutationHeld is reserved for complete protocol integration.
// Call it FIRST in a fresh READ COMMITTED authority transaction, before controller,
// target, or ordinary DML locks. Its shared lock survives until outer commit/rollback.
// No runtime path invokes it in this foundation; even a valid floor returns unsupported.
func (t managedControllerTransaction) checkWriterFloorMutationHeld(ctx context.Context, authority string) error {
	if t.tx == nil {
		return managed.ErrUnavailable
	}

	if err := writerFloorLock(ctx, t.tx, authority, false); err != nil {
		return err
	}

	return managedstate.CheckWriterFloorMutation(ctx, writerFloorSnapshot{tx: t.tx, authority: authority}, authority)
}

var _ managedstate.WriterFloorReadBackend = (*writerFloorBackend)(nil)
var _ managedstate.WriterFloorAdminBackend = (*writerFloorBackend)(nil)
var _ managedstate.WriterFloorAdminTransaction = (*writerFloorTransaction)(nil)
