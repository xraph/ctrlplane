package store_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/store/postgres"
)

type exclusionFixture struct {
	closed                                                                                   map[*postgres.Store]bool
	t                                                                                        *testing.T
	root, admin                                                                              *postgres.Store
	rootDSN, databaseDSN, database, owner, administrator, legacy, runtime, history, password string
	runtimeStores                                                                            []*postgres.Store
	allStores                                                                                []*postgres.Store
	policy                                                                                   postgres.WriterFloorExclusionPolicy
}

func exclusionExec(t *testing.T, s *postgres.Store, query string, args ...any) {
	t.Helper()

	if _, err := pgdriver.Unwrap(s.DB()).Exec(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func exclusionOpen(t *testing.T, dsn string) *postgres.Store {
	t.Helper()

	d := pgdriver.New()
	if err := d.Open(t.Context(), dsn); err != nil {
		t.Fatal(err)
	}

	db, err := grove.Open(d)
	if err != nil {
		_ = d.Close()

		t.Fatal(err)
	}

	s := postgres.New(db)
	if err = s.Ping(t.Context()); err != nil {
		_ = s.Close()

		t.Fatal(err)
	}

	return s
}

func exclusionToken(t *testing.T) string {
	t.Helper()

	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}

	return hex.EncodeToString(raw[:])
}

func newExclusionFixture(t *testing.T) *exclusionFixture {
	t.Helper()

	dsn := os.Getenv("CTRLPLANE_TEST_POSTGRES_DSN")
	if dsn == "" {
		if os.Getenv("CTRLPLANE_WRITER_EXCLUSION_REQUIRED") == "1" {
			t.Fatal("required exclusion PostgreSQL DSN missing")
		}

		t.Skip("CTRLPLANE_TEST_POSTGRES_DSN not configured")
	}

	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.User == nil {
		t.Fatal("exclusion fixture requires PostgreSQL URL DSN")
	}

	token := exclusionToken(t)
	f := &exclusionFixture{t: t, rootDSN: dsn, database: "exclusion_" + token, owner: "exo_" + token, administrator: "exa_" + token, legacy: "exl_" + token, runtime: "exr_" + token, history: "exh_" + token, password: exclusionToken(t)}
	f.root = exclusionOpen(t, dsn)
	createdRoles := []string{}
	createdDB := false

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		for _, s := range f.allStores {
			if e := f.closeStore(s); e != nil {
				t.Error(e)
			}
		}

		if createdDB {
			if _, e := pgdriver.Unwrap(f.root.DB()).Exec(ctx, "DROP DATABASE "+f.database+" WITH (FORCE)"); e != nil {
				t.Error(e)
			}
		}
		// Only this fixture's generated role names are touched. Database removal
		// removes its owned objects and grants before cluster-global role cleanup.
		for _, role := range slices.Backward(createdRoles) {
			if _, e := pgdriver.Unwrap(f.root.DB()).Exec(ctx, "DROP ROLE "+role); e != nil {
				t.Error(e)
			}
		}

		if e := f.root.Close(); e != nil {
			t.Error(e)
		}
	})

	var super bool
	if err = pgdriver.Unwrap(f.root.DB()).NewRaw("SELECT rolsuper FROM pg_roles WHERE rolname=session_user").Scan(t.Context(), &super); err != nil || !super {
		t.Fatal("fixture setup requires disposable cluster administrator", err)
	}

	var prepared string
	if err = pgdriver.Unwrap(f.root.DB()).NewRaw("SHOW max_prepared_transactions").Scan(t.Context(), &prepared); err != nil || prepared != "0" {
		t.Fatal("fresh fixture must disable prepared transactions", err)
	}

	for _, name := range []string{f.owner, f.administrator, f.legacy, f.runtime, f.history} {
		login := "LOGIN"
		if name == f.owner {
			login = "NOLOGIN"
		}

		exclusionExec(t, f.root, "CREATE ROLE "+name+" "+login+" NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '"+f.password+"'")
		createdRoles = append(createdRoles, name)
	}

	exclusionExec(t, f.root, "GRANT "+f.owner+" TO "+f.administrator)
	exclusionExec(t, f.root, "CREATE DATABASE "+f.database+" OWNER "+f.owner)

	createdDB = true
	parsed.Path = "/" + f.database
	query := parsed.Query()
	query.Del("options")
	query.Del("role")
	query.Set("pool_max_conns", "1")
	query.Set("pool_min_conns", "0")
	parsed.RawQuery = query.Encode()
	f.databaseDSN = parsed.String()
	setup := exclusionOpen(t, f.databaseDSN)
	exclusionExec(t, setup, "ALTER SCHEMA public OWNER TO "+f.owner)

	if err = setup.Close(); err != nil {
		t.Fatal(err)
	}

	f.admin = f.openRole(f.administrator, false)
	// A one-connection pool makes SET ROLE and activation use the same session.
	// If the connection is replaced, the production session/current-user check
	// must fail closed. This fixture never treats pool configuration as identity.
	exclusionExec(t, f.admin, "SET ROLE "+f.owner)

	var identity string
	if err = pgdriver.Unwrap(f.admin.DB()).NewRaw("SELECT session_user||'/'||current_user").Scan(t.Context(), &identity); err != nil || identity != f.administrator+"/"+f.owner {
		t.Fatal(identity, err)
	}

	if err = f.admin.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}

	a, err := postgres.NewWriterFloorAdmin(f.admin.DB(), "exclusion-test")
	if err != nil {
		t.Fatal(err)
	}

	if err = a.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}

	f.policy = postgres.WriterFloorExclusionPolicy{OwnerRole: f.owner, AdminRole: f.administrator, ExcludedRoles: []string{f.legacy, f.runtime, f.history}, AdditionalTables: []string{"grove_migrations", "grove_migration_locks"}}
	// These are intentionally unfenced positive-control grants, never deployed grants.
	for _, role := range f.policy.ExcludedRoles {
		exclusionExec(t, f.admin, "GRANT CONNECT ON DATABASE "+f.database+" TO "+role)
		exclusionExec(t, f.admin, "GRANT USAGE ON SCHEMA public TO "+role)
		exclusionExec(t, f.admin, "GRANT ALL ON ALL TABLES IN SCHEMA public TO "+role)
		exclusionExec(t, f.admin, "GRANT ALL ON ALL SEQUENCES IN SCHEMA public TO "+role)
	}

	return f
}

