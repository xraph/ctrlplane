package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

func TestManagedAttemptPostgresReceiptProbe(t *testing.T) {
	s, _ := newProjectionPG(t)

	action := projectionAction(t)
	if _, err := s.SubmitManagedAction(t.Context(), action); err != nil {
		t.Fatal(err)
	}

	before, err := s.ReadManagedTarget(t.Context(), action.Key)
	if err != nil {
		t.Fatal(err)
	}

	beforeBytes, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}

	operation := id.New(managed.PrefixOperation)
	digest := managed.Digest([]byte("exact held receipt"))
	stop := errors.New("rollback probe")

	err = (managedBackend{store: s}).ControllerAtomic(t.Context(), "probe", []managed.Key{action.Key}, func(tx managedstate.ControllerTransaction) error {
		if _, err := tx.Read(t.Context(), action.Key); err != nil {
			return err
		}

		check := func(ctx context.Context, key managed.Key, op id.ID, hash string, want error) {
			t.Helper()

			if _, err := tx.ReadReceipt(ctx, key, op, hash); !errors.Is(err, want) {
				t.Fatalf("probe expected %v got %v", want, err)
			}
		}
		check(t.Context(), action.Key, operation, digest, managed.ErrNotFound)

		receipt, err := tx.Atomic(t.Context(), action.Key, operation, digest, func(target *managed.Target, now time.Time) (managed.Receipt, error) {
			target.Revision++

			return managed.Receipt{ID: operation, Digest: digest, Revision: target.Revision, AcceptedAt: now}, nil
		})
		if err != nil {
			return err
		}

		found, err := tx.ReadReceipt(t.Context(), action.Key, operation, digest)
		if err != nil || found != receipt {
			t.Fatal("same transaction receipt not visible", err)
		}

		wrong := action.Key
		wrong.TenantID = "wrong-tenant"
		check(t.Context(), wrong, operation, digest, managed.ErrAuthority)

		absent := action.Key
		absent.InstanceID = id.New(id.PrefixInstance)
		check(t.Context(), absent, operation, digest, managed.ErrNotFound)
		check(t.Context(), action.Key, id.New(managed.PrefixOperation), digest, managed.ErrNotFound)
		check(t.Context(), action.Key, operation, managed.Digest([]byte("wrong")), managed.ErrConflict)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		check(ctx, action.Key, operation, digest, context.Canceled)

		held := tx.(managedControllerTransaction)
		for _, bad := range []string{`"malformed-receipt"`, `{"id":"` + id.New(managed.PrefixOperation).String() + `"}`, `null`} {
			if _, err = held.tx.NewRaw("UPDATE cp_managed_commands SET receipt=$3::jsonb WHERE target_id=$1 AND operation_id=$2", action.InstanceID.String(), operation.String(), bad).Exec(t.Context()); err != nil {
				return err
			}

			check(t.Context(), action.Key, operation, digest, managed.ErrUnavailable)
		}

		if _, err = held.tx.NewRaw("DELETE FROM cp_managed_commands WHERE target_id=$1 AND operation_id=$2", action.InstanceID.String(), operation.String()).Exec(t.Context()); err != nil {
			return err
		}

		check(t.Context(), action.Key, operation, digest, managed.ErrNotFound)

		raw, err := json.Marshal(receipt)
		if err != nil {
			return err
		}

		if _, err = held.tx.NewRaw("INSERT INTO cp_managed_commands(target_id,operation_id,receipt) VALUES($1,$2,$3::jsonb)", action.InstanceID.String(), operation.String(), string(raw)).Exec(t.Context()); err != nil {
			return err
		}

		found, err = tx.ReadReceipt(t.Context(), action.Key, operation, digest)
		if err != nil || found != receipt {
			t.Fatal("absence cached across write", err)
		}

		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}

	after, err := s.ReadManagedTarget(t.Context(), action.Key)
	if err != nil {
		t.Fatal(err)
	}

	afterBytes, err := json.Marshal(after)
	if err != nil || string(beforeBytes) != string(afterBytes) {
		t.Fatal("parent write survived rollback", err)
	}

	if err = (managedBackend{store: s}).ControllerAtomic(t.Context(), "probe", []managed.Key{action.Key}, func(tx managedstate.ControllerTransaction) error {
		if _, e := tx.Read(t.Context(), action.Key); e != nil {
			return e
		}

		_, e := tx.ReadReceipt(t.Context(), action.Key, operation, digest)

		return e
	}); !errors.Is(err, managed.ErrNotFound) {
		t.Fatal("receipt write survived rollback", err)
	}
}

