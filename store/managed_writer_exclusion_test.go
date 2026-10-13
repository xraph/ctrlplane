package store_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/store/postgres"
)

func exclusionAudit() *admin.AuditEntry {
	return &admin.AuditEntry{TenantID: "exclusion-tenant", ActorID: "exclusion-actor", Action: "fixture.write", Resource: "fixture", ResourceID: "isolated", Details: map[string]any{"purpose": "writer-exclusion"}}
}

func TestWriterExclusionAuditAndEveryTableDML(t *testing.T) {
	f := newExclusionFixture(t)
	legacy := f.openRole(f.legacy, true)
	before := f.snapshot()

	if err := legacy.InsertAuditEntry(t.Context(), exclusionAudit()); err != nil {
		t.Fatal("ordinary legacy audit positive control", err)
	}

	if reflect.DeepEqual(before, f.snapshot()) {
		t.Fatal("audit positive control made no change")
	}
	// The same principal also has a real direct SQL positive control.
	exclusionExec(t, legacy, "UPDATE public.cp_audit_entries SET action='before-cutover'")
	f.drain()

	if _, err := f.activate(t.Context(), f.activation()); err != nil {
		t.Fatal(err)
	}

	for _, role := range []string{f.legacy, f.runtime, f.history} {
		s := f.readOnly(role)
		before = f.snapshot()

		exclusionDenied(t, s.InsertAuditEntry(t.Context(), exclusionAudit()))

		cfg, err := pgxpool.ParseConfig(f.dsn(role))
		if err != nil {
			t.Fatal(err)
		}

		copyConn, err := pgx.ConnectConfig(t.Context(), cfg.ConnConfig)
		if err != nil {
			t.Fatal(err)
		}

		_, copyErr := copyConn.CopyFrom(t.Context(), pgx.Identifier{"public", "cp_audit_entries"}, []string{"tenant_id", "actor_id", "action"}, pgx.CopyFromRows([][]any{{"t", "a", "denied-copy"}}))
		closeErr := copyConn.Close(t.Context())
		exclusionDenied(t, copyErr)

		if closeErr != nil {
			t.Fatal(closeErr)
		}

		queries := []string{
			"INSERT INTO public.cp_audit_entries(tenant_id,actor_id,action) VALUES('t','a','denied')",
			"UPDATE public.cp_audit_entries SET action=action WHERE false",
			"DELETE FROM public.cp_managed_controller_records WHERE false",
			"TRUNCATE public.cp_audit_entries",
			"SELECT nextval('public.cp_audit_entries_id_seq')",
			"SELECT setval('public.cp_audit_entries_id_seq',1)",
			"CREATE TABLE public.runtime_escape(id integer)",
			"SELECT public.cp_writer_floor_history_immutable()",
		}
		for _, query := range queries {
			_, err := pgdriver.Unwrap(s.DB()).Exec(t.Context(), query)
			exclusionDenied(t, err)
		}

		var tables []struct {
			Name string `grove:"name"`
		}
		if err := pgdriver.Unwrap(f.admin.DB()).NewRaw("SELECT tablename AS name FROM pg_tables WHERE schemaname='public' ORDER BY tablename").Scan(t.Context(), &tables); err != nil {
			t.Fatal(err)
		}

		for _, table := range tables {
			// Every authority/bookkeeping table must reject even a zero-row DML
			// statement. Denial cannot rely on a row trigger finding one row.
			_, err := pgdriver.Unwrap(s.DB()).Exec(t.Context(), `DELETE FROM public."`+table.Name+`" WHERE false`)
			exclusionDenied(t, err)

			if f.privilege(role, table.Name, "INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER") {
				t.Fatal("effective mutation grant survived", role, table.Name)
			}
		}

		f.unchanged(before)
	}
}

