package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
)

func projectionAction(t *testing.T) managed.ActionSubmission {
	t.Helper()

	identity := projectionIdentity()

	owner := id.New(id.PrefixWorkload)
	for n := range identity.Manifest.Members {
		identity.Manifest.Members[n].OwnerID = owner
	}

	provision := managed.ProvisionSpec{Version: "1", Services: []managed.ProvisionService{
		{Name: "api", Image: "example/api@sha256:" + strings.Repeat("a", 64), Role: "main", CPUMillis: "100", MemoryMiB: "16", PIDLimit: "16", Startup: managed.StartupBindingRef{BindingID: "api-binding", BindingGeneration: "1", BindingDigest: strings.Repeat("b", 64)}},
		{Name: "sidecar", Image: "example/sidecar@sha256:" + strings.Repeat("c", 64), Role: "sidecar", CPUMillis: "100", MemoryMiB: "16", PIDLimit: "16", Startup: managed.StartupBindingRef{BindingID: "query-binding", BindingGeneration: "1", BindingDigest: strings.Repeat("d", 64)}},
	}}

	raw, err := json.Marshal(provision)
	if err != nil {
		t.Fatal(err)
	}

	spec, err := json.Marshal(managed.ManagedSpec{Profile: "ctrlplane.managed.v1", RuntimeRoles: []managed.RuntimeRole{managed.Polling, managed.RetainedQuery}, Provision: raw})
	if err != nil {
		t.Fatal(err)
	}

	identity.Manifest.Spec = spec
	identity.Manifest.SpecDigest = managed.Digest(spec)
	q := managed.ActionSubmission{Command: managed.Command{Key: identity.Key, ID: id.New(managed.PrefixOperation)}, Kind: managed.ActionDeploy, Installation: "installation", Authority: "authority", ApplicationID: "app", OrganizationID: "org", Actor: managed.AuditActor{Subject: "verified-operator"}, OriginalCommand: []byte(`{"request":"deploy"}`), NewTarget: &identity, ReleaseID: id.New(id.PrefixRelease), OccurredAt: time.Now().UTC().Add(-time.Second)}
	signProjectionAction(t, &q)

	return q
}

func signProjectionAction(t *testing.T, q *managed.ActionSubmission) {
	t.Helper()

	q.OriginalDigest = managed.Digest(q.OriginalCommand)

	var err error

	q.CanonicalDigest, err = managed.ActionDigest(*q)
	if err != nil {
		t.Fatal(err)
	}
}

func projectionIdentity() managed.Identity {
	iid := id.New(id.PrefixInstance)
	spec := []byte(`{"image":"sha256:fixture"}`)

	return managed.Identity{ProvisionOperationID: id.New(managed.PrefixOperation), Key: managed.Key{TenantID: id.New(id.PrefixTenant).String(), InstanceID: iid}, Physical: managed.Physical{Provider: "docker", Placement: "daemon/configuration-digest", Project: "cp-" + iid.String(), Generation: iid.String()}, Manifest: managed.Manifest{Version: 1, Spec: spec, SpecDigest: managed.Digest(spec), Members: []managed.Member{{Name: "a", Service: "api", Installation: "installation", Namespace: "namespace", Queue: "queue", Build: "v1", ArtifactDigest: managed.Digest([]byte("image")), ConfigurationDigest: managed.Digest([]byte("config"))}, {Name: "b", Service: "sidecar", Installation: "installation", Namespace: "other", Queue: "queue", Build: "v2", ArtifactDigest: managed.Digest([]byte("image2")), ConfigurationDigest: managed.Digest([]byte("config2"))}}}}
}

func projectionCommand(t *testing.T, s *Store, key managed.Key) managed.Command {
	t.Helper()

	target, err := s.ReadManagedTarget(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}

	return managed.Command{Key: key, ID: id.New(managed.PrefixOperation), Revision: target.Revision, Owner: "projection-test", LeaseEpoch: target.LeaseEpoch}
}

func projectionLease(t *testing.T, s *Store, key managed.Key, action id.ID) managed.ProjectionRequest {
	t.Helper()
	command := projectionCommand(t, s, key)

	command.Owner = "projection-test"
	if _, err := s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: command, Duration: managed.MaxLease}); err != nil {
		t.Fatal(err)
	}

	return managed.ProjectionRequest{Command: projectionCommand(t, s, key), ActionID: action, Stage: managed.ProjectionPending}
}

func projectionFixture(t *testing.T) (*Store, managed.ActionSubmission, managed.ProjectionRequest) {
	t.Helper()
	s := projectionStore(t)

	q := projectionAction(t)
	if _, err := s.SubmitManagedAction(t.Context(), q); err != nil {
		t.Fatal(err)
	}

	return s, q, projectionLease(t, s, q.Key, q.ID)
}