func TestManagedAttemptPostgresImmutableBatches(t *testing.T) {
	s, _ := newProjectionPG(t)

	action := projectionAction(t)
	if _, err := s.SubmitManagedAction(t.Context(), action); err != nil {
		t.Fatal(err)
	}

	scope := managed.Digest([]byte("batch scope"))
	rowKey := managed.Digest([]byte("batch row"))
	keys := []managedstate.ControllerRecordKey{{Kind: "removal_command", Scope: scope, Key: rowKey}, {Kind: "removal_fence", Scope: scope, Key: rowKey}, {Kind: "removal_command", Scope: managed.Digest([]byte("other scope")), Key: rowKey}}
	operation := id.New(managed.PrefixOperation)
	missing := id.New(managed.PrefixOperation)
	stop := errors.New("batch rollback")

	err := (managedBackend{store: s}).ControllerAtomic(t.Context(), "batch", []managed.Key{action.Key}, func(tx managedstate.ControllerTransaction) error {
		rows, err := tx.ReadImmutableRecords(t.Context(), keys)
		if err != nil || len(rows) != 3 {
			t.Fatal("missing exact records", err)
		}

		for _, row := range rows {
			if row.Found || row.Size != 0 || len(row.Data) != 0 {
				t.Fatal("missing was present")
			}
		}

		for n, k := range keys {
			if err = tx.WriteRecord(t.Context(), k.Kind, k.Scope, k.Key, []byte{byte(n + 1)}, true); err != nil {
				return err
			}
		}

		rows, err = tx.ReadImmutableRecords(t.Context(), keys)
		if err != nil {
			return err
		}

		for n, row := range rows {
			if row.ControllerRecordKey != keys[n] || !row.Found || row.Size != 1 || len(row.Data) != 1 || row.Data[0] != byte(n+1) {
				t.Fatal("tuple or staged visibility lost", err)
			}
		}

		rows[0].Data[0] = 99

		again, err := tx.ReadImmutableRecords(t.Context(), keys)
		if err != nil || again[0].Data[0] != 1 {
			t.Fatal("batch bytes alias store", err)
		}
		// Corruption-only setup stays inside this transaction and is restored by rollback.
		if _, e := tx.(managedControllerTransaction).tx.NewRaw("ALTER TABLE cp_managed_controller_records DROP CONSTRAINT cp_managed_removal_attempt_size").Exec(t.Context()); e != nil {
			return e
		}

		for _, size := range []int{0, managed.MaxAttemptCommand, managed.MaxAttemptCommand + 1} {
			k := keys[0]
			if err = tx.WriteRecord(t.Context(), k.Kind, k.Scope, k.Key, []byte(strings.Repeat("x", size)), false); err != nil {
				return err
			}

			found, e := tx.ReadImmutableRecords(t.Context(), keys[:1])
			if e != nil || !found[0].Found || found[0].Size != size {
				t.Fatal("empty/oversized became absent", e)
			}

			if size > managed.MaxAttemptCommand && len(found[0].Data) != 0 {
				t.Fatal("oversized payload crossed cap")
			}

			if size <= managed.MaxAttemptCommand && len(found[0].Data) != size {
				t.Fatal("bounded payload lost")
			}
		}

		maximumKeys := make([]managedstate.ControllerRecordKey, 64)
		maximumPayload := []byte(strings.Repeat("m", managed.MaxAttemptFence))

		for n := range maximumKeys {
			maximumKeys[n] = managedstate.ControllerRecordKey{Kind: "removal_fence", Scope: managed.Digest([]byte("maximum batch")), Key: managed.Digest([]byte(strconv.Itoa(n)))}

			k := maximumKeys[n]
			if e := tx.WriteRecord(t.Context(), k.Kind, k.Scope, k.Key, maximumPayload, true); e != nil {
				return e
			}
		}

		maximumRows, e := tx.ReadImmutableRecords(t.Context(), maximumKeys)
		if e != nil || len(maximumRows) != 64 {
			t.Fatal("maximum batch unavailable", e)
		}

		total := 0

		for _, row := range maximumRows {
			if !row.Found || row.Size != managed.MaxAttemptFence || len(row.Data) != row.Size {
				t.Fatal("maximum batch truncated")
			}

			total += len(row.Data)
		}

		if total != 3145728 {
			t.Fatal("maximum payload accounting", total)
		}

		t.Logf("actual64-key maximum record transport bytes=%d", total)

		if result, e := tx.ReadImmutableRecords(t.Context(), append(keys, keys[0])); !errors.Is(e, managed.ErrInvalid) || result != nil {
			t.Fatal("duplicate batch accepted", e)
		}

		large := make([]managedstate.ControllerRecordKey, 65)
		for n := range large {
			large[n] = keys[0]
			large[n].Key = managed.Digest([]byte(strconv.Itoa(n)))
		}

		if result, e := tx.ReadImmutableRecords(t.Context(), large); !errors.Is(e, managed.ErrInvalid) || result != nil {
			t.Fatal("oversized batch accepted", e)
		}

		forbidden := keys[0]

		forbidden.Kind = "step"
		if _, e := tx.ReadImmutableRecords(t.Context(), []managedstate.ControllerRecordKey{forbidden}); !errors.Is(e, managed.ErrInvalid) {
			t.Fatal("mutable allowlist bypass", e)
		}

		stamp := time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC)
		expected := managed.Receipt{ID: operation, Digest: strings.Repeat("f", 64), Revision: math.MaxInt64, LeaseEpoch: math.MaxInt64, AcceptedAt: stamp, LeaseUntil: stamp}

		encoded, e := json.Marshal(expected)
		if e != nil || len(encoded) > managed.MaxAttemptReceipt {
			t.Fatal("actual maximal A receipt exceeds cap", len(encoded), e)
		}

		t.Logf("actual maximal A receipt codec bytes=%d cap=%d", len(encoded), managed.MaxAttemptReceipt)

		if _, e = tx.Atomic(t.Context(), action.Key, operation, expected.Digest, func(*managed.Target, time.Time) (managed.Receipt, error) { return expected, nil }); e != nil {
			return e
		}

		receipts, e := tx.ReadImmutableReceipts(t.Context(), action.Key, []id.ID{operation, missing})
		if e != nil || len(receipts) != 2 || !receipts[0].Found || receipts[0].Receipt != expected || receipts[1].Found {
			t.Fatal("receipt staged visibility", e)
		}

		if result, e := tx.ReadImmutableReceipts(t.Context(), action.Key, []id.ID{operation, operation}); !errors.Is(e, managed.ErrInvalid) || result != nil {
			t.Fatal("duplicate receipt accepted", e)
		}

		wrong := action.Key

		wrong.TenantID = "another-tenant"
		if result, e := tx.ReadImmutableReceipts(t.Context(), wrong, []id.ID{operation}); !errors.Is(e, managed.ErrAuthority) || result != nil {
			t.Fatal("receipt tenant escaped", e)
		}

		wrong = action.Key

		wrong.InstanceID = id.New(id.PrefixInstance)
		if result, e := tx.ReadImmutableReceipts(t.Context(), wrong, []id.ID{operation}); !errors.Is(e, managed.ErrNotFound) || result != nil {
			t.Fatal("receipt parent absent", e)
		}

		canceled, cancel := context.WithCancel(t.Context())
		cancel()

		if result, e := tx.ReadImmutableRecords(canceled, keys); !errors.Is(e, context.Canceled) || result != nil {
			t.Fatal("record cancellation ignored", e)
		}

		if result, e := tx.ReadImmutableReceipts(canceled, action.Key, []id.ID{operation}); !errors.Is(e, context.Canceled) || result != nil {
			t.Fatal("receipt cancellation ignored", e)
		}

		held := tx.(managedControllerTransaction)
		if _, e = held.tx.NewRaw("UPDATE cp_managed_commands SET receipt=jsonb_set(receipt,'{id}',to_jsonb($3::text)) WHERE target_id=$1 AND operation_id=$2", action.InstanceID.String(), operation.String(), missing.String()).Exec(t.Context()); e != nil {
			return e
		}

		if result, e := tx.ReadImmutableReceipts(t.Context(), action.Key, []id.ID{operation}); !errors.Is(e, managed.ErrUnavailable) || result != nil {
			t.Fatal("wrong payload operation accepted", e)
		}

		if _, e = held.tx.NewRaw("UPDATE cp_managed_commands SET receipt=$3::jsonb WHERE target_id=$1 AND operation_id=$2", action.InstanceID.String(), operation.String(), string(encoded)).Exec(t.Context()); e != nil {
			return e
		}

		if _, e = held.tx.NewRaw("UPDATE cp_managed_targets SET data=jsonb_set(data,'{instance_id}',to_jsonb($2::text)) WHERE id=$1", action.InstanceID.String(), id.New(id.PrefixInstance).String()).Exec(t.Context()); e != nil {
			return e
		}

		if result, e := tx.ReadImmutableReceipts(t.Context(), action.Key, []id.ID{operation}); !errors.Is(e, managed.ErrUnavailable) || result != nil {
			t.Fatal("wrong parent payload identity accepted", e)
		}

		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}

	err = (managedBackend{store: s}).ControllerAtomic(t.Context(), "batch rollback", []managed.Key{action.Key}, func(tx managedstate.ControllerTransaction) error {
		rows, e := tx.ReadImmutableRecords(t.Context(), keys)
		if e != nil {
			return e
		}

		for _, row := range rows {
			if row.Found {
				t.Fatal("rolled back record survived")
			}
		}

		receipts, e := tx.ReadImmutableReceipts(t.Context(), action.Key, []id.ID{operation})
		if e != nil || receipts[0].Found {
			t.Fatal("rolled back receipt survived", e)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestManagedAttemptPostgresHeldBatches(t *testing.T) {
	s, _ := newProjectionPG(t)

	action := projectionAction(t)
	if _, err := s.SubmitManagedAction(t.Context(), action); err != nil {
		t.Fatal(err)
	}

	before, err := s.ReadManagedTarget(t.Context(), action.Key)
	if err != nil {
		t.Fatal(err)
	}

	beforeRaw, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}

	err = (managedBackend{store: s}).ControllerAtomic(t.Context(), "held-batch", []managed.Key{action.Key}, func(tx managedstate.ControllerTransaction) error {
		keys := []managedstate.ControllerRecordKey{}

		for i, kind := range []string{"step", "removal_provider_slot", "step_failure", "step_history"} {
			scope := action.TenantID
			if kind == "removal_provider_slot" {
				scope = managed.Digest([]byte("scope"))
			}

			keys = append(keys, managedstate.ControllerRecordKey{Kind: kind, Scope: scope, Key: managed.Digest([]byte(strconv.Itoa(i)))})
		}

		rows, e := tx.ReadHeldRecords(t.Context(), keys)
		if e != nil || len(rows) != 4 {
			t.Fatal(e)
		}

		for _, row := range rows {
			if row.Found || row.Size != 0 || len(row.Data) != 0 {
				t.Fatal("absence lost")
			}
		}

		for i, selected := range keys {
			raw := []byte(strings.Repeat("x", managedstate.HeldRecordLimit(selected.Kind)))
			if e = tx.WriteRecord(t.Context(), selected.Kind, selected.Scope, selected.Key, raw, true); e != nil {
				return e
			}

			rows, e = tx.ReadHeldRecords(t.Context(), keys)
			if e != nil || !rows[i].Found || string(rows[i].Data) != string(raw) || rows[i].ControllerRecordKey != selected {
				t.Fatal("held mutation/identity/bytes", e)
			}

			rows[i].Data[0] = 'Y'

			again, readErr := tx.ReadHeldRecords(t.Context(), keys)
			if readErr != nil || string(again[i].Data) != string(raw) {
				t.Fatal("borrowed bytes", readErr)
			}
		}

		if _, e = tx.ReadHeldRecords(t.Context(), append(keys, keys[0])); !errors.Is(e, managed.ErrInvalid) {
			t.Fatal("duplicate", e)
		}

		over := make([]managedstate.ControllerRecordKey, 65)
		if _, e = tx.ReadHeldRecords(t.Context(), over); !errors.Is(e, managed.ErrInvalid) {
			t.Fatal("bound", e)
		}

		bad := keys[0]

		bad.Kind = "removal_attempt_state"
		if _, e = tx.ReadHeldRecords(t.Context(), []managedstate.ControllerRecordKey{bad}); !errors.Is(e, managed.ErrInvalid) {
			t.Fatal("kind", e)
		}

		canceled, cancel := context.WithCancel(t.Context())
		cancel()

		if _, e = tx.ReadHeldRecords(canceled, keys); !errors.Is(e, context.Canceled) {
			t.Fatal("cancel", e)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	after, err := s.ReadManagedTarget(t.Context(), action.Key)
	if err != nil {
		t.Fatal(err)
	}

	afterRaw, err := json.Marshal(after)
	if err != nil || string(afterRaw) != string(beforeRaw) {
		t.Fatal("parent changed", err)
	}
}