func TestWriterExclusionTypedCallbackRollback(t *testing.T) {
	f := newExclusionFixture(t)
	// Install a real initial marker without grant cutover, then use a wrong
	// expected digest for the next activation. Validation reaches the held
	// callback after the new cutover has staged its ACL revocations.
	plain, err := postgres.NewWriterFloorAdmin(f.admin.DB(), "exclusion-test")
	if err != nil {
		t.Fatal(err)
	}

	first, err := plain.Activate(t.Context(), f.activation())
	if err != nil {
		t.Fatal(err)
	}

	f.drain()

	q := first.Activation()
	q.Epoch = "2"
	q.ActivationID = id.New(managed.PrefixOperation)
	q.ExpectedDigest = managed.Digest([]byte("wrong-current"))

	before, acl := f.snapshot(), f.aclSnapshot()
	if _, err = f.activate(t.Context(), q); !errors.Is(err, managed.ErrConflict) {
		t.Fatal("expected typed callback conflict after staged revocations", err)
	}

	f.unchanged(before)

	if f.aclSnapshot() != acl {
		t.Fatal("callback failure committed staged privilege changes")
	}

	if !f.privilege(f.legacy, "cp_audit_entries", "INSERT") {
		t.Fatal("rollback lost the pre-cutover runtime grant")
	}

	q.ExpectedDigest = first.Digest
	if _, err = f.activate(t.Context(), q); err != nil {
		t.Fatal("valid correction cannot commit", err)
	}

	if f.privilege(f.legacy, "cp_audit_entries", "INSERT") {
		t.Fatal("successful cutover retained writer grant")
	}
}

func TestWriterExclusionReplayRepairsGrantsWithoutMovingFloor(t *testing.T) {
	f := newExclusionFixture(t)
	f.drain()
	q := f.activation()

	first, err := f.activate(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}

	before := f.snapshot()
	exclusionExec(t, f.admin, "GRANT INSERT ON public.cp_audit_entries TO "+f.legacy)

	replay, err := f.activate(t.Context(), q)
	if err != nil || replay != first {
		t.Fatal(replay, err)
	}

	f.unchanged(before)

	if f.privilege(f.legacy, "cp_audit_entries", "INSERT") {
		t.Fatal("exact replay skipped privilege revocation")
	}

	q.MinimumProtocol = "2"
	f.requireRefusal(q)
}

func TestWriterExclusionPublicAndColumnGrants(t *testing.T) {
	f := newExclusionFixture(t)
	exclusionExec(t, f.admin, "GRANT INSERT ON public.cp_audit_entries TO "+f.legacy+" WITH GRANT OPTION")
	delegated := f.openRole(f.legacy, true)
	exclusionExec(t, delegated, "GRANT INSERT ON public.cp_audit_entries TO "+f.runtime)
	exclusionExec(t, f.admin, "GRANT INSERT(tenant_id,actor_id,action), UPDATE(action) ON public.cp_audit_entries TO "+f.legacy)
	exclusionExec(t, f.admin, "GRANT INSERT ON public.cp_audit_entries TO PUBLIC")
	exclusionExec(t, f.admin, "GRANT USAGE,UPDATE ON public.cp_audit_entries_id_seq TO PUBLIC")
	exclusionExec(t, f.admin, "GRANT CREATE ON SCHEMA public TO PUBLIC")
	exclusionExec(t, f.admin, "ALTER DEFAULT PRIVILEGES FOR ROLE "+f.owner+" GRANT INSERT ON TABLES TO PUBLIC")
	f.drain()

	if _, err := f.activate(t.Context(), f.activation()); err != nil {
		t.Fatal(err)
	}

	s := f.readOnly(f.legacy)
	before := f.snapshot()

	exclusionDenied(t, s.InsertAuditEntry(t.Context(), exclusionAudit()))
	_, err := pgdriver.Unwrap(s.DB()).Exec(t.Context(), "UPDATE public.cp_audit_entries SET action='denied' WHERE false")
	exclusionDenied(t, err)
	f.unchanged(before)
	exclusionExec(t, f.admin, "CREATE TABLE public.future_fixture(id integer)")

	if f.privilege(f.legacy, "future_fixture", "INSERT") {
		t.Fatal("owner default grants reopened future table")
	}
}