func assertProjectionRefused(t *testing.T, s *Store, q managed.ProjectionRequest) {
	t.Helper()
	before := projectionSnapshot(t, s)

	receipt, err := s.ProjectManagedRows(t.Context(), q)
	if err == nil || !receipt.ID.IsNil() {
		t.Fatalf("projection accepted corrupt prerequisite: %v", err)
	}

	if after := projectionSnapshot(t, s); after != before {
		t.Fatal("refused projection changed retained or ordinary state")
	}
}

func TestManagedProjectionRetainedAssociations(t *testing.T) {
	for _, kind := range []string{"release", "target_projection"} {
		for _, mutation := range []string{"missing", "target", "action", "release", "manifest_or_receipt"} {
			t.Run(kind+"/"+mutation, func(t *testing.T) {
				s, q, request := projectionFixture(t)
				if _, err := s.ProjectManagedRows(t.Context(), request); err != nil {
					t.Fatal(err)
				}

				key := q.ReleaseID.String()
				if kind == "target_projection" {
					key = q.InstanceID.String()
				}

				corruptProjectionRecord(t, s, kind, key, func(raw []byte) []byte {
					if mutation == "missing" {
						return nil
					}

					if kind == "release" {
						var binding managed.ReleaseBinding
						if err := json.Unmarshal(raw, &binding); err != nil {
							t.Fatal(err)
						}

						switch mutation {
						case "target":
							binding.Target.InstanceID = id.New(id.PrefixInstance)
						case "action":
							binding.ActionID = id.New(managed.PrefixOperation)
						case "release":
							binding.Key.ReleaseID = id.New(id.PrefixRelease)
						case "manifest_or_receipt":
							binding.Manifest.Spec = append(binding.Manifest.Spec, ' ')
						}

						return projectionJSON(t, binding)
					}

					var receipt managed.ProjectionReceipt
					if err := json.Unmarshal(raw, &receipt); err != nil {
						t.Fatal(err)
					}

					switch mutation {
					case "target":
						receipt.Target.InstanceID = id.New(id.PrefixInstance)
					case "action":
						receipt.ActionID = id.New(managed.PrefixOperation)
					case "release":
						receipt.ReleaseID = id.New(id.PrefixRelease)
					case "manifest_or_receipt":
						receipt.ID = id.New(managed.PrefixOperation)
					}

					return projectionJSON(t, receipt)
				})
				assertProjectionRefused(t, s, request)
			})
		}
	}
}

func TestManagedProjectionMissingAcceptance(t *testing.T) {
	for _, kind := range []string{"action", "audit"} {
		for _, replay := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/replay=%t", kind, replay), func(t *testing.T) {
				s, q, request := projectionFixture(t)
				if replay {
					if _, err := s.ProjectManagedRows(t.Context(), request); err != nil {
						t.Fatal(err)
					}
				}

				corruptProjectionRecord(t, s, kind, q.ID.String(), func([]byte) []byte { return nil })
				assertProjectionRefused(t, s, request)
			})
		}
	}
}

func TestManagedProjectionOwnerOriginReceipt(t *testing.T) {
	for _, kind := range []string{"projection", "target_projection"} {
		t.Run(kind, func(t *testing.T) {
			s, q, request := projectionFixture(t)
			if _, err := s.ProjectManagedRows(t.Context(), request); err != nil {
				t.Fatal(err)
			}

			replacement := projectionAction(t)
			replacement.Command = projectionCommand(t, s, q.Key)
			replacement.NewTarget.TenantID = q.TenantID

			target, err := s.ReadManagedTarget(t.Context(), q.Key)
			if err != nil {
				t.Fatal(err)
			}

			replacement.ExpectedDesiredRevision = target.DesiredRevision
			for n := range replacement.NewTarget.Manifest.Members {
				replacement.NewTarget.Manifest.Members[n].OwnerID = q.NewTarget.Manifest.Members[0].OwnerID
			}

			signProjectionAction(t, &replacement)

			if _, err = s.SubmitManagedAction(t.Context(), replacement); err != nil {
				t.Fatal(err)
			}

			request = projectionLease(t, s, replacement.NewTarget.Key, replacement.ID)

			key := q.ID.String()
			if kind == "target_projection" {
				key = q.InstanceID.String()
			}

			corruptProjectionRecord(t, s, kind, key, func([]byte) []byte { return nil })
			assertProjectionRefused(t, s, request)
		})
	}
}

func TestManagedProjectionInvalidParents(t *testing.T) {
	for _, kind := range []string{"release_instance", "tombstone"} {
		t.Run(kind, func(t *testing.T) {
			s, q, request := projectionFixture(t)
			if _, err := s.ProjectManagedRows(t.Context(), request); err != nil {
				t.Fatal(err)
			}

			corruptProjectionParent(t, s, q, kind)
			assertProjectionRefused(t, s, request)
		})
	}
}

