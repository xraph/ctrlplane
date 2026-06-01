package testutil

import (
	"context"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/id"
)

// SystemAdminRole is the role string ctrlplane gates IsSystemAdmin on.
// Exposed so tests don't hard-code the magic string in two places.
const SystemAdminRole = "system:admin"

// AdminContext returns a context carrying ctrlplane Claims with the
// system-admin role. Tests use this to call admin-gated methods
// (CreateTenant, ListTenants, SetQuota, etc.) without going through
// the full Authsome→ctrlplane bridge stack.
//
// The synthetic SubjectID identifies these calls as test-driven in
// any audit trail the store records.
func (ts *TestServer) AdminContext() context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{
		SubjectID: "ctrlplanetest:admin",
		Roles:     []string{SystemAdminRole},
	})
}

// UserContext returns a context carrying vanilla user Claims —
// authenticated, not admin. For methods that gate on
// auth.RequireClaims but not IsSystemAdmin (GetTenant, GetTenantByExternalID).
//
// subjectID may be empty; the helper substitutes a stable test value
// so a missing argument doesn't silently produce empty audit rows.
func (ts *TestServer) UserContext(subjectID string) context.Context {
	if subjectID == "" {
		subjectID = "ctrlplanetest:user"
	}

	return auth.WithClaims(context.Background(), &auth.Claims{
		SubjectID: subjectID,
	})
}

// SeedTenant inserts a tenant directly via the store, bypassing the
// admin-service auth gate. Returns the inserted tenant for assertion.
//
// externalID may be empty when the test doesn't exercise the
// org→tenant mapping path. plan defaults to "free" for the same
// reason — empty is treated as "I don't care".
func (ts *TestServer) SeedTenant(t *testing.T, name, externalID, plan string) *admin.Tenant {
	t.Helper()

	if plan == "" {
		plan = "free"
	}

	tenant := &admin.Tenant{
		Entity:     ctrlplane.NewEntity(id.PrefixTenant),
		Name:       name,
		Slug:       name,
		ExternalID: externalID,
		Plan:       plan,
		Status:     admin.TenantStatus("active"),
	}
	if err := ts.Store.InsertTenant(context.Background(), tenant); err != nil {
		t.Fatalf("ctrlplanetest: seed tenant %q: %v", name, err)
	}

	return tenant
}

// SeedTenantWithQuota seeds a tenant and applies a quota in one call.
// Saves the boilerplate of SeedTenant + ts.CP.Admin.SetQuota when a
// test needs both side-by-side.
func (ts *TestServer) SeedTenantWithQuota(t *testing.T, name, externalID, plan string, q admin.Quota) *admin.Tenant {
	t.Helper()

	tenant := ts.SeedTenant(t, name, externalID, plan)
	if err := ts.CP.Admin.SetQuota(ts.AdminContext(), tenant.ID.String(), q); err != nil {
		t.Fatalf("ctrlplanetest: seed quota for %q: %v", name, err)
	}

	return tenant
}

// MustGetTenantByExternalID is a panic-on-error wrapper around
// admin.Service.GetTenantByExternalID for tests that have already
// asserted the tenant exists and want a one-liner.
func (ts *TestServer) MustGetTenantByExternalID(t *testing.T, externalID string) *admin.Tenant {
	t.Helper()

	got, err := ts.CP.Admin.GetTenantByExternalID(ts.UserContext(""), externalID)
	if err != nil {
		t.Fatalf("ctrlplanetest: get tenant by external id %q: %v", externalID, err)
	}

	return got
}
