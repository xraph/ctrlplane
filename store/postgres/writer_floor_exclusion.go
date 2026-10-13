package postgres

import (
	"regexp"
	"sort"
	"strings"

	"github.com/xraph/grove"

	"github.com/xraph/ctrlplane/managed"
)

// WriterFloorExclusionPolicy is trusted deployment configuration for a dedicated
// authority database. It is never accepted from a managed command or HTTP request.
// Every retired writer, history and current runtime principal belongs in ExcludedRoles.
// This disabled-protocol cutover grants none of them authority DML.
type WriterFloorExclusionPolicy struct {
	OwnerRole     string
	AdminRole     string
	ExcludedRoles []string
	// AdditionalTables lists exact reviewed Grove bookkeeping tables in public.
	// Unknown catalog objects refuse; this is not a wildcard or a discovery result.
	AdditionalTables []string
}

type writerFloorExclusion struct{ sql string }

var writerFloorSQLIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// NewWriterFloorExclusionAdmin opts a dedicated admin handle into atomic grant
// revocation and floor activation. Its connection must have session_user AdminRole
// and current_user OwnerRole on every acquired connection. Runtime constructors
// and the existing floor administrator keep their existing installation behavior.
// The launcher must first close admission, disable these logins and drain sessions.
// Construction does not verify those facts; activation checks the installed catalog.
func NewWriterFloorExclusionAdmin(db *grove.DB, authority string, policy WriterFloorExclusionPolicy) (*WriterFloorAdmin, error) {
	a, err := NewWriterFloorAdmin(db, authority)
	if err != nil {
		return nil, err
	}

	sql, err := writerFloorExclusionSQL(policy)
	if err != nil {
		return nil, err
	}

	a.backend.exclusion = &writerFloorExclusion{sql: sql}

	return a, nil
}

func writerFloorExclusionSQL(p WriterFloorExclusionPolicy) (string, error) {
	if !writerFloorSQLIdentifier.MatchString(p.OwnerRole) || !writerFloorSQLIdentifier.MatchString(p.AdminRole) || p.OwnerRole == p.AdminRole || p.OwnerRole == "public" || p.AdminRole == "public" || strings.HasPrefix(p.OwnerRole, "pg_") || strings.HasPrefix(p.AdminRole, "pg_") || len(p.ExcludedRoles) < 1 || len(p.ExcludedRoles) > 16 || len(p.AdditionalTables) > 4 {
		return "", managed.ErrInvalid
	}

	roles := append([]string(nil), p.ExcludedRoles...)
	tables := append([]string(nil), writerFloorAuthorityTables...)

	seen := map[string]bool{p.OwnerRole: true, p.AdminRole: true, "public": true}
	for _, role := range roles {
		if !writerFloorSQLIdentifier.MatchString(role) || strings.HasPrefix(role, "pg_") || seen[role] {
			return "", managed.ErrInvalid
		}

		seen[role] = true
	}

	seen = make(map[string]bool)
	for _, name := range tables {
		seen[name] = true
	}

	for _, name := range p.AdditionalTables {
		if !writerFloorSQLIdentifier.MatchString(name) || strings.HasPrefix(name, "cp_") || strings.HasPrefix(name, "pg_") || seen[name] {
			return "", managed.ErrInvalid
		}

		seen[name] = true
		tables = append(tables, name)
	}

	sort.Strings(roles)
	sort.Strings(tables)
	// Identifiers are restricted above, and appear as SQL literals. SQL identifiers
	// originating in the catalog are quoted separately with format('%I', ...).
	array := func(values []string) string { return "ARRAY['" + strings.Join(values, "','") + "']::text[]" }

	return strings.NewReplacer("@OWNER@", "'"+p.OwnerRole+"'", "@ADMIN@", "'"+p.AdminRole+"'", "@ROLES@", array(roles), "@TABLES@", array(tables)).Replace(writerFloorExclusionTemplate), nil
}

var writerFloorAuthorityTables = []string{
	"cp_audit_entries", "cp_bootstrap_workloads", "cp_certificates", "cp_datacenters",
	"cp_deployments", "cp_domains", "cp_health_checks", "cp_health_results", "cp_instances",
	"cp_logs", "cp_managed_commands", "cp_managed_controller_records", "cp_managed_targets",
	"cp_metrics", "cp_releases", "cp_resource_snapshots", "cp_routes", "cp_secrets",
	"cp_templates", "cp_tenants", "cp_traces", "cp_workloads",
	"cp_writer_floor_current", "cp_writer_floor_history",
}

