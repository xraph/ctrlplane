package store_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/xraph/ctrlplane/app"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/store"
	"github.com/xraph/ctrlplane/workload"

	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/managed"
	"github.com/xraph/ctrlplane/store/memory"
)

func TestManagedOwnerProtection(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s managed.Store
			if backend == "memory" {
				s = memory.New()
			} else {
				pg, _ := managedPG(t)
				s = pg
			}

			reader := s.(managed.OwnerProtectionStore)
			tenant := id.New(id.PrefixTenant).String()

			for range 17 {
				padding := managedIdentity()

				padding.TenantID = tenant
				if _, err := s.CreateManagedTarget(t.Context(), padding); err != nil {
					t.Fatal(err)
				}
			}

			identity := managedIdentity()
			identity.TenantID = tenant
			owner := managed.OwnerKey{TenantID: identity.TenantID, OwnerID: id.New(id.PrefixWorkload)}

			identity.Manifest.Members[0].OwnerID = owner.OwnerID
			if _, err := s.CreateManagedTarget(t.Context(), identity); err != nil {
				t.Fatal(err)
			}

			assertProtected := func() {
				t.Helper()

				p, err := reader.InspectManagedOwnerProtection(t.Context(), owner)
				if err != nil || !p.Protected {
					t.Fatalf("owner lookup: %+v %v", p, err)
				}
			}
			assertProtected()

			aggregate := s.(store.Store)
			w := workload.NewWorkload()
			w.ID = owner.OwnerID
			w.TenantID = owner.TenantID
			w.Slug = "retained-owner"
			w.Name = "retained-owner"
			w.State = workload.StateActive
			w.ReplicaCount = 3

			w.PreviousReplicas = 7
			if err := aggregate.InsertWorkload(t.Context(), w); err != nil {
				t.Fatal(err)
			}

			before, err := aggregate.GetWorkloadByID(t.Context(), owner.TenantID, owner.OwnerID)
			if err != nil {
				t.Fatal(err)
			}

			cp, err := app.New(app.WithStore(aggregate), app.WithAuth(&auth.NoopProvider{}))
			if err != nil {
				t.Fatal(err)
			}

			ctx := auth.WithClaims(t.Context(), &auth.Claims{SubjectID: "operator", TenantID: owner.TenantID})

			for _, run := range []func() error{func() error { return cp.Workloads.Pause(ctx, owner.OwnerID) }, func() error {
				_, err := cp.Workloads.Scale(ctx, owner.OwnerID, 0)

				return err
			}, func() error { return cp.Workloads.Delete(ctx, owner.OwnerID) }} {
				if err := run(); !errors.Is(err, managed.ErrBlocked) {
					t.Fatalf("same-authority workload guard: %v", err)
				}
			}

			after, err := aggregate.GetWorkloadByID(t.Context(), owner.TenantID, owner.OwnerID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("parent changed: %+v %v", after, err)
			}

			foreign := owner

			foreign.TenantID = "foreign"
			if p, err := reader.InspectManagedOwnerProtection(t.Context(), foreign); err != nil || p.Protected {
				t.Fatalf("foreign lookup: %+v %v", p, err)
			}

			absent := owner

			absent.OwnerID = id.New(id.PrefixWorkload)
			if p, err := reader.InspectManagedOwnerProtection(t.Context(), absent); err != nil || p.Protected {
				t.Fatalf("absent lookup: %+v %v", p, err)
			}

			command := managed.Command{Key: identity.Key, ID: id.New(managed.PrefixOperation), Revision: 1, Owner: "fixture"}

			lease, err := s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: command, Duration: managed.MaxLease})
			if err != nil {
				t.Fatal(err)
			}

			command.ID = id.New(managed.PrefixOperation)
			command.Revision = lease.Revision
			command.LeaseEpoch = lease.LeaseEpoch

			sealed, err := s.SealManagedTarget(t.Context(), command)
			if err != nil {
				t.Fatal(err)
			}

			command.ID = id.New(managed.PrefixOperation)

			command.Revision = sealed.Revision
			if _, err = s.TombstoneManagedUnissuedTarget(t.Context(), command); err != nil {
				t.Fatal(err)
			}

			assertProtected()

			if err := managed.NewGuard(s).CheckTenant(t.Context(), owner.TenantID); !errors.Is(err, managed.ErrBlocked) {
				t.Fatalf("retained tenant guard: %v", err)
			}
		})
	}
}

