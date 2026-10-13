package admin_test

import (
	"context"
	"testing"

	"github.com/xraph/ctrlplane/admin"
	"github.com/xraph/ctrlplane/auth"
	"github.com/xraph/ctrlplane/provider"
	"github.com/xraph/ctrlplane/store/memory"
)

type keyedProvider struct {
	provider.Provider

	name string
}

func (p *keyedProvider) Info() provider.ProviderInfo         { return provider.ProviderInfo{Name: p.name} }
func (p *keyedProvider) Capabilities() []provider.Capability { return nil }

// healthByKey answers only for registry keys, like providerhealth.Cache.
type healthByKey map[string]admin.ProviderHealthSnapshot

func (h healthByKey) Get(key string) (admin.ProviderHealthSnapshot, bool) {
	s, ok := h[key]

	return s, ok
}

// TestListProviders_HealthByRegistryKey registers a provider under a key
// that differs from its Info().Name. The health cache is keyed by registry
// key, so ListProviders has to look it up by key to report it healthy.
func TestListProviders_HealthByRegistryKey(t *testing.T) {
	t.Parallel()

	registry := provider.NewRegistry()
	registry.Register("gcp-conn-1", &keyedProvider{name: "cloudrun"})

	store := memory.New()
	svc := admin.NewService(store, store, store, store, registry, nil, nil)

	setter, ok := svc.(interface {
		SetProviderHealth(getter admin.ProviderHealthGetter)
	})
	if !ok {
		t.Fatal("admin service does not accept a provider health source")
	}

	setter.SetProviderHealth(healthByKey{"gcp-conn-1": {Healthy: true}})

	statuses, err := svc.ListProviders(auth.WithClaims(context.Background(), adminClaims()))
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}

	if len(statuses) != 1 {
		t.Fatalf("got %d statuses, want 1", len(statuses))
	}

	got := statuses[0]
	if got.Key != "gcp-conn-1" || got.Name != "cloudrun" || !got.Healthy {
		t.Fatalf("status = %+v, want key gcp-conn-1, name cloudrun, healthy", got)
	}
}