func TestWriterExclusionRejectsUnsafeRoles(t *testing.T) {
	for _, kind := range []string{"superuser", "createdb", "createrole", "replication", "bypassrls", "login", "member", "granted_role", "table_owner", "schema_owner", "database_owner"} {
		t.Run(kind, func(t *testing.T) {
			f := newExclusionFixture(t)
			f.drain()

			switch kind {
			case "superuser":
				exclusionExec(t, f.root, "ALTER ROLE "+f.legacy+" SUPERUSER")
			case "createdb":
				exclusionExec(t, f.root, "ALTER ROLE "+f.legacy+" CREATEDB")
			case "createrole":
				exclusionExec(t, f.root, "ALTER ROLE "+f.legacy+" CREATEROLE")
			case "replication":
				exclusionExec(t, f.root, "ALTER ROLE "+f.legacy+" REPLICATION")
			case "bypassrls":
				exclusionExec(t, f.root, "ALTER ROLE "+f.legacy+" BYPASSRLS")
			case "login":
				exclusionExec(t, f.root, "ALTER ROLE "+f.legacy+" LOGIN")
			case "member":
				exclusionExec(t, f.root, "GRANT "+f.owner+" TO "+f.legacy)
			case "granted_role":
				exclusionExec(t, f.root, "GRANT "+f.legacy+" TO "+f.runtime)
			case "database_owner":
				exclusionExec(t, f.root, "ALTER DATABASE "+f.database+" OWNER TO "+f.legacy)
			case "table_owner", "schema_owner":
				setup := exclusionOpen(t, f.databaseDSN)
				if kind == "table_owner" {
					exclusionExec(t, setup, "ALTER TABLE public.cp_audit_entries OWNER TO "+f.legacy)
					exclusionExec(t, setup, "GRANT SELECT ON public.cp_audit_entries, public.cp_audit_entries_id_seq TO "+f.owner)
				} else {
					exclusionExec(t, setup, "ALTER SCHEMA public OWNER TO "+f.legacy)
				}

				if err := setup.Close(); err != nil {
					t.Fatal(err)
				}
			}

			f.requireRefusal(f.activation())
		})
	}
}

func TestWriterExclusionCatalogDrift(t *testing.T) {
	for _, query := range []string{
		"CREATE TABLE public.unreviewed(id integer)",
		"CREATE VIEW public.unreviewed AS SELECT 1 AS n",
		"CREATE TABLE public.unreviewed(id integer) PARTITION BY RANGE(id)",
		"CREATE SCHEMA pgxescape",
		"CREATE FUNCTION public.unreviewed() RETURNS integer LANGUAGE SQL SECURITY DEFINER AS 'SELECT 1'",
		"CREATE FUNCTION public.cp_writer_floor_history_immutable(integer) RETURNS integer LANGUAGE SQL AS 'SELECT 1'",
		"CREATE SEQUENCE public.unowned_sequence",
		"CREATE RULE exclusion_rule AS ON UPDATE TO public.cp_audit_entries DO ALSO NOTHING",
		"CREATE TRIGGER unexpected BEFORE UPDATE ON public.cp_audit_entries FOR EACH STATEMENT EXECUTE FUNCTION public.cp_writer_floor_history_immutable()",
		"DROP TABLE public.cp_audit_entries",
	} {
		t.Run(query, func(t *testing.T) {
			f := newExclusionFixture(t)
			f.drain()
			exclusionExec(t, f.admin, query)
			f.requireRefusal(f.activation())
		})
	}
}

func TestWriterExclusionOpenPoolAndPreparedStatementRefuse(t *testing.T) {
	for _, kind := range []string{"pool", "transaction", "prepared_statement"} {
		t.Run(kind, func(t *testing.T) {
			f := newExclusionFixture(t)
			old := f.openRole(f.legacy, true)
			exclusionExec(t, old, "SELECT 1")

			var held *pgdriver.PgTx

			if kind == "transaction" {
				var err error

				held, err = pgdriver.Unwrap(old.DB()).BeginTxQuery(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}

				t.Cleanup(func() { _ = held.Rollback() })
			}

			if kind == "prepared_statement" {
				exclusionExec(t, old, "PREPARE old_write AS UPDATE public.cp_audit_entries SET action=action WHERE false")
			}

			for _, role := range f.policy.ExcludedRoles {
				exclusionExec(t, f.root, "ALTER ROLE "+role+" NOLOGIN")
			}
			// Disabling LOGIN is deliberately insufficient while this pool exists.
			f.requireRefusal(f.activation())

			if held != nil {
				if err := held.Rollback(); err != nil {
					t.Fatal(err)
				}
			}

			f.drain()

			if _, err := f.activate(t.Context(), f.activation()); err != nil {
				t.Fatal("drained retry refused", err)
			}
		})
	}
}

