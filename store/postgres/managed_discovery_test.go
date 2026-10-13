package postgres

import (
	"context"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/internal/managedstate"
	"github.com/xraph/ctrlplane/managed"
)

func discoveryPG(t *testing.T) *Store {
	t.Helper()

	if os.Getenv("CTRLPLANE_DISCOVERY_REQUIRED") == "1" && os.Getenv("CTRLPLANE_TEST_POSTGRES_DSN") == "" {
		t.Fatal("CTRLPLANE_TEST_POSTGRES_DSN required")
	}

	s, _ := newProjectionPG(t)

	return s
}

func TestManagedDiscoveryPostgresConcurrentAcceptanceExactlyOneIndex(t *testing.T) {
	s := discoveryPG(t)
	q := projectionAction(t)
	start := make(chan struct{})
	results := make(chan error, 2)

	var group sync.WaitGroup
	for range 2 {
		group.Go(func() { <-start; _, err := s.SubmitManagedAction(t.Context(), q); results <- err })
	}

	close(start)
	group.Wait()
	close(results)

	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}

	head, err := s.ReadManagedWorkHead(t.Context(), q.Key)
	if err != nil || head.LastOrdinal != "1" {
		t.Fatal(head, err)
	}

	page, err := s.ListManagedUnresolvedWork(t.Context(), managed.WorkPageRequest{Key: q.Key, Limit: 32})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != q.ID {
		t.Fatal(page, err)
	}
}

func TestManagedDiscoveryPostgresAcceptanceAndAuditRollback(t *testing.T) {
	s := discoveryPG(t)

	q := projectionAction(t)
	if _, err := s.pg.NewRaw(`CREATE FUNCTION public.discovery_test_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='work_index' THEN RAISE EXCEPTION 'injected work index failure'; END IF; RETURN NEW; END $$;
CREATE TRIGGER discovery_test_fail BEFORE INSERT ON cp_managed_controller_records FOR EACH ROW EXECUTE FUNCTION public.discovery_test_fail()`).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SubmitManagedAction(t.Context(), q); err == nil {
		t.Fatal("partial action accepted")
	}

	for _, query := range []string{"SELECT count(*) FROM cp_managed_targets", "SELECT count(*) FROM cp_managed_controller_records"} {
		var count int
		if err := s.pg.NewRaw(query).Scan(t.Context(), &count); err != nil || count != 0 {
			t.Fatal("partial target/action/audit/discovery persisted", count, err)
		}
	}
}

func TestManagedDiscoveryPostgresPagesAndMissingRowRefusal(t *testing.T) {
	s := discoveryPG(t)

	action := projectionAction(t)
	if _, err := s.SubmitManagedAction(t.Context(), action); err != nil {
		t.Fatal(err)
	}

	projectionLease(t, s, action.Key, action.ID)

	for range 3 {
		target, err := s.ReadManagedTarget(t.Context(), action.Key)
		if err != nil {
			t.Fatal(err)
		}

		q := action
		q.Command = projectionCommand(t, s, action.Key)
		q.Kind = managed.ActionPause
		q.NewTarget = nil
		q.ReleaseID = id.Nil
		q.ExpectedDesiredRevision = target.DesiredRevision
		q.OriginalCommand = []byte(q.ID.String())
		q.OccurredAt = time.Now().UTC().Add(-time.Second)
		signProjectionAction(t, &q)

		if _, err = s.SubmitManagedAction(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}

	cursor := ""
	count := 0

	for {
		page, err := s.ListManagedUnresolvedWork(t.Context(), managed.WorkPageRequest{Key: action.Key, Limit: 1, Cursor: cursor})
		if err != nil || len(page.Items) != 1 {
			t.Fatal(page, err)
		}

		count++
		if page.Items[0].Ordinal != managed.Decimal(strconv.Itoa(count)) {
			t.Fatal(page)
		}

		cursor = page.NextCursor
		if cursor == "" {
			break
		}

		if count > 4 {
			t.Fatal("page loop")
		}
	}

	if count != 4 {
		t.Fatal(count)
	}

	if _, err := s.pg.NewRaw("DELETE FROM cp_managed_controller_records WHERE kind='work_index' AND key='00000000000000000002'").Exec(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ListManagedUnresolvedWork(t.Context(), managed.WorkPageRequest{Key: action.Key, Limit: 32}); err == nil {
		t.Fatal("missing ordinal hidden")
	}
}

func TestManagedDiscoveryPostgresWithholdsOversizedIndexPayload(t *testing.T) {
	s := discoveryPG(t)

	q := projectionAction(t)
	if _, err := s.SubmitManagedAction(t.Context(), q); err != nil {
		t.Fatal(err)
	}

	if _, err := s.pg.NewRaw("ALTER TABLE cp_managed_controller_records DROP CONSTRAINT cp_managed_work_discovery_size").Exec(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, err := s.pg.NewRaw("UPDATE cp_managed_controller_records SET data=$1 WHERE kind='work_index'", make([]byte, managed.MaxWorkReference+1)).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}

	var scope string
	if err := s.pg.NewRaw("SELECT scope FROM cp_managed_controller_records WHERE kind='work_index'").Scan(t.Context(), &scope); err != nil {
		t.Fatal(err)
	}

	err := (managedBackend{store: s}).ControllerAtomic(t.Context(), "test-discovery", []managed.Key{q.Key}, func(tx managedstate.ControllerTransaction) error {
		rows, readErr := tx.(managedstate.DiscoveryPageTransaction).ListDiscoveryWork(t.Context(), scope, "00000000000000000000", 1)
		if readErr != nil {
			return readErr
		}

		if len(rows) != 1 || rows[0].Size != managed.MaxWorkReference+1 || len(rows[0].Data) != 0 {
			t.Fatal("oversized index allocated", rows)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err = s.ListManagedUnresolvedWork(context.Background(), managed.WorkPageRequest{Key: q.Key, Limit: 1}); err == nil {
		t.Fatal("corrupt page accepted")
	}
}