func (f *exclusionFixture) dsn(role string) string {
	f.t.Helper()

	parsed, err := url.Parse(f.databaseDSN)
	if err != nil {
		f.t.Fatal(err)
	}

	parsed.User = url.UserPassword(role, f.password)

	return parsed.String()
}

func (f *exclusionFixture) openRole(role string, runtime bool) *postgres.Store {
	s := exclusionOpen(f.t, f.dsn(role))

	f.allStores = append(f.allStores, s)
	if runtime {
		f.runtimeStores = append(f.runtimeStores, s)
	}

	return s
}

func (f *exclusionFixture) drain() {
	f.t.Helper()

	for _, s := range f.runtimeStores {
		if err := f.closeStore(s); err != nil {
			f.t.Fatal(err)
		}
	}

	f.runtimeStores = nil
	for _, role := range f.policy.ExcludedRoles {
		exclusionExec(f.t, f.root, "ALTER ROLE "+role+" NOLOGIN")
	}
}

func (f *exclusionFixture) activation() managed.WriterFloorActivation {
	return managed.WriterFloorActivation{Schema: managed.WriterFloorSchema, AuthorityID: "exclusion-test", MinimumProtocol: "1", Epoch: "1", ActivationID: id.New(managed.PrefixOperation)}
}

func (f *exclusionFixture) activate(ctx context.Context, q managed.WriterFloorActivation) (managed.WriterFloor, error) {
	a, err := postgres.NewWriterFloorExclusionAdmin(f.admin.DB(), "exclusion-test", f.policy)
	if err != nil {
		return managed.WriterFloor{}, err
	}

	return a.Activate(ctx, q)
}

func (f *exclusionFixture) readOnly(role string) *postgres.Store {
	// Test-only access lets an adversary reach actual SQL after cutover, instead
	// of counting a connection rejection as a DML refusal. No DML is restored.
	exclusionExec(f.t, f.root, "ALTER ROLE "+role+" LOGIN")
	exclusionExec(f.t, f.admin, "GRANT CONNECT ON DATABASE "+f.database+" TO "+role)
	exclusionExec(f.t, f.admin, "GRANT USAGE ON SCHEMA public TO "+role)
	exclusionExec(f.t, f.admin, "GRANT SELECT ON ALL TABLES IN SCHEMA public TO "+role)

	return f.openRole(role, true)
}