// Executed only inside WriterFloorAdminAtomic, after its exclusive namespace and
// authority locks and before the typed floor callback. Any refusal rolls back both
// grants and marker/history. Exact activation replay also rechecks/revokes grants.
// No SECURITY DEFINER function, runtime-executable cutover, trigger bypass or GUC
// carrying a claimed protocol is installed. Timeouts only bound administrative work.
const writerFloorExclusionTemplate = `DO $cutover$
DECLARE
    owner_name text := @OWNER@;
    admin_name text := @ADMIN@;
    excluded text[] := @ROLES@;
    expected_tables text[] := @TABLES@;
    owner_id oid;
    principal text;
    grantee text;
    relation record;
    column_names text;
    lock_list text;
BEGIN
    PERFORM pg_catalog.set_config('search_path','pg_catalog',true);
    PERFORM pg_catalog.set_config('lock_timeout','5s',true);
    PERFORM pg_catalog.set_config('statement_timeout','15s',true);
    IF session_user <> admin_name OR current_user <> owner_name THEN
        RAISE EXCEPTION 'floor exclusion requires separate configured admin and owner' USING ERRCODE='42501';
    END IF;
    SELECT oid INTO STRICT owner_id FROM pg_catalog.pg_roles WHERE rolname=owner_name
      AND NOT rolcanlogin AND NOT rolsuper AND NOT rolcreaterole AND NOT rolcreatedb AND NOT rolreplication AND NOT rolbypassrls;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname=admin_name
        AND NOT rolsuper AND NOT rolcreaterole AND NOT rolcreatedb AND NOT rolreplication AND NOT rolbypassrls)
      OR (SELECT datdba FROM pg_catalog.pg_database WHERE datname=current_database()) <> owner_id
      OR (SELECT nspowner FROM pg_catalog.pg_namespace WHERE nspname='public') <> owner_id THEN
        RAISE EXCEPTION 'unexpected administrator or database/schema ownership';
    END IF;
    FOREACH principal IN ARRAY excluded LOOP
        IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname=principal AND NOT rolcanlogin
            AND NOT rolsuper AND NOT rolcreaterole AND NOT rolcreatedb AND NOT rolreplication AND NOT rolbypassrls)
          OR EXISTS (SELECT 1 FROM pg_catalog.pg_auth_members m JOIN pg_catalog.pg_roles r
            ON r.oid=m.member OR r.oid=m.roleid WHERE r.rolname=principal) THEN
            RAISE EXCEPTION 'excluded principal must be disabled and have no memberships: %',principal;
        END IF;
    END LOOP;
    IF current_setting('max_prepared_transactions') <> '0'
      OR EXISTS (SELECT 1 FROM pg_catalog.pg_prepared_xacts WHERE database=current_database())
      OR EXISTS (SELECT 1 FROM pg_catalog.pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid()) THEN
        RAISE EXCEPTION 'authority cutover requires drained clients and disabled prepared transactions';
    END IF;
    IF EXISTS (SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname<>'public' AND nspname<>'information_schema' AND nspname !~ '^pg_')
      OR EXISTS (SELECT 1 FROM pg_catalog.pg_extension WHERE extname<>'plpgsql') THEN
        RAISE EXCEPTION 'unreviewed schema or extension';
    END IF;
    IF (SELECT array_agg(c.relname::text ORDER BY c.relname::text COLLATE "C") FROM pg_catalog.pg_class c
        JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind='r') IS DISTINCT FROM expected_tables
      OR EXISTS (SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
        WHERE n.nspname='public' AND (c.relkind NOT IN ('r','S','i') OR c.relowner<>owner_id)) THEN
        RAISE EXCEPTION 'unreviewed table, view, partition, sequence or owner';
    END IF;
    IF EXISTS (SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
        WHERE n.nspname='public' AND c.relkind='S' AND NOT EXISTS (
            SELECT 1 FROM pg_catalog.pg_depend d JOIN pg_catalog.pg_class t ON t.oid=d.refobjid
            WHERE d.classid='pg_catalog.pg_class'::regclass AND d.objid=c.oid
              AND d.refclassid='pg_catalog.pg_class'::regclass AND d.deptype IN ('a','i')
              AND t.relnamespace=n.oid AND t.relname=ANY(expected_tables)))
      OR EXISTS (SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
        WHERE n.nspname='public' AND (p.proname<>'cp_writer_floor_history_immutable' OR p.pronargs<>0 OR p.prorettype<>'pg_catalog.trigger'::regtype OR p.proowner<>owner_id OR p.prosecdef))
      OR EXISTS (SELECT 1 FROM pg_catalog.pg_rewrite r JOIN pg_catalog.pg_class c ON c.oid=r.ev_class
        JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public')
      OR EXISTS (SELECT 1 FROM pg_catalog.pg_trigger t JOIN pg_catalog.pg_class c ON c.oid=t.tgrelid
        JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND NOT t.tgisinternal
        AND (c.relname<>'cp_writer_floor_history' OR t.tgname<>'cp_writer_floor_history_immutable'
          OR t.tgfoid<> 'public.cp_writer_floor_history_immutable()'::regprocedure)) THEN
        RAISE EXCEPTION 'unreviewed sequence, function, rule or trigger';
    END IF;
    SELECT string_agg(format('public.%I',name),',' ORDER BY name) INTO lock_list FROM unnest(expected_tables) AS name;
    EXECUTE 'LOCK TABLE '||lock_list||' IN ACCESS EXCLUSIVE MODE';
    -- No admitted protocol exists yet. Remove direct, PUBLIC and column grants
    -- from every excluded principal. CASCADE also removes delegated grant paths.
    FOREACH principal IN ARRAY array_prepend('public',excluded) LOOP
        grantee := CASE WHEN principal='public' THEN 'PUBLIC' ELSE quote_ident(principal) END;
        EXECUTE format('REVOKE ALL PRIVILEGES ON DATABASE %I FROM %s CASCADE',current_database(),grantee);
        EXECUTE 'REVOKE ALL PRIVILEGES ON SCHEMA public FROM '||grantee||' CASCADE';
        EXECUTE 'REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM '||grantee||' CASCADE';
        EXECUTE 'REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM '||grantee||' CASCADE';
        EXECUTE 'REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public FROM '||grantee||' CASCADE';
        FOR relation IN SELECT c.oid,c.relname FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
            WHERE n.nspname='public' AND c.relkind='r' ORDER BY c.relname LOOP
            SELECT string_agg(quote_ident(attname),',' ORDER BY attnum) INTO column_names
                FROM pg_catalog.pg_attribute WHERE attrelid=relation.oid AND attnum>0 AND NOT attisdropped;
            EXECUTE format('REVOKE ALL PRIVILEGES (%s) ON public.%I FROM %s CASCADE',column_names,relation.relname,grantee);
        END LOOP;
        EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I REVOKE ALL ON TABLES FROM %s',owner_name,grantee);
        EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I REVOKE ALL ON SEQUENCES FROM %s',owner_name,grantee);
        EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I REVOKE ALL ON FUNCTIONS FROM %s',owner_name,grantee);
        EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA public REVOKE ALL ON TABLES FROM %s',owner_name,grantee);
        EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA public REVOKE ALL ON SEQUENCES FROM %s',owner_name,grantee);
        EXECUTE format('ALTER DEFAULT PRIVILEGES FOR ROLE %I IN SCHEMA public REVOKE ALL ON FUNCTIONS FROM %s',owner_name,grantee);
    END LOOP;
    FOREACH principal IN ARRAY excluded LOOP
        IF has_database_privilege(principal,current_database(),'CREATE') OR has_database_privilege(principal,current_database(),'TEMP')
          OR has_schema_privilege(principal,'public','CREATE') OR EXISTS (
            SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
              WHERE n.nspname='public' AND c.relkind='r' AND
              (has_table_privilege(principal,c.oid,'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
               OR has_any_column_privilege(principal,c.oid,'INSERT,UPDATE,REFERENCES')))
          OR EXISTS (SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
              WHERE CASE WHEN n.nspname='public' AND c.relkind='S' THEN has_sequence_privilege(principal,c.oid,'USAGE,UPDATE') ELSE false END)
          OR EXISTS (SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
              WHERE n.nspname='public' AND has_function_privilege(principal,p.oid,'EXECUTE')) THEN
            RAISE EXCEPTION 'effective mutation path survived exclusion: %',principal;
        END IF;
    END LOOP;
END; $cutover$;`
