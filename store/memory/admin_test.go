package memory

import (
	"context"
	"errors"
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/id"
)

func newTenant(name, externalID string) *admin.Tenant {
	t := &admin.Tenant{
		Entity:     ctrlplane.NewEntity(id.PrefixTenant),
		Name:       name,
		Slug:       name,
		ExternalID: externalID,
		Status:     admin.TenantStatus("active"),
	}
	return t
}

func TestGetTenantByExternalID_emptyExternalID_returnsNotFound(t *testing.T) {
	s := New()
	_, err := s.GetTenantByExternalID(context.Background(), "")
	if !errors.Is(err, ctrlplane.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestGetTenantByExternalID_unknownExternalID_returnsNotFound(t *testing.T) {
	s := New()
	_, err := s.GetTenantByExternalID(context.Background(), "missing-org")
	if !errors.Is(err, ctrlplane.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestGetTenantByExternalID_match_returnsTenant(t *testing.T) {
	s := New()
	tenant := newTenant("acme", "org-acme-1")
	if err := s.InsertTenant(context.Background(), tenant); err != nil {
		t.Fatalf("InsertTenant: %v", err)
	}

	got, err := s.GetTenantByExternalID(context.Background(), "org-acme-1")
	if err != nil {
		t.Fatalf("GetTenantByExternalID: %v", err)
	}
	if got.ID.String() != tenant.ID.String() {
		t.Fatalf("returned wrong tenant: want %s, got %s", tenant.ID, got.ID)
	}
	if got.ExternalID != "org-acme-1" {
		t.Fatalf("ExternalID round-trip: got %q", got.ExternalID)
	}
}

func TestGetTenantByExternalID_returnsCloneNotAlias(t *testing.T) {
	// Confirms the memory store returns a copy, so callers can't
	// mutate stored state by accident.
	s := New()
	tenant := newTenant("acme", "org-acme")
	_ = s.InsertTenant(context.Background(), tenant)

	got, err := s.GetTenantByExternalID(context.Background(), "org-acme")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got.Name = "MUTATED"

	again, err := s.GetTenantByExternalID(context.Background(), "org-acme")
	if err != nil {
		t.Fatalf("get again: %v", err)
	}
	if again.Name != "acme" {
		t.Fatalf("memory store leaked aliasing: stored Name was mutated to %q", again.Name)
	}
}

func TestGetTenantByExternalID_amongMany(t *testing.T) {
	s := New()
	wanted := newTenant("acme", "org-target")
	_ = s.InsertTenant(context.Background(), newTenant("foo", "org-other-1"))
	_ = s.InsertTenant(context.Background(), wanted)
	_ = s.InsertTenant(context.Background(), newTenant("bar", "org-other-2"))
	_ = s.InsertTenant(context.Background(), newTenant("noext", "")) // no external id

	got, err := s.GetTenantByExternalID(context.Background(), "org-target")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID.String() != wanted.ID.String() {
		t.Fatalf("wrong tenant returned among many: want %s, got %s", wanted.ID, got.ID)
	}
}