func TestWriterExclusionAdministrativeDeadline(t *testing.T) {
	f := newExclusionFixture(t)
	f.drain()
	blocker := exclusionOpen(t, f.databaseDSN)
	t.Cleanup(func() {
		if err := f.closeStore(blocker); err != nil {
			t.Error(err)
		}
	})

	held, err := pgdriver.Unwrap(blocker.DB()).BeginTxQuery(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = held.Rollback() })

	if _, err = held.NewRaw("SELECT pg_advisory_xact_lock(hashtextextended('ctrlplane-writer-floor-mutation-admission',0))").Exec(t.Context()); err != nil {
		t.Fatal(err)
	}

	before, acl := f.snapshot(), f.aclSnapshot()

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()

	if _, err = f.activate(ctx, f.activation()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("expected blocked initial floor lock deadline", err)
	}

	if time.Since(start) > 3*time.Second {
		t.Fatal("caller deadline not honored")
	}

	if err = held.Rollback(); err != nil {
		t.Fatal(err)
	}

	if err := f.closeStore(blocker); err != nil {
		t.Fatal(err)
	}

	f.unchanged(before)

	if f.aclSnapshot() != acl {
		t.Fatal("timed-out activation changed privileges")
	}
	// Cancellation may discard the session with SET ROLE. Reestablish it explicitly.
	exclusionExec(t, f.admin, "SET ROLE "+f.owner)

	if _, err := f.activate(t.Context(), f.activation()); err != nil {
		t.Fatal(err)
	}
}

func TestWriterExclusionHistoryAndPositiveProtocolRemainClosed(t *testing.T) {
	f := newExclusionFixture(t)

	action := controllerAction(t)
	if _, err := f.admin.SubmitManagedAction(t.Context(), action); err != nil {
		t.Fatal(err)
	}

	historical, err := f.admin.ReadManagedTarget(t.Context(), action.Key)
	if err != nil {
		t.Fatal(err)
	}

	f.drain()

	if _, err = f.activate(t.Context(), f.activation()); err != nil {
		t.Fatal(err)
	}

	history := f.readOnly(f.history)

	observed, err := history.ReadManagedTarget(t.Context(), action.Key)
	if err != nil || !reflect.DeepEqual(observed, historical) {
		t.Fatal("historical representation changed", err)
	}

	before := f.snapshot()
	_, err = pgdriver.Unwrap(history.DB()).Exec(t.Context(), "SELECT 1 FROM public.cp_managed_targets FOR UPDATE")
	exclusionDenied(t, err)

	runtime := f.readOnly(f.runtime)
	for _, query := range []string{"SET application_name='protocol-999'", "SET ctrlplane.writer_protocol='999'"} {
		exclusionExec(t, runtime, query)
	}

	exclusionDenied(t, runtime.InsertAuditEntry(t.Context(), exclusionAudit()))
	f.unchanged(before)
}

func TestWriterExclusionLegacyInstallationRemainsOptional(t *testing.T) {
	f := newExclusionFixture(t)
	legacy := f.openRole(f.legacy, true)

	plain, err := postgres.NewWriterFloorAdmin(f.admin.DB(), "exclusion-test")
	if err != nil {
		t.Fatal(err)
	}
	// The ordinary administrator is intentionally not a grant installer. Protected
	// hosts must require the explicit exclusion constructor and credential policy.
	if _, err = plain.Activate(t.Context(), f.activation()); err != nil {
		t.Fatal(err)
	}

	if err = legacy.InsertAuditEntry(t.Context(), exclusionAudit()); err != nil {
		t.Fatal("legacy constructor silently opted into grant cutover", err)
	}

	if !f.privilege(f.legacy, "cp_audit_entries", "INSERT") {
		t.Fatal("legacy grants changed")
	}
}
