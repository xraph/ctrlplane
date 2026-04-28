package admin_test

import (
	"context"
	"errors"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/store/memory"
)

// userClaims returns a vanilla authenticated-but-not-admin Claims set.
// GetTenantByExternalID requires Claims but not IsSystemAdmin.
func userClaims() *auth.Claims {
	return &auth.Claims{
		SubjectID: "user-1",
		Email:     "alice@x.com",
	}
}

// adminClaims returns a system-admin Claims set for tests that need
// to call admin-gated methods (CreateTenant/SetQuota/etc.).
func adminClaims() *auth.Claims {
	return &auth.Claims{
		SubjectID: "system",
		Roles:     []string{"system:admin"},
	}
}

// newServiceForGetByExternalID builds the minimum admin.Service needed
// for the new lookup. The other dependencies are nil because the
// targeted method only touches `store` + the auth-from-context check.
func newServiceForGetByExternalID(t *testing.T) (admin.Service, *memory.Store) {
	t.Helper()
	store := memory.New()
	svc := admin.NewService(store, nil, nil, nil, nil, nil, nil)
	return svc, store
}

func TestService_GetTenantByExternalID_unauthenticated_rejected(t *testing.T) {
	svc, _ := newServiceForGetByExternalID(t)

	// auth.RequireClaims returns auth.ErrUnauthorized (sibling sentinel
	// to ctrlplane.ErrUnauthorized — same message, different value).
	_, err := svc.GetTenantByExternalID(context.Background(), "anything")
	if !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("want auth.ErrUnauthorized, got %v", err)
	}
}

func TestService_GetTenantByExternalID_authenticated_returnsTenant(t *testing.T) {
	svc, store := newServiceForGetByExternalID(t)

	tenant := &admin.Tenant{
		Entity:     ctrlplane.NewEntity(id.PrefixTenant),
		Name:       "Acme",
		Slug:       "acme",
		ExternalID: "org-acme",
		Status:     admin.TenantStatus("active"),
	}
	if err := store.InsertTenant(context.Background(), tenant); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ctx := auth.WithClaims(context.Background(), userClaims())
	got, err := svc.GetTenantByExternalID(ctx, "org-acme")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID.String() != tenant.ID.String() {
		t.Fatalf("returned wrong tenant")
	}
}

func TestService_GetTenantByExternalID_authenticatedButMissing_returnsNotFound(t *testing.T) {
	svc, _ := newServiceForGetByExternalID(t)

	ctx := auth.WithClaims(context.Background(), userClaims())
	_, err := svc.GetTenantByExternalID(ctx, "no-such")
	if !errors.Is(err, ctrlplane.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// Sanity: the auth model intentionally does NOT require system admin,
// so a vanilla user can resolve their own tenant via the external key
// (the upstream system that issued the key is the gatekeeper).
// Confirm by also calling with admin claims and getting the same shape.
func TestService_GetTenantByExternalID_adminAndUser_bothSucceed(t *testing.T) {
	svc, store := newServiceForGetByExternalID(t)

	tenant := &admin.Tenant{
		Entity:     ctrlplane.NewEntity(id.PrefixTenant),
		Name:       "Acme",
		Slug:       "acme-2",
		ExternalID: "org-acme-2",
	}
	_ = store.InsertTenant(context.Background(), tenant)

	for label, claims := range map[string]*auth.Claims{
		"user":  userClaims(),
		"admin": adminClaims(),
	} {
		ctx := auth.WithClaims(context.Background(), claims)
		got, err := svc.GetTenantByExternalID(ctx, "org-acme-2")
		if err != nil {
			t.Errorf("%s: %v", label, err)
			continue
		}
		if got.ID.String() != tenant.ID.String() {
			t.Errorf("%s: returned wrong tenant", label)
		}
	}
}