func projectionJSON(t *testing.T, value any) []byte {
	t.Helper()

	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}

	return raw
}
func openProjectionPG(t *testing.T, dsn string) *Store {
	t.Helper()

	d := pgdriver.New()
	if err := d.Open(t.Context(), dsn); err != nil {
		t.Fatal(err)
	}

	db, err := grove.Open(d)
	if err != nil {
		t.Fatal(err)
	}

	s := New(db)

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})

	return s
}
func newProjectionPG(t *testing.T) (*Store, string) {
	t.Helper()

	dsn := os.Getenv("CTRLPLANE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("CTRLPLANE_TEST_POSTGRES_DSN unset")
	}

	root := openProjectionPG(t, dsn)

	name := "managed_" + id.New(id.PrefixTenant).String()
	if _, err := pgdriver.Unwrap(root.DB()).Exec(t.Context(), "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if _, err := pgdriver.Unwrap(root.DB()).Exec(context.Background(), "DROP DATABASE "+name); err != nil {
			t.Error(err)
		}
	})

	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}

	parsed.Path = "/" + name

	s := openProjectionPG(t, parsed.String())
	if err = s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}

	return s, parsed.String()
}

func projectionStore(t *testing.T) *Store {
	t.Helper()

	s, _ := newProjectionPG(t)

	return s
}
func corruptProjectionRecord(t *testing.T, s *Store, kind, key string, change func([]byte) []byte) {
	t.Helper()

	var raw []byte
	if err := s.pg.NewRaw("SELECT data FROM cp_managed_controller_records WHERE kind=$1 AND key=$2", kind, key).Scan(t.Context(), &raw); err != nil {
		t.Fatal(err)
	}

	next := change(raw)
	if next == nil {
		if _, err := s.pg.NewRaw("DELETE FROM cp_managed_controller_records WHERE kind=$1 AND key=$2", kind, key).Exec(t.Context()); err != nil {
			t.Fatal(err)
		}
	} else if _, err := s.pg.NewRaw("UPDATE cp_managed_controller_records SET data=$3 WHERE kind=$1 AND key=$2", kind, key, next).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
}
func projectionSnapshot(t *testing.T, s *Store) string {
	t.Helper()

	var result strings.Builder

	for _, table := range []string{"cp_managed_targets", "cp_managed_commands", "cp_managed_controller_records", "cp_instances", "cp_workloads", "cp_releases"} {
		var exists bool
		if err := s.pg.NewRaw("SELECT to_regclass($1) IS NOT NULL", table).Scan(t.Context(), &exists); err != nil {
			t.Fatal(err)
		}

		if !exists {
			result.WriteString(table + ":absent")

			continue
		}

		var value string
		if err := s.pg.NewRaw("SELECT COALESCE(string_agg(to_jsonb(r)::text, '' ORDER BY to_jsonb(r)::text), '') FROM "+table+" r").Scan(t.Context(), &value); err != nil {
			t.Fatal(err)
		}

		result.WriteString(table + ":" + value)
	}

	return result.String()
}
func corruptProjectionParent(t *testing.T, s *Store, q managed.ActionSubmission, kind string) {
	t.Helper()

	if kind == "release_instance" {
		if _, err := s.pg.NewRaw("UPDATE cp_releases SET instance_id=$1 WHERE id=$2", id.New(id.PrefixInstance).String(), q.ReleaseID.String()).Exec(t.Context()); err != nil {
			t.Fatal(err)
		}
	} else if _, err := s.pg.NewRaw("UPDATE cp_managed_targets SET phase='deleted',data=jsonb_set(data,'{phase}','\"deleted\"') WHERE id=$1", q.InstanceID.String()).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
}
func TestManagedProjectionMissingControllerSchema(t *testing.T) {
	s, _, request := projectionFixture(t)
	if _, err := s.pg.NewRaw("DROP TABLE cp_managed_controller_records").Exec(t.Context()); err != nil {
		t.Fatal(err)
	}

	assertProjectionRefused(t, s, request)
}

func TestManagedProjectionReceiptIdentity(t *testing.T) {
	s, _, request := projectionFixture(t)
	if _, err := s.ProjectManagedRows(t.Context(), request); err != nil {
		t.Fatal(err)
	}

	replacementID := id.New(managed.PrefixOperation)

	for _, record := range []struct{ kind, key string }{
		{"projection", request.ActionID.String()},
		{"target_projection", request.InstanceID.String()},
	} {
		corruptProjectionRecord(t, s, record.kind, record.key, func(raw []byte) []byte {
			var receipt managed.ProjectionReceipt
			if err := json.Unmarshal(raw, &receipt); err != nil {
				t.Fatal(err)
			}

			receipt.ID = replacementID

			return projectionJSON(t, receipt)
		})
	}

	assertProjectionRefused(t, s, request)
}
