package instance

import (
	"testing"

	ctrlplane "github.com/xraph/ctrlplane"
	"github.com/xraph/ctrlplane/event"
	"github.com/xraph/ctrlplane/id"
	"github.com/xraph/ctrlplane/provider"
)

// TestCreate_RecordsRegistryKey registers a provider under a key that
// differs from its Info().Name, the way you would register one provider
// per cloud connection, and checks the instance keeps the key and is found
// again by it.
func TestCreate_RecordsRegistryKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		providerName string
		setDefault   bool
	}{
		{name: "explicit key", providerName: "gcp-conn-1"},
		{name: "explicit Info().Name", providerName: "cloudrun"},
		{name: "registry default", setDefault: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newDelStore()
			prov := newDelProvider("cloudrun")
			registry := provider.NewRegistry()
			registry.Register("gcp-conn-1", prov)
			registry.Register("gcp-conn-2", newDelProvider("cloudrun-other"))

			if tt.setDefault {
				registry.SetDefault("gcp-conn-1")
			}

			svc := NewService(store, registry, event.NewInMemoryBus(), &guardAuthorizer{}, nil)

			inst, err := svc.Create(srcCtx(), CreateRequest{
				Name:         "api",
				ProviderName: tt.providerName,
				Services:     []provider.ServiceSpec{{Name: "api", Image: "nginx:1.27", Role: provider.RoleMain}},
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}

			if inst.ProviderName != "gcp-conn-1" {
				t.Fatalf("ProviderName = %q, want registry key %q", inst.ProviderName, "gcp-conn-1")
			}

			if err := svc.Delete(srcCtx(), inst.ID); err != nil {
				t.Fatalf("Delete: %v", err)
			}

			if got := prov.deprovisions.Load(); got != 1 {
				t.Fatalf("Deprovision calls on the keyed provider = %d, want 1", got)
			}
		})
	}
}

// TestDelete_LegacyRowHoldingInfoName covers rows written before instances
// recorded registry keys: they hold Info().Name and must still reach the
// provider.
func TestDelete_LegacyRowHoldingInfoName(t *testing.T) {
	t.Parallel()

	store := newDelStore()
	prov := newDelProvider("cloudrun")
	registry := provider.NewRegistry()
	registry.Register("gcp-conn-1", prov)

	inst := &Instance{
		Entity:       ctrlplane.NewEntity(id.PrefixInstance),
		TenantID:     "ten_1",
		Name:         "legacy",
		ProviderName: "cloudrun",
		State:        provider.StateRunning,
		Services:     []provider.ServiceSpec{{Name: "main", Image: "x", Role: provider.RoleMain}},
	}
	store.put(inst)

	svc := NewService(store, registry, event.NewInMemoryBus(), &guardAuthorizer{}, nil)

	if err := svc.Delete(srcCtx(), inst.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if got := prov.deprovisions.Load(); got != 1 {
		t.Fatalf("Deprovision calls = %d, want 1", got)
	}
}