func TestManagedOwnerMalformedPostgresAuthority(t *testing.T) {
	s, _ := managedPG(t)

	identity := managedIdentity()
	if _, err := s.CreateManagedTarget(t.Context(), identity); err != nil {
		t.Fatal(err)
	}

	owner := managed.OwnerKey{TenantID: identity.TenantID, OwnerID: id.New(id.PrefixWorkload)}
	for _, body := range []string{`{}`, `{"manifest":{"members":null}}`, `{"tenant_id":"foreign"}`} {
		if _, err := pgdriver.Unwrap(s.DB()).Exec(t.Context(), "UPDATE cp_managed_targets SET data=$1::jsonb WHERE id=$2", body, identity.InstanceID.String()); err != nil {
			t.Fatal(err)
		}

		if _, err := s.InspectManagedOwnerProtection(t.Context(), owner); !errors.Is(err, managed.ErrAuthority) {
			t.Fatalf("malformed authority became absence: %v", err)
		}
	}

	if _, err := pgdriver.Unwrap(s.DB()).Exec(t.Context(), "UPDATE cp_managed_targets SET id='' WHERE id=$1", identity.InstanceID.String()); err != nil {
		t.Fatal(err)
	}

	if _, err := s.InspectManagedOwnerProtection(t.Context(), owner); err == nil {
		t.Fatal("malformed empty physical key became absence")
	}

	if _, err := pgdriver.Unwrap(s.DB()).Exec(t.Context(), "DROP TABLE cp_managed_commands; DROP TABLE cp_managed_targets"); err != nil {
		t.Fatal(err)
	}

	if err := managed.NewGuard(s).CheckOwner(t.Context(), owner, "workloads.delete"); !errors.Is(err, managed.ErrUnavailable) {
		t.Fatalf("missing schema: %v", err)
	}
}

func TestManagedOwnerMalformedMemoryAuthority(t *testing.T) {
	s := memory.New()
	identity := managedIdentity()

	identity.Manifest.Members[0].OwnerID = id.New(id.PrefixInstance)
	if _, err := s.CreateManagedTarget(t.Context(), identity); err != nil {
		t.Fatal(err)
	}

	owner := managed.OwnerKey{TenantID: identity.TenantID, OwnerID: id.New(id.PrefixWorkload)}
	if _, err := s.InspectManagedOwnerProtection(t.Context(), owner); !errors.Is(err, managed.ErrAuthority) {
		t.Fatalf("malformed immutable owner became absence: %v", err)
	}
}

func TestManagedDeletedTenantProtection(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s managed.Store = memory.New()

			if backend == "postgres" {
				pg, _ := managedPG(t)
				s = pg
			}

			identity := managedIdentity()
			if _, err := s.CreateManagedTarget(t.Context(), identity); err != nil {
				t.Fatal(err)
			}

			command := managed.Command{Key: identity.Key, ID: id.New(managed.PrefixOperation), Revision: 1, Owner: "fixture"}

			lease, err := s.ClaimManagedLease(t.Context(), managed.LeaseRequest{Command: command, Duration: managed.MaxLease})
			if err != nil {
				t.Fatal(err)
			}

			command.ID = id.New(managed.PrefixOperation)
			command.Revision = lease.Revision
			command.LeaseEpoch = lease.LeaseEpoch

			sealed, err := s.SealManagedTarget(t.Context(), command)
			if err != nil {
				t.Fatal(err)
			}

			command.ID = id.New(managed.PrefixOperation)

			command.Revision = sealed.Revision
			if _, err = s.TombstoneManagedUnissuedTarget(t.Context(), command); err != nil {
				t.Fatal(err)
			}

			obligations, err := s.HasManagedObligations(t.Context(), identity.TenantID)
			if err != nil || obligations {
				t.Fatalf("A obligations changed: %v %v", obligations, err)
			}

			if err := managed.NewGuard(s).CheckTenant(t.Context(), identity.TenantID); !errors.Is(err, managed.ErrBlocked) {
				t.Fatalf("deleted-only tenant not protected: %v", err)
			}
		})
	}
}