func (f *exclusionFixture) snapshot() []string {
	f.t.Helper()

	var tables []struct {
		Name string `grove:"name"`
	}

	pg := pgdriver.Unwrap(f.admin.DB())
	if err := pg.NewRaw("SELECT tablename AS name FROM pg_tables WHERE schemaname='public' ORDER BY tablename COLLATE \"C\"").Scan(f.t.Context(), &tables); err != nil {
		f.t.Fatal(err)
	}

	out := []string{}

	for _, table := range tables {
		// Names come from a fresh fixture catalog; quote every identifier.
		var rows string

		quoted := `"` + strings.ReplaceAll(table.Name, `"`, `""`) + `"`
		if err := pg.NewRaw("SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM public."+quoted+" t").Scan(f.t.Context(), &rows); err != nil {
			f.t.Fatal(err)
		}

		out = append(out, table.Name+":"+rows)
	}

	var sequences []struct {
		Name string `grove:"name"`
	}
	if err := pg.NewRaw("SELECT sequencename AS name FROM pg_sequences WHERE schemaname='public' ORDER BY sequencename COLLATE \"C\"").Scan(f.t.Context(), &sequences); err != nil {
		f.t.Fatal(err)
	}

	for _, sequence := range sequences {
		var state string

		quoted := `"` + strings.ReplaceAll(sequence.Name, `"`, `""`) + `"`
		if err := pg.NewRaw("SELECT last_value::text||':'||is_called::text FROM public."+quoted).Scan(f.t.Context(), &state); err != nil {
			f.t.Fatal(err)
		}

		out = append(out, sequence.Name+":"+state)
	}

	return out
}

func (f *exclusionFixture) aclSnapshot() string {
	f.t.Helper()

	var raw string

	err := pgdriver.Unwrap(f.admin.DB()).NewRaw(`SELECT jsonb_build_object(
      'relations',(SELECT jsonb_agg(jsonb_build_array(c.relname,c.relacl::text) ORDER BY c.relname) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public'),
      'columns',(SELECT jsonb_agg(jsonb_build_array(c.relname,a.attname,a.attacl::text) ORDER BY c.relname,a.attnum) FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND a.attnum>0),
      'routines',(SELECT jsonb_agg(jsonb_build_array(p.oid::regprocedure::text,p.proacl::text) ORDER BY p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='public'),
      'defaults',(SELECT jsonb_agg(to_jsonb(d) ORDER BY d.oid) FROM pg_default_acl d),
      'schema',(SELECT nspacl::text FROM pg_namespace WHERE nspname='public'),
      'database',(SELECT datacl::text FROM pg_database WHERE datname=current_database()))::text`).Scan(f.t.Context(), &raw)
	if err != nil {
		f.t.Fatal(err)
	}

	return raw
}

func exclusionDenied(t *testing.T, err error) {
	t.Helper()

	var state interface{ SQLState() string }
	if err == nil || !errors.As(err, &state) || state.SQLState() != "42501" {
		t.Fatalf("expected database insufficient_privilege, got %v", err)
	}
}

func (f *exclusionFixture) unchanged(before []string) {
	f.t.Helper()

	if !reflect.DeepEqual(before, f.snapshot()) {
		f.t.Fatal("authority table or sequence changed after refused operation")
	}
}

func (f *exclusionFixture) privilege(role, table, privilege string) bool {
	f.t.Helper()

	var ok bool
	if err := pgdriver.Unwrap(f.admin.DB()).NewRaw("SELECT has_table_privilege($1,$2,$3)", role, "public."+table, privilege).Scan(f.t.Context(), &ok); err != nil {
		f.t.Fatal(err)
	}

	return ok
}

func (f *exclusionFixture) requireRefusal(q managed.WriterFloorActivation) {
	f.t.Helper()

	before, acl := f.snapshot(), f.aclSnapshot()
	if _, err := f.activate(f.t.Context(), q); err == nil {
		f.t.Fatal("unsafe activation succeeded")
	}

	f.unchanged(before)

	if after := f.aclSnapshot(); after != acl {
		f.t.Fatal("refused activation changed grants", fmt.Sprintf("before=%s after=%s", acl, after))
	}
}

func (f *exclusionFixture) closeStore(s *postgres.Store) error {
	if f.closed == nil {
		f.closed = make(map[*postgres.Store]bool)
	}

	if f.closed[s] {
		return nil
	}

	f.closed[s] = true

	return s.Close()
}
