package store_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/store/postgres"
)

func TestManagedRemovalContinuationPostgresBindingReadFreshness(t *testing.T) {
	f := newRemovalFixture(t, "postgres")

	s := f.s.Authority.(*postgres.Store)
	if _, err := s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
		t.Fatal(err)
	}

	continueRemovalFixture(t, f)
	f.command(f.reservations[0], managed.AttemptBegin)
	rows := continuationPGRows(t, s, f.key.Key)
	checked := 0

	for _, row := range rows.Rows() {
		if row.Kind != "removal_current_binding" {
			continue
		}

		checked++

		if _, err := s.ReadManagedRemovalAttempt(t.Context(), f.key); err != nil {
			t.Fatal(err)
		}

		pg := pgdriver.Unwrap(s.DB())
		if _, err := pg.Exec(t.Context(), "DELETE FROM cp_managed_controller_records WHERE kind=$1 AND scope=$2 AND key=$3", row.Kind, row.Scope, row.Key); err != nil {
			t.Fatal(err)
		}

		before := attemptPostgresSnapshot(t, s)
		if _, err := s.ReadManagedRemovalAttempt(t.Context(), f.key); err == nil {
			t.Fatal("missing current binding reused")
		}

		if !reflect.DeepEqual(before, attemptPostgresSnapshot(t, s)) {
			t.Fatal("missing binding repaired")
		}

		var observation managed.AttemptCurrentBindingObservation
		if err := json.Unmarshal(row.Data, &observation); err != nil {
			t.Fatal(err)
		}

		observation.BindingVersion = "99"

		wrongVersion, err := json.Marshal(observation)
		if err != nil {
			t.Fatal(err)
		}

		for name, raw := range map[string][]byte{
			"empty":        []byte(`{}`),
			"null":         []byte(`null`),
			"unknown":      []byte(`{"unknown":true}`),
			"duplicate":    append([]byte(`{"schema_version":"1",`), row.Data[1:]...),
			"noncanonical": append(bytes.Clone(row.Data), ' '),
			"version":      wrongVersion,
		} {
			corrupt := row
			corrupt.Data = raw
			rows.Put(corrupt)

			before = attemptPostgresSnapshot(t, s)
			if _, err := s.ReadManagedRemovalAttempt(t.Context(), f.key); err == nil {
				t.Fatal("changed current binding reused", name)
			}

			if !reflect.DeepEqual(before, attemptPostgresSnapshot(t, s)) {
				t.Fatal("changed binding repaired", name)
			}
		}

		rows.Put(row)

		if _, err := s.ReadManagedRemovalAttempt(t.Context(), f.key); err != nil {
			t.Fatal("restored binding", err)
		}
	}

	if checked != 1 {
		t.Fatalf("current binding rows=%d", checked)
	}
}

func TestManagedRemovalContinuationPostgresBindingPostWrite(t *testing.T) {
	f := newRemovalFixture(t, "postgres")
	if _, err := f.s.RevokeManagedRemovalAttempt(t.Context(), f.request()); err != nil {
		t.Fatal(err)
	}

	continueRemovalFixture(t, f)
	f.command(f.reservations[0], managed.AttemptBegin)
	s := f.s.Authority.(*postgres.Store)

	pg := pgdriver.Unwrap(s.DB())
	if _, err := pg.Exec(t.Context(), `CREATE FUNCTION corrupt_binding_after_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN UPDATE cp_managed_controller_records SET data=convert_to('{}','UTF8') WHERE kind='removal_current_binding'; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}

	if _, err := pg.Exec(t.Context(), `CREATE TRIGGER corrupt_binding_after_event AFTER INSERT ON cp_managed_controller_records FOR EACH ROW WHEN (NEW.kind='removal_attempt_event') EXECUTE FUNCTION corrupt_binding_after_event()`); err != nil {
		t.Fatal(err)
	}

	q := f.request()

	before := attemptPostgresSnapshot(t, s)
	if _, err := s.RevokeManagedRemovalAttempt(t.Context(), q); err == nil {
		t.Fatal("post-write current binding corruption accepted")
	}

	if !reflect.DeepEqual(before, attemptPostgresSnapshot(t, s)) {
		t.Fatal("failed post-write validation leaked staged writes")
	}

	if _, err := pg.Exec(t.Context(), `DROP TRIGGER corrupt_binding_after_event ON cp_managed_controller_records`); err != nil {
		t.Fatal(err)
	}

	if _, err := s.RevokeManagedRemovalAttempt(t.Context(), q); err != nil {
		t.Fatal("valid retry", err)
	}
}
