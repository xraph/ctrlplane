package testutil_test

import (
	"context"
	"errors"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/testutil"
)

func TestNewServer_constructsUsableCtrlPlane(t *testing.T) {
	ts := testutil.NewServer(t)

	if ts.CP == nil {
		t.Fatal("CP should be set")
	}
	if ts.Store == nil {
		t.Fatal("Store should be set")
	}
	if ts.Auth == nil {
		t.Fatal("Auth should be set (default NoopProvider)")
	}
	if ts.Server != nil {
		t.Fatal("Server should be nil unless WithHTTPAPI()")
	}
}

func TestSeedTenant_roundtrips(t *testing.T) {
	ts := testutil.NewServer(t)
	tenant := ts.SeedTenant(t, "Acme", "org-acme", "pro")

	got, err := ts.CP.Admin.GetTenantByExternalID(ts.UserContext(""), "org-acme")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID.String() != tenant.ID.String() {
		t.Fatalf("returned wrong tenant: want %s, got %s", tenant.ID, got.ID)
	}
	if got.Plan != "pro" {
		t.Fatalf("plan: want pro, got %q", got.Plan)
	}
}

func TestSeedTenant_emptyPlanDefaultsFree(t *testing.T) {
	ts := testutil.NewServer(t)
	tenant := ts.SeedTenant(t, "X", "org-x", "")
	if tenant.Plan != "free" {
		t.Fatalf("want free, got %q", tenant.Plan)
	}
}

func TestSeedTenantWithQuota_appliesQuota(t *testing.T) {
	ts := testutil.NewServer(t)
	tenant := ts.SeedTenantWithQuota(t, "Y", "org-y", "pro", admin.Quota{MaxInstances: 42})

	usage, err := ts.CP.Admin.GetQuota(ts.AdminContext(), tenant.ID.String())
	if err != nil {
		t.Fatalf("get quota: %v", err)
	}
	if usage.Quota.MaxInstances != 42 {
		t.Fatalf("want MaxInstances=42, got %d", usage.Quota.MaxInstances)
	}
}

func TestAdminContext_satisfiesIsSystemAdmin(t *testing.T) {
	ts := testutil.NewServer(t)
	// CreateTenant gates on IsSystemAdmin → call must succeed.
	if _, err := ts.CP.Admin.CreateTenant(ts.AdminContext(), admin.CreateTenantRequest{
		Name:       "Direct",
		Plan:       "free",
		ExternalID: "org-direct",
	}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
}

func TestUserContext_doesNotSatisfyIsSystemAdmin(t *testing.T) {
	ts := testutil.NewServer(t)
	// CreateTenant must reject a vanilla user context.
	_, err := ts.CP.Admin.CreateTenant(ts.UserContext("u-1"), admin.CreateTenantRequest{
		Name: "Forbidden",
	})
	if !errors.Is(err, ctrlplane.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestMustGetTenantByExternalID_returnsSeed(t *testing.T) {
	ts := testutil.NewServer(t)
	want := ts.SeedTenant(t, "A", "ext-a", "free")
	got := ts.MustGetTenantByExternalID(t, "ext-a")
	if got.ID.String() != want.ID.String() {
		t.Fatalf("returned wrong tenant: want %s, got %s", want.ID, got.ID)
	}
}

func TestNewServer_cleanupClosesServer(t *testing.T) {
	// Calling Close twice (once via t.Cleanup, once explicit) must not
	// panic. Validates the idempotency baked into TestServer.Close.
	ts := testutil.NewServer(t)
	ts.Close()
	ts.Close()
}

// Compile-time sanity: ensure the package's public surface didn't shrink
// during a refactor — every helper a downstream test might import is
// referenced here.
var _ = []any{
	testutil.NewServer,
	testutil.WithAuthProvider,
	testutil.WithProvider,
	testutil.WithHTTPAPI,
	testutil.SystemAdminRole,
}

// Make sure the unused import of context doesn't bite when this file
// loses its only context use during a test rewrite.
var _ = context.Background
